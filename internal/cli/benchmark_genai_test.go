package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/forge"
	"github.com/JLCode-tech/gkebnkctl/internal/genai"
	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
)

const genaiArtifactA = `{"benchmark_id":"a","request_latency":{"avg":2000,"p50":1900,"p99":2500},
 "request_count":{"avg":100},"time_to_first_token":{"avg":400,"p50":380,"p90":500,"p95":540,"p99":600},
 "inter_token_latency":{"avg":20,"p50":19,"p95":25,"p99":30},"output_token_throughput":{"avg":640},
 "input_sequence_length":{"avg":5000,"sum":500000},"benchmark_duration":{"avg":20}}`

const genaiArtifactB = `{"benchmark_id":"b","request_latency":{"avg":1500,"p50":1400,"p99":2000},
 "request_count":{"avg":100},"time_to_first_token":{"avg":200,"p50":190,"p90":260,"p95":280,"p99":320},
 "inter_token_latency":{"avg":20,"p50":19,"p95":25,"p99":30},"output_token_throughput":{"avg":660},
 "input_sequence_length":{"avg":5000,"sum":500000},"benchmark_duration":{"avg":19}}`

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// execIngest parses args on the ingest command and runs it, the way the
// telemetry tests drive their command (cobra Execute would run the root).
func execIngest(t *testing.T, out *bytes.Buffer, args ...string) error {
	t.Helper()
	benchmarkIngestCmd.SetOut(out)
	benchmarkIngestCmd.SetErr(out)
	if err := benchmarkIngestCmd.ParseFlags(args); err != nil {
		return err
	}
	return runBenchmarkIngest(benchmarkIngestCmd, benchmarkIngestCmd.Flags().Args())
}

func resetIngestFlags() {
	flagBenchIngestBefore, flagBenchIngestAfter = nil, nil
	flagBenchIngestPush, flagBenchIngestTTFTDrop = false, 0
	flagOutput = ""
}

func TestBenchmarkIngest_TableCompareAndScrapes(t *testing.T) {
	resetIngestFlags()
	defer resetIngestFlags()
	dir := t.TempDir()
	a := writeTemp(t, dir, "baseline.json", genaiArtifactA)
	b := writeTemp(t, dir, "prefix-shared.json", genaiArtifactB)
	before := writeTemp(t, dir, "before.prom", "vllm:prefix_cache_queries_total 100\nvllm:prefix_cache_hits_total 10\n")
	after := writeTemp(t, dir, "after.prom", "vllm:prefix_cache_queries_total 300\nvllm:prefix_cache_hits_total 170\nvllm:kv_cache_usage_perc 0.5\n")

	var out bytes.Buffer
	if err := execIngest(t, &out, a, b, "--metrics-before", before, "--metrics-after", "decode="+after, "--expect-ttft-drop", "20"); err != nil {
		t.Fatalf("ingest: %v\n%s", err, out.String())
	}
	s := out.String()
	for _, want := range []string{"baseline", "prefix-shared", "380.0", "190.0", "80.0%", "TTFT p50 prefix-shared vs baseline: -50.0%"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
}

func TestBenchmarkIngest_TTFTExpectationFails(t *testing.T) {
	resetIngestFlags()
	defer resetIngestFlags()
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.json", genaiArtifactA)
	b := writeTemp(t, dir, "b.json", genaiArtifactB)
	var out bytes.Buffer
	err := execIngest(t, &out, a, b, "--expect-ttft-drop", "60")
	if err == nil || !strings.Contains(err.Error(), "TTFT expectation not met") {
		t.Fatalf("expected TTFT failure, got %v", err)
	}
}

func TestBenchmarkIngest_JSONAndGenAIOutInput(t *testing.T) {
	resetIngestFlags()
	defer resetIngestFlags()
	dir := t.TempDir()
	m := &genai.Metrics{TTFTP50Ms: 123, PrefixCacheHitRate: genai.Float(0.4)}
	if err := writeGenAIOut(filepath.Join(dir, "sub", "run.genai.json"), m); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	flagOutput = "json"
	if err := execIngest(t, &out, filepath.Join(dir, "sub", "run.genai.json")); err != nil {
		t.Fatalf("ingest: %v\n%s", err, out.String())
	}
	var doc struct {
		Schema  string `json:"schema"`
		Results []struct {
			Label   string        `json:"label"`
			Source  string        `json:"source"`
			Metrics genai.Metrics `json:"metrics"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("json: %v\n%s", err, out.String())
	}
	if doc.Schema != "gkebnkctl.benchmark.genai.v1" || len(doc.Results) != 1 || doc.Results[0].Source != "genai" ||
		doc.Results[0].Metrics.TTFTP50Ms != 123 || doc.Results[0].Metrics.PrefixCacheHitRate == nil {
		t.Errorf("doc = %+v", doc)
	}
}

func TestBenchmarkIngest_PushMergesGenAI(t *testing.T) {
	resetIngestFlags()
	defer resetIngestFlags()
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.json", genaiArtifactA)

	origPush := pushRawAiperfResultFn
	defer func() { pushRawAiperfResultFn = origPush }()
	var got forge.RawAiperfPushOptions
	pushRawAiperfResultFn = func(_ context.Context, opts forge.RawAiperfPushOptions) (forge.RawAiperfPushResponse, error) {
		got = opts
		return forge.RawAiperfPushResponse{RunID: 77, Status: "completed"}, nil
	}
	origProxy, origModel, origLabel := flagBenchProxy, flagBenchModel, flagBenchRunLabel
	defer func() { flagBenchProxy, flagBenchModel, flagBenchRunLabel = origProxy, origModel, origLabel }()
	flagBenchProxy, flagBenchModel, flagBenchRunLabel = "f5-bnk", "llama3", ""

	var out bytes.Buffer
	if err := execIngest(t, &out, a, "--push"); err != nil {
		t.Fatalf("ingest --push: %v\n%s", err, out.String())
	}
	if got.GenAI == nil || got.GenAI.TTFTP50Ms != 380 || got.RunLabel != "a" || got.Proxy != "f5-bnk" || got.Model != "llama3" {
		t.Errorf("push opts = %+v", got)
	}
	if !strings.Contains(out.String(), "77") {
		t.Errorf("run id not shown:\n%s", out.String())
	}
}

func TestExecuteBenchmarkRun_ScrapesAndAttaches(t *testing.T) {
	origRun := runAiperfFn
	origPod := podMetricsScrapeFn
	origRemote := remoteMetricsScrapeFn
	origSel, origURLs, origOut := flagBenchMetricsPodSelector, flagBenchMetricsURLs, flagBenchGenAIOut
	defer func() {
		runAiperfFn, podMetricsScrapeFn, remoteMetricsScrapeFn = origRun, origPod, origRemote
		flagBenchMetricsPodSelector, flagBenchMetricsURLs, flagBenchGenAIOut = origSel, origURLs, origOut
	}()

	calls := 0
	podMetricsScrapeFn = func(_ context.Context, _, ns, selector string, port int, role string) ([]genai.Scrape, error) {
		calls++
		if ns != "default" || selector != "app=vllm" || port != 8000 || role != "prefill" {
			t.Errorf("scrape args = %s %s %d %s", ns, selector, port, role)
		}
		text := "vllm:prefix_cache_queries_total 0\nvllm:prefix_cache_hits_total 0\nvllm:kv_cache_usage_perc 0.1\n"
		if calls == 2 {
			text = "vllm:prefix_cache_queries_total 40\nvllm:prefix_cache_hits_total 30\nvllm:kv_cache_usage_perc 0.6\n"
		}
		return []genai.Scrape{{Role: role, Endpoint: "pod/default/vllm-0:8000", Text: text}}, nil
	}
	remoteMetricsScrapeFn = func(_ context.Context, _ jumphost.ProbeOptions, specs []string) ([]genai.Scrape, error) {
		if len(specs) != 1 || specs[0] != "decode=http://10.0.20.5:8000/metrics" {
			t.Errorf("remote specs = %v", specs)
		}
		return []genai.Scrape{{Role: "decode", Endpoint: specs[0], Text: "vllm:kv_cache_usage_perc 0.25\n"}}, nil
	}
	runAiperfFn = func(_ context.Context, _ jumphost.AiperfRunOptions) (*jumphost.AiperfResult, error) {
		return &jumphost.AiperfResult{RawJSON: genaiArtifactA, TotalRequests: 100, DurationSeconds: 20}, nil
	}
	flagBenchMetricsPodSelector = []string{"prefill=app=vllm"}
	flagBenchMetricsURLs = []string{"decode=http://10.0.20.5:8000/metrics"}
	flagBenchGenAIOut = filepath.Join(t.TempDir(), "run.json")

	res, err := executeBenchmarkRun(context.Background(), jumphost.AiperfRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("pod scrape calls = %d, want 2 (before + after)", calls)
	}
	g := res.GenAI
	if g == nil || g.TTFTP95Ms != 540 || g.PrefixCacheHitRate == nil || *g.PrefixCacheHitRate != 0.75 {
		t.Fatalf("genai = %+v", g)
	}
	if g.PrefillWorkerUtilization == nil || *g.PrefillWorkerUtilization != 0.6 || g.DecodeWorkerUtilization == nil || *g.DecodeWorkerUtilization != 0.25 {
		t.Errorf("utilization = %v %v", g.PrefillWorkerUtilization, g.DecodeWorkerUtilization)
	}
	if g.InputTokensPerSec != 25000 || g.OutputTokensPerSec != 640 {
		t.Errorf("throughput = %v %v", g.InputTokensPerSec, g.OutputTokensPerSec)
	}
	b, err := os.ReadFile(flagBenchGenAIOut)
	if err != nil || !genai.IsMetricsJSON(b) {
		t.Errorf("--genai-out not written: %v", err)
	}
}

func TestExecuteBenchmarkRun_NoScrapeLeavesCacheFieldsNil(t *testing.T) {
	origRun := runAiperfFn
	defer func() { runAiperfFn = origRun }()
	runAiperfFn = func(_ context.Context, _ jumphost.AiperfRunOptions) (*jumphost.AiperfResult, error) {
		return &jumphost.AiperfResult{RawJSON: genaiArtifactB}, nil
	}
	res, err := executeBenchmarkRun(context.Background(), jumphost.AiperfRunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.GenAI == nil || res.GenAI.TTFTP50Ms != 190 || res.GenAI.PrefixCacheHitRate != nil {
		t.Errorf("genai = %+v", res.GenAI)
	}
}

func TestScrapeRemoteMetrics_BuildsCurlAndRoles(t *testing.T) {
	orig := runStagingCommandsFn
	defer func() { runStagingCommandsFn = orig }()
	var gotCmds []string
	runStagingCommandsFn = func(_ context.Context, _ jumphost.ProbeOptions, cmds []string) ([]string, error) {
		gotCmds = cmds
		return []string{"a 1\n", "b 2\n"}, nil
	}
	scrapes, err := scrapeRemoteMetrics(context.Background(), jumphost.ProbeOptions{}, []string{"http://h1:8000/metrics", "decode=http://h2:9090/metrics"})
	if err != nil {
		t.Fatal(err)
	}
	if len(gotCmds) != 2 || gotCmds[0] != "curl -sS --max-time 10 'http://h1:8000/metrics'" {
		t.Errorf("cmds = %v", gotCmds)
	}
	if scrapes[1].Role != "decode" || scrapes[1].Text != "b 2\n" || scrapes[0].Role != "" {
		t.Errorf("scrapes = %+v", scrapes)
	}
	if _, err := scrapeRemoteMetrics(context.Background(), jumphost.ProbeOptions{}, []string{"file:///etc/passwd"}); err == nil {
		t.Error("non-http URL must be rejected")
	}
}

func TestPodRole(t *testing.T) {
	if podRole("prefill", map[string]string{podRoleLabel: "decode"}) != "prefill" {
		t.Error("explicit role must win")
	}
	if podRole("", map[string]string{podRoleLabel: "Decode"}) != "decode" {
		t.Error("label role not read")
	}
	if podRole("", map[string]string{"app": "vllm"}) != "" {
		t.Error("unlabelled pod must have no role")
	}
}

func TestBenchmarkRunFlags_GenAIRegistered(t *testing.T) {
	for _, name := range []string{"prefix-prompt-length", "num-prefix-prompts", "random-seed", "metrics-pod-selector", "metrics-namespace", "metrics-port", "metrics-url", "genai-out"} {
		if benchmarkCmd.PersistentFlags().Lookup(name) == nil {
			t.Errorf("benchmark: flag --%s missing", name)
		}
		if forgeBenchmarkCmd.Flags().Lookup(name) == nil {
			t.Errorf("forge benchmark: flag --%s missing", name)
		}
	}
	found := false
	for _, c := range benchmarkCmd.Commands() {
		if c.Name() == "ingest" {
			found = true
		}
	}
	if !found {
		t.Error("benchmark ingest not registered")
	}
}

// A --genai-out file (no aiperf artifact) is pushed through the structured
// endpoint with its percentiles, throughput and GenAI block.
func TestBenchmarkIngest_PushGenAIOutStructured(t *testing.T) {
	resetIngestFlags()
	defer resetIngestFlags()
	dir := t.TempDir()
	g := writeTemp(t, dir, "noprefix.json", `{"ttft_p50_ms":109.7,"ttft_p90_ms":111.1,"ttft_p95_ms":111.2,"ttft_p99_ms":112.2,"itl_p50_ms":1.15,"itl_p95_ms":1.24,"itl_p99_ms":1.24,"input_tokens_per_sec":3664,"output_tokens_per_sec":198,"total_tokens_per_sec":3862}`)

	origRaw, origStruct := pushRawAiperfResultFn, pushBenchmarkResultFn
	defer func() { pushRawAiperfResultFn, pushBenchmarkResultFn = origRaw, origStruct }()
	pushRawAiperfResultFn = func(_ context.Context, _ forge.RawAiperfPushOptions) (forge.RawAiperfPushResponse, error) {
		t.Fatal("raw push must not be used for a --genai-out file")
		return forge.RawAiperfPushResponse{}, nil
	}
	var gotRes *jumphost.AiperfResult
	var gotOpts forge.BenchmarkPushOptions
	pushBenchmarkResultFn = func(_ context.Context, res *jumphost.AiperfResult, opts forge.BenchmarkPushOptions) (forge.BenchmarkPushResponse, error) {
		gotRes, gotOpts = res, opts
		return forge.BenchmarkPushResponse{RunID: 91, Status: "completed"}, nil
	}
	origProxy, origModel, origLabel := flagBenchProxy, flagBenchModel, flagBenchRunLabel
	defer func() { flagBenchProxy, flagBenchModel, flagBenchRunLabel = origProxy, origModel, origLabel }()
	flagBenchProxy, flagBenchModel, flagBenchRunLabel = "f5-bnk", "llama3", ""

	var out bytes.Buffer
	if err := execIngest(t, &out, g, "--push"); err != nil {
		t.Fatalf("ingest --push: %v\n%s", err, out.String())
	}
	if gotRes == nil || gotRes.TTFT.P50 != 109.7 || gotRes.ITL.P99 != 1.24 || gotRes.OutputTokenThroughput != 198 || gotRes.GenAI == nil || gotRes.GenAI.TotalTokensPerSec != 3862 {
		t.Errorf("structured result = %+v", gotRes)
	}
	if gotOpts.RunLabel != "noprefix" || gotOpts.Proxy != "f5-bnk" || gotOpts.AiperfConfig["source"] != "genai-out" {
		t.Errorf("push opts = %+v", gotOpts)
	}
	if !strings.Contains(out.String(), "91") {
		t.Errorf("run id not shown:\n%s", out.String())
	}
}

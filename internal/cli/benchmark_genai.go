package cli

// benchmark_genai.go — GenAI inference metrics for benchmark runs.
//
//   - --prefix-prompt-length / --num-prefix-prompts / --random-seed drive aiperf's
//     shared-prefix workload so prompt prefix caching can be exercised.
//   - --metrics-pod-selector (Kubernetes API pod proxy) and --metrics-url (curl
//     from the jumphost) scrape the model servers or the endpoint picker before
//     and after every aiperf run; the delta gives the prefix-cache hit rate and
//     the prefill/decode pool KV utilization.
//   - --genai-out writes the metric set next to the Forge push.
//   - `gkebnkctl benchmark ingest` parses aiperf artifacts (and --genai-out files)
//     offline, compares TTFT between them and optionally pushes them to Forge.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/JLCode-tech/gkebnkctl/internal/forge"
	"github.com/JLCode-tech/gkebnkctl/internal/genai"
	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
)

var (
	flagBenchPrefixPromptLength int
	flagBenchNumPrefixPrompts   int
	flagBenchRandomSeed         int
	flagBenchMetricsPodSelector []string
	flagBenchMetricsNamespace   string
	flagBenchMetricsPort        int
	flagBenchMetricsURLs        []string
	flagBenchGenAIOut           string

	flagBenchIngestBefore   []string
	flagBenchIngestAfter    []string
	flagBenchIngestPush     bool
	flagBenchIngestTTFTDrop float64
)

// podRoleLabel is the label llm-d puts on disaggregated model-server pods.
const podRoleLabel = "llm-d.ai/role"

func bindGenAIFlags(f *pflag.FlagSet) {
	f.IntVar(&flagBenchPrefixPromptLength, "prefix-prompt-length", 0,
		"tokens of shared prefix prepended to every synthetic prompt (aiperf --prefix-prompt-length); 0 = none")
	f.IntVar(&flagBenchNumPrefixPrompts, "num-prefix-prompts", 0,
		"size of the shared-prefix pool (aiperf --num-prefix-prompts); 0 = none")
	f.IntVar(&flagBenchRandomSeed, "random-seed", 0,
		"aiperf --random-seed so repeated runs replay the same prompts; 0 = aiperf default")
	f.StringSliceVar(&flagBenchMetricsPodSelector, "metrics-pod-selector", nil,
		"label selector of model-server or EPP pods whose /metrics are scraped through the Kubernetes API before and after the run "+
			"(repeatable; prefix with prefill= or decode= for a disaggregated pool, otherwise the pod's llm-d.ai/role label is used)")
	f.StringVar(&flagBenchMetricsNamespace, "metrics-namespace", "default", "namespace of --metrics-pod-selector pods")
	f.IntVar(&flagBenchMetricsPort, "metrics-port", 8000, "container port serving /metrics for --metrics-pod-selector pods")
	f.StringSliceVar(&flagBenchMetricsURLs, "metrics-url", nil,
		"Prometheus endpoint scraped from the jumphost before and after the run (repeatable; prefix with prefill= or decode=)")
	f.StringVar(&flagBenchGenAIOut, "genai-out", "", "write the GenAI metrics of the run to this JSON file")
}

// ── scrape seams ─────────────────────────────────────────────────────────────

// podMetricsScrapeFn scrapes /metrics of every pod matching selector through
// the API server pod proxy. Injected in tests.
var podMetricsScrapeFn = scrapePodMetricsViaAPI

// remoteMetricsScrapeFn runs curl on the jumphost for each URL. Injected in tests.
var remoteMetricsScrapeFn = scrapeRemoteMetrics

// runStagingCommandsFn is the jumphost seam used by scrapeRemoteMetrics.
var runStagingCommandsFn = jumphost.RunStagingCommands

func genAIScrapeConfigured() bool {
	return len(flagBenchMetricsPodSelector) > 0 || len(flagBenchMetricsURLs) > 0
}

// collectGenAIScrapes runs every configured scrape. Failures are reported and
// skipped: a missing metrics source must never fail the benchmark.
func collectGenAIScrapes(ctx context.Context, probOpts jumphost.ProbeOptions) []genai.Scrape {
	if !genAIScrapeConfigured() {
		return nil
	}
	var out []genai.Scrape
	if len(flagBenchMetricsPodSelector) > 0 {
		kubeconfig, err := resolveKubeconfigFlags("", flagBenchConfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠ metrics scrape: %v\n", err)
		}
		if kubeconfig == "" {
			kubeconfig = k8s.DefaultKubeconfigPath()
		}
		for _, spec := range flagBenchMetricsPodSelector {
			role, selector, err := genai.ParseRoleSpec(spec)
			if err != nil {
				fmt.Fprintf(os.Stderr, "⚠ metrics scrape: %v\n", err)
				continue
			}
			scrapes, err := podMetricsScrapeFn(ctx, kubeconfig, flagBenchMetricsNamespace, selector, flagBenchMetricsPort, role)
			if err != nil {
				fmt.Fprintf(os.Stderr, "⚠ metrics scrape (%s): %v\n", selector, err)
				continue
			}
			out = append(out, scrapes...)
		}
	}
	if len(flagBenchMetricsURLs) > 0 {
		scrapes, err := remoteMetricsScrapeFn(ctx, probOpts, flagBenchMetricsURLs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠ metrics scrape (jumphost): %v\n", err)
		}
		out = append(out, scrapes...)
	}
	return out
}

// scrapePodMetricsViaAPI GETs /api/v1/namespaces/<ns>/pods/<pod>:<port>/proxy/metrics
// for every Running pod matching selector. role overrides the pod label.
func scrapePodMetricsViaAPI(ctx context.Context, kubeconfigPath, namespace, selector string, port int, role string) ([]genai.Scrape, error) {
	cs, err := k8s.BuildClientset(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("list pods %s/%s: %w", namespace, selector, err)
	}
	var out []genai.Scrape
	running := 0
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		running++
		body, err := cs.CoreV1().RESTClient().Get().
			Namespace(namespace).Resource("pods").
			Name(fmt.Sprintf("%s:%d", pod.Name, port)).
			SubResource("proxy").Suffix("metrics").
			DoRaw(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠ metrics scrape: pod %s/%s: %v\n", namespace, pod.Name, err)
			continue
		}
		out = append(out, genai.Scrape{
			Role:     podRole(role, pod.Labels),
			Endpoint: fmt.Sprintf("pod/%s/%s:%d", namespace, pod.Name, port),
			Text:     string(body),
		})
	}
	switch {
	case len(out) == 0 && running == 0:
		return nil, fmt.Errorf("no running pod matched %q in %s", selector, namespace)
	case len(out) == 0:
		return nil, fmt.Errorf("%d running pod(s) matched %q in %s but none served /metrics on port %d", running, selector, namespace, port)
	}
	return out, nil
}

// podRole returns the explicit role, else the pod's llm-d.ai/role label when
// it is prefill or decode, else "".
func podRole(explicit string, labels map[string]string) string {
	if explicit != "" {
		return explicit
	}
	switch strings.ToLower(labels[podRoleLabel]) {
	case genai.RolePrefill:
		return genai.RolePrefill
	case genai.RoleDecode:
		return genai.RoleDecode
	}
	return ""
}

// scrapeRemoteMetrics curls each [role=]URL from the jumphost.
func scrapeRemoteMetrics(ctx context.Context, probOpts jumphost.ProbeOptions, specs []string) ([]genai.Scrape, error) {
	var (
		cmds  []string
		metas []genai.Scrape
	)
	for _, spec := range specs {
		role, u, err := genai.ParseRoleSpec(spec)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return nil, fmt.Errorf("--metrics-url %q: want http:// or https://", u)
		}
		cmds = append(cmds, fmt.Sprintf("curl -sS --max-time 10 %s", shellQuote(u)))
		metas = append(metas, genai.Scrape{Role: role, Endpoint: u})
	}
	outs, err := runStagingCommandsFn(ctx, probOpts, cmds)
	for i := range outs {
		metas[i].Text = outs[i]
	}
	return metas[:len(outs)], err
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ── attach ───────────────────────────────────────────────────────────────────

// attachGenAI computes the run's GenAI metrics from the aiperf artifact and the
// scrapes, stores them on the result, prints a summary and honours --genai-out.
func attachGenAI(result *jumphost.AiperfResult, before, after []genai.Scrape) {
	if result == nil {
		return
	}
	m := forge.DeriveGenAIMetrics(result)
	notes := genai.Attach(m, before, after)
	result.GenAI = m

	for _, line := range m.Summary() {
		fmt.Fprintf(os.Stderr, "  %s\n", line)
	}
	for _, n := range notes {
		fmt.Fprintf(os.Stderr, "  %s\n", n)
	}
	if flagBenchGenAIOut != "" {
		if err := writeGenAIOut(flagBenchGenAIOut, m); err != nil {
			fmt.Fprintf(os.Stderr, "⚠ --genai-out: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "✓ GenAI metrics written to %s\n", flagBenchGenAIOut)
		}
	}
}

func writeGenAIOut(path string, m *genai.Metrics) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// ── benchmark ingest ─────────────────────────────────────────────────────────

var benchmarkIngestCmd = &cobra.Command{
	Use:   "ingest <profile_export_aiperf.json|genai.json>...",
	Short: "Parse aiperf artifacts offline: TTFT/ITL percentiles, token throughput, prefix-cache hit rate; compare and optionally push to Forge",
	Long: `ingest reads one or more aiperf profile_export_aiperf.json artifacts (or
files written by --genai-out) and prints the GenAI metric set of each.

--metrics-before / --metrics-after take Prometheus scrapes of the model servers
or the EPP saved to files ([prefill=|decode=]path); their delta yields
prefix_cache_hit_rate and the pool KV utilization for every artifact given.

With two or more inputs the TTFT p50 of each later input is compared with the
first; --expect-ttft-drop fails the command when the drop is below that many
percent. --push sends each artifact to Forge (POST /api/benchmarks/results/aiperf)
with the GenAI fields merged, using the benchmark --forge-* / --proxy / --model /
--run-label / --vip flags.

Examples:
  gkebnkctl benchmark ingest baseline.json prefix-shared.json --expect-ttft-drop 20
  gkebnkctl benchmark ingest run.json --metrics-before before.prom --metrics-after after.prom -o json
  gkebnkctl benchmark ingest run.json --push --proxy f5-bnk --model llama3 --run-label nightly`,
	Args: cobra.MinimumNArgs(1),
	RunE: runBenchmarkIngest,
}

func init() {
	f := benchmarkIngestCmd.Flags()
	f.StringSliceVar(&flagBenchIngestBefore, "metrics-before", nil, "Prometheus scrape file taken before the run ([prefill=|decode=]path, repeatable)")
	f.StringSliceVar(&flagBenchIngestAfter, "metrics-after", nil, "Prometheus scrape file taken after the run ([prefill=|decode=]path, repeatable)")
	f.BoolVar(&flagBenchIngestPush, "push", false, "push each aiperf artifact to Forge with the GenAI fields merged")
	f.Float64Var(&flagBenchIngestTTFTDrop, "expect-ttft-drop", 0, "fail unless every later input's TTFT p50 is at least this many percent below the first input's")
	benchmarkCmd.AddCommand(benchmarkIngestCmd)
}

type ingestRow struct {
	Label   string         `json:"label"`
	Source  string         `json:"source"`
	Metrics *genai.Metrics `json:"metrics"`
	RunID   int            `json:"run_id,omitempty"`
	raw     []byte
}

// readScrapeFiles loads [role=]path scrape files. The i-th --metrics-before
// file pairs with the i-th --metrics-after file, so the endpoint key is the
// position rather than the file name; the role of the after file applies.
func readScrapeFiles(specs []string) ([]genai.Scrape, error) {
	var out []genai.Scrape
	for i, spec := range specs {
		role, path, err := genai.ParseRoleSpec(spec)
		if err != nil {
			return nil, err
		}
		b, err := os.ReadFile(path) // #nosec G304 -- operator-supplied scrape file path
		if err != nil {
			return nil, err
		}
		out = append(out, genai.Scrape{Role: role, Endpoint: fmt.Sprintf("scrape#%d", i), Text: string(b)})
	}
	return out, nil
}

func runBenchmarkIngest(cmd *cobra.Command, args []string) error {
	before, err := readScrapeFiles(flagBenchIngestBefore)
	if err != nil {
		return fmt.Errorf("--metrics-before: %w", err)
	}
	after, err := readScrapeFiles(flagBenchIngestAfter)
	if err != nil {
		return fmt.Errorf("--metrics-after: %w", err)
	}

	rows := make([]ingestRow, 0, len(args))
	for _, path := range args {
		b, err := os.ReadFile(path) // #nosec G304 -- operator-supplied artifact path
		if err != nil {
			return err
		}
		row := ingestRow{Label: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))}
		if genai.IsMetricsJSON(b) {
			var m genai.Metrics
			if err := json.Unmarshal(b, &m); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			row.Source, row.Metrics = "genai", &m
		} else {
			m, err := genai.ParseAiperfArtifact(b)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			genai.Attach(m, before, after)
			row.Source, row.Metrics, row.raw = "aiperf", m, b
		}
		rows = append(rows, row)
	}

	if flagBenchIngestPush {
		for i := range rows {
			label := flagBenchRunLabel
			if label == "" {
				label = rows[i].Label
			}
			if rows[i].raw == nil {
				// A --genai-out file has no aiperf artifact (it stayed on the jumphost),
				// so it goes through the structured endpoint the run command falls
				// back to, carrying the percentiles and throughput it does have.
				resp, err := pushBenchmarkResultFn(cmd.Context(), aiperfResultFromGenAI(rows[i].Metrics, flagBenchModel, flagBenchEndpoint), forge.BenchmarkPushOptions{
					RestURL:    flagBenchForgeURL,
					Creds:      effectiveForgeCreds(),
					AgentToken: effectiveAgentToken(flagBenchAgentName),
					RunLabel:   label,
					Proxy:      flagBenchProxy,
					AgentName:  flagBenchAgentName,
					AiperfConfig: map[string]any{
						"model":    flagBenchModel,
						"endpoint": flagBenchEndpoint,
						"source":   "genai-out",
					},
				})
				if err != nil {
					return fmt.Errorf("push %s: %w", rows[i].Label, err)
				}
				rows[i].RunID = resp.RunID
				fmt.Fprintf(cmd.ErrOrStderr(), "✓ pushed %s (structured, from --genai-out): run_id=%d\n", rows[i].Label, resp.RunID)
				continue
			}
			resp, err := pushRawAiperfResultFn(cmd.Context(), forge.RawAiperfPushOptions{
				RestURL:    flagBenchForgeURL,
				Creds:      effectiveForgeCreds(),
				AgentToken: effectiveAgentToken(flagBenchAgentName),
				RawJSON:    rows[i].raw,
				GenAI:      rows[i].Metrics,
				Proxy:      flagBenchProxy,
				Model:      flagBenchModel,
				URL:        optionalBaseURL(flagBenchVIP),
				AgentName:  flagBenchAgentName,
				RunLabel:   label,
			})
			if err != nil {
				return fmt.Errorf("push %s: %w", rows[i].Label, err)
			}
			rows[i].RunID = resp.RunID
			fmt.Fprintf(cmd.ErrOrStderr(), "✓ pushed %s: run_id=%d\n", rows[i].Label, resp.RunID)
		}
	}

	var failures []string
	if len(rows) > 1 {
		base := rows[0].Metrics.TTFTP50Ms
		for _, r := range rows[1:] {
			if base <= 0 {
				break
			}
			drop := (base - r.Metrics.TTFTP50Ms) / base * 100
			if flagBenchIngestTTFTDrop > 0 && drop < flagBenchIngestTTFTDrop {
				failures = append(failures, fmt.Sprintf("%s: TTFT p50 %.1f ms vs %.1f ms (%.1f%% drop, want ≥ %.1f%%)", r.Label, r.Metrics.TTFTP50Ms, base, drop, flagBenchIngestTTFTDrop))
			}
		}
	}

	if flagOutput == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(map[string]any{"schema": "gkebnkctl.benchmark.genai.v1", "results": rows}); err != nil {
			return err
		}
	} else {
		printIngestTable(cmd, rows)
	}
	if len(failures) > 0 {
		return fmt.Errorf("TTFT expectation not met: %s", strings.Join(failures, "; "))
	}
	return nil
}

func printIngestTable(cmd *cobra.Command, rows []ingestRow) {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "LABEL\tTTFT p50\tTTFT p95\tTTFT p99\tITL p50\tITL p99\tIN tok/s\tOUT tok/s\tPREFIX HIT\tRUN")
	for _, r := range rows {
		m := r.Metrics
		hit := "n/a"
		if m.PrefixCacheHitRate != nil {
			hit = fmt.Sprintf("%.1f%%", *m.PrefixCacheHitRate*100)
		}
		run := "-"
		if r.RunID != 0 {
			run = fmt.Sprintf("%d", r.RunID)
		}
		fmt.Fprintf(w, "%s\t%.1f\t%.1f\t%.1f\t%.2f\t%.2f\t%.1f\t%.1f\t%s\t%s\n",
			r.Label, m.TTFTP50Ms, m.TTFTP95Ms, m.TTFTP99Ms, m.ITLP50Ms, m.ITLP99Ms, m.InputTokensPerSec, m.OutputTokensPerSec, hit, run)
	}
	_ = w.Flush()
	if len(rows) > 1 && rows[0].Metrics.TTFTP50Ms > 0 {
		base := rows[0].Metrics.TTFTP50Ms
		for _, r := range rows[1:] {
			drop := (base - r.Metrics.TTFTP50Ms) / base * 100
			fmt.Fprintf(cmd.OutOrStdout(), "TTFT p50 %s vs %s: %+.1f%%\n", r.Label, rows[0].Label, -drop)
		}
	}
}

func optionalBaseURL(host string) string {
	if host == "" {
		return ""
	}
	return llmBaseURL(host)
}

// aiperfResultFromGenAI builds the result a --genai-out file can still push:
// the TTFT/ITL percentiles and token throughput it carries, with the GenAI
// block attached. Request counts and the raw artifact are unknown.
func aiperfResultFromGenAI(m *genai.Metrics, model, endpoint string) *jumphost.AiperfResult {
	return &jumphost.AiperfResult{
		Model:                 model,
		Endpoint:              endpoint,
		OutputTokenThroughput: m.OutputTokensPerSec,
		TTFT:                  jumphost.DistributionStats{Unit: "ms", P50: m.TTFTP50Ms, P90: m.TTFTP90Ms, P95: m.TTFTP95Ms, P99: m.TTFTP99Ms},
		ITL:                   jumphost.DistributionStats{Unit: "ms", P50: m.ITLP50Ms, P95: m.ITLP95Ms, P99: m.ITLP99Ms},
		GenAI:                 m,
	}
}

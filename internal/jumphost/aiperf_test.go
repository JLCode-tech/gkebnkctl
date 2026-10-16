package jumphost_test

import (
	"context"
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
)

func TestBuildAiperfCmd_DefaultsAndRequiredFlags(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{
			VIP:      "10.0.10.100",
			SourceIP: "10.0.11.50",
		},
		Config: jumphost.AiperfConfig{
			Model:      "llama3",
			HostHeader: "gkebnkctl-aiinference.local",
		},
	}

	cmd := jumphost.BuildAiperfCmd(opts)

	mustContain := []string{
		"aiperf profile",
		"-m", "llama3",
		"-u", "http://10.0.10.100",
		"--endpoint-type", "chat",
		"--concurrency", "1",
		"--request-count", "10",
		"--synthetic-input-tokens-mean", "512",
		"--output-tokens-mean", "128",
		"--tokenizer",
		"NousResearch/Meta-Llama-3-8B-Instruct",
		"--header", "Host:gkebnkctl-aiinference.local",
		"--artifact-dir",
		"--ui", "none",
		"cat",
		"profile_export_aiperf.json",
	}
	for _, want := range mustContain {
		if !strings.Contains(cmd, want) {
			t.Errorf("buildAiperfCmd missing %q; cmd=%q", want, cmd)
		}
	}

	mustNotContain := []string{
		"--base-url", "--num-users", "--num-requests", "--input-len", "--output-len",
		"--output-format", "--interface", "--run-label",
	}
	for _, bad := range mustNotContain {
		if strings.Contains(cmd, bad) {
			t.Errorf("buildAiperfCmd must NOT contain %q (removed flag); cmd=%q", bad, cmd)
		}
	}
}

func TestBuildAiperfCmd_StreamingFlag(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config: jumphost.AiperfConfig{
			Model:     "llama3",
			Streaming: true,
		},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if !strings.Contains(cmd, "--streaming") {
		t.Errorf("buildAiperfCmd missing --streaming when Streaming=true; cmd=%q", cmd)
	}
	if strings.Contains(cmd, " --stream ") || strings.Contains(cmd, " --stream\n") {
		t.Errorf("buildAiperfCmd must use --streaming not --stream; cmd=%q", cmd)
	}
}

func TestBuildAiperfCmd_NoStreamByDefault(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config:       jumphost.AiperfConfig{Model: "llama3"},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if strings.Contains(cmd, "--streaming") {
		t.Errorf("buildAiperfCmd must NOT contain --streaming when Streaming=false; cmd=%q", cmd)
	}
}

func TestBuildAiperfCmd_CustomConcurrencyAndRequests(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config: jumphost.AiperfConfig{
			Model:       "llama3",
			Concurrency: 4,
			NumRequests: 100,
			ISL:         256,
			OSL:         64,
		},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	for _, want := range []string{
		"--concurrency 4",
		"--request-count 100",
		"--synthetic-input-tokens-mean 256",
		"--output-tokens-mean 64",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("buildAiperfCmd missing %q; cmd=%q", want, cmd)
		}
	}
}

func TestBuildAiperfCmd_NoInterfaceEvenWithSourceIP(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100", SourceIP: "10.0.11.50"},
		Config:       jumphost.AiperfConfig{Model: "llama3"},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if strings.Contains(cmd, "--interface") {
		t.Errorf("buildAiperfCmd must NOT include --interface (removed); cmd=%q", cmd)
	}
}

func TestBuildAiperfCmd_HostHeaderWhenSet(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config: jumphost.AiperfConfig{
			Model:      "llama3",
			HostHeader: "myroute.local",
		},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if !strings.Contains(cmd, "--header") {
		t.Errorf("buildAiperfCmd missing --header when HostHeader set; cmd=%q", cmd)
	}
	if !strings.Contains(cmd, "Host:myroute.local") {
		t.Errorf("buildAiperfCmd missing Host:myroute.local; cmd=%q", cmd)
	}
}

func TestBuildAiperfCmd_NoHostHeaderWhenEmpty(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config:       jumphost.AiperfConfig{Model: "llama3"},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if strings.Contains(cmd, "--header") {
		t.Errorf("buildAiperfCmd must NOT include --header when HostHeader empty; cmd=%q", cmd)
	}
}

func TestBuildAiperfCmd_CustomTokenizer(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config: jumphost.AiperfConfig{
			Model:     "llama3",
			Tokenizer: "meta-llama/Llama-3-8B",
		},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if !strings.Contains(cmd, "meta-llama/Llama-3-8B") {
		t.Errorf("buildAiperfCmd must use custom tokenizer; cmd=%q", cmd)
	}
}

const minimalAiperfJSON = `some info line
another info line
{
  "schema_version": "1.3",
  "aiperf_version": "0.10.0",
  "benchmark_id": "f43cfc1c6cce",
  "request_throughput": {"unit": "requests/sec", "avg": 0.8233},
  "request_latency": {
    "unit": "ms",
    "avg": 2413.5,
    "p50": 2431.75,
    "p90": 2438.5,
    "p99": 2439.45,
    "min": 2358.58,
    "max": 2439.55,
    "std": 30.01,
    "count": 10,
    "sum": 24135.8
  },
  "request_count": {"unit": "requests", "avg": 10.0},
  "time_to_first_token": {
    "unit": "ms",
    "avg": 195.84,
    "p50": 175.05,
    "p90": 251.45,
    "p99": 253.45,
    "min": 126.76,
    "max": 253.67,
    "std": 46.21,
    "count": 10,
    "sum": 1958.4
  },
  "inter_token_latency": {
    "unit": "ms",
    "avg": 35.2,
    "p50": 34.73,
    "p90": 35.95,
    "p99": 35.96,
    "min": 34.66,
    "max": 35.96,
    "std": 0.60,
    "count": 10,
    "sum": 352.0
  },
  "output_token_throughput": {"unit": "tokens/sec", "avg": 52.69},
  "output_sequence_length": {"unit": "tokens", "avg": 64.0, "p50": 64.0, "p90": 64.0, "p99": 64.0, "min": 64.0, "max": 64.0, "std": 0.0, "count": 10, "sum": 640.0},
  "input_sequence_length":  {"unit": "tokens", "avg": 256.0, "p50": 256.0, "p90": 256.0, "p99": 256.0, "min": 256.0, "max": 256.0, "std": 0.0, "count": 10, "sum": 2560.0},
  "total_output_tokens":    {"unit": "tokens", "avg": 640.0},
  "benchmark_duration":     {"unit": "sec", "avg": 12.14},
  "start_time": "2026-06-13T05:11:41.465270",
  "end_time":   "2026-06-13T05:11:53.616416",
  "was_cancelled": false,
  "error_summary": []
}`

func TestRunAiperf_SSHExecStubbed(t *testing.T) {
	var capturedCmd string

	origPrepare := *jumphost.PrepareIAPKeyFn
	*jumphost.PrepareIAPKeyFn = func(_ context.Context, _, _ string) (string, string, func(), error) {
		return "/fake/key", "/fake/key.pub", func() {}, nil
	}
	defer func() { *jumphost.PrepareIAPKeyFn = origPrepare }()

	origPush := *jumphost.PushSSHPublicKeyFn
	*jumphost.PushSSHPublicKeyFn = func(_ context.Context, _, _, _ string) error { return nil }
	defer func() { *jumphost.PushSSHPublicKeyFn = origPush }()

	origExec := *jumphost.AiperfSSHExecFn
	*jumphost.AiperfSSHExecFn = func(_ context.Context, _, _, _, cmd string) (string, error) {
		capturedCmd = cmd
		return minimalAiperfJSON, nil
	}
	defer func() { *jumphost.AiperfSSHExecFn = origExec }()

	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{
			Region:     "us-central1",
			InstanceID: "test-jumphost",
			VIP:        "10.0.10.100",
			SourceIP:   "10.0.11.50",
		},
		Config: jumphost.AiperfConfig{
			Model:       "llama3",
			Concurrency: 2,
			NumRequests: 20,
		},
	}

	result, err := jumphost.RunAiperf(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunAiperf: %v", err)
	}

	if !strings.Contains(capturedCmd, "aiperf profile") {
		t.Errorf("SSH exec was not called with 'aiperf profile'; got %q", capturedCmd)
	}
	if !strings.Contains(capturedCmd, "--concurrency 2") {
		t.Errorf("command missing --concurrency 2; got %q", capturedCmd)
	}
	if !strings.Contains(capturedCmd, "--endpoint-type") {
		t.Errorf("command missing --endpoint-type; got %q", capturedCmd)
	}

	if result == nil {
		t.Fatal("RunAiperf returned nil result")
	}
	if result.TotalRequests != 10 {
		t.Errorf("TotalRequests = %d, want 10", result.TotalRequests)
	}
	if result.RequestLatency.P50 != 2431.75 {
		t.Errorf("RequestLatency.P50 = %v, want 2431.75", result.RequestLatency.P50)
	}
	if result.TTFT.P50 != 175.05 {
		t.Errorf("TTFT.P50 = %v, want 175.05", result.TTFT.P50)
	}
	if result.OutputTokenThroughput != 52.69 {
		t.Errorf("OutputTokenThroughput = %v, want 52.69", result.OutputTokenThroughput)
	}
}

func TestParseAiperfJSON_Fixture(t *testing.T) {
	origPrepare := *jumphost.PrepareIAPKeyFn
	*jumphost.PrepareIAPKeyFn = func(_ context.Context, _, _ string) (string, string, func(), error) {
		return "/k", "/k.pub", func() {}, nil
	}
	defer func() { *jumphost.PrepareIAPKeyFn = origPrepare }()

	origPush := *jumphost.PushSSHPublicKeyFn
	*jumphost.PushSSHPublicKeyFn = func(_ context.Context, _, _, _ string) error { return nil }
	defer func() { *jumphost.PushSSHPublicKeyFn = origPush }()

	origExec := *jumphost.AiperfSSHExecFn
	*jumphost.AiperfSSHExecFn = func(_ context.Context, _, _, _, _ string) (string, error) {
		return minimalAiperfJSON, nil
	}
	defer func() { *jumphost.AiperfSSHExecFn = origExec }()

	result, err := jumphost.RunAiperf(context.Background(), jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{Region: "r", InstanceID: "i", VIP: "10.0.10.100"},
		Config:       jumphost.AiperfConfig{Model: "llama3"},
	})
	if err != nil {
		t.Fatalf("RunAiperf: %v", err)
	}

	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"Model", result.Model, "llama3"},
		{"BaseURL", result.BaseURL, "http://10.0.10.100"},
		{"AiperfVersion", result.AiperfVersion, "0.10.0"},
		{"SchemaVersion", result.SchemaVersion, "1.3"},
		{"BenchmarkID", result.BenchmarkID, "f43cfc1c6cce"},
		{"TotalRequests", result.TotalRequests, 10},
		{"Successful", result.Successful, 10},
		{"Failed", result.Failed, 0},
		{"DurationSeconds", result.DurationSeconds, 12.14},
		{"RequestLatency.P50", result.RequestLatency.P50, 2431.75},
		{"RequestLatency.P99", result.RequestLatency.P99, 2439.45},
		{"TTFT.Avg", result.TTFT.Avg, 195.84},
		{"TTFT.P50", result.TTFT.P50, 175.05},
		{"ITL.Avg", result.ITL.Avg, 35.2},
		{"RequestThroughput", result.RequestThroughput, 0.8233},
		{"OutputTokenThroughput", result.OutputTokenThroughput, 52.69},
		{"AvgInputTokens", result.AvgInputTokens, 256.0},
		{"AvgOutputTokens", result.AvgOutputTokens, 64.0},
		{"TotalOutputTokens", result.TotalOutputTokens, 640.0},
		{"WasCancelled", result.WasCancelled, false},
		{"StartTime", result.StartTime, "2026-06-13T05:11:41.465270"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.field, c.got, c.want)
		}
	}
}

func TestRunAiperf_BackfillsIdentityFromConfig(t *testing.T) {
	origPrepare := *jumphost.PrepareIAPKeyFn
	*jumphost.PrepareIAPKeyFn = func(_ context.Context, _, _ string) (string, string, func(), error) {
		return "/k", "/k.pub", func() {}, nil
	}
	defer func() { *jumphost.PrepareIAPKeyFn = origPrepare }()

	origPush := *jumphost.PushSSHPublicKeyFn
	*jumphost.PushSSHPublicKeyFn = func(_ context.Context, _, _, _ string) error { return nil }
	defer func() { *jumphost.PushSSHPublicKeyFn = origPush }()

	origExec := *jumphost.AiperfSSHExecFn
	*jumphost.AiperfSSHExecFn = func(_ context.Context, _, _, _, _ string) (string, error) {
		return `{
			"request_count": {"unit":"requests","avg":5.0},
			"benchmark_duration": {"unit":"sec","avg":1.0},
			"request_throughput": {"unit":"requests/sec","avg":5.0},
			"request_latency": {"unit":"ms","avg":200.0,"p50":200.0,"p90":200.0,"p99":200.0,"min":100.0,"max":300.0},
			"time_to_first_token": {"unit":"ms","avg":50.0,"p50":50.0,"p90":50.0,"p99":50.0,"min":30.0,"max":70.0},
			"inter_token_latency": {"unit":"ms","avg":10.0,"p50":10.0,"p90":10.0,"p99":10.0,"min":5.0,"max":15.0},
			"output_token_throughput": {"unit":"tokens/sec","avg":50.0},
			"output_sequence_length": {"unit":"tokens","avg":64.0},
			"input_sequence_length": {"unit":"tokens","avg":256.0},
			"total_output_tokens": {"unit":"tokens","avg":320.0},
			"error_summary": [],
			"was_cancelled": false,
			"start_time": "2026-06-13T05:00:00",
			"end_time": "2026-06-13T05:00:01"
		}`, nil
	}
	defer func() { *jumphost.AiperfSSHExecFn = origExec }()

	result, err := jumphost.RunAiperf(context.Background(), jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{Region: "r", InstanceID: "i", VIP: "10.0.11.50"},
		Config: jumphost.AiperfConfig{
			Model:        "my-model",
			EndpointPath: "/v1/chat/completions",
		},
	})
	if err != nil {
		t.Fatalf("RunAiperf: %v", err)
	}
	if result.Model != "my-model" {
		t.Errorf("Model = %q, want %q (backfill)", result.Model, "my-model")
	}
	if result.BaseURL != "http://10.0.11.50" {
		t.Errorf("BaseURL = %q, want %q (backfill)", result.BaseURL, "http://10.0.11.50")
	}
	if result.Endpoint != "/v1/chat/completions" {
		t.Errorf("Endpoint = %q, want %q (backfill)", result.Endpoint, "/v1/chat/completions")
	}
}

func TestEnsureAiperf_GuardsPlaceholder(t *testing.T) {
	var capturedCmd string

	origPrepare := *jumphost.PrepareIAPKeyFn
	*jumphost.PrepareIAPKeyFn = func(_ context.Context, _, _ string) (string, string, func(), error) {
		return "/fake/key", "/fake/key.pub", func() {}, nil
	}
	defer func() { *jumphost.PrepareIAPKeyFn = origPrepare }()

	origPush := *jumphost.PushSSHPublicKeyFn
	*jumphost.PushSSHPublicKeyFn = func(_ context.Context, _, _, _ string) error { return nil }
	defer func() { *jumphost.PushSSHPublicKeyFn = origPush }()

	origExec := *jumphost.AiperfSSHExecFn
	*jumphost.AiperfSSHExecFn = func(_ context.Context, _, _, _, cmd string) (string, error) {
		capturedCmd = cmd
		return "ok:aiperf 0.10.0", nil
	}
	defer func() { *jumphost.AiperfSSHExecFn = origExec }()

	err := jumphost.EnsureAiperf(context.Background(), jumphost.ProbeOptions{
		Region:     "us-central1",
		InstanceID: "test-jumphost",
	})
	if err != nil {
		t.Fatalf("EnsureAiperf: unexpected error: %v", err)
	}

	if !strings.Contains(capturedCmd, "0\\.1\\.0") && !strings.Contains(capturedCmd, "0.1.0") {
		t.Errorf("EnsureAiperf must guard against 0.1.0 placeholder; got: %q", capturedCmd)
	}
}

func TestRunAiperf_RawJSONPopulated(t *testing.T) {
	origPrepare := *jumphost.PrepareIAPKeyFn
	*jumphost.PrepareIAPKeyFn = func(_ context.Context, _, _ string) (string, string, func(), error) {
		return "/k", "/k.pub", func() {}, nil
	}
	defer func() { *jumphost.PrepareIAPKeyFn = origPrepare }()

	origPush := *jumphost.PushSSHPublicKeyFn
	*jumphost.PushSSHPublicKeyFn = func(_ context.Context, _, _, _ string) error { return nil }
	defer func() { *jumphost.PushSSHPublicKeyFn = origPush }()

	origExec := *jumphost.AiperfSSHExecFn
	*jumphost.AiperfSSHExecFn = func(_ context.Context, _, _, _, _ string) (string, error) {
		return "noise line\nanother noise\n" + minimalAiperfJSON[strings.Index(minimalAiperfJSON, "{"):], nil
	}
	defer func() { *jumphost.AiperfSSHExecFn = origExec }()

	result, err := jumphost.RunAiperf(context.Background(), jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{Region: "r", InstanceID: "i", VIP: "10.0.10.100"},
		Config:       jumphost.AiperfConfig{Model: "llama3"},
	})
	if err != nil {
		t.Fatalf("RunAiperf: %v", err)
	}
	if result.RawJSON == "" {
		t.Fatal("RawJSON must not be empty after RunAiperf")
	}
	if !strings.HasPrefix(result.RawJSON, "{") {
		t.Errorf("RawJSON must start at '{', got prefix %q", result.RawJSON[:min(30, len(result.RawJSON))])
	}
	if !strings.Contains(result.RawJSON, `"schema_version"`) {
		t.Error("RawJSON missing 'schema_version' key from fixture")
	}
	if !strings.Contains(result.RawJSON, `"benchmark_id"`) {
		t.Error("RawJSON missing 'benchmark_id' key from fixture")
	}
}

func TestBuildAiperfCmd_ISLStddev(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config: jumphost.AiperfConfig{
			Model:     "llama3",
			ISL:       500,
			OSL:       128,
			ISLStddev: 50,
		},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if !strings.Contains(cmd, "--synthetic-input-tokens-stddev 50") {
		t.Errorf("missing --synthetic-input-tokens-stddev 50; cmd=%q", cmd)
	}
	if !strings.Contains(cmd, "--synthetic-input-tokens-mean") {
		t.Errorf("missing --synthetic-input-tokens-mean when SeqDist empty; cmd=%q", cmd)
	}
}

func TestBuildAiperfCmd_SeqDist_OmitsISLOSL(t *testing.T) {
	const seqDist = "300|100,64|16:70;4000|500,256|64:30"
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config: jumphost.AiperfConfig{
			Model:   "llama3",
			ISL:     500,
			OSL:     128,
			SeqDist: seqDist,
		},
	}
	cmd := jumphost.BuildAiperfCmd(opts)

	if !strings.Contains(cmd, "--seq-dist") {
		t.Errorf("missing --seq-dist; cmd=%q", cmd)
	}
	if !strings.Contains(cmd, seqDist) {
		t.Errorf("seq-dist value not in command; cmd=%q", cmd)
	}
	if strings.Contains(cmd, "--synthetic-input-tokens-mean") {
		t.Errorf("--synthetic-input-tokens-mean must be omitted when SeqDist set; cmd=%q", cmd)
	}
	if strings.Contains(cmd, "--output-tokens-mean") {
		t.Errorf("--output-tokens-mean must be omitted when SeqDist set; cmd=%q", cmd)
	}
}

func TestBuildAiperfCmd_PrefixFlags(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config: jumphost.AiperfConfig{
			Model:              "llama3",
			PrefixPromptLength: 4000,
			NumPrefixPrompts:   20,
		},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if !strings.Contains(cmd, "--prefix-prompt-length 4000") {
		t.Errorf("missing --prefix-prompt-length 4000; cmd=%q", cmd)
	}
	if !strings.Contains(cmd, "--num-prefix-prompts 20") {
		t.Errorf("missing --num-prefix-prompts 20; cmd=%q", cmd)
	}

	optsZero := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config:       jumphost.AiperfConfig{Model: "llama3"},
	}
	cmdZero := jumphost.BuildAiperfCmd(optsZero)
	if strings.Contains(cmdZero, "--prefix-prompt-length") {
		t.Errorf("--prefix-prompt-length must be omitted when 0; cmd=%q", cmdZero)
	}
	if strings.Contains(cmdZero, "--num-prefix-prompts") {
		t.Errorf("--num-prefix-prompts must be omitted when 0; cmd=%q", cmdZero)
	}
}

func TestBuildAiperfCmd_ExtraInputs(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config: jumphost.AiperfConfig{
			Model:       "llama3",
			ExtraInputs: []string{"ignore_eos:true", "temperature:0.0"},
		},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if !strings.Contains(cmd, "--extra-inputs") {
		t.Errorf("missing --extra-inputs flag; cmd=%q", cmd)
	}
	if !strings.Contains(cmd, "ignore_eos:true") {
		t.Errorf("missing ignore_eos:true in extra-inputs; cmd=%q", cmd)
	}
	if !strings.Contains(cmd, "temperature:0.0") {
		t.Errorf("missing temperature:0.0 in extra-inputs; cmd=%q", cmd)
	}
}

func TestBuildAiperfCmd_VariantLabel_NotInCmd(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config: jumphost.AiperfConfig{
			Model:        "llama3",
			VariantLabel: "c150-isl5000",
		},
	}
	cmd := jumphost.BuildAiperfCmd(opts)
	if strings.Contains(cmd, "VariantLabel") || strings.Contains(cmd, "variant-label") || strings.Contains(cmd, "c150-isl5000") {
		t.Errorf("VariantLabel must NOT be emitted as an aiperf flag; cmd=%q", cmd)
	}
}

func TestValidateTraceURL_AllowedSchemes(t *testing.T) {
	cases := []string{
		"https://raw.githubusercontent.com/kvcache-ai/Mooncake/main/trace.jsonl",
		"http://10.0.0.1:8080/trace.jsonl",
	}
	for _, url := range cases {
		if err := jumphost.ValidateTraceURL(url); err != nil {
			t.Errorf("ValidateTraceURL(%q) = %v, want nil", url, err)
		}
	}
}

func TestValidateTraceURL_BlockedSchemes(t *testing.T) {
	cases := []string{
		"file:///etc/passwd",
		"ftp://example.com/trace.jsonl",
		"gopher://example.com/trace.jsonl",
		"data:application/json,{}",
		"/local/path/trace.jsonl",
		"",
	}
	for _, url := range cases {
		if err := jumphost.ValidateTraceURL(url); err == nil {
			t.Errorf("ValidateTraceURL(%q) = nil, want error", url)
		}
	}
}

func TestDilateTimestamp_Numeric(t *testing.T) {
	got := jumphost.DilateTimestamp(80.0, 0.80)
	want := 100.0
	if got != want {
		t.Errorf("DilateTimestamp(80, 0.80) = %v, want %v", got, want)
	}
}

func TestDilateTimestamp_Zero(t *testing.T) {
	got := jumphost.DilateTimestamp(0, 0.80)
	if got != 0 {
		t.Errorf("DilateTimestamp(0, 0.80) = %v, want 0", got)
	}
}

func TestDilateTimestamp_LargeFactor(t *testing.T) {
	got := jumphost.DilateTimestamp(100.0, 2.0)
	want := 50.0
	if got != want {
		t.Errorf("DilateTimestamp(100, 2.0) = %v, want %v", got, want)
	}
}

const mooncakeTraceURL = "https://raw.githubusercontent.com/kvcache-ai/Mooncake/refs/heads/main/FAST25-release/traces/toolagent_trace.jsonl"

func mooncakeOpts() jumphost.AiperfRunOptions {
	return jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{VIP: "10.0.10.100"},
		Config: jumphost.AiperfConfig{
			Model:                 "llama3",
			Tokenizer:             "NousResearch/Meta-Llama-3-8B-Instruct",
			TraceURL:              mooncakeTraceURL,
			TraceDilation:         0.80,
			CustomDatasetType:     "mooncake_trace",
			FixedSchedule:         true,
			WorkersMax:            200,
			RandomSeed:            42,
			RequestTimeoutSeconds: 1000,
			ProfileExportLevel:    "summary",
			RecordProcessors:      8,
			Goodput:               "time_to_first_token:5000 inter_token_latency:100",
			Streaming:             true,
		},
	}
}

func TestBuildMooncakeCmd_AllCanonicalFlags(t *testing.T) {
	cmd, err := jumphost.BuildMooncakeCmd(mooncakeOpts())
	if err != nil {
		t.Fatalf("BuildMooncakeCmd: %v", err)
	}

	mustContain := []string{
		"ulimit -n 65536",
		"AIPERF_HTTP_CONNECTION_LIMIT=200",
		"curl -fsSL",
		mooncakeTraceURL,
		"python3.11 -c",
		"aiperf profile",
		"-m", "llama3",
		"-u", "http://10.0.10.100",
		"--tokenizer", "NousResearch/Meta-Llama-3-8B-Instruct",
		"--input-file",
		"--custom-dataset-type", "mooncake_trace",
		"--fixed-schedule",
		"--workers-max", "200",
		"--random-seed", "42",
		"--request-timeout-seconds", "1000",
		"--profile-export-level", "summary",
		"--record-processors", "8",
		"--goodput", "time_to_first_token:5000 inter_token_latency:100",
		"--streaming",
		"--output-artifact-dir",
		"--ui", "none",
		"profile_export_aiperf.json",
	}
	for _, want := range mustContain {
		if !strings.Contains(cmd, want) {
			t.Errorf("BuildMooncakeCmd missing %q; cmd=%q", want, cmd)
		}
	}
}

func TestBuildMooncakeCmd_SSRFReject(t *testing.T) {
	opts := mooncakeOpts()
	opts.Config.TraceURL = "file:///etc/passwd"
	_, err := jumphost.BuildMooncakeCmd(opts)
	if err == nil {
		t.Error("BuildMooncakeCmd must reject file:// TraceURL; got nil error")
	}
}

func TestBuildMooncakeCmd_DefaultDilation(t *testing.T) {
	opts := mooncakeOpts()
	opts.Config.TraceDilation = 0
	cmd, err := jumphost.BuildMooncakeCmd(opts)
	if err != nil {
		t.Fatalf("BuildMooncakeCmd: %v", err)
	}
	if !strings.Contains(cmd, "0.8") {
		t.Errorf("BuildMooncakeCmd with TraceDilation=0 should default to 0.8; cmd=%q", cmd)
	}
}

func TestBuildAiperfCmd_NativeScenario_InferenceXAgentXMvp(t *testing.T) {
	opts := jumphost.AiperfRunOptions{
		ProbeOptions: jumphost.ProbeOptions{
			VIP: "10.0.10.100",
		},
		Config: jumphost.AiperfConfig{
			Model:               "llama3",
			Scenario:            "inferencex-agentx-mvp",
			PublicDataset:       "custom_multi_turn",
			RequestRate:         4,
			UseServerTokenCount: true,
			MaxContextLength:    32768,
			UnsafeOverride:      []string{"dataset.conversation_num=30"},
			HostHeader:          "gkebnkctl-aiinference.local",
		},
	}

	cmd := jumphost.BuildAiperfCmd(opts)

	mustContain := []string{
		"aiperf profile",
		"-m 'llama3'",
		"-u 'http://10.0.10.100'",
		"--scenario 'inferencex-agentx-mvp'",
		"--public-dataset 'custom_multi_turn'",
		"--request-rate 4",
		"--use-server-token-count",
		"--max-context-length 32768",
		"--unsafe-override 'dataset.conversation_num=30'",
		"--header 'Host:gkebnkctl-aiinference.local'",
		"--artifact-dir",
		"--ui none",
		"cat",
		"profile_export_aiperf.json",
	}
	for _, want := range mustContain {
		if !strings.Contains(cmd, want) {
			t.Errorf("buildAiperfCmd missing %q; cmd=%q", want, cmd)
		}
	}

	mustNotContain := []string{
		"--endpoint-type",
		"--concurrency",
		"--request-count",
		"--synthetic-input-tokens-mean",
		"--output-tokens-mean",
		"--tokenizer",
	}
	for _, bad := range mustNotContain {
		if strings.Contains(cmd, bad) {
			t.Errorf("buildAiperfCmd must NOT contain %q for native scenario; cmd=%q", bad, cmd)
		}
	}
}

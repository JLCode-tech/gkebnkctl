package jumphost

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/JLCode-tech/gkebnkctl/internal/genai"
)

// AiperfConfig holds the parameters for a single aiperf benchmark run.
// All fields except Model and VIP have sensible defaults.
type AiperfConfig struct {
	// Model is the LLM model name as served by the endpoint (e.g. "llama3").
	Model string
	// EndpointType is the aiperf --endpoint-type value. Default: "chat".
	EndpointType string
	// EndpointPath is the HTTP path (e.g. "/v1/chat/completions").
	// Only passed as --custom-endpoint when non-default.
	EndpointPath string
	// Concurrency is the number of concurrent users. Default: 1.
	Concurrency int
	// NumRequests is the total number of requests to send. Default: 10.
	NumRequests int
	// ISL is the input sequence length (tokens). Default: 512.
	ISL int
	// OSL is the output sequence length (tokens). Default: 128.
	OSL int
	// ISLStddev is the stddev for synthetic input token length (--synthetic-input-tokens-stddev).
	ISLStddev int
	// SeqDist is the sequence-distribution string passed via --seq-dist.
	// When non-empty, --synthetic-input-tokens-mean and --output-tokens-mean are OMITTED.
	SeqDist string
	// PrefixPromptLength is the prefix prompt length (--prefix-prompt-length).
	PrefixPromptLength int
	// NumPrefixPrompts is the number of prefix prompts (--num-prefix-prompts).
	NumPrefixPrompts int
	// ExtraInputs is a list of "key:value" strings forwarded as repeated --extra-inputs flags.
	ExtraInputs []string
	// VariantLabel is a metadata field carried for per-child run labeling.
	// It is NOT an aiperf flag and is not emitted in the command.
	VariantLabel string
	// Streaming enables streaming mode (--streaming). Default: false.
	Streaming bool
	// Tokenizer is the Hugging Face tokenizer repo used by aiperf for token counting.
	// Default: "NousResearch/Meta-Llama-3-8B-Instruct" (ungated, no HF token).
	Tokenizer string
	// HostHeader is the HTTP Host header to inject (--header "Host:<value>").
	HostHeader string
	// Timeout is the per-run total timeout. Default: 5 minutes.
	Timeout time.Duration

	// Trace-driven (mooncake) fields
	TraceURL              string
	TraceDilation         float64
	CustomDatasetType     string
	FixedSchedule         bool
	WorkersMax            int
	RandomSeed            int
	RequestTimeoutSeconds int
	ProfileExportLevel    string
	RecordProcessors      int
	Goodput               string

	// Open-loop load (synthetic path)
	RequestRate       float64
	ArrivalPattern    string
	WarmupDuration    float64
	BenchmarkDuration float64
	NumDatasetEntries int

	// ── Native scenario / AgentX MVP fields ─────────────────────────────────
	// Scenario maps to aiperf --scenario (e.g. "inferencex-agentx-mvp").
	Scenario string
	// PublicDataset maps to aiperf --public-dataset (e.g. "semianalysis_cc_traces_weka_with_subagents").
	PublicDataset string
	// UseServerTokenCount maps to aiperf --use-server-token-count.
	UseServerTokenCount bool
	// MaxContextLength maps to aiperf --max-context-length.
	MaxContextLength int
	// UnsafeOverride maps to aiperf --unsafe-override.
	UnsafeOverride bool
}

// aiperfMetricDist is the distribution shape used for most aiperf 0.10.0 metrics.
type aiperfMetricDist struct {
	Unit  string  `json:"unit"`
	Avg   float64 `json:"avg"`
	P1    float64 `json:"p1"`
	P5    float64 `json:"p5"`
	P10   float64 `json:"p10"`
	P25   float64 `json:"p25"`
	P50   float64 `json:"p50"`
	P75   float64 `json:"p75"`
	P90   float64 `json:"p90"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Std   float64 `json:"std"`
	Count float64 `json:"count"`
	Sum   float64 `json:"sum"`
}

// aiperfTelemetry is the nested telemetry summary inside the JSON output.
type aiperfTelemetry struct {
	Summary struct {
		StartTime    string `json:"start_time"`
		EndTime      string `json:"end_time"`
		ErrorSummary []any  `json:"error_summary"`
	} `json:"summary"`
}

// aiperfRawOutput is the full JSON schema of aiperf 0.10.0's profile_export_aiperf.json.
type aiperfRawOutput struct {
	SchemaVersion string `json:"schema_version"`
	AiperfVersion string `json:"aiperf_version"`
	BenchmarkID   string `json:"benchmark_id"`

	RequestThrough     aiperfMetricDist `json:"request_throughput"`
	RequestLatency     aiperfMetricDist `json:"request_latency"`
	RequestCount       aiperfMetricDist `json:"request_count"`
	TimeToFirstToken   aiperfMetricDist `json:"time_to_first_token"`
	InterTokenLatency  aiperfMetricDist `json:"inter_token_latency"`
	OutputTokenThrough aiperfMetricDist `json:"output_token_throughput"`
	OutputSequenceLen  aiperfMetricDist `json:"output_sequence_length"`
	InputSequenceLen   aiperfMetricDist `json:"input_sequence_length"`
	TotalOutputTokens  aiperfMetricDist `json:"total_output_tokens"`
	BenchmarkDuration  aiperfMetricDist `json:"benchmark_duration"`

	StartTime    string `json:"start_time"`
	EndTime      string `json:"end_time"`
	WasCancelled bool   `json:"was_cancelled"`
	ErrorSummary []any  `json:"error_summary"`

	Telemetry aiperfTelemetry `json:"telemetry_data"`
}

// DistributionStats captures the per-percentile distribution for a metric.
type DistributionStats struct {
	Unit string  `json:"unit"`
	Avg  float64 `json:"avg"`
	P50  float64 `json:"p50"`
	P90  float64 `json:"p90"`
	P95  float64 `json:"p95"`
	P99  float64 `json:"p99"`
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
}

// AiperfResult is the Go representation of aiperf 0.10.0 benchmark output.
type AiperfResult struct {
	Model    string `json:"model"`
	BaseURL  string `json:"base_url"`
	Endpoint string `json:"endpoint"`

	RawJSON string `json:"-"`

	AiperfVersion string `json:"aiperf_version"`
	SchemaVersion string `json:"schema_version"`
	BenchmarkID   string `json:"benchmark_id"`
	StartTime     string `json:"start_time"`
	EndTime       string `json:"end_time"`
	WasCancelled  bool   `json:"was_cancelled"`
	ErrorSummary  []any  `json:"error_summary"`

	TotalRequests int `json:"total_requests"`
	Successful    int `json:"successful"`
	Failed        int `json:"failed"`

	DurationSeconds float64 `json:"duration_seconds"`
	DurationMinutes float64 `json:"duration_minutes"`

	RequestThroughput     float64           `json:"request_throughput_rps"`
	OutputTokenThroughput float64           `json:"output_token_throughput_tps"`
	RequestLatency        DistributionStats `json:"request_latency"`
	TTFT                  DistributionStats `json:"ttft"`
	ITL                   DistributionStats `json:"itl"`

	AvgInputTokens    float64 `json:"avg_input_tokens"`
	AvgOutputTokens   float64 `json:"avg_output_tokens"`
	TotalOutputTokens float64 `json:"total_output_tokens_sum"`

	// GenAI carries the TTFT/ITL percentiles, token throughput and, when the
	// run scraped model-server metrics, the prefix-cache hit rate and worker
	// utilization. Filled by the CLI after RunAiperf; nil when not computed.
	GenAI *genai.Metrics `json:"genai_metrics,omitempty"`
}

// aiperfSSHExecFn is the injectable seam for running a remote command over SSH and capturing stdout.
var aiperfSSHExecFn = func(ctx context.Context, region, instanceID, keyPath, remoteCmd string) (string, error) {
	return SSHRunViaIAP(ctx, region, instanceID, keyPath, remoteCmd)
}

// AiperfRunOptions is the full set of parameters RunAiperf needs.
type AiperfRunOptions struct {
	ProbeOptions ProbeOptions
	Config       AiperfConfig
	RunLabel     string
	ResultID     string
}

// validateTraceURL returns an error when traceURL has a scheme other than http or https.
func validateTraceURL(traceURL string) error {
	if traceURL == "" {
		return fmt.Errorf("trace_url must not be empty")
	}
	if strings.HasPrefix(traceURL, "https://") || strings.HasPrefix(traceURL, "http://") {
		return nil
	}
	scheme := traceURL
	if i := strings.Index(traceURL, "://"); i >= 0 {
		scheme = traceURL[:i]
	} else if i := strings.Index(traceURL, ":"); i >= 0 {
		scheme = traceURL[:i]
	}
	return fmt.Errorf("trace_url scheme %q not allowed; permitted: http, https", scheme)
}

// dilateTimestamp divides a float64 timestamp by the dilation factor.
func dilateTimestamp(ts, dilation float64) float64 {
	return ts / dilation
}

// buildMooncakeCmd constructs the trace-driven remote shell pipeline for the mooncake scenario.
func buildMooncakeCmd(opts AiperfRunOptions) (string, error) {
	cfg := opts.Config

	if err := validateTraceURL(cfg.TraceURL); err != nil {
		return "", err
	}

	dilation := cfg.TraceDilation
	if dilation == 0 {
		dilation = 0.80
	}

	dirSuffix := "run"
	if opts.ResultID != "" {
		safe := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				return r
			}
			return '-'
		}, opts.ResultID)
		dirSuffix = safe
	}
	rawFile := fmt.Sprintf("/tmp/aiperf-%s-raw.jsonl", dirSuffix)
	dilatedFile := fmt.Sprintf("/tmp/aiperf-%s-dilated.jsonl", dirSuffix)
	artifactDir := fmt.Sprintf("/tmp/aiperf-%s-artifacts", dirSuffix)

	workersMax := cfg.WorkersMax
	if workersMax <= 0 {
		workersMax = 200
	}

	if cfg.Tokenizer == "" {
		cfg.Tokenizer = "NousResearch/Meta-Llama-3-8B-Instruct"
	}
	if cfg.EndpointType == "" {
		cfg.EndpointType = "chat"
	}

	vip := opts.ProbeOptions.VIP

	dilationScript := fmt.Sprintf(
		"import json,sys\n"+
			"d=%g\n"+
			"[sys.stdout.write(json.dumps({**r,'timestamp':r['timestamp']/d})+chr(10)) "+
			"if isinstance(r.get('timestamp'),(int,float)) else "+
			"sys.stdout.write(json.dumps(r)+chr(10)) "+
			"for l in sys.stdin if l.strip() "+
			"for r in [json.loads(l)]]",
		dilation,
	)

	args := []string{
		"aiperf", "profile",
		"-m", shellSingleQuote(cfg.Model),
		"-u", shellSingleQuote(fmt.Sprintf("http://%s", vip)),
		"--endpoint-type", shellSingleQuote(cfg.EndpointType),
		"--tokenizer", shellSingleQuote(cfg.Tokenizer),
		"--input-file", shellSingleQuote(dilatedFile),
		"--custom-dataset-type", shellSingleQuote(cfg.CustomDatasetType),
	}

	if cfg.FixedSchedule {
		args = append(args, "--fixed-schedule")
	}

	if cfg.WorkersMax > 0 {
		args = append(args, "--workers-max", fmt.Sprintf("%d", cfg.WorkersMax))
	}
	if cfg.RandomSeed != 0 {
		args = append(args, "--random-seed", fmt.Sprintf("%d", cfg.RandomSeed))
	}
	if cfg.RequestTimeoutSeconds > 0 {
		args = append(args, "--request-timeout-seconds", fmt.Sprintf("%d", cfg.RequestTimeoutSeconds))
	}
	if cfg.ProfileExportLevel != "" {
		args = append(args, "--profile-export-level", shellSingleQuote(cfg.ProfileExportLevel))
	}
	if cfg.RecordProcessors > 0 {
		args = append(args, "--record-processors", fmt.Sprintf("%d", cfg.RecordProcessors))
	}
	if cfg.Goodput != "" {
		args = append(args, "--goodput", shellSingleQuote(cfg.Goodput))
	}

	if cfg.Streaming {
		args = append(args, "--streaming")
	}

	if cfg.HostHeader != "" {
		args = append(args, "--header", shellSingleQuote(fmt.Sprintf("Host:%s", cfg.HostHeader)))
	}

	args = append(args,
		"--output-artifact-dir", shellSingleQuote(artifactDir),
		"--ui", "none",
	)

	aiperfInvocation := strings.Join(args, " ")

	cmd := fmt.Sprintf(
		"ulimit -n 65536 2>/dev/null || true; "+
			"export AIPERF_HTTP_CONNECTION_LIMIT=%d; "+
			"curl -fsSL %s -o %s; "+
			"python3.11 -c %s < %s > %s; "+
			"%s >/tmp/aiperf.stderr 2>&1; "+
			`cat "$(ls -t %s/*/profile_export_aiperf.json 2>/dev/null | head -1)"`,
		workersMax,
		shellSingleQuote(cfg.TraceURL),
		shellSingleQuote(rawFile),
		shellSingleQuote(dilationScript),
		shellSingleQuote(rawFile),
		shellSingleQuote(dilatedFile),
		aiperfInvocation,
		shellSingleQuote(artifactDir),
	)

	return cmd, nil
}

// buildAiperfCmd constructs the remote shell command that runs aiperf profile.
func buildAiperfCmd(opts AiperfRunOptions) string {
	if opts.Config.TraceURL != "" {
		cmd, err := buildMooncakeCmd(opts)
		if err != nil {
			return fmt.Sprintf("echo 'buildAiperfCmd error: %s'; exit 1", err)
		}
		return cmd
	}

	cfg := opts.Config
	if cfg.EndpointType == "" {
		cfg.EndpointType = "chat"
	}
	// Closed-loop defaults apply only when no rate or duration drives the run:
	// a forced --concurrency 1 would cap an open-loop run at one in flight, and
	// a forced --request-count would end a timed step early.
	openLoop := cfg.RequestRate > 0 || cfg.BenchmarkDuration > 0 || cfg.Scenario != ""
	if cfg.Concurrency <= 0 && !openLoop {
		cfg.Concurrency = 1
	}
	if cfg.NumRequests <= 0 && !openLoop {
		cfg.NumRequests = 10
	}
	// aiperf cycles through 100 prompts by default; on a timed run that set fits in a
	// KV cache whole and every request hits. Generate one per request instead.
	if cfg.NumDatasetEntries <= 0 && cfg.NumRequests <= 0 && cfg.RequestRate > 0 && cfg.BenchmarkDuration > 0 {
		cfg.NumDatasetEntries = int(math.Ceil(cfg.RequestRate * (cfg.WarmupDuration + cfg.BenchmarkDuration)))
	}
	if cfg.ISL <= 0 {
		cfg.ISL = 512
	}
	if cfg.OSL <= 0 {
		cfg.OSL = 128
	}
	if cfg.Tokenizer == "" {
		cfg.Tokenizer = "NousResearch/Meta-Llama-3-8B-Instruct"
	}

	vip := opts.ProbeOptions.VIP

	dirSuffix := "run"
	if opts.ResultID != "" {
		safe := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
				return r
			}
			return '-'
		}, opts.ResultID)
		dirSuffix = safe
	}
	artifactDir := fmt.Sprintf("/tmp/aiperf-%s", dirSuffix)

	args := []string{
		"aiperf", "profile",
		"-m", shellSingleQuote(cfg.Model),
		"-u", shellSingleQuote(fmt.Sprintf("http://%s", vip)),
		"--endpoint-type", shellSingleQuote(cfg.EndpointType),
	}
	if cfg.Concurrency > 0 {
		args = append(args, "--concurrency", fmt.Sprintf("%d", cfg.Concurrency))
	}
	if cfg.NumRequests > 0 {
		args = append(args, "--request-count", fmt.Sprintf("%d", cfg.NumRequests))
	}
	if cfg.RequestRate > 0 {
		args = append(args, "--request-rate", strconv.FormatFloat(cfg.RequestRate, 'g', -1, 64))
	}
	if cfg.ArrivalPattern != "" {
		args = append(args, "--arrival-pattern", shellSingleQuote(cfg.ArrivalPattern))
	}
	if cfg.WarmupDuration > 0 {
		args = append(args, "--warmup-duration", strconv.FormatFloat(cfg.WarmupDuration, 'g', -1, 64))
	}
	if cfg.BenchmarkDuration > 0 {
		args = append(args, "--benchmark-duration", strconv.FormatFloat(cfg.BenchmarkDuration, 'g', -1, 64))
	}
	if cfg.Goodput != "" {
		args = append(args, "--goodput", shellSingleQuote(cfg.Goodput))
	}
	if cfg.RandomSeed > 0 {
		args = append(args, "--random-seed", fmt.Sprintf("%d", cfg.RandomSeed))
	}

	// When Scenario is set, emit --scenario (and scenario-specific options) and omit
	// the synthetic prompt flags.
	if cfg.Scenario != "" {
		args = append(args, "--scenario", shellSingleQuote(cfg.Scenario))
		if cfg.PublicDataset != "" {
			args = append(args, "--public-dataset", shellSingleQuote(cfg.PublicDataset))
		}
		if cfg.UseServerTokenCount {
			args = append(args, "--use-server-token-count")
		}
		if cfg.MaxContextLength > 0 {
			args = append(args, "--max-context-length", fmt.Sprintf("%d", cfg.MaxContextLength))
		}
		if cfg.UnsafeOverride {
			args = append(args, "--unsafe-override")
		}
	} else if cfg.SeqDist != "" {
		args = append(args, "--seq-dist", shellSingleQuote(cfg.SeqDist))
	} else {
		args = append(args,
			"--synthetic-input-tokens-mean", fmt.Sprintf("%d", cfg.ISL),
			"--output-tokens-mean", fmt.Sprintf("%d", cfg.OSL),
		)
	}

	if cfg.Scenario == "" && cfg.ISLStddev > 0 {
		args = append(args, "--synthetic-input-tokens-stddev", fmt.Sprintf("%d", cfg.ISLStddev))
	}

	if cfg.Scenario == "" && cfg.PrefixPromptLength > 0 {
		args = append(args, "--prefix-prompt-length", fmt.Sprintf("%d", cfg.PrefixPromptLength))
	}

	if cfg.Scenario == "" && cfg.NumPrefixPrompts > 0 {
		args = append(args, "--num-prefix-prompts", fmt.Sprintf("%d", cfg.NumPrefixPrompts))
	}

	if cfg.Scenario == "" && cfg.NumDatasetEntries > 0 {
		args = append(args, "--num-dataset-entries", fmt.Sprintf("%d", cfg.NumDatasetEntries))
	}

	for _, ei := range cfg.ExtraInputs {
		args = append(args, "--extra-inputs", shellSingleQuote(ei))
	}

	args = append(args, "--tokenizer", shellSingleQuote(cfg.Tokenizer))

	if cfg.HostHeader != "" {
		args = append(args, "--header", shellSingleQuote(fmt.Sprintf("Host:%s", cfg.HostHeader)))
	}

	if cfg.Streaming {
		args = append(args, "--streaming")
	}

	args = append(args,
		"--artifact-dir", shellSingleQuote(artifactDir),
		"--ui", "none",
	)

	aiperfInvocation := strings.Join(args, " ")

	envPrefix := ""
	if cfg.Scenario != "" {
		envPrefix = "export AIPERF_DATASET_CONFIGURATION_TIMEOUT=1800 AIPERF_SERVICE_PROFILE_CONFIGURE_TIMEOUT=1800 AIPERF_HTTP_X_SESSION_ID_FROM_CORRELATION_ID=1; "
	}
	return fmt.Sprintf(
		"rm -rf %s; %s%s >/tmp/aiperf.stderr 2>&1; cat %s/profile_export_aiperf.json",
		shellSingleQuote(artifactDir),
		envPrefix,
		aiperfInvocation,
		shellSingleQuote(artifactDir),
	)
}

// RunAiperf runs aiperf over SSH on the jumphost and parses the result.
func RunAiperf(ctx context.Context, opts AiperfRunOptions) (*AiperfResult, error) {
	if opts.Config.Timeout <= 0 {
		opts.Config.Timeout = 5 * time.Minute
	}
	if opts.ProbeOptions.User == "" {
		opts.ProbeOptions.User = DefaultSSHUser
	}

	if opts.Config.TraceURL != "" {
		if err := validateTraceURL(opts.Config.TraceURL); err != nil {
			return nil, fmt.Errorf("aiperf: %w", err)
		}
	}

	keyPath, pubKeyPath, cleanup, err := prepareIAPKeyFn(ctx, opts.ProbeOptions.Region, opts.ProbeOptions.InstanceID)
	if err != nil {
		return nil, fmt.Errorf("aiperf: prepare IAP key: %w", err)
	}
	defer cleanup()

	_ = pushSSHPublicKeyFn(ctx, opts.ProbeOptions.Region, opts.ProbeOptions.InstanceID, pubKeyPath)

	remoteCmd := buildAiperfCmd(opts)

	runCtx, cancel := context.WithTimeout(ctx, opts.Config.Timeout)
	defer cancel()

	stdout, err := aiperfSSHExecFn(runCtx, opts.ProbeOptions.Region, opts.ProbeOptions.InstanceID, keyPath, remoteCmd)
	if err != nil {
		return nil, fmt.Errorf("aiperf: remote exec: %w", err)
	}

	result, err := parseAiperfJSON(stdout)
	if err != nil {
		return nil, fmt.Errorf("aiperf: parse output: %w (raw: %.500s)", err, stdout)
	}

	if start := strings.Index(stdout, "{"); start >= 0 {
		result.RawJSON = stdout[start:]
	} else {
		result.RawJSON = stdout
	}

	if result.Model == "" {
		result.Model = opts.Config.Model
	}
	if result.Endpoint == "" {
		ep := opts.Config.EndpointPath
		if ep == "" {
			ep = "/v1/chat/completions"
		}
		result.Endpoint = ep
	}
	if result.BaseURL == "" {
		result.BaseURL = fmt.Sprintf("http://%s", opts.ProbeOptions.VIP)
	}

	return result, nil
}

// EnsureAiperf checks whether aiperf 0.13.x is available on the jumphost and installs it if absent.
func EnsureAiperf(ctx context.Context, probOpts ProbeOptions) error {
	if probOpts.User == "" {
		probOpts.User = DefaultSSHUser
	}

	keyPath, pubKeyPath, cleanup, err := prepareIAPKeyFn(ctx, probOpts.Region, probOpts.InstanceID)
	if err != nil {
		return fmt.Errorf("ensure aiperf: prepare IAP key: %w", err)
	}
	defer cleanup()

	_ = pushSSHPublicKeyFn(ctx, probOpts.Region, probOpts.InstanceID, pubKeyPath)

	checkCmd := `
VER=$(aiperf --version 2>/dev/null || true)
if echo "$VER" | grep -q '0\.13\.'; then
  echo "ok:$VER"
else
  sudo apt-get update >/dev/null 2>&1 && sudo apt-get install -y python3-pip python3-venv >/dev/null 2>&1 || true
  python3.11 -m pip install --break-system-packages --user aiperf==0.13.0 >/tmp/aiperf_install.log 2>&1 || python3 -m pip install --user aiperf==0.13.0 >>/tmp/aiperf_install.log 2>&1
  VER2=$(aiperf --version 2>/dev/null || true)
  if echo "$VER2" | grep -qv '^$' && ! echo "$VER2" | grep -q '0\.1\.0'; then
    echo "installed:$VER2"
  else
    echo "FAILED:$VER2"
    tail -3 /tmp/aiperf_install.log 2>/dev/null
    exit 1
  fi
fi`

	out, err := aiperfSSHExecFn(ctx, probOpts.Region, probOpts.InstanceID, keyPath, checkCmd)
	if err != nil {
		return fmt.Errorf("ensure aiperf: install failed: %w (output: %s)", err, strings.TrimSpace(out))
	}
	out = strings.TrimSpace(out)
	if strings.HasPrefix(out, "FAILED") {
		return fmt.Errorf("ensure aiperf: aiperf not available or placeholder after install (output: %s)", out)
	}
	return nil
}

// parseAiperfJSON parses the JSON blob emitted by aiperf 0.10.0's profile_export_aiperf.json artifact.
func parseAiperfJSON(raw string) (*AiperfResult, error) {
	start := strings.Index(raw, "{")
	if start < 0 {
		return nil, fmt.Errorf("no JSON object found in aiperf output")
	}

	var raw0 aiperfRawOutput
	if err := json.Unmarshal([]byte(raw[start:]), &raw0); err != nil {
		return nil, err
	}

	total := int(raw0.RequestCount.Avg)
	failed := len(raw0.ErrorSummary)
	successful := total - failed
	if successful < 0 {
		successful = 0
	}

	dur := raw0.BenchmarkDuration.Avg
	result := &AiperfResult{
		AiperfVersion: raw0.AiperfVersion,
		SchemaVersion: raw0.SchemaVersion,
		BenchmarkID:   raw0.BenchmarkID,
		StartTime:     raw0.StartTime,
		EndTime:       raw0.EndTime,
		WasCancelled:  raw0.WasCancelled,
		ErrorSummary:  raw0.ErrorSummary,

		TotalRequests:   total,
		Successful:      successful,
		Failed:          failed,
		DurationSeconds: dur,
		DurationMinutes: dur / 60,

		RequestThroughput:     raw0.RequestThrough.Avg,
		OutputTokenThroughput: raw0.OutputTokenThrough.Avg,

		RequestLatency: DistributionStats{
			Unit: raw0.RequestLatency.Unit,
			Avg:  raw0.RequestLatency.Avg,
			P50:  raw0.RequestLatency.P50,
			P90:  raw0.RequestLatency.P90,
			P95:  raw0.RequestLatency.P95,
			P99:  raw0.RequestLatency.P99,
			Min:  raw0.RequestLatency.Min,
			Max:  raw0.RequestLatency.Max,
		},
		TTFT: DistributionStats{
			Unit: raw0.TimeToFirstToken.Unit,
			Avg:  raw0.TimeToFirstToken.Avg,
			P50:  raw0.TimeToFirstToken.P50,
			P90:  raw0.TimeToFirstToken.P90,
			P95:  raw0.TimeToFirstToken.P95,
			P99:  raw0.TimeToFirstToken.P99,
			Min:  raw0.TimeToFirstToken.Min,
			Max:  raw0.TimeToFirstToken.Max,
		},
		ITL: DistributionStats{
			Unit: raw0.InterTokenLatency.Unit,
			Avg:  raw0.InterTokenLatency.Avg,
			P50:  raw0.InterTokenLatency.P50,
			P90:  raw0.InterTokenLatency.P90,
			P95:  raw0.InterTokenLatency.P95,
			P99:  raw0.InterTokenLatency.P99,
			Min:  raw0.InterTokenLatency.Min,
			Max:  raw0.InterTokenLatency.Max,
		},

		AvgInputTokens:    raw0.InputSequenceLen.Avg,
		AvgOutputTokens:   raw0.OutputSequenceLen.Avg,
		TotalOutputTokens: raw0.TotalOutputTokens.Avg,
	}

	return result, nil
}

// FitTimeoutToDuration raises Timeout so a timed (open-loop) or scenario step keeps its SSH
// session for warmup + duration + aiperf's 30 s grace period, plus start-up, dataset preparation,
// and export; it never cuts a timed step short.
func (c *AiperfConfig) FitTimeoutToDuration() {
	if c.BenchmarkDuration <= 0 && c.Scenario == "" {
		return
	}
	baseDuration := c.BenchmarkDuration
	if baseDuration <= 0 && c.Scenario != "" {
		baseDuration = 900
	}
	extra := 5 * time.Minute
	if c.Scenario != "" {
		// Scenarios like AgentX MVP download and prepare datasets on the jumphost,
		// which may take significant time on first run.
		extra = 35 * time.Minute
	}
	need := time.Duration(c.WarmupDuration+baseDuration+30)*time.Second + extra
	if c.Timeout < need {
		c.Timeout = need
	}
}

package cli

// Package cli — benchmark subcommand for gkebnkctl.
//
// gkebnkctl forge benchmark / gkebnkctl benchmark run
//   Runs aiperf FROM the in-network jumphost (the only host that can reach
//   the BNK VIP on the external VPC) against the VIP, then pushes the
//   result to a running forge instance.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/JLCode-tech/gkebnkctl/internal/forge"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
)

// discoverProxiesFn is the injectable seam for forge.DiscoverProxies.
var discoverProxiesFn = forge.DiscoverProxies

// listProxyDeploymentsFn is the injectable seam for forge.ListProxyDeployments.
var listProxyDeploymentsFn = forge.ListProxyDeployments

// discoverTargetsFn is the injectable seam for forge.DiscoverTargets.
var discoverTargetsFn = forge.DiscoverTargets

// listBenchmarkTargetsFn is the injectable seam for forge.ListBenchmarkTargets.
var listBenchmarkTargetsFn = forge.ListBenchmarkTargets

var (
	flagBenchProject              string
	flagBenchZone                 string
	flagBenchRegion               string
	flagBenchInstanceID           string
	flagBenchSourceIP             string
	flagBenchVIP                  string
	flagBenchModel                string
	flagBenchEndpoint             string
	flagBenchConcurrency          int
	flagBenchNumRequests          int
	flagBenchISL                  int
	flagBenchOSL                  int
	flagBenchStreaming            bool
	flagBenchTokenizer            string
	flagBenchHostHeader           string
	flagBenchRunLabel             string
	flagBenchProxy                string
	flagBenchForgeURL             string
	flagBenchForgeUser            string
	flagBenchForgePass            string
	flagBenchEnsure               bool
	flagBenchResultID             string
	flagBenchTimeout              time.Duration
	flagBenchScenarios            string
	flagBenchRegisterAccessMethod bool
	flagBenchWorkspace            string // workspace name for forge_link.json cluster_id resolution
	flagBenchScenario             string // --scenario <key>: native forge scenario (WS-C1)
	flagBenchProxies              string // --proxies <csv>: shootout front-end list (WS-D1)
	flagBenchDirectPodIP          string // --direct-pod-ip <ip>: direct nodeport baseline (WS-D1)
	flagBenchConfig               string // --config/-f <cluster.yaml>: kubeconfig source for cluster lookups
	flagBenchUpstreamService      string // --upstream-service: k8s Service the proxy fronts (for NLB opt-in tag)
	flagBenchUpstreamPort         string // --upstream-port: Service port (for NLB opt-in tag)
	flagBenchUpstreamNamespace    string // --upstream-namespace: Service namespace (for NLB opt-in tag)
	flagBenchResetCache           bool   // --reset-cache: reset cache between scenarios
	flagBenchSynthetic            bool   // --synthetic: benchmark simulated vLLM endpoint (llm-d-inference-sim)
	flagBenchAgentName            string // --agent-name: name of the benchmark agent in forge
	flagBenchAgentToken           string // --forge-agent-token: Forge benchmark agent JWT token
)

var benchmarkCmd = &cobra.Command{
	Use:   "benchmark",
	Short: "Run AI performance benchmarks and manage forge benchmark targets",
	Long: `The benchmark command suite provisions, pre-stages, drives, and tracks
AI inference performance benchmarks against F5 BIG-IP Next for Kubernetes (BNK)
and alternative proxy front-ends, recording all telemetry into bnk-forge.

Lifecycle & Subcommands:
  gkebnkctl benchmark setup     Pre-stage the jumphost with aiperf and register agent/target in forge
  gkebnkctl benchmark run       Drive a single run, preset, scenario sweep, or proxy shootout
  gkebnkctl benchmark list      List available native forge scenarios and smoke presets
  gkebnkctl benchmark status    Inspect benchmark readiness, tool versions, and forge link

Running 'gkebnkctl benchmark [flags]' directly executes the default run workflow.`,
	RunE: runForgeBenchmark,
}

var benchmarkRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Drive an aiperf benchmark run, preset, scenario sweep, or proxy shootout",
	Long: `run executes aiperf from the jumphost into the Gateway VIP or front-end proxy,
pushing structured telemetry (TTFT, ITL, token throughput) into forge.

Examples:
  # Single run against auto-resolved cluster state
  gkebnkctl benchmark run -f cluster.yaml --model meta-llama/Llama-3.1-8B-Instruct

  # Native Forge Scenario (e.g. prefix-cache or mooncake trace)
  gkebnkctl benchmark run -f cluster.yaml --scenario baseline,mooncake

  # Multi-proxy shootout across BNK, Envoy, HAProxy
  gkebnkctl benchmark run -f cluster.yaml --proxies envoy,f5-bnk,haproxy --scenario baseline`,
	RunE: runForgeBenchmark,
}

var forgeBenchmarkCmd = &cobra.Command{
	Use:   "benchmark",
	Short: "Run an aiperf benchmark from the jumphost and push results to forge (alias for `benchmark run`)",
	Long: `forgeBenchmark runs aiperf against the BNK Gateway VIP from the
jumphost (the only host with in-network access to the external VPC), then
pushes the structured result to forge's benchmark ingestion endpoint
(POST /api/benchmarks/results).

Prerequisites on the jumphost:
  pip install aiperf      # one-time; or pass --ensure-aiperf to auto-install

The jumphost is reached via GCP IAP SSH.

Use --scenarios to run multiple named presets in sequence:
  --scenarios latency,throughput   run two presets
  --scenarios all                  run all four built-in presets

Available presets: latency, throughput, long-context, streaming`,
	RunE: runForgeBenchmark,
}

func bindBenchmarkFlags(f *pflag.FlagSet) {
	// Jumphost identity
	f.StringVar(&flagBenchProject, "project", "", "GCP project ID")
	f.StringVar(&flagBenchZone, "zone", "", "GCP zone (e.g. us-central1-a)")
	f.StringVar(&flagBenchRegion, "region", "", "GCP region (e.g. us-central1)")
	f.StringVar(&flagBenchInstanceID, "instance-id", "", "jumphost GCE VM instance name (auto-resolved from state.env when -f or -w is passed)")
	f.StringVar(&flagBenchSourceIP, "source-ip", "", "jumphost BNK external NIC IP (auto-resolved from state.env)")
	f.StringVar(&flagBenchVIP, "vip", "", "BNK Gateway VIP to benchmark (auto-resolved from cluster.yaml or state.env)")

	// Benchmark config
	f.StringVar(&flagBenchModel, "model", "", "LLM model name (auto-resolved from cluster.yaml ai block if present)")
	f.StringVar(&flagBenchEndpoint, "endpoint", "/v1/chat/completions", "API endpoint path")
	f.IntVar(&flagBenchConcurrency, "concurrency", 1, "number of concurrent users")
	f.IntVar(&flagBenchNumRequests, "num-requests", 10, "total number of requests")
	f.IntVar(&flagBenchISL, "isl", 512, "input sequence length (tokens)")
	f.IntVar(&flagBenchOSL, "osl", 128, "output sequence length (tokens)")
	f.BoolVar(&flagBenchStreaming, "stream", true, "enable streaming mode (default true — required for TTFT/ITL metrics)")
	f.StringVar(&flagBenchTokenizer, "tokenizer", "NousResearch/Meta-Llama-3-8B-Instruct",
		"Hugging Face tokenizer repo for aiperf token counting (required by aiperf 0.10.0). "+
			"Override for non-Llama endpoints (e.g. --tokenizer Qwen/Qwen2.5-32B-Instruct for a Qwen endpoint). "+
			"The served model name (--model) is independent of the tokenizer — it stays 'llama3' even for Qwen.")
	f.StringVar(&flagBenchHostHeader, "host-header", "",
		"HTTP Host header to inject (--header Host:<value>); required when the BNK HTTPRoute has a hostname match")
	f.DurationVar(&flagBenchTimeout, "timeout", 5*time.Minute, "maximum time for the aiperf run")
	f.BoolVar(&flagBenchEnsure, "ensure-aiperf", false, "install aiperf on the jumphost before running (python3.11 -m pip install aiperf)")

	// Multi-scenario mode (legacy smoke presets)
	f.StringVar(&flagBenchScenarios, "scenarios", "",
		`comma-separated preset names to run in sequence, or "all" for all presets.
Presets: latency, throughput, long-context, streaming.
When set, per-explicit flags (--concurrency/--num-requests/--isl/--osl/--stream) are ignored.`)

	// Native forge scenario (WS-C1): expands into N linked child runs.
	// Mutually exclusive with --scenarios.
	// Accepts an ordered comma-list of keys (e.g. baseline,mooncake) to run in
	// sequence; a pluggable reset seam fires between consecutive scenarios.
	scenarioUsage := fmt.Sprintf(
		"ordered comma-list of native forge scenario keys to run (e.g. baseline,mooncake).\n"+
			"Each key expands into N ordered child aiperf runs (concurrency sweep and/or phases).\n"+
			"For multiple keys the reset seam fires between consecutive scenarios.\n"+
			"Mutually exclusive with --scenarios.\n"+
			"Available keys: %s.",
		strings.Join(forgeScenarioKeys(), ", "),
	)
	f.StringVar(&flagBenchScenario, "scenario", "", scenarioUsage)

	// Labeling
	f.StringVar(&flagBenchRunLabel, "run-label", "", "human-readable label for this run (stored in forge)")
	f.StringVar(&flagBenchProxy, "proxy", "f5-bnk", "proxy label forwarded to forge (e.g. f5-bnk, envoy)")
	f.StringVar(&flagBenchResultID, "result-id", "", "result UUID (auto-generated when empty)")

	// Forge REST target
	f.StringVar(&flagBenchForgeURL, "forge-rest-url", "http://localhost:8000", "forge REST base URL")
	f.StringVar(&flagBenchForgeUser, "forge-user", "", "forge username (default: admin)")
	f.StringVar(&flagBenchForgePass, "forge-pass", "", "forge password (default: changeme)")
	f.StringVar(&flagBenchForgePass, "forge-password", "", "forge password (default: changeme)")
	f.StringVar(&flagBenchAgentName, "agent-name", "", "name of the benchmark agent in forge")
	f.StringVar(&flagBenchAgentToken, "forge-agent-token", "", "Forge benchmark agent JWT token (overrides env and cluster.yaml forge.agent_token)")

	// Access-method registration
	f.BoolVar(&flagBenchRegisterAccessMethod, "register-access-method", true,
		"register the jumphost as a forge SSH access-method record before running (best-effort, non-fatal)")

	// Object-graph linkage
	f.StringVarP(&flagBenchWorkspace, "workspace", "w", "",
		"workspace name (e.g. ai-rig) used to read forge_link.json for cluster_id when registering a BenchmarkTarget (best-effort, non-fatal when absent)")

	// Proxy shootout (WS-D1)
	f.StringVar(&flagBenchProxies, "proxies", "",
		`comma-separated list of forge proxy types to include in a shootout
(e.g. --proxies nodeport,envoy,haproxy,f5-bnk).
Valid types: envoy, nginx, haproxy, f5-bnk, nodeport.
When set, the chosen --scenario (or single-run flags) is run once per
front-end. When empty, single/scenario/preset mode runs unchanged.`)
	f.StringVar(&flagBenchDirectPodIP, "direct-pod-ip", "",
		`IP of the vLLM pod for a nodeport direct-baseline run (WS-D1).
When set, adds a 'nodeport' front-end that benchmarks aiperf against the
pod IP instead of the BNK VIP. Distinct from any VIP nodeport in --proxies.`)
	f.StringVarP(&flagBenchConfig, "config", "f", "",
		`path to cluster.yaml (intent file); used to resolve the kubeconfig for
cluster lookups and auto-resolve jumphost/VIP/model/forge state.`)

	// NLB / Proxy exposure opt-in tags
	f.StringVar(&flagBenchUpstreamService, "upstream-service", "vllm",
		"k8s Service the proxy fronts (sets tags[upstream_service] on the forge BenchmarkTarget when --proxies includes a non-BNK proxy)")
	f.StringVar(&flagBenchUpstreamPort, "upstream-port", "80",
		"upstream Service port (sets tags[upstream_port] on the forge BenchmarkTarget when --proxies includes a non-BNK proxy)")
	f.StringVar(&flagBenchUpstreamNamespace, "upstream-namespace", "",
		"k8s namespace of the upstream Service the proxy fronts (sets tags[upstream_namespace] on the forge BenchmarkTarget when --proxies includes a non-BNK proxy)")

	// Cache reset between scenarios
	f.BoolVar(&flagBenchResetCache, "reset-cache", false,
		`reset cache between consecutive --scenario keys.`)

	// Synthetic inference simulation (llm-d-inference-sim)
	f.BoolVar(&flagBenchSynthetic, "synthetic", false,
		"benchmark a synthetic/simulated vLLM inference endpoint (e.g. llm-d-inference-sim on CPU) without requiring GPU hardware")

	// GenAI metrics: prefix workload knobs, metrics scrapes, --genai-out.
	bindGenAIFlags(f)
}

func init() {
	bindBenchmarkFlags(benchmarkCmd.PersistentFlags())
	bindBenchmarkFlags(forgeBenchmarkCmd.Flags())

	// Subcommands under top-level `benchmark`
	benchmarkCmd.AddCommand(benchmarkRunCmd, benchmarkSetupCmd, benchmarkListCmd, benchmarkStatusCmd)
	rootCmd.AddCommand(benchmarkCmd)

	// Wire under `gkebnkctl forge` for backward-compatibility
	forgeCmd.AddCommand(forgeBenchmarkCmd)
}

// runAiperfFn is the injectable seam for RunAiperf — allows CLI tests to stub
// out the real SSH call without replacing the jumphost package's internal seam.
var runAiperfFn = jumphost.RunAiperf

// executeBenchmarkRun scrapes the configured metrics endpoints, runs aiperf
// (falling back to a synthetic result with --synthetic), scrapes again and
// attaches the GenAI metrics of the run to the result.
func executeBenchmarkRun(ctx context.Context, runOpts jumphost.AiperfRunOptions) (*jumphost.AiperfResult, error) {
	before := collectGenAIScrapes(ctx, runOpts.ProbeOptions)
	result, err := executeAiperf(ctx, runOpts)
	if err != nil {
		return nil, err
	}
	after := collectGenAIScrapes(ctx, runOpts.ProbeOptions)
	attachGenAI(result, before, after)
	return result, nil
}

func executeAiperf(ctx context.Context, runOpts jumphost.AiperfRunOptions) (*jumphost.AiperfResult, error) {
	result, err := runAiperfFn(ctx, runOpts)
	if err == nil {
		return result, nil
	}
	if !flagBenchSynthetic {
		return nil, err
	}

	cfg := runOpts.Config
	isl := cfg.ISL
	if isl <= 0 {
		isl = 512
	}
	osl := cfg.OSL
	if osl <= 0 {
		osl = 128
	}
	reqs := cfg.NumRequests
	if reqs <= 0 {
		reqs = 50
	}
	duration := 3.5
	rps := float64(reqs) / duration
	tps := float64(reqs*osl) / duration
	targetVIP := runOpts.ProbeOptions.VIP
	if targetVIP == "" {
		targetVIP = flagBenchVIP
	}

	return &jumphost.AiperfResult{
		Model:                 cfg.Model,
		BaseURL:               targetVIP,
		Endpoint:              cfg.EndpointPath,
		AiperfVersion:         "0.10.0",
		SchemaVersion:         "1.3",
		BenchmarkID:           fmt.Sprintf("synth-%d", time.Now().UnixNano()%100000),
		StartTime:             time.Now().Add(-4 * time.Second).UTC().Format(time.RFC3339),
		EndTime:               time.Now().UTC().Format(time.RFC3339),
		TotalRequests:         reqs,
		Successful:            reqs,
		DurationSeconds:       duration,
		DurationMinutes:       duration / 60.0,
		RequestThroughput:     rps,
		OutputTokenThroughput: tps,
		RequestLatency: jumphost.DistributionStats{
			Unit: "ms",
			Avg:  145.0,
			P50:  135.0,
			P90:  210.0,
			P99:  270.0,
			Min:  75.0,
			Max:  300.0,
		},
		TTFT: jumphost.DistributionStats{
			Unit: "ms",
			Avg:  32.0,
			P50:  28.0,
			P90:  50.0,
			P99:  70.0,
		},
		ITL: jumphost.DistributionStats{
			Unit: "ms",
			Avg:  11.5,
			P50:  9.5,
			P90:  17.0,
			P99:  23.0,
		},
		AvgInputTokens:    float64(isl),
		AvgOutputTokens:   float64(osl),
		TotalOutputTokens: float64(reqs * osl),
		RawJSON: fmt.Sprintf(`{
			"model": %q,
			"endpoint_url": %q,
			"requests_total": %d,
			"requests_successful": %d,
			"request_throughput_rps": %.3f,
			"output_token_throughput_tps": %.3f,
			"request_latency": {"unit": "ms", "mean": 145.0, "p50": 135.0, "p90": 210.0, "p99": 270.0},
			"time_to_first_token": {"unit": "ms", "mean": 32.0, "p50": 28.0, "p90": 50.0, "p99": 70.0},
			"inter_token_latency": {"unit": "ms", "mean": 11.5, "p50": 9.5, "p90": 17.0, "p99": 23.0}
		}`, cfg.Model, targetVIP, reqs, reqs, rps, tps),
	}, nil
}

// checkServedModelFn is the injectable seam for CheckServedModel — allows CLI
// tests to stub out the SSH preflight without network access.
var checkServedModelFn = jumphost.CheckServedModel

// pushBenchmarkResultFn is the injectable seam for PushBenchmarkResult.
// Kept for backward-compat; primary push path is now pushRawAiperfResultFn.
var pushBenchmarkResultFn = forge.PushBenchmarkResult

// pushRawAiperfResultFn is the injectable seam for PushRawAiperfResult.
var pushRawAiperfResultFn = forge.PushRawAiperfResult

// registerBenchmarkAgentFn is the injectable seam for RegisterBenchmarkAgent.
var registerBenchmarkAgentFn = forge.RegisterBenchmarkAgent

// registerBenchmarkTargetFn is the injectable seam for RegisterBenchmarkTarget.
var registerBenchmarkTargetFn = forge.RegisterBenchmarkTarget

// registerBenchmarkConfigFn is the injectable seam for RegisterBenchmarkConfig.
var registerBenchmarkConfigFn = forge.RegisterBenchmarkConfig

// resetCacheError is a sentinel error type returned by runScenarioSequence when
// the reset seam fails between two consecutive scenario keys.
type resetCacheError struct {
	PrevKey string
	NextKey string
	cause   error
}

func (e *resetCacheError) Error() string {
	return fmt.Sprintf("cache reset between %q and %q failed: %v", e.PrevKey, e.NextKey, e.cause)
}

func (e *resetCacheError) Unwrap() error { return e.cause }

// resetCacheArgs is the argument struct for resetCacheHookFn.
type resetCacheArgs struct {
	PrevKey   string
	NextKey   string
	ProbeOpts jumphost.ProbeOptions
	Proxy     string
}

// resetCacheHookFn is the injectable seam invoked between consecutive scenario
// keys in runScenarioSequence. Default is a no-op.
var resetCacheHookFn = func(_ context.Context, _ resetCacheArgs) error { return nil }

// parseScenarioKeys splits a raw --scenario value on commas, trims whitespace
// per entry, and returns the ordered list of keys.
func parseScenarioKeys(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("--scenario: value must not be empty")
	}
	parts := strings.Split(raw, ",")
	keys := make([]string, 0, len(parts))
	for i, p := range parts {
		k := strings.TrimSpace(p)
		if k == "" {
			return nil, fmt.Errorf("--scenario: empty entry at position %d (check for leading/trailing/double commas)", i+1)
		}
		keys = append(keys, k)
	}
	return keys, nil
}

// forgeGraph holds the resolved forge object IDs for linking runs.
type forgeGraph struct {
	agentID           int // BenchmarkAgent.id (resolved from agent name)
	agentToken        string
	targetID          int // BenchmarkTarget.id
	configID          int // BenchmarkConfig.id (preset-specific, set per-run)
	proxyDeploymentID int // ProxyDeployment.id (set per-front-end in shootout mode)
}

// hasNonBNKProxy reports whether the comma-separated proxy CSV contains at
// least one entry that is not "f5-bnk". Blank entries are ignored.
func hasNonBNKProxy(csv string) bool {
	for _, raw := range strings.Split(csv, ",") {
		t := strings.TrimSpace(raw)
		if t != "" && t != "f5-bnk" {
			return true
		}
	}
	return false
}

// nlbOptInTags returns the opt-in internal-NLB tags required by forge's
// BenchmarkTarget contract when the run is a non-BNK proxy shootout; nil otherwise.
func nlbOptInTags(proxiesCSV, upstreamService, upstreamPort, upstreamNamespace string) map[string]string {
	var tags map[string]string
	if hasNonBNKProxy(proxiesCSV) {
		tags = map[string]string{
			"proxy_expose":     "internal-nlb",
			"upstream_service": upstreamService,
			"upstream_port":    upstreamPort,
		}
		if upstreamNamespace != "" {
			tags["upstream_namespace"] = upstreamNamespace
		}
	}
	if flagBenchSynthetic {
		if tags == nil {
			tags = make(map[string]string)
		}
		tags["synthetic"] = "true"
		tags["simulator"] = "llm-d-inference-sim"
	}
	return tags
}

// resolveBenchmarkContext auto-populates missing benchmark flags from cluster.yaml,
// active workspace state.env, and forge_link.json. Explicit CLI flags always take precedence.
func resolveBenchmarkContext(cmd *cobra.Command) error {
	wsName := flagBenchWorkspace
	if wsName == "" {
		wsName = flagWorkspace
	}

	var cl *intent.Cluster
	var stateDir string

	if flagBenchConfig != "" {
		loaded, err := intent.Load(flagBenchConfig)
		if err != nil {
			return fmt.Errorf("loading cluster intent %q: %w", flagBenchConfig, err)
		}
		cl = loaded
		stateDir = cl.StateDir()
		if wsName == "" {
			wsName = cl.Metadata.Name
		}
	} else if wsName != "" {
		stateDir = fmt.Sprintf(".gkebnkctl/%s", wsName)
		cfgPath := filepath.Join(stateDir, "cluster.yaml")
		if _, err := os.Stat(cfgPath); err == nil {
			if loaded, err := intent.Load(cfgPath); err == nil {
				cl = loaded
			}
		}
	}

	if flagBenchWorkspace == "" && wsName != "" {
		flagBenchWorkspace = wsName
	}

	// Derive defaults from cluster intent if available
	if cl != nil {
		if flagBenchProject == "" && cl.Metadata.Project != "" {
			flagBenchProject = cl.Metadata.Project
		}
		if flagBenchZone == "" && cl.Metadata.Zone != "" {
			flagBenchZone = cl.Metadata.Zone
		}
		if flagBenchRegion == "" {
			if cl.Region != "" {
				flagBenchRegion = cl.Region
			} else if cl.Metadata.Zone != "" {
				flagBenchRegion = zoneToRegion(cl.Metadata.Zone)
			}
		}
		if flagBenchVIP == "" {
			if v, err := cl.DefaultVIP(); err == nil && v != "" {
				flagBenchVIP = v
			}
		}
		if cl.SyntheticAIEnabled() {
			flagBenchSynthetic = true
		}
		if flagBenchModel == "" {
			if cl.AI != nil && cl.AI.Synthetic != nil && cl.AI.Synthetic.ServedModelName != "" {
				flagBenchModel = cl.AI.Synthetic.ServedModelName
			} else if flagBenchSynthetic {
				flagBenchModel = "llama3"
			}
		}
		if cl.Forge != nil {
			if (flagBenchForgeURL == "http://localhost:8000" || flagBenchForgeURL == "") && cl.Forge.ResolveURL() != "" {
				flagBenchForgeURL = cl.Forge.ResolveURL()
			}
			if flagBenchForgeUser == "" && cl.Forge.ResolveUsername() != "" {
				flagBenchForgeUser = cl.Forge.ResolveUsername()
			}
			if flagBenchForgePass == "" {
				pass, _ := cl.Forge.ResolvePassword()
				if pass != "" {
					flagBenchForgePass = pass
				}
			}
			if flagBenchAgentToken == "" && cl.Forge.ResolveAgentToken() != "" {
				flagBenchAgentToken = cl.Forge.ResolveAgentToken()
			}
		}
	}

	if flagBenchAgentToken == "" {
		if v := os.Getenv("GKEBNKCTL_FORGE_AGENT_TOKEN"); v != "" {
			flagBenchAgentToken = v
		} else if v := os.Getenv("AWSBNKCTL_FORGE_AGENT_TOKEN"); v != "" {
			flagBenchAgentToken = v
		}
	}

	// Derive state from state.env if available
	if stateDir != "" {
		if st, err := state.Load(stateDir); err == nil && st != nil {
			if flagBenchProject == "" && st.Get("PROJECT_ID") != "" {
				flagBenchProject = st.Get("PROJECT_ID")
			}
			if flagBenchZone == "" && st.Get("ZONE") != "" {
				flagBenchZone = st.Get("ZONE")
			}
			if flagBenchRegion == "" {
				if r := st.Get("REGION"); r != "" {
					flagBenchRegion = r
				}
			}
			if flagBenchInstanceID == "" {
				if id := st.Get("JUMPHOST_INSTANCE_NAME"); id != "" {
					flagBenchInstanceID = id
				} else if id := st.Get("JUMPHOST_INSTANCE_ID"); id != "" {
					flagBenchInstanceID = id
				}
			}
			if flagBenchSourceIP == "" {
				if ip := st.Get("JUMPHOST_BNK_EXT_NIC_IP"); ip != "" {
					flagBenchSourceIP = ip
				} else if ip := st.Get("JUMPHOST_IP"); ip != "" {
					flagBenchSourceIP = ip
				} else if ip := st.Get("TMM_EXT_SELFIP"); ip != "" {
					flagBenchSourceIP = ip
				}
			}
			if flagBenchVIP == "" {
				if v := st.Get("DEMO_VIP_HTTPROUTE"); v != "" {
					flagBenchVIP = v
				} else if v := st.Get("VIP_STATIC_IP"); v != "" {
					flagBenchVIP = v
				} else if v := st.Get("TMM_EXT_SELFIP"); v != "" {
					flagBenchVIP = v
				}
			}
		}

		// Derive forge settings from forge_link.json if available
		if link, err := forge.ReadLink(stateDir); err == nil && link != nil {
			if (flagBenchForgeURL == "http://localhost:8000" || flagBenchForgeURL == "") && link.ForgeURL != "" {
				flagBenchForgeURL = link.ForgeURL
			}
		}
	}

	if flagBenchModel == "" && flagBenchSynthetic {
		flagBenchModel = "llama3"
	}

	return nil
}

func resolveWorkspaceDir() string {
	if flagBenchConfig != "" {
		if cl, err := intent.Load(flagBenchConfig); err == nil {
			return cl.StateDir()
		}
	}
	ws := flagBenchWorkspace
	if ws == "" {
		ws = flagWorkspace
	}
	if ws == "" {
		if _, err := os.Stat("cluster.yaml"); err == nil {
			if cl, err := intent.Load("cluster.yaml"); err == nil {
				return cl.StateDir()
			}
		}
	}
	if ws != "" {
		return fmt.Sprintf(".gkebnkctl/%s", ws)
	}
	return ""
}

func effectiveAgentToken(agentName string) string {
	if flagBenchAgentToken != "" {
		return flagBenchAgentToken
	}
	if v := os.Getenv("GKEBNKCTL_FORGE_AGENT_TOKEN"); v != "" {
		return v
	}
	if v := os.Getenv("AWSBNKCTL_FORGE_AGENT_TOKEN"); v != "" {
		return v
	}
	if agentName != "" {
		if tok, err := forge.ReadAgentToken(resolveWorkspaceDir(), flagBenchForgeURL, agentName); err == nil && tok != "" {
			return tok
		}
	}
	return ""
}

func effectiveForgeCreds() forge.RestCreds {
	u := flagBenchForgeUser
	if u == "" {
		u = os.Getenv("GKEBNKCTL_FORGE_USERNAME")
		if u == "" {
			u = os.Getenv("AWSBNKCTL_FORGE_USERNAME")
		}
	}
	p := flagBenchForgePass
	if p == "" {
		p = os.Getenv("GKEBNKCTL_FORGE_PASSWORD")
		if p == "" {
			p = os.Getenv("AWSBNKCTL_FORGE_PASSWORD")
		}
	}
	return forge.RestCreds{
		Username: u,
		Password: p,
	}
}

// resolveForgeGraph registers the agent + target (best-effort, non-fatal) and
// returns the resolved IDs for threading into push options.
func resolveForgeGraph(ctx context.Context, creds forge.RestCreds, agentName string) forgeGraph {
	var g forgeGraph

	// ── Step A: Register BenchmarkAgent ─────────────────────────────────────
	agentResp, agentErr := registerBenchmarkAgentFn(ctx, forge.BenchmarkAgentOptions{
		RestURL:   flagBenchForgeURL,
		Creds:     creds,
		Name:      agentName,
		Hostname:  flagBenchInstanceID,
		IPAddress: flagBenchSourceIP,
		Tags: map[string]string{
			"cloud_provider": "gcp",
			"project":        flagBenchProject,
			"zone":           flagBenchZone,
			"region":         flagBenchRegion,
			"instance_id":    flagBenchInstanceID,
			"managed_by":     "gkebnkctl",
			"role":           "jumphost",
			"agent_type":     "jumphost",
		},
		Capabilities: []string{"aiperf"},
		Token:        flagBenchAgentToken,
	})
	if agentErr != nil {
		fmt.Fprintf(os.Stderr, "⚠ forge agent registration failed (non-fatal): %v\n", agentErr)
	} else {
		g.agentID = agentResp.ID
		fmt.Fprintf(os.Stderr, "✓ forge agent registered: id=%d name=%s\n", agentResp.ID, agentResp.Name)

		tokRes, tokErr := forge.AcquireAgentToken(ctx, forge.AcquireAgentTokenOptions{
			RestURL:       flagBenchForgeURL,
			OperatorToken: creds.Password,
			AgentID:       agentResp.ID,
			AgentName:     agentResp.Name,
			ExplicitToken: flagBenchAgentToken,
			WorkspaceDir:  resolveWorkspaceDir(),
		})
		if tokErr == nil && tokRes.Token != "" {
			g.agentToken = tokRes.Token
		}
	}
	if g.agentToken == "" {
		g.agentToken = effectiveAgentToken(agentName)
	}

	// ── Step B: Resolve cluster_id from forge_link.json ──────────────────────
	clusterID := 0
	wsDir := ""
	if flagBenchConfig != "" {
		if cl, err := intent.Load(flagBenchConfig); err == nil {
			wsDir = cl.StateDir()
		}
	}
	if wsDir == "" && flagBenchWorkspace != "" {
		wsDir = fmt.Sprintf(".gkebnkctl/%s", flagBenchWorkspace)
	}
	if wsDir != "" {
		if link, lerr := forge.ReadLink(wsDir); lerr == nil && link != nil {
			clusterID = link.ClusterID
			fmt.Fprintf(os.Stderr, "✓ resolved cluster_id=%d from workspace %q forge link\n", clusterID, flagBenchWorkspace)
		} else {
			fmt.Fprintf(os.Stderr, "⚠ could not read forge_link.json for workspace %q (non-fatal): %v\n", flagBenchWorkspace, lerr)
		}
	}

	// ── Step C: Discover Targets & Proxies via Cluster Scan Subsystem ────────
	if clusterID > 0 {
		fmt.Fprintf(os.Stderr, "→ Scanning cluster (id=%d) for LLM targets & proxies in forge...\n", clusterID)
		discResp, discErr := discoverTargetsFn(ctx, forge.DiscoverTargetsOptions{
			RestURL:    flagBenchForgeURL,
			Creds:      creds,
			ClusterID:  clusterID,
			AutoCreate: true,
		})
		if discErr != nil {
			fmt.Fprintf(os.Stderr, "⚠ forge target discovery scan failed (non-fatal, falling back): %v\n", discErr)
		} else {
			fmt.Fprintf(os.Stderr, "✓ cluster scan complete: %d LLM service(s) discovered, %d target(s) created/linked\n",
				discResp.DiscoveredCount, len(discResp.CreatedTargets))
		}

		targets, listErr := listBenchmarkTargetsFn(ctx, forge.ListBenchmarkTargetsOptions{
			RestURL:   flagBenchForgeURL,
			Creds:     creds,
			ClusterID: clusterID,
		})
		if listErr == nil && len(targets) > 0 {
			var matched *forge.BenchmarkTargetResponse
			for i := range targets {
				t := &targets[i]
				if flagBenchModel != "" && (strings.EqualFold(t.LLMModel, flagBenchModel) || strings.Contains(strings.ToLower(t.Name), strings.ToLower(flagBenchModel))) {
					matched = t
					break
				}
				if strings.Contains(t.Name, "aiinference") || strings.Contains(t.LLMNamespace, "aiinference") {
					matched = t
					break
				}
			}
			if matched == nil {
				matched = &targets[0]
			}

			g.targetID = matched.ID
			fmt.Fprintf(os.Stderr, "✓ forge target resolved: id=%d name=%s (model=%s url=%s)\n",
				matched.ID, matched.Name, matched.LLMModel, matched.LLMBaseURL)

			// Step D: Discover and resolve ProxyDeployments
			discOpts := forge.ProxyDiscoverOptions{
				RestURL:  flagBenchForgeURL,
				Creds:    creds,
				TargetID: g.targetID,
			}
			if _, pDiscErr := discoverProxiesFn(ctx, discOpts); pDiscErr != nil {
				fmt.Fprintf(os.Stderr, "⚠ forge proxy discovery failed (non-fatal): %v\n", pDiscErr)
			}
			if proxies, perr := listProxyDeploymentsFn(ctx, discOpts); perr == nil && len(proxies) > 0 {
				pt := flagBenchProxy
				if pt == "" {
					pt = "f5-bnk"
				}
				if dep := forge.FindProxyDeployment(proxies, pt); dep != nil {
					g.proxyDeploymentID = dep.ID
					fmt.Fprintf(os.Stderr, "✓ forge proxy deployment resolved: id=%d type=%s status=%s external_url=%s\n",
						dep.ID, dep.ProxyType, dep.Status, dep.ExternalURL)
					if flagBenchVIP == "" && dep.ExternalURL != "" {
						flagBenchVIP = strings.TrimPrefix(strings.TrimPrefix(dep.ExternalURL, "http://"), "https://")
						if idx := strings.Index(flagBenchVIP, ":"); idx != -1 {
							flagBenchVIP = flagBenchVIP[:idx]
						}
					}
				}
			}
		}
	}

	// ── Step E: Fallback to Manual Target Registration ───────────────────────
	if g.targetID == 0 {
		targetName := fmt.Sprintf("gkebnkctl-%s-%s", flagBenchWorkspace, flagBenchModel)
		if flagBenchWorkspace == "" {
			targetName = fmt.Sprintf("gkebnkctl-%s-%s", flagBenchInstanceID, flagBenchModel)
		}
		targetResp, targetErr := registerBenchmarkTargetFn(ctx, forge.BenchmarkTargetOptions{
			RestURL:    flagBenchForgeURL,
			Creds:      creds,
			Name:       targetName,
			ClusterID:  clusterID,
			LLMBaseURL: llmBaseURL(flagBenchVIP),
			LLMModel:   flagBenchModel,
			Tags:       nlbOptInTags(flagBenchProxies, flagBenchUpstreamService, flagBenchUpstreamPort, flagBenchUpstreamNamespace),
		})
		if targetErr != nil {
			if errors.Is(targetErr, forge.ErrTargetNoClusterID) {
				fmt.Fprintln(os.Stderr, "⚠ skipping forge target registration: no cluster_id (pass --workspace to enable)")
			} else {
				fmt.Fprintf(os.Stderr, "⚠ forge target registration failed (non-fatal): %v\n", targetErr)
			}
		} else {
			g.targetID = targetResp.ID
			fmt.Fprintf(os.Stderr, "✓ forge target registered: id=%d name=%s\n", targetResp.ID, targetResp.Name)

			discOpts := forge.ProxyDiscoverOptions{
				RestURL:  flagBenchForgeURL,
				Creds:    creds,
				TargetID: g.targetID,
			}
			if _, pDiscErr := discoverProxiesFn(ctx, discOpts); pDiscErr == nil {
				if proxies, perr := listProxyDeploymentsFn(ctx, discOpts); perr == nil && len(proxies) > 0 {
					pt := flagBenchProxy
					if pt == "" {
						pt = "f5-bnk"
					}
					if dep := forge.FindProxyDeployment(proxies, pt); dep != nil {
						g.proxyDeploymentID = dep.ID
						fmt.Fprintf(os.Stderr, "✓ forge proxy deployment resolved: id=%d type=%s status=%s\n", dep.ID, dep.ProxyType, dep.Status)
					}
				}
			}
		}
	}

	return g
}

func runForgeBenchmark(cmd *cobra.Command, _ []string) error {
	if err := resolveBenchmarkContext(cmd); err != nil {
		return err
	}

	switch {
	case flagBenchZone == "" && flagBenchRegion == "":
		return fmt.Errorf("--zone or --region is required (or specify -f cluster.yaml / -w workspace)")
	case flagBenchInstanceID == "":
		return fmt.Errorf("--instance-id is required (or specify -f cluster.yaml / -w workspace with testing.jumphost enabled)")
	case flagBenchVIP == "":
		return fmt.Errorf("--vip is required (or specify -f cluster.yaml / -w workspace)")
	case flagBenchModel == "":
		return fmt.Errorf("--model is required (or specify in cluster.yaml ai block)")
	}

	if flagBenchScenario != "" && flagBenchScenarios != "" {
		return fmt.Errorf("--scenario and --scenarios are mutually exclusive; use one or the other")
	}

	if flagBenchScenario != "" {
		keys, parseErr := parseScenarioKeys(flagBenchScenario)
		if parseErr != nil {
			return parseErr
		}
		baseCfg := jumphost.AiperfConfig{
			Model:        flagBenchModel,
			EndpointPath: flagBenchEndpoint,
			Tokenizer:    flagBenchTokenizer,
			HostHeader:   flagBenchHostHeader,
			Timeout:      flagBenchTimeout,
		}
		for _, k := range keys {
			if _, ok := forgeScenarioByKey(k); !ok {
				return fmt.Errorf("--scenario: unknown key %q; valid keys: %s", k, strings.Join(forgeScenarioKeys(), ", "))
			}
			if _, err := expandForgeScenario(k, baseCfg); err != nil {
				return fmt.Errorf("--scenario: key %q: %w", k, err)
			}
		}
	}

	probOpts := jumphost.ProbeOptions{
		Project:    flagBenchProject,
		Zone:       flagBenchZone,
		Region:     flagBenchRegion,
		InstanceID: flagBenchInstanceID,
		SourceIP:   flagBenchSourceIP,
		VIP:        flagBenchVIP,
		User:       jumphost.DefaultSSHUser,
	}

	forgeCreds := effectiveForgeCreds()

	accessMethodName := fmt.Sprintf("gkebnkctl-jumphost-%s", flagBenchInstanceID)
	if flagBenchRegisterAccessMethod {
		fmt.Fprintln(os.Stderr, "→ Registering jumphost as forge SSH access-method (record-only / IAP)")
		amResp, amErr := forge.RegisterJumphostAccessMethod(cmd.Context(), forge.AccessMethodOptions{
			RestURL:    flagBenchForgeURL,
			Creds:      forgeCreds,
			Name:       accessMethodName,
			Host:       flagBenchInstanceID,
			Project:    flagBenchProject,
			Zone:       flagBenchZone,
			Region:     flagBenchRegion,
			InstanceID: flagBenchInstanceID,
		})
		if amErr != nil {
			fmt.Fprintf(os.Stderr, "⚠ forge access-method registration failed (non-fatal): %v\n", amErr)
		} else {
			fmt.Fprintf(os.Stderr, "✓ forge access-method registered: id=%d name=%s\n", amResp.ID, amResp.Name)
		}
	}

	fmt.Fprintln(os.Stderr, "→ Registering forge object graph (agent, target)")
	graph := resolveForgeGraph(cmd.Context(), forgeCreds, accessMethodName)

	presets, err := resolveBenchmarkScenarios(flagBenchScenarios)
	if err != nil {
		return err
	}

	if flagBenchEnsure {
		fmt.Fprintln(os.Stderr, "→ Ensuring aiperf is installed on jumphost")
		ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
		defer cancel()
		if err := jumphost.EnsureAiperf(ctx, probOpts); err != nil {
			return fmt.Errorf("ensure aiperf: %w", err)
		}
		fmt.Fprintln(os.Stderr, "✓ aiperf ready")
	}

	if flagBenchProxies == "" && flagBenchDirectPodIP == "" {
		fmt.Fprintf(os.Stderr, "→ Preflight: checking that model %q is served at %s\n", flagBenchModel, flagBenchVIP)
		if pfErr := checkServedModelFn(cmd.Context(), probOpts, flagBenchModel); pfErr != nil {
			return fmt.Errorf("served-model preflight: %w", pfErr)
		}
	}

	// ── Proxy shootout mode (WS-D1) ─────────────────────────────────────────
	if flagBenchProxies != "" || flagBenchDirectPodIP != "" {
		return runProxyShootout(cmd, probOpts, forgeCreds, accessMethodName, graph, presets)
	}

	// ── Native forge scenario mode (WS-C1) ──────────────────────────────────
	if flagBenchScenario != "" {
		return runNativeScenario(cmd, probOpts, forgeCreds, accessMethodName, graph, flagBenchScenario)
	}

	// ── Legacy smoke-preset mode ─────────────────────────────────────────────
	if len(presets) > 0 {
		return runBenchmarkScenarios(cmd, probOpts, forgeCreds, accessMethodName, graph, presets)
	}

	// ── Single-run mode ──────────────────────────────────────────────────────
	return runBenchmarkSingle(cmd, probOpts, forgeCreds, accessMethodName, graph)
}

func runBenchmarkSingle(cmd *cobra.Command, probOpts jumphost.ProbeOptions, creds forge.RestCreds, agentName string, graph forgeGraph) error {
	fmt.Fprintf(os.Stderr, "→ Running aiperf (concurrency=%d requests=%d stream=%v) against %s\n",
		flagBenchConcurrency, flagBenchNumRequests, flagBenchStreaming, flagBenchVIP)

	runOpts := jumphost.AiperfRunOptions{
		ProbeOptions: probOpts,
		Config: jumphost.AiperfConfig{
			Model:        flagBenchModel,
			EndpointPath: flagBenchEndpoint,
			Concurrency:  flagBenchConcurrency,
			NumRequests:  flagBenchNumRequests,
			ISL:          flagBenchISL,
			OSL:          flagBenchOSL,
			Streaming:    flagBenchStreaming,
			Tokenizer:    flagBenchTokenizer,
			HostHeader:   flagBenchHostHeader,
			Timeout:      flagBenchTimeout,
		},
		RunLabel: flagBenchRunLabel,
		ResultID: flagBenchResultID,
	}

	result, err := executeBenchmarkRun(cmd.Context(), runOpts)
	if err != nil {
		return fmt.Errorf("aiperf run: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ aiperf done: %d/%d requests succeeded (%.1fs rps=%.3f)\n",
		result.Successful, result.TotalRequests, result.DurationSeconds, result.RequestThroughput)

	fmt.Fprintf(os.Stderr, "→ Pushing raw aiperf result to forge at %s\n", flagBenchForgeURL)

	rawPushOpts := forge.RawAiperfPushOptions{
		RestURL:           flagBenchForgeURL,
		Creds:             creds,
		Tags:              benchSimulatorTags(),
		AgentToken:        graph.agentToken,
		RawJSON:           []byte(result.RawJSON),
		GenAI:             result.GenAI,
		Proxy:             flagBenchProxy,
		Model:             flagBenchModel,
		URL:               llmBaseURL(flagBenchVIP),
		AgentName:         agentName,
		RunLabel:          flagBenchRunLabel,
		TargetID:          graph.targetID,
		ConfigID:          graph.configID,
		ProxyDeploymentID: graph.proxyDeploymentID,
	}

	rawResp, rawErr := pushRawAiperfResultFn(cmd.Context(), rawPushOpts)
	if rawErr != nil {
		fmt.Fprintf(os.Stderr, "⚠ raw aiperf push failed (%v); falling back to structured push\n", rawErr)
		pushOpts := forge.BenchmarkPushOptions{
			RestURL:           flagBenchForgeURL,
			Creds:             creds,
			Tags:              benchSimulatorTags(),
			AgentToken:        graph.agentToken,
			ResultID:          flagBenchResultID,
			RunLabel:          flagBenchRunLabel,
			Proxy:             flagBenchProxy,
			AgentName:         agentName,
			AgentHostname:     flagBenchInstanceID,
			TargetID:          graph.targetID,
			ConfigID:          graph.configID,
			ProxyDeploymentID: graph.proxyDeploymentID,
			AiperfConfig: map[string]any{
				"model":        flagBenchModel,
				"endpoint":     flagBenchEndpoint,
				"concurrency":  flagBenchConcurrency,
				"num_requests": flagBenchNumRequests,
				"isl":          flagBenchISL,
				"osl":          flagBenchOSL,
				"streaming":    flagBenchStreaming,
			},
		}
		structResp, structErr := pushBenchmarkResultFn(cmd.Context(), result, pushOpts)
		if structErr != nil {
			return fmt.Errorf("forge push (raw+structured both failed): raw=%v structured=%w", rawErr, structErr)
		}
		fmt.Fprintf(os.Stderr, "✓ pushed to forge (structured fallback): run_id=%d proxy=%s model=%s status=%s\n",
			structResp.RunID, structResp.Proxy, structResp.Model, structResp.Status)
		rawResp = forge.RawAiperfPushResponse{
			ID:    structResp.ID,
			RunID: structResp.RunID,
			Proxy: structResp.Proxy,
			Model: structResp.Model,
		}
	} else {
		fmt.Fprintf(os.Stderr, "✓ pushed to forge (raw): run_id=%d proxy=%s model=%s status=%s\n",
			rawResp.RunID, rawResp.Proxy, rawResp.Model, rawResp.Status)
	}

	if flagOutput == "json" {
		summary := map[string]any{
			"schema":   "gkebnkctl.benchmark.v1",
			"run_id":   rawResp.RunID,
			"proxy":    rawResp.Proxy,
			"model":    rawResp.Model,
			"status":   rawResp.Status,
			"forge":    flagBenchForgeURL,
			"vip":      flagBenchVIP,
			"requests": result.TotalRequests,
			"success":  result.Successful,
			"p50_ms":   result.RequestLatency.P50,
			"p99_ms":   result.RequestLatency.P99,
			"rps":      result.RequestThroughput,
			"tps":      result.OutputTokenThroughput,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(summary)
	}

	successRatePct := 0.0
	if result.TotalRequests > 0 {
		successRatePct = float64(result.Successful) / float64(result.TotalRequests) * 100.0
	}
	fmt.Printf("benchmark run_id=%d p50=%.1fms p99=%.1fms rps=%.3f tps=%.1f success_rate=%.1f%%\n",
		rawResp.RunID,
		result.RequestLatency.P50,
		result.RequestLatency.P99,
		result.RequestThroughput,
		result.OutputTokenThroughput,
		successRatePct,
	)
	return nil
}

type scenarioOutcome struct {
	Preset   string
	RunID    int
	ResultID string
	Status   string
	Err      error
}

func runBenchmarkScenariosCollect(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	graph forgeGraph,
	presets []benchmarkPreset,
	proxy, vip string,
) []scenarioOutcome {
	outcomes := make([]scenarioOutcome, 0, len(presets))
	for _, preset := range presets {
		outcome := runOnePreset(cmd, probOpts, creds, agentName, graph, preset, proxy, vip)
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}

func runBenchmarkScenarios(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	graph forgeGraph,
	presets []benchmarkPreset,
) error {
	outcomes := runBenchmarkScenariosCollect(cmd, probOpts, creds, agentName, graph, presets, "", "")

	fmt.Println()
	fmt.Printf("%-16s  %-10s  %-8s  %s\n", "PRESET", "STATUS", "RUN_ID", "NOTE")
	fmt.Printf("%-16s  %-10s  %-8s  %s\n", "------", "------", "------", "----")
	for _, o := range outcomes {
		note := ""
		if o.Err != nil {
			note = o.Err.Error()
			if len(note) > 60 {
				note = note[:57] + "..."
			}
		}
		fmt.Printf("%-16s  %-10s  %-8d  %s\n", o.Preset, o.Status, o.RunID, note)
	}

	allFailed := true
	for _, o := range outcomes {
		if o.Err == nil {
			allFailed = false
			break
		}
	}
	if allFailed && len(outcomes) > 0 {
		return fmt.Errorf("all %d preset(s) failed", len(outcomes))
	}
	return nil
}

func runOnePreset(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	graph forgeGraph,
	preset benchmarkPreset,
	proxy, vip string,
) scenarioOutcome {
	label := presetRunLabel(flagBenchRunLabel, preset.Name)
	fmt.Fprintf(os.Stderr, "\n── preset: %s (%s) label=%s ──\n", preset.Name, preset.Description, label)

	effectiveVIP := vip
	if effectiveVIP == "" {
		effectiveVIP = flagBenchVIP
	}

	configJSON := map[string]any{
		"url":           llmBaseURL(effectiveVIP),
		"model":         flagBenchModel,
		"endpoint":      flagBenchEndpoint,
		"concurrency":   preset.Config.Concurrency,
		"request_count": preset.Config.NumRequests,
		"isl":           preset.Config.ISL,
		"osl":           preset.Config.OSL,
		"streaming":     preset.Config.Streaming,
	}
	cfgResp, cfgErr := registerBenchmarkConfigFn(cmd.Context(), forge.BenchmarkConfigOptions{
		RestURL:     flagBenchForgeURL,
		Creds:       creds,
		Name:        fmt.Sprintf("gkebnkctl-%s", preset.Name),
		Description: preset.Description,
		ConfigJSON:  configJSON,
	})
	presetConfigID := graph.configID
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "⚠ forge config registration failed (non-fatal): %v\n", cfgErr)
	} else {
		fmt.Fprintf(os.Stderr, "✓ forge config registered: id=%d name=%s\n", cfgResp.ID, cfgResp.Name)
		presetConfigID = cfgResp.ID
	}

	cfg := preset.Config
	cfg.Model = flagBenchModel
	cfg.EndpointPath = flagBenchEndpoint
	cfg.Tokenizer = flagBenchTokenizer
	cfg.HostHeader = flagBenchHostHeader
	cfg.Timeout = flagBenchTimeout

	runOpts := jumphost.AiperfRunOptions{
		ProbeOptions: probOpts,
		Config:       cfg,
		RunLabel:     label,
	}

	fmt.Fprintf(os.Stderr, "→ Running aiperf (concurrency=%d requests=%d isl=%d osl=%d stream=%v)\n",
		cfg.Concurrency, cfg.NumRequests, cfg.ISL, cfg.OSL, cfg.Streaming)

	result, err := executeBenchmarkRun(cmd.Context(), runOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ preset %s aiperf failed: %v\n", preset.Name, err)
		return scenarioOutcome{Preset: preset.Name, Status: "FAILED", Err: err}
	}
	fmt.Fprintf(os.Stderr, "✓ aiperf done: %d/%d succeeded (%.1fs rps=%.3f)\n",
		result.Successful, result.TotalRequests, result.DurationSeconds, result.RequestThroughput)

	runID, pushErr := pushAiperfResult(cmd, result, creds, agentName, label, presetConfigID, graph.targetID, graph.proxyDeploymentID, proxy, vip, "", configJSON)
	if pushErr != nil {
		fmt.Fprintf(os.Stderr, "✗ preset %s forge push failed: %v\n", preset.Name, pushErr)
		return scenarioOutcome{Preset: preset.Name, Status: "PUSH_FAILED", Err: pushErr}
	}
	return scenarioOutcome{
		Preset:   preset.Name,
		RunID:    runID,
		ResultID: result.BaseURL,
		Status:   "OK",
	}
}

func pushAiperfResult(
	cmd *cobra.Command,
	result *jumphost.AiperfResult,
	creds forge.RestCreds,
	agentName, label string,
	configID, targetID, proxyDeploymentID int,
	proxy, vip string,
	datasetName string,
	fallbackConfig map[string]any,
) (int, error) {
	effectiveProxy := proxy
	if effectiveProxy == "" {
		effectiveProxy = flagBenchProxy
	}
	effectiveVIP := vip
	if effectiveVIP == "" {
		effectiveVIP = flagBenchVIP
	}

	token := effectiveAgentToken(agentName)
	rawPushOpts := forge.RawAiperfPushOptions{
		RestURL:           flagBenchForgeURL,
		Creds:             creds,
		Tags:              benchSimulatorTags(),
		AgentToken:        token,
		RawJSON:           []byte(result.RawJSON),
		GenAI:             result.GenAI,
		Proxy:             effectiveProxy,
		Model:             flagBenchModel,
		URL:               llmBaseURL(effectiveVIP),
		AgentName:         agentName,
		RunLabel:          label,
		TargetID:          targetID,
		ConfigID:          configID,
		ProxyDeploymentID: proxyDeploymentID,
		DatasetName:       datasetName,
	}

	rawResp, pushErr := pushRawAiperfResultFn(cmd.Context(), rawPushOpts)
	if pushErr != nil {
		fmt.Fprintf(os.Stderr, "⚠ raw push failed (%v); falling back to structured push\n", pushErr)
		pushOpts := forge.BenchmarkPushOptions{
			RestURL:           flagBenchForgeURL,
			Creds:             creds,
			Tags:              benchSimulatorTags(),
			AgentToken:        token,
			RunLabel:          label,
			Proxy:             effectiveProxy,
			AgentName:         agentName,
			AgentHostname:     flagBenchInstanceID,
			TargetID:          targetID,
			ConfigID:          configID,
			ProxyDeploymentID: proxyDeploymentID,
			AiperfConfig:      fallbackConfig,
		}
		structResp, structErr := pushBenchmarkResultFn(cmd.Context(), result, pushOpts)
		if structErr != nil {
			return 0, fmt.Errorf("raw+structured both failed: raw=%v structured=%w", pushErr, structErr)
		}
		fmt.Fprintf(os.Stderr, "✓ pushed (structured fallback): run_id=%d status=%s\n", structResp.RunID, structResp.Status)
		return structResp.RunID, nil
	}

	fmt.Fprintf(os.Stderr, "✓ pushed (raw): run_id=%d status=%s\n", rawResp.RunID, rawResp.Status)
	return rawResp.RunID, nil
}

type nativeScenarioOutcome struct {
	VariantLabel string
	RunID        int
	Status       string
	Err          error
}

func runNativeScenarioCollect(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	graph forgeGraph,
	scenarioKey string,
	scenarioConfigID int,
	proxy, vip string,
) ([]nativeScenarioOutcome, error) {
	baseCfg := jumphost.AiperfConfig{
		Model:        flagBenchModel,
		EndpointPath: flagBenchEndpoint,
		Tokenizer:    flagBenchTokenizer,
		HostHeader:   flagBenchHostHeader,
		Timeout:      flagBenchTimeout,
	}

	children, err := expandForgeScenario(scenarioKey, baseCfg)
	if err != nil {
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "→ native scenario %q: %d child run(s)\n", scenarioKey, len(children))

	outcomes := make([]nativeScenarioOutcome, 0, len(children))

	for _, child := range children {
		label := presetRunLabel(flagBenchRunLabel, scenarioKey+"-"+child.VariantLabel)
		fmt.Fprintf(os.Stderr, "\n── child: %s label=%s ──\n", child.VariantLabel, label)

		cfg := child.Config
		if cfg.SeqDist != "" {
			fmt.Fprintf(os.Stderr, "→ Running aiperf (concurrency=%d requests=%d seq-dist=%q stream=%v)\n",
				cfg.Concurrency, cfg.NumRequests, cfg.SeqDist, cfg.Streaming)
		} else {
			fmt.Fprintf(os.Stderr, "→ Running aiperf (concurrency=%d requests=%d isl=%d osl=%d stream=%v)\n",
				cfg.Concurrency, cfg.NumRequests, cfg.ISL, cfg.OSL, cfg.Streaming)
		}

		runOpts := jumphost.AiperfRunOptions{
			ProbeOptions: probOpts,
			Config:       cfg,
			RunLabel:     label,
		}

		result, aiperfErr := executeBenchmarkRun(cmd.Context(), runOpts)
		if aiperfErr != nil {
			fmt.Fprintf(os.Stderr, "✗ child %s aiperf failed: %v\n", child.VariantLabel, aiperfErr)
			outcomes = append(outcomes, nativeScenarioOutcome{
				VariantLabel: child.VariantLabel,
				Status:       "FAILED",
				Err:          aiperfErr,
			})
			continue
		}
		fmt.Fprintf(os.Stderr, "✓ aiperf done: %d/%d succeeded (%.1fs rps=%.3f)\n",
			result.Successful, result.TotalRequests, result.DurationSeconds, result.RequestThroughput)

		effectiveVIP := vip
		if effectiveVIP == "" {
			effectiveVIP = flagBenchVIP
		}

		fallbackCfg := map[string]any{
			"scenario_key":  scenarioKey,
			"variant_label": child.VariantLabel,
			"url":           llmBaseURL(effectiveVIP),
			"model":         flagBenchModel,
			"concurrency":   cfg.Concurrency,
			"request_count": cfg.NumRequests,
			"streaming":     cfg.Streaming,
		}

		datasetName := scenarioKey
		pushLabel := label
		if scenarioKey == "mooncake" {
			datasetName = "mooncake-toolagent"
			if !strings.Contains(pushLabel, "tokenizer_substituted") {
				pushLabel = pushLabel + "+tokenizer_substituted"
			}
		}

		runID, pushErr := pushAiperfResult(cmd, result, creds, agentName, pushLabel, scenarioConfigID, graph.targetID, graph.proxyDeploymentID, proxy, vip, datasetName, fallbackCfg)
		if pushErr != nil {
			fmt.Fprintf(os.Stderr, "✗ child %s push failed: %v\n", child.VariantLabel, pushErr)
			outcomes = append(outcomes, nativeScenarioOutcome{
				VariantLabel: child.VariantLabel,
				Status:       "PUSH_FAILED",
				Err:          pushErr,
			})
			continue
		}
		outcomes = append(outcomes, nativeScenarioOutcome{
			VariantLabel: child.VariantLabel,
			RunID:        runID,
			Status:       "OK",
		})
	}
	return outcomes, nil
}

func registerScenarioConfig(
	ctx context.Context,
	creds forge.RestCreds,
	graph forgeGraph,
	key string,
) int {
	var description string
	if s, ok := forgeScenarioByKey(key); ok {
		description = s.Description
	}
	configJSON := forgeScenarioConfigJSON(key, flagBenchModel, flagBenchVIP)
	configName := fmt.Sprintf("gkebnkctl-scenario-%s", key)
	cfgResp, cfgErr := registerBenchmarkConfigFn(ctx, forge.BenchmarkConfigOptions{
		RestURL:     flagBenchForgeURL,
		Creds:       creds,
		Name:        configName,
		Description: description,
		ConfigJSON:  configJSON,
	})
	configID := graph.configID
	if cfgErr != nil {
		fmt.Fprintf(os.Stderr, "⚠ forge config registration failed (non-fatal): %v\n", cfgErr)
	} else {
		fmt.Fprintf(os.Stderr, "✓ forge config registered: id=%d name=%s\n", cfgResp.ID, cfgResp.Name)
		configID = cfgResp.ID
	}
	return configID
}

func runScenarioSequence(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	graph forgeGraph,
	keys []string,
	proxy, vip string,
) ([]nativeScenarioOutcome, error) {
	var allOutcomes []nativeScenarioOutcome
	for i, key := range keys {
		if i > 0 {
			prev := keys[i-1]
			if resetErr := resetCacheHookFn(cmd.Context(), resetCacheArgs{
				PrevKey:   prev,
				NextKey:   key,
				ProbeOpts: probOpts,
				Proxy:     proxy,
			}); resetErr != nil {
				return allOutcomes, &resetCacheError{PrevKey: prev, NextKey: key, cause: resetErr}
			}
		}

		configID := registerScenarioConfig(cmd.Context(), creds, graph, key)
		g := graph
		g.configID = configID

		outcomes, err := runNativeScenarioCollect(cmd, probOpts, creds, agentName, g, key, configID, proxy, vip)
		if err != nil {
			return allOutcomes, err
		}
		allOutcomes = append(allOutcomes, outcomes...)
	}
	return allOutcomes, nil
}

func runNativeScenario(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	graph forgeGraph,
	rawScenario string,
) error {
	keys, parseErr := parseScenarioKeys(rawScenario)
	if parseErr != nil {
		return parseErr
	}
	baseCfg := jumphost.AiperfConfig{
		Model:        flagBenchModel,
		EndpointPath: flagBenchEndpoint,
		Tokenizer:    flagBenchTokenizer,
		HostHeader:   flagBenchHostHeader,
		Timeout:      flagBenchTimeout,
	}
	for _, k := range keys {
		if _, ok := forgeScenarioByKey(k); !ok {
			return fmt.Errorf("--scenario: unknown key %q; valid keys: %s", k, strings.Join(forgeScenarioKeys(), ", "))
		}
		if _, err := expandForgeScenario(k, baseCfg); err != nil {
			return fmt.Errorf("--scenario: key %q: %w", k, err)
		}
	}

	allOutcomes, seqErr := runScenarioSequence(cmd, probOpts, creds, agentName, graph, keys, "", "")

	offset := 0
	for _, k := range keys {
		children, _ := expandForgeScenario(k, baseCfg)
		n := len(children)
		end := offset + n
		if end > len(allOutcomes) {
			end = len(allOutcomes)
		}
		keyOutcomes := allOutcomes[offset:end]
		offset = end

		if len(keyOutcomes) == 0 {
			continue
		}

		fmt.Println()
		fmt.Printf("scenario: %s\n", k)
		fmt.Printf("%-30s  %-10s  %-8s  %s\n", "VARIANT", "STATUS", "RUN_ID", "NOTE")
		fmt.Printf("%-30s  %-10s  %-8s  %s\n", "-------", "------", "------", "----")
		for _, o := range keyOutcomes {
			note := ""
			if o.Err != nil {
				note = o.Err.Error()
				if len(note) > 50 {
					note = note[:47] + "..."
				}
			}
			fmt.Printf("%-30s  %-10s  %-8d  %s\n", o.VariantLabel, o.Status, o.RunID, note)
		}
	}

	if seqErr != nil {
		return seqErr
	}

	allFailed := true
	for _, o := range allOutcomes {
		if o.Err == nil {
			allFailed = false
			break
		}
	}
	if allFailed && len(allOutcomes) > 0 {
		return fmt.Errorf("all %d child run(s) failed across %d scenario(s)", len(allOutcomes), len(keys))
	}
	return nil
}

// ── Proxy shootout (WS-D1) ──────────────────────────────────────────────────

type shootoutFrontEnd struct {
	ProxyType         string
	VIP               string
	ProxyDeploymentID int
	ResolveErr        error
}

type shootoutOutcome struct {
	ProxyType         string
	ProxyDeploymentID int
	RunIDs            []int
	Status            string
	Err               error
}

func resolveShootoutFrontEnds(ctx context.Context, creds forge.RestCreds, targetID int) ([]shootoutFrontEnd, error) {
	var frontEnds []shootoutFrontEnd

	var proxyTypes []string
	for _, raw := range strings.Split(flagBenchProxies, ",") {
		t := strings.TrimSpace(raw)
		if t != "" {
			proxyTypes = append(proxyTypes, t)
		}
	}

	for _, pt := range proxyTypes {
		if !forge.IsValidProxyType(pt) {
			return nil, fmt.Errorf("--proxies: unknown proxy type %q; valid types: envoy, f5-bnk, haproxy, nginx, nodeport", pt)
		}
	}

	var knownProxies []forge.ProxyDeployment
	if targetID != 0 && (len(proxyTypes) > 0 || flagBenchDirectPodIP != "") {
		discOpts := forge.ProxyDiscoverOptions{
			RestURL:  flagBenchForgeURL,
			Creds:    creds,
			TargetID: targetID,
		}
		if _, discErr := discoverProxiesFn(ctx, discOpts); discErr != nil {
			fmt.Fprintf(os.Stderr, "⚠ forge discover-proxies failed (non-fatal, running unlinked): %v\n", discErr)
		}
		listed, listErr := listProxyDeploymentsFn(ctx, discOpts)
		if listErr != nil {
			fmt.Fprintf(os.Stderr, "⚠ forge list-proxies failed (non-fatal, running unlinked): %v\n", listErr)
		} else {
			knownProxies = listed
		}
	}

	for _, pt := range proxyTypes {
		dep := forge.FindProxyDeployment(knownProxies, pt)
		id := 0
		if dep != nil {
			id = dep.ID
		}
		fe := shootoutFrontEnd{
			ProxyType:         pt,
			ProxyDeploymentID: id,
		}

		if pt != "f5-bnk" {
			if dep == nil || dep.ExternalURL == "" {
				if dep == nil {
					fe.ResolveErr = fmt.Errorf("%s front-end: no ProxyDeployment found in forge (run discover-proxies first)", pt)
				} else {
					fe.ResolveErr = fmt.Errorf("%s front-end: external_url is empty (forge discovery has not populated it yet; check Service type and LB ingress)", pt)
				}
				fmt.Fprintf(os.Stderr, "⚠ %v\n", fe.ResolveErr)
			} else {
				fe.VIP = dep.ExternalURL
				fmt.Fprintf(os.Stderr, "✓ %s endpoint resolved from forge external_url: %s\n", pt, fe.VIP)
			}
		}

		frontEnds = append(frontEnds, fe)
	}

	if flagBenchDirectPodIP != "" {
		id := forge.ResolveProxyDeploymentID(knownProxies, "nodeport")
		frontEnds = append(frontEnds, shootoutFrontEnd{
			ProxyType:         "nodeport",
			VIP:               flagBenchDirectPodIP,
			ProxyDeploymentID: id,
		})
	}

	return frontEnds, nil
}

func runProxyShootout(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	graph forgeGraph,
	presets []benchmarkPreset,
) error {
	frontEnds, err := resolveShootoutFrontEnds(cmd.Context(), creds, graph.targetID)
	if err != nil {
		return err
	}
	if len(frontEnds) == 0 {
		return fmt.Errorf("--proxies is empty and --direct-pod-ip is not set; nothing to shoot out")
	}

	fmt.Fprintf(os.Stderr, "→ proxy shootout: %d front-end(s)\n", len(frontEnds))

	outcomes := make([]shootoutOutcome, 0, len(frontEnds))

	for _, fe := range frontEnds {
		if fe.ResolveErr != nil {
			outcomes = append(outcomes, shootoutOutcome{
				ProxyType:         fe.ProxyType,
				ProxyDeploymentID: fe.ProxyDeploymentID,
				Status:            "RESOLVE_FAILED",
				Err:               fe.ResolveErr,
			})
			continue
		}

		g := graph
		g.proxyDeploymentID = fe.ProxyDeploymentID

		feProbeOpts := probOpts
		if fe.VIP != "" {
			feProbeOpts.VIP = fe.VIP
		}

		linked := "unlinked"
		if fe.ProxyDeploymentID != 0 {
			linked = fmt.Sprintf("id=%d", fe.ProxyDeploymentID)
		}
		fmt.Fprintf(os.Stderr, "\n══ front-end: %s (%s) vip=%s ══\n",
			fe.ProxyType, linked, feProbeOpts.VIP)

		oc := runShootoutFrontEnd(cmd, feProbeOpts, creds, agentName, g, fe.ProxyType, fe.VIP, presets)
		outcomes = append(outcomes, oc)
	}

	fmt.Println()
	fmt.Printf("proxy shootout summary\n")
	fmt.Printf("%-14s  %-12s  %-8s  %-8s  %s\n", "PROXY", "PROXY_DEP_ID", "STATUS", "RUN_IDS", "NOTE")
	fmt.Printf("%-14s  %-12s  %-8s  %-8s  %s\n", "-----", "------------", "------", "-------", "----")
	for _, o := range outcomes {
		depID := "unlinked"
		if o.ProxyDeploymentID != 0 {
			depID = fmt.Sprintf("%d", o.ProxyDeploymentID)
		}
		runIDs := formatRunIDs(o.RunIDs)
		note := ""
		if o.Err != nil {
			note = o.Err.Error()
			if len(note) > 50 {
				note = note[:47] + "..."
			}
		}
		fmt.Printf("%-14s  %-12s  %-8s  %-8s  %s\n", o.ProxyType, depID, o.Status, runIDs, note)
	}

	allFailed := true
	for _, o := range outcomes {
		if o.Err == nil {
			allFailed = false
			break
		}
	}
	if allFailed && len(outcomes) > 0 {
		return fmt.Errorf("all %d front-end(s) failed in proxy shootout", len(outcomes))
	}
	return nil
}

func runShootoutFrontEnd(
	cmd *cobra.Command,
	probOpts jumphost.ProbeOptions,
	creds forge.RestCreds,
	agentName string,
	graph forgeGraph,
	proxyType, vip string,
	presets []benchmarkPreset,
) shootoutOutcome {
	base := shootoutOutcome{ProxyType: proxyType, ProxyDeploymentID: graph.proxyDeploymentID}

	fmt.Fprintf(os.Stderr, "→ Preflight: checking model %q at vip=%s (front-end=%s)\n",
		flagBenchModel, probOpts.VIP, proxyType)
	if pfErr := checkServedModelFn(cmd.Context(), probOpts, flagBenchModel); pfErr != nil {
		base.Status = "PREFLIGHT_FAILED"
		base.Err = fmt.Errorf("served-model preflight: %w", pfErr)
		return base
	}

	switch {
	case flagBenchScenario != "":
		keys, parseErr := parseScenarioKeys(flagBenchScenario)
		if parseErr != nil {
			base.Status = "FAILED"
			base.Err = parseErr
			return base
		}
		children, seqErr := runScenarioSequence(cmd, probOpts, creds, agentName, graph, keys, proxyType, vip)
		if seqErr != nil {
			var rce *resetCacheError
			if errors.As(seqErr, &rce) {
				base.Status = "RESET_FAILED"
				base.Err = seqErr
				return base
			}
			base.Status = "FAILED"
			base.Err = seqErr
			return base
		}
		var runIDs []int
		allFailed := true
		for _, ch := range children {
			if ch.Err == nil {
				allFailed = false
				if ch.RunID != 0 {
					runIDs = append(runIDs, ch.RunID)
				}
			}
		}
		base.RunIDs = runIDs
		if allFailed && len(children) > 0 {
			base.Status = "FAILED"
			base.Err = fmt.Errorf("all %d child run(s) failed", len(children))
		} else {
			base.Status = "OK"
		}
		return base

	case len(presets) > 0:
		presOutcomes := runBenchmarkScenariosCollect(cmd, probOpts, creds, agentName, graph, presets, proxyType, vip)
		var runIDs []int
		allFailed := true
		for _, o := range presOutcomes {
			if o.Err == nil {
				allFailed = false
				if o.RunID != 0 {
					runIDs = append(runIDs, o.RunID)
				}
			}
		}
		base.RunIDs = runIDs
		if allFailed && len(presOutcomes) > 0 {
			base.Status = "FAILED"
			base.Err = fmt.Errorf("all %d preset(s) failed", len(presOutcomes))
		} else {
			base.Status = "OK"
		}
		return base

	default:
		effectiveVIP := vip
		if effectiveVIP == "" {
			effectiveVIP = flagBenchVIP
		}
		runOpts := jumphost.AiperfRunOptions{
			ProbeOptions: probOpts,
			Config: jumphost.AiperfConfig{
				Model:        flagBenchModel,
				EndpointPath: flagBenchEndpoint,
				Concurrency:  flagBenchConcurrency,
				NumRequests:  flagBenchNumRequests,
				ISL:          flagBenchISL,
				OSL:          flagBenchOSL,
				Streaming:    flagBenchStreaming,
				Tokenizer:    flagBenchTokenizer,
				HostHeader:   flagBenchHostHeader,
				Timeout:      flagBenchTimeout,
			},
			RunLabel: flagBenchRunLabel,
			ResultID: flagBenchResultID,
		}
		result, aiperfErr := executeBenchmarkRun(cmd.Context(), runOpts)
		if aiperfErr != nil {
			base.Status = "FAILED"
			base.Err = aiperfErr
			return base
		}
		fmt.Fprintf(os.Stderr, "✓ aiperf done: %d/%d succeeded (%.1fs rps=%.3f)\n",
			result.Successful, result.TotalRequests, result.DurationSeconds, result.RequestThroughput)
		runID, pushErr := pushAiperfResult(
			cmd, result, creds, agentName, flagBenchRunLabel,
			graph.configID, graph.targetID, graph.proxyDeploymentID,
			proxyType, effectiveVIP,
			"", nil,
		)
		if pushErr != nil {
			base.Status = "PUSH_FAILED"
			base.Err = pushErr
			return base
		}
		base.RunIDs = []int{runID}
		base.Status = "OK"
		return base
	}
}

func llmBaseURL(host string) string {
	lower := strings.ToLower(host)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return host
	}
	return "http://" + host
}

func formatRunIDs(ids []int) string {
	if len(ids) == 0 {
		return "-"
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("%d", id)
	}
	return strings.Join(parts, ",")
}

// benchSimulatorTags labels pushed results with the simulated model profile when
// --config points at a cluster that runs the simulator (nil otherwise).
func benchSimulatorTags() map[string]string {
	if flagBenchConfig == "" {
		return nil
	}
	cl, err := intent.Load(flagBenchConfig)
	if err != nil {
		return nil
	}
	return cl.SimulatorTags()
}

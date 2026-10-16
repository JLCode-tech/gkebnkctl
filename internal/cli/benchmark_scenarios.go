package cli

// benchmark_scenarios.go — faithful Go port of forge's benchmark_scenarios.py
// expand_scenario engine.
//
// The public API mirrors the Python module:
//   - forgeScenarioRegistry — keyed map of all 8 synthetic presets
//   - expandForgeScenario(key, base)  — expands a key into ordered child configs
//
// Values (concurrency sweeps, request-count formulas, seq-dist strings, prefix
// lengths, extra-inputs) are kept byte-identical to the Python source so that
// forge-agent orchestration produces identical workloads.
//
// mooncake (trace download + 0.80 time-dilation + tokenizer substitution)
// is implemented. expandForgeScenario allows the "mooncake" key and applies
// the llama3 model + tokenizer override; the shell pipeline is in buildMooncakeCmd.

import (
	"fmt"
	"math"
	"strconv"

	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
)

// syntheticModel is the default LLM targeted by all synthetic scenarios.
const syntheticModel = "meta-llama/Llama-3.1-70B-Instruct"

// defaultSweep is the standard concurrency sweep used by most scenarios.
var defaultSweep = []int{50, 100, 150, 200}

// heavyConcISL is the high (concurrency, ISL) pair list used by high-concurrency
// and prefix-cache scenarios.
var heavyConcISL = [][2]int{{150, 5000}, {200, 7000}, {250, 9000}, {300, 10000}}

// rcMin computes request count: max(c*5, 20).
func rcMin(c int) int {
	v := c * 5
	if v < 20 {
		return 20
	}
	return v
}

// syntheticBase returns the base AiperfConfig fields shared by all synthetic scenarios.
func syntheticBase(endpointType string) jumphost.AiperfConfig {
	if endpointType == "" {
		endpointType = "chat"
	}
	return jumphost.AiperfConfig{
		Model:        syntheticModel,
		EndpointType: endpointType,
		Streaming:    true,
		ExtraInputs:  []string{"ignore_eos:true"},
	}
}

// forgeScenarioChild is one child variant produced by expandForgeScenario.
type forgeScenarioChild struct {
	VariantLabel string
	Phase        string
	Turn         int
	Round        int
	Config       jumphost.AiperfConfig
}

// forgeScenario is the scenario preset structure.
type forgeScenario struct {
	Key           string
	Name          string
	Description   string
	Tags          []string
	TraceDriven   bool
	buildVariants func() []forgeScenarioChild
}

// ---------------------------------------------------------------------------
// Variant builders
// ---------------------------------------------------------------------------

func baselineVariants() []forgeScenarioChild {
	out := make([]forgeScenarioChild, len(defaultSweep))
	for i, c := range defaultSweep {
		cfg := syntheticBase("")
		cfg.Concurrency = c
		cfg.NumRequests = rcMin(c)
		cfg.ISL = 500
		cfg.OSL = 128
		out[i] = forgeScenarioChild{
			VariantLabel: fmt.Sprintf("c%d", c),
			Config:       cfg,
		}
	}
	return out
}

func highConcurrencyVariants() []forgeScenarioChild {
	out := make([]forgeScenarioChild, len(heavyConcISL))
	for i, pair := range heavyConcISL {
		c, isl := pair[0], pair[1]
		cfg := syntheticBase("")
		cfg.Concurrency = c
		cfg.NumRequests = c * 5
		cfg.ISL = isl
		cfg.OSL = 128
		out[i] = forgeScenarioChild{
			VariantLabel: fmt.Sprintf("c%d-isl%d", c, isl),
			Config:       cfg,
		}
	}
	return out
}

func mixedWorkloadVariants() []forgeScenarioChild {
	var out []forgeScenarioChild

	// Warmup phase: fixed c=50
	warmupCfg := syntheticBase("")
	warmupCfg.Concurrency = 50
	warmupCfg.NumRequests = 150
	warmupCfg.ISL = 500
	warmupCfg.OSL = 128
	out = append(out, forgeScenarioChild{
		VariantLabel: "warmup",
		Phase:        "warmup",
		Config:       warmupCfg,
	})

	// Short phase: sweep, ISL 500, OSL 64
	for _, c := range defaultSweep {
		cfg := syntheticBase("")
		cfg.Concurrency = c
		cfg.NumRequests = c * 5
		cfg.ISL = 500
		cfg.OSL = 64
		out = append(out, forgeScenarioChild{
			VariantLabel: fmt.Sprintf("short-c%d", c),
			Phase:        "short",
			Config:       cfg,
		})
	}

	// Long phase: sweep, ISL 600, OSL 128, prefix
	for _, c := range defaultSweep {
		cfg := syntheticBase("")
		cfg.Concurrency = c
		cfg.NumRequests = c * 5
		cfg.ISL = 600
		cfg.OSL = 128
		cfg.PrefixPromptLength = 1400
		cfg.NumPrefixPrompts = 10
		out = append(out, forgeScenarioChild{
			VariantLabel: fmt.Sprintf("long-c%d", c),
			Phase:        "long",
			Config:       cfg,
		})
	}

	return out
}

func multiTurnVariants() []forgeScenarioChild {
	var out []forgeScenarioChild

	// Turn 1: no prefix
	for _, c := range defaultSweep {
		cfg := syntheticBase("")
		cfg.Concurrency = c
		cfg.NumRequests = c * 5
		cfg.ISL = 500
		cfg.OSL = 128
		out = append(out, forgeScenarioChild{
			VariantLabel: fmt.Sprintf("turn1-c%d", c),
			Turn:         1,
			Config:       cfg,
		})
	}

	// Turns 2-4: growing prefix lengths
	for _, tp := range [][2]int{{2, 500}, {3, 1000}, {4, 1500}} {
		turn, prefixLen := tp[0], tp[1]
		for _, c := range defaultSweep {
			cfg := syntheticBase("")
			cfg.Concurrency = c
			cfg.NumRequests = c * 5
			cfg.ISL = 500
			cfg.OSL = 128
			cfg.PrefixPromptLength = prefixLen
			cfg.NumPrefixPrompts = 10
			out = append(out, forgeScenarioChild{
				VariantLabel: fmt.Sprintf("turn%d-c%d", turn, c),
				Turn:         turn,
				Config:       cfg,
			})
		}
	}

	return out
}

func prefixCacheVariants() []forgeScenarioChild {
	out := make([]forgeScenarioChild, len(heavyConcISL))
	for i, pair := range heavyConcISL {
		c, isl := pair[0], pair[1]
		prefixLen := isl * 8 / 10
		uniqueLen := isl - prefixLen
		cfg := syntheticBase("chat")
		cfg.Concurrency = c
		cfg.NumRequests = c * 5
		cfg.ISL = uniqueLen
		cfg.ISLStddev = uniqueLen / 10
		cfg.OSL = 128
		cfg.NumPrefixPrompts = 20
		cfg.PrefixPromptLength = prefixLen
		out[i] = forgeScenarioChild{
			VariantLabel: fmt.Sprintf("isl%d-c%d", isl, c),
			Config:       cfg,
		}
	}
	return out
}

func bimodalVariants() []forgeScenarioChild {
	const seqDist = "300|100,64|16:70;4000|500,256|64:30"
	out := make([]forgeScenarioChild, len(defaultSweep))
	for i, c := range defaultSweep {
		cfg := syntheticBase("")
		cfg.Concurrency = c
		cfg.NumRequests = rcMin(c)
		cfg.SeqDist = seqDist
		out[i] = forgeScenarioChild{
			VariantLabel: fmt.Sprintf("c%d", c),
			Config:       cfg,
		}
	}
	return out
}

func sustainedLoadVariants() []forgeScenarioChild {
	sweep := []int{50, 100, 150, 200, 250}
	out := make([]forgeScenarioChild, len(sweep))
	for i, c := range sweep {
		cfg := syntheticBase("")
		cfg.Concurrency = c
		cfg.NumRequests = c * 10
		cfg.ISL = 1500
		cfg.ISLStddev = 300
		cfg.OSL = 128
		out[i] = forgeScenarioChild{
			VariantLabel: fmt.Sprintf("c%d", c),
			Config:       cfg,
		}
	}
	return out
}

func burstRecoveryVariants() []forgeScenarioChild {
	const burstSeqDist = "300|100,64|16:50;3000|400,200|50:50"
	var out []forgeScenarioChild
	for rnd := 1; rnd <= 5; rnd++ {
		// Burst phase
		burstCfg := syntheticBase("")
		burstCfg.Concurrency = 200
		burstCfg.NumRequests = 400
		burstCfg.SeqDist = burstSeqDist
		out = append(out, forgeScenarioChild{
			VariantLabel: fmt.Sprintf("round%d-burst", rnd),
			Phase:        "burst",
			Round:        rnd,
			Config:       burstCfg,
		})

		// Probe phase
		probeCfg := syntheticBase("")
		probeCfg.Concurrency = 25
		probeCfg.NumRequests = 50
		probeCfg.ISL = 256
		probeCfg.ISLStddev = 50
		probeCfg.OSL = 64
		out = append(out, forgeScenarioChild{
			VariantLabel: fmt.Sprintf("round%d-probe", rnd),
			Phase:        "probe",
			Round:        rnd,
			Config:       probeCfg,
		})
	}
	return out
}

const mooncakeTraceURL = "https://raw.githubusercontent.com/kvcache-ai/Mooncake/refs/heads/main/FAST25-release/traces/toolagent_trace.jsonl"
const mooncakeDilation = 0.80

func mooncakeVariants() []forgeScenarioChild {
	return []forgeScenarioChild{
		{
			VariantLabel: "trace",
			Config: jumphost.AiperfConfig{
				Model:                 "Qwen/Qwen3-32B",
				Streaming:             true,
				TraceURL:              mooncakeTraceURL,
				TraceDilation:         mooncakeDilation,
				CustomDatasetType:     "mooncake_trace",
				FixedSchedule:         true,
				WorkersMax:            200,
				RandomSeed:            42,
				RequestTimeoutSeconds: 1000,
				ProfileExportLevel:    "summary",
				RecordProcessors:      8,
				Goodput:               "time_to_first_token:5000 inter_token_latency:100",
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

// Open-loop Poisson request-rate sweeps — the gateway-comparison standard
// (MLPerf Server, GAIE / llm-d QPS sweeps). Requests arrive on a schedule no
// matter how fast the proxy answers, so a slower proxy shows up as queueing
// (higher TTFT) instead of hiding behind fewer requests as in a closed loop.
// Mirror of benchmark_scenarios.py POISSON_RATES / POISSON_PREFIX_RATES.
var (
	poissonRates        = []float64{2, 4, 8, 16, 32}
	poissonPrefixRates  = []float64{1, 2, 4, 8, 16}
	poissonAgenticRates = []float64{8, 16, 24, 32}
)

const (
	// poissonGoodput: MLPerf Server-style per-request targets (Llama-2-70B: TTFT 2 s, TPOT 200 ms).
	poissonGoodput = "time_to_first_token:2000 inter_token_latency:200"
	// Every step warms up, then measures for at least stepDurationFloorS, and
	// long enough to collect stepMinRequests so p99 rests on a few hundred samples.
	stepWarmupS        = 30
	stepDurationFloorS = 120
	stepMinRequests    = 300
)

// stepDurationS is the measured seconds for one rate step: the floor, longer at low rates.
func stepDurationS(rate float64) float64 {
	return math.Max(stepDurationFloorS, math.Ceil(stepMinRequests/rate))
}

// poissonVariants builds one open-loop step per rate on top of workload (ISL/OSL/prefix).
func poissonVariants(rates []float64, workload func(*jumphost.AiperfConfig)) []forgeScenarioChild {
	out := make([]forgeScenarioChild, len(rates))
	for i, rate := range rates {
		cfg := syntheticBase("")
		cfg.RequestRate = rate
		cfg.ArrivalPattern = "poisson"
		cfg.WarmupDuration = stepWarmupS
		cfg.BenchmarkDuration = stepDurationS(rate)
		cfg.Goodput = poissonGoodput
		// Same prompts for every proxy, so sweeps compare like for like; a new seed per
		// step so each step brings new sessions instead of replaying the last step's.
		cfg.RandomSeed = 42 + i
		workload(&cfg)
		out[i] = forgeScenarioChild{
			VariantLabel: strconv.FormatFloat(rate, 'g', -1, 64) + "rps",
			Config:       cfg,
		}
	}
	return out
}

// poissonRateVariants: chat 1k in / 128 out (ISL random ±10%), one step per request rate.
func poissonRateVariants() []forgeScenarioChild {
	return poissonVariants(poissonRates, func(c *jumphost.AiperfConfig) {
		c.ISL, c.ISLStddev, c.OSL = 1000, 100, 128
	})
}

// poissonPrefixVariants: KV-aware routing workload, 5k prompts with an 80% shared prefix over 20 groups.
func poissonPrefixVariants() []forgeScenarioChild {
	return poissonVariants(poissonPrefixRates, func(c *jumphost.AiperfConfig) {
		c.ISL, c.ISLStddev, c.OSL = 1000, 100, 128
		c.PrefixPromptLength, c.NumPrefixPrompts = 4000, 20
	})
}

// poissonAgenticVariants: agentic long context, 300 sessions sharing a 16k-token
// context, 1k new tokens / 64 out. The shared contexts (~4.8M tokens) exceed the KV
// cache of four 70B replicas, so replicas evict: only cache-aware routing keeps a
// session on one replica; round robin re-prefills it everywhere.
func poissonAgenticVariants() []forgeScenarioChild {
	return poissonVariants(poissonAgenticRates, func(c *jumphost.AiperfConfig) {
		c.ISL, c.ISLStddev, c.OSL = 1000, 100, 64
		c.PrefixPromptLength, c.NumPrefixPrompts = 16000, 300
	})
}

// agentxConcurrencyLadder is the session concurrency sweep for InferenceX AgentX MVP.
var agentxConcurrencyLadder = []int{8, 16, 32, 64}

func agentxMvpVariants() []forgeScenarioChild {
	children := make([]forgeScenarioChild, len(agentxConcurrencyLadder))
	for i, c := range agentxConcurrencyLadder {
		children[i] = forgeScenarioChild{
			VariantLabel: fmt.Sprintf("c%d", c),
			Config: jumphost.AiperfConfig{
				Scenario:            "inferencex-agentx-mvp",
				PublicDataset:       "semianalysis_cc_traces_weka_with_subagents",
				EndpointType:        "chat",
				Streaming:           true,
				UseServerTokenCount: true,
				BenchmarkDuration:   900,
				RandomSeed:          20260707,
				Concurrency:         c,
			},
		}
	}
	return children
}

var forgeScenarioRegistry = []forgeScenario{
	{
		Key:           "inferencex-agentx-mvp",
		Name:          "InferenceX AgentX MVP (Weka traces)",
		Description:   "SemiAnalysis AgentX MVP benchmark: multi-turn Claude Code agentic traces with subagent trees, 128k context, and realistic inter-turn timing.",
		Tags:          []string{"trace", "agentic", "subagents", "prefix-cache"},
		TraceDriven:   true,
		buildVariants: agentxMvpVariants,
	},
	{
		Key:           "baseline",
		Name:          "Baseline",
		Description:   "ISL 500 / OSL 128, concurrency sweep {50,100,150,200}, rc=max(c*5,20).",
		Tags:          []string{"synthetic"},
		buildVariants: baselineVariants,
	},
	{
		Key:           "high-concurrency",
		Name:          "High Concurrency",
		Description:   "Large prompts (ISL 5000-10000) / OSL 128, no prefix sharing, (concurrency,ISL) pairs (150,5000)(200,7000)(250,9000)(300,10000), rc=c*5.",
		Tags:          []string{"synthetic"},
		buildVariants: highConcurrencyVariants,
	},
	{
		Key:           "mixed-workload",
		Name:          "Mixed Workload",
		Description:   "Warmup + short + long phases per concurrency; long phase uses prefix prompts.",
		Tags:          []string{"synthetic", "multi-phase"},
		buildVariants: mixedWorkloadVariants,
	},
	{
		Key:           "multi-turn",
		Name:          "Multi-Turn",
		Description:   "Turn 1 no-prefix, turns 2-4 with growing prefix-prompt-length (500/1000/1500).",
		Tags:          []string{"synthetic", "prefix-cache"},
		buildVariants: multiTurnVariants,
	},
	{
		Key:           "prefix-cache",
		Name:          "Prefix Cache (chat)",
		Description:   "80% shared prefix on large prompts / OSL 128, 20 prefix prompts, prefix-prompt-length 4000-8000 (unique-token mean 1000-2000), (concurrency,ISL) pairs (150,5000)(200,7000)(250,9000)(300,10000).",
		Tags:          []string{"synthetic", "prefix-cache"},
		buildVariants: prefixCacheVariants,
	},
	{
		Key:           "bimodal",
		Name:          "Bimodal",
		Description:   "Two-mode seq-dist (short 70% / long 30%), rc=max(c*5,20), sweep.",
		Tags:          []string{"synthetic"},
		buildVariants: bimodalVariants,
	},
	{
		Key:           "sustained-load",
		Name:          "Sustained Load",
		Description:   "ISL 1500±300 / OSL 128, rc=c*10, sweep {50,100,150,200,250}.",
		Tags:          []string{"synthetic"},
		buildVariants: sustainedLoadVariants,
	},
	{
		Key:           "burst-recovery",
		Name:          "Burst Recovery",
		Description:   "5 rounds of burst (c=200) + probe (c=25) phases.",
		Tags:          []string{"synthetic", "multi-phase"},
		buildVariants: burstRecoveryVariants,
	},
	{
		Key:           "poisson-rate",
		Name:          "Poisson Rate Sweep (chat)",
		Description:   "Open-loop Poisson arrivals, one step per request rate {2,4,8,16,32}. ISL 1000±100 / OSL 128. Each step: 30 s warmup, then at least 120 s and 300 requests measured. Goodput targets TTFT 2000 ms, ITL 200 ms.",
		Tags:          []string{"synthetic", "open-loop"},
		buildVariants: poissonRateVariants,
	},
	{
		Key:           "poisson-rate-prefix",
		Name:          "Poisson Rate Sweep (shared prefix)",
		Description:   "Open-loop Poisson arrivals for KV-aware routing: 5000-token prompts with an 80% shared prefix (20 groups) / OSL 128, one step per request rate {1,2,4,8,16}. Each step: 30 s warmup, then at least 120 s and 300 requests measured. Goodput targets TTFT 2000 ms, ITL 200 ms.",
		Tags:          []string{"synthetic", "open-loop", "prefix-cache"},
		buildVariants: poissonPrefixVariants,
	},
	{
		Key:           "poisson-rate-agentic",
		Name:          "Poisson Rate Sweep (agentic long context)",
		Description:   "Open-loop Poisson arrivals for cache-aware routing on agent traffic: 300 sessions sharing a 16000-token context, 1000 new tokens / OSL 64 per request, one step per request rate {8,16,24,32}. The shared contexts exceed the KV cache of several replicas, so run it against several replicas. Each step: 30 s warmup, then at least 120 s and 300 requests measured. Goodput targets TTFT 2000 ms, ITL 200 ms.",
		Tags:          []string{"synthetic", "open-loop", "prefix-cache", "agentic"},
		buildVariants: poissonAgenticVariants,
	},
	{
		Key:           "mooncake",
		Name:          "Mooncake Trace (production)",
		Description:   "Open-loop, trace-driven Qwen3-32B run replayed against llama3 rig with 0.80x time-dilation. No concurrency sweep.",
		Tags:          []string{"trace", "production"},
		TraceDriven:   true,
		buildVariants: mooncakeVariants,
	},
}

func forgeScenarioKeys() []string {
	keys := make([]string, 0, len(forgeScenarioRegistry))
	for _, s := range forgeScenarioRegistry {
		keys = append(keys, s.Key)
	}
	return keys
}

func forgeScenarioByKey(key string) (forgeScenario, bool) {
	for _, s := range forgeScenarioRegistry {
		if s.Key == key {
			return s, true
		}
	}
	return forgeScenario{}, false
}

const mooncakeLlamaModel = "llama3"
const mooncakeLlamaTokenizer = "NousResearch/Meta-Llama-3-8B-Instruct"

func expandForgeScenario(key string, base jumphost.AiperfConfig) ([]forgeScenarioChild, error) {
	scenario, ok := forgeScenarioByKey(key)
	if !ok {
		return nil, fmt.Errorf("unknown --scenario %q; valid keys: %v", key, forgeScenarioKeys())
	}

	children := scenario.buildVariants()

	for i := range children {
		cfg := &children[i].Config

		if scenario.Key == "mooncake" {
			cfg.Model = mooncakeLlamaModel
			cfg.Tokenizer = mooncakeLlamaTokenizer
		} else {
			if base.Model != "" {
				cfg.Model = base.Model
			} else if cfg.Model == "" {
				cfg.Model = syntheticModel
			}
			if base.Tokenizer != "" {
				cfg.Tokenizer = base.Tokenizer
			}
		}

		if base.HostHeader != "" {
			cfg.HostHeader = base.HostHeader
		}
		if base.EndpointPath != "" {
			cfg.EndpointPath = base.EndpointPath
		}
		if base.Timeout > 0 {
			cfg.Timeout = base.Timeout
		}
		cfg.FitTimeoutToDuration()
	}

	return children, nil
}

func forgeScenarioConfigJSON(key string, model, vip string) map[string]any {
	s, ok := forgeScenarioByKey(key)
	if !ok {
		return map[string]any{"scenario_key": key}
	}
	return map[string]any{
		"scenario_key":  key,
		"scenario_name": s.Name,
		"model":         model,
		"url":           llmBaseURL(vip),
		"description":   s.Description,
		"tags":          s.Tags,
	}
}

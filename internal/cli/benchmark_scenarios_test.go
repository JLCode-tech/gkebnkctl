package cli

// benchmark_scenarios_test.go — table-driven tests asserting the exact expanded
// child list for every scenario against the Python source values.
//
// Reference: bnk-forge-v2/backend/services/benchmark_scenarios.py
// Values here must match the Python source EXACTLY.

import (
	"testing"
	"time"

	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
)

// ---------------------------------------------------------------------------
// forgeScenarioByKey
// ---------------------------------------------------------------------------

func TestForgeScenarioByKey_KnownKey(t *testing.T) {
	s, ok := forgeScenarioByKey("baseline")
	if !ok {
		t.Fatal("forgeScenarioByKey(baseline): not found")
	}
	if s.Key != "baseline" {
		t.Errorf("Key = %q, want %q", s.Key, "baseline")
	}
}

func TestForgeScenarioByKey_UnknownKey(t *testing.T) {
	_, ok := forgeScenarioByKey("does-not-exist")
	if ok {
		t.Error("forgeScenarioByKey(does-not-exist): expected not-found")
	}
}

// ---------------------------------------------------------------------------
// expandForgeScenario — error paths
// ---------------------------------------------------------------------------

func TestExpandForgeScenario_UnknownKey(t *testing.T) {
	_, err := expandForgeScenario("no-such-scenario", defaultBaseCfg())
	if err == nil {
		t.Error("expected error for unknown scenario key, got nil")
	}
}

// ---------------------------------------------------------------------------
// mooncake — trace-driven scenario (WS-C2)
// ---------------------------------------------------------------------------

func TestExpandForgeScenario_Mooncake_SingleChild(t *testing.T) {
	children, err := expandForgeScenario("mooncake", defaultBaseCfg())
	if err != nil {
		t.Fatalf("expandForgeScenario(mooncake): %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("mooncake: expected 1 child, got %d", len(children))
	}
	ch := children[0]
	if ch.VariantLabel != "trace" {
		t.Errorf("VariantLabel = %q, want %q", ch.VariantLabel, "trace")
	}
}

func TestExpandForgeScenario_Mooncake_ModelTokenizerOverride(t *testing.T) {
	// expandForgeScenario must always override model=llama3 and
	// tokenizer=NousResearch/Meta-Llama-3-8B-Instruct for mooncake,
	// regardless of what the base config carries.
	base := jumphost.AiperfConfig{
		Model:     "some-other-model",
		Tokenizer: "some-other-tokenizer",
	}
	children, err := expandForgeScenario("mooncake", base)
	if err != nil {
		t.Fatalf("expandForgeScenario(mooncake): %v", err)
	}
	ch := children[0]
	if ch.Config.Model != mooncakeLlamaModel {
		t.Errorf("Model = %q, want %q (llama3 rig override)", ch.Config.Model, mooncakeLlamaModel)
	}
	if ch.Config.Tokenizer != mooncakeLlamaTokenizer {
		t.Errorf("Tokenizer = %q, want %q", ch.Config.Tokenizer, mooncakeLlamaTokenizer)
	}
}

func TestExpandForgeScenario_Mooncake_FlagValues(t *testing.T) {
	// Assert all mooncake registry values match benchmark_scenarios.py _mooncake_variants().
	children, err := expandForgeScenario("mooncake", defaultBaseCfg())
	if err != nil {
		t.Fatalf("expandForgeScenario(mooncake): %v", err)
	}
	cfg := children[0].Config

	if cfg.TraceURL != mooncakeTraceURL {
		t.Errorf("TraceURL = %q, want %q", cfg.TraceURL, mooncakeTraceURL)
	}
	if cfg.TraceDilation != mooncakeDilation {
		t.Errorf("TraceDilation = %v, want %v", cfg.TraceDilation, mooncakeDilation)
	}
	if cfg.CustomDatasetType != "mooncake_trace" {
		t.Errorf("CustomDatasetType = %q, want %q", cfg.CustomDatasetType, "mooncake_trace")
	}
	if !cfg.FixedSchedule {
		t.Errorf("FixedSchedule = false, want true")
	}
	if cfg.WorkersMax != 200 {
		t.Errorf("WorkersMax = %d, want 200", cfg.WorkersMax)
	}
	if cfg.RandomSeed != 42 {
		t.Errorf("RandomSeed = %d, want 42", cfg.RandomSeed)
	}
	if cfg.RequestTimeoutSeconds != 1000 {
		t.Errorf("RequestTimeoutSeconds = %d, want 1000", cfg.RequestTimeoutSeconds)
	}
	if cfg.ProfileExportLevel != "summary" {
		t.Errorf("ProfileExportLevel = %q, want %q", cfg.ProfileExportLevel, "summary")
	}
	if cfg.RecordProcessors != 8 {
		t.Errorf("RecordProcessors = %d, want 8", cfg.RecordProcessors)
	}
	if cfg.Goodput != "time_to_first_token:5000 inter_token_latency:100" {
		t.Errorf("Goodput = %q, want %q", cfg.Goodput, "time_to_first_token:5000 inter_token_latency:100")
	}
	if !cfg.Streaming {
		t.Errorf("Streaming = false, want true (mooncake base)")
	}
}

func TestExpandForgeScenario_Mooncake_HostHeaderPropagated(t *testing.T) {
	// HostHeader from base should still reach mooncake children.
	base := jumphost.AiperfConfig{HostHeader: "test.local"}
	children, err := expandForgeScenario("mooncake", base)
	if err != nil {
		t.Fatalf("expandForgeScenario(mooncake): %v", err)
	}
	if children[0].Config.HostHeader != "test.local" {
		t.Errorf("HostHeader = %q, want %q", children[0].Config.HostHeader, "test.local")
	}
}

// ---------------------------------------------------------------------------
// Scenario child-count assertions (all 8 synthetic scenarios)
// ---------------------------------------------------------------------------

// TestScenario_ChildCounts asserts that each scenario expands to exactly the
// expected number of children.
func TestScenario_ChildCounts(t *testing.T) {
	cases := []struct {
		key   string
		count int
	}{
		{"inferencex-agentx-mvp", 4}, // rates {2,4,8,16}
		{"baseline", 4},              // DEFAULT_SWEEP = {50,100,150,200}
		{"high-concurrency", 4},      // HEAVY_CONC_ISL = 4 pairs
		{"mixed-workload", 9},        // 1 warmup + 4 short + 4 long
		{"multi-turn", 16},           // 4 turns × 4 sweep = 16
		{"prefix-cache", 4},          // HEAVY_CONC_ISL = 4 pairs
		{"bimodal", 4},               // DEFAULT_SWEEP
		{"sustained-load", 5},        // sweep {50,100,150,200,250}
		{"burst-recovery", 10},       // 5 rounds × 2 (burst+probe)
		{"mooncake", 1},              // single trace variant
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			children, err := expandForgeScenario(tc.key, defaultBaseCfg())
			if err != nil {
				t.Fatalf("expandForgeScenario(%q): %v", tc.key, err)
			}
			if len(children) != tc.count {
				t.Errorf("child count = %d, want %d", len(children), tc.count)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// baseline — exact values per variant
// ---------------------------------------------------------------------------

func TestScenario_Baseline_Values(t *testing.T) {
	children, err := expandForgeScenario("baseline", defaultBaseCfg())
	if err != nil {
		t.Fatalf("expandForgeScenario: %v", err)
	}
	// DEFAULT_SWEEP = (50, 100, 150, 200), rc = max(c*5, 20)
	expected := []struct {
		label string
		c     int
		rc    int
		isl   int
		osl   int
	}{
		{"c50", 50, 250, 500, 128},
		{"c100", 100, 500, 500, 128},
		{"c150", 150, 750, 500, 128},
		{"c200", 200, 1000, 500, 128},
	}
	for i, ex := range expected {
		ch := children[i]
		if ch.VariantLabel != ex.label {
			t.Errorf("[%d] VariantLabel = %q, want %q", i, ch.VariantLabel, ex.label)
		}
		if ch.Config.Concurrency != ex.c {
			t.Errorf("[%d] Concurrency = %d, want %d", i, ch.Config.Concurrency, ex.c)
		}
		if ch.Config.NumRequests != ex.rc {
			t.Errorf("[%d] NumRequests = %d, want %d", i, ch.Config.NumRequests, ex.rc)
		}
		if ch.Config.ISL != ex.isl {
			t.Errorf("[%d] ISL = %d, want %d", i, ch.Config.ISL, ex.isl)
		}
		if ch.Config.OSL != ex.osl {
			t.Errorf("[%d] OSL = %d, want %d", i, ch.Config.OSL, ex.osl)
		}
		if !ch.Config.Streaming {
			t.Errorf("[%d] Streaming must be true (from synthetic base)", i)
		}
		assertExtraInputsContain(t, ch.Config.ExtraInputs, "ignore_eos:true", i)
	}
}

// ---------------------------------------------------------------------------
// high-concurrency — HEAVY_CONC_ISL pairs
// ---------------------------------------------------------------------------

func TestScenario_HighConcurrency_Values(t *testing.T) {
	children, err := expandForgeScenario("high-concurrency", defaultBaseCfg())
	if err != nil {
		t.Fatalf("expandForgeScenario: %v", err)
	}
	// HEAVY_CONC_ISL = ((150,5000),(200,7000),(250,9000),(300,10000)), rc=c*5
	expected := []struct {
		label string
		c     int
		isl   int
		rc    int
	}{
		{"c150-isl5000", 150, 5000, 750},
		{"c200-isl7000", 200, 7000, 1000},
		{"c250-isl9000", 250, 9000, 1250},
		{"c300-isl10000", 300, 10000, 1500},
	}
	for i, ex := range expected {
		ch := children[i]
		if ch.VariantLabel != ex.label {
			t.Errorf("[%d] VariantLabel = %q, want %q", i, ch.VariantLabel, ex.label)
		}
		if ch.Config.Concurrency != ex.c {
			t.Errorf("[%d] Concurrency = %d, want %d", i, ch.Config.Concurrency, ex.c)
		}
		if ch.Config.ISL != ex.isl {
			t.Errorf("[%d] ISL = %d, want %d", i, ch.Config.ISL, ex.isl)
		}
		if ch.Config.NumRequests != ex.rc {
			t.Errorf("[%d] NumRequests = %d, want %d", i, ch.Config.NumRequests, ex.rc)
		}
		if ch.Config.OSL != 128 {
			t.Errorf("[%d] OSL = %d, want 128", i, ch.Config.OSL)
		}
	}
}

// ---------------------------------------------------------------------------
// mixed-workload — warmup + short + long phases
// ---------------------------------------------------------------------------

func TestScenario_MixedWorkload_Structure(t *testing.T) {
	children, err := expandForgeScenario("mixed-workload", defaultBaseCfg())
	if err != nil {
		t.Fatalf("expandForgeScenario: %v", err)
	}

	// Index 0: warmup
	w := children[0]
	if w.VariantLabel != "warmup" {
		t.Errorf("children[0].VariantLabel = %q, want %q", w.VariantLabel, "warmup")
	}
	if w.Phase != "warmup" {
		t.Errorf("children[0].Phase = %q, want %q", w.Phase, "warmup")
	}
	if w.Config.Concurrency != 50 {
		t.Errorf("warmup Concurrency = %d, want 50", w.Config.Concurrency)
	}
	if w.Config.NumRequests != 150 {
		t.Errorf("warmup NumRequests = %d, want 150", w.Config.NumRequests)
	}

	// Indices 1-4: short phase (DEFAULT_SWEEP, rc=c*5, ISL=500, OSL=64)
	for i, c := range []int{50, 100, 150, 200} {
		ch := children[1+i]
		wantLabel := "short-c" + itoa(c)
		if ch.VariantLabel != wantLabel {
			t.Errorf("short[%d].VariantLabel = %q, want %q", i, ch.VariantLabel, wantLabel)
		}
		if ch.Phase != "short" {
			t.Errorf("short[%d].Phase = %q, want %q", i, ch.Phase, "short")
		}
		if ch.Config.ISL != 500 || ch.Config.OSL != 64 {
			t.Errorf("short[%d] ISL=%d OSL=%d, want ISL=500 OSL=64", i, ch.Config.ISL, ch.Config.OSL)
		}
		if ch.Config.NumRequests != c*5 {
			t.Errorf("short[%d] NumRequests = %d, want %d", i, ch.Config.NumRequests, c*5)
		}
	}

	// Indices 5-8: long phase (DEFAULT_SWEEP, rc=c*5, ISL=600, OSL=128, prefix)
	for i, c := range []int{50, 100, 150, 200} {
		ch := children[5+i]
		wantLabel := "long-c" + itoa(c)
		if ch.VariantLabel != wantLabel {
			t.Errorf("long[%d].VariantLabel = %q, want %q", i, ch.VariantLabel, wantLabel)
		}
		if ch.Phase != "long" {
			t.Errorf("long[%d].Phase = %q, want %q", i, ch.Phase, "long")
		}
		if ch.Config.ISL != 600 {
			t.Errorf("long[%d] ISL = %d, want 600", i, ch.Config.ISL)
		}
		if ch.Config.PrefixPromptLength != 1400 {
			t.Errorf("long[%d] PrefixPromptLength = %d, want 1400", i, ch.Config.PrefixPromptLength)
		}
		if ch.Config.NumPrefixPrompts != 10 {
			t.Errorf("long[%d] NumPrefixPrompts = %d, want 10", i, ch.Config.NumPrefixPrompts)
		}
		if ch.Config.NumRequests != c*5 {
			t.Errorf("long[%d] NumRequests = %d, want %d", i, ch.Config.NumRequests, c*5)
		}
	}
}

// ---------------------------------------------------------------------------
// multi-turn — 4 turns × 4 sweep = 16 children
// ---------------------------------------------------------------------------

func TestScenario_MultiTurn_Values(t *testing.T) {
	children, err := expandForgeScenario("multi-turn", defaultBaseCfg())
	if err != nil {
		t.Fatalf("expandForgeScenario: %v", err)
	}

	// Turn 1: no prefix (indices 0-3)
	for i, c := range []int{50, 100, 150, 200} {
		ch := children[i]
		if ch.VariantLabel != "turn1-c"+itoa(c) {
			t.Errorf("[%d] VariantLabel = %q", i, ch.VariantLabel)
		}
		if ch.Turn != 1 {
			t.Errorf("[%d] Turn = %d, want 1", i, ch.Turn)
		}
		if ch.Config.PrefixPromptLength != 0 {
			t.Errorf("[%d] turn1 PrefixPromptLength = %d, want 0 (no prefix)", i, ch.Config.PrefixPromptLength)
		}
	}

	// Turn 2: prefix=500 (indices 4-7)
	for i, c := range []int{50, 100, 150, 200} {
		ch := children[4+i]
		if ch.Turn != 2 {
			t.Errorf("turn2[%d] Turn = %d, want 2", i, ch.Turn)
		}
		if ch.Config.PrefixPromptLength != 500 {
			t.Errorf("turn2[%d] PrefixPromptLength = %d, want 500", i, ch.Config.PrefixPromptLength)
		}
		if ch.Config.NumPrefixPrompts != 10 {
			t.Errorf("turn2[%d] NumPrefixPrompts = %d, want 10", i, ch.Config.NumPrefixPrompts)
		}
		if ch.Config.NumRequests != c*5 {
			t.Errorf("turn2[%d] NumRequests = %d, want %d", i, ch.Config.NumRequests, c*5)
		}
	}

	// Turn 3: prefix=1000 (indices 8-11)
	for i := range []int{50, 100, 150, 200} {
		ch := children[8+i]
		if ch.Config.PrefixPromptLength != 1000 {
			t.Errorf("turn3[%d] PrefixPromptLength = %d, want 1000", i, ch.Config.PrefixPromptLength)
		}
	}

	// Turn 4: prefix=1500 (indices 12-15)
	for i := range []int{50, 100, 150, 200} {
		ch := children[12+i]
		if ch.Config.PrefixPromptLength != 1500 {
			t.Errorf("turn4[%d] PrefixPromptLength = %d, want 1500", i, ch.Config.PrefixPromptLength)
		}
	}
}

// ---------------------------------------------------------------------------
// prefix-cache — 80% prefix sharing formula
// ---------------------------------------------------------------------------

func TestScenario_PrefixCache_Values(t *testing.T) {
	children, err := expandForgeScenario("prefix-cache", defaultBaseCfg())
	if err != nil {
		t.Fatalf("expandForgeScenario: %v", err)
	}

	// HEAVY_CONC_ISL = ((150,5000),(200,7000),(250,9000),(300,10000))
	// prefix = ISL*8//10, unique = ISL-prefix, stddev = unique//10, num_prefix_prompts=20
	type expectRow struct {
		label         string
		c             int
		isl           int
		prefixLen     int
		uniqueLen     int
		stddev        int
		numPfxPrompts int
	}
	expected := []expectRow{
		{"isl5000-c150", 150, 5000, 4000, 1000, 100, 20},
		{"isl7000-c200", 200, 7000, 5600, 1400, 140, 20},
		{"isl9000-c250", 250, 9000, 7200, 1800, 180, 20},
		{"isl10000-c300", 300, 10000, 8000, 2000, 200, 20},
	}
	for i, ex := range expected {
		ch := children[i]
		if ch.VariantLabel != ex.label {
			t.Errorf("[%d] VariantLabel = %q, want %q", i, ch.VariantLabel, ex.label)
		}
		if ch.Config.Concurrency != ex.c {
			t.Errorf("[%d] Concurrency = %d, want %d", i, ch.Config.Concurrency, ex.c)
		}
		if ch.Config.ISL != ex.uniqueLen {
			t.Errorf("[%d] ISL(unique) = %d, want %d", i, ch.Config.ISL, ex.uniqueLen)
		}
		if ch.Config.ISLStddev != ex.stddev {
			t.Errorf("[%d] ISLStddev = %d, want %d", i, ch.Config.ISLStddev, ex.stddev)
		}
		if ch.Config.PrefixPromptLength != ex.prefixLen {
			t.Errorf("[%d] PrefixPromptLength = %d, want %d", i, ch.Config.PrefixPromptLength, ex.prefixLen)
		}
		if ch.Config.NumPrefixPrompts != ex.numPfxPrompts {
			t.Errorf("[%d] NumPrefixPrompts = %d, want %d", i, ch.Config.NumPrefixPrompts, ex.numPfxPrompts)
		}
		if ch.Config.OSL != 128 {
			t.Errorf("[%d] OSL = %d, want 128", i, ch.Config.OSL)
		}
		if ch.Config.NumRequests != ex.c*5 {
			t.Errorf("[%d] NumRequests = %d, want %d", i, ch.Config.NumRequests, ex.c*5)
		}
	}
}

// ---------------------------------------------------------------------------
// bimodal — seq-dist string verbatim
// ---------------------------------------------------------------------------

func TestScenario_Bimodal_SeqDist(t *testing.T) {
	const wantSeqDist = "300|100,64|16:70;4000|500,256|64:30"

	children, err := expandForgeScenario("bimodal", defaultBaseCfg())
	if err != nil {
		t.Fatalf("expandForgeScenario: %v", err)
	}
	for i, ch := range children {
		if ch.Config.SeqDist != wantSeqDist {
			t.Errorf("[%d] SeqDist = %q, want %q", i, ch.Config.SeqDist, wantSeqDist)
		}
	}
	// rc = max(c*5, 20) for DEFAULT_SWEEP
	sweepAndRC := [][2]int{{50, 250}, {100, 500}, {150, 750}, {200, 1000}}
	for i, pair := range sweepAndRC {
		c, rc := pair[0], pair[1]
		if children[i].Config.Concurrency != c {
			t.Errorf("[%d] Concurrency = %d, want %d", i, children[i].Config.Concurrency, c)
		}
		if children[i].Config.NumRequests != rc {
			t.Errorf("[%d] NumRequests = %d, want %d", i, children[i].Config.NumRequests, rc)
		}
	}
}

// ---------------------------------------------------------------------------
// sustained-load — extended sweep + stddev
// ---------------------------------------------------------------------------

func TestScenario_SustainedLoad_Values(t *testing.T) {
	children, err := expandForgeScenario("sustained-load", defaultBaseCfg())
	if err != nil {
		t.Fatalf("expandForgeScenario: %v", err)
	}
	if len(children) != 5 {
		t.Fatalf("len = %d, want 5", len(children))
	}
	sweep := []int{50, 100, 150, 200, 250}
	for i, c := range sweep {
		ch := children[i]
		if ch.Config.Concurrency != c {
			t.Errorf("[%d] Concurrency = %d, want %d", i, ch.Config.Concurrency, c)
		}
		if ch.Config.NumRequests != c*10 {
			t.Errorf("[%d] NumRequests = %d, want %d", i, ch.Config.NumRequests, c*10)
		}
		if ch.Config.ISL != 1500 {
			t.Errorf("[%d] ISL = %d, want 1500", i, ch.Config.ISL)
		}
		if ch.Config.ISLStddev != 300 {
			t.Errorf("[%d] ISLStddev = %d, want 300", i, ch.Config.ISLStddev)
		}
		if ch.Config.OSL != 128 {
			t.Errorf("[%d] OSL = %d, want 128", i, ch.Config.OSL)
		}
	}
}

// ---------------------------------------------------------------------------
// burst-recovery — 5 rounds × burst+probe
// ---------------------------------------------------------------------------

func TestScenario_BurstRecovery_Values(t *testing.T) {
	const wantBurstSeqDist = "300|100,64|16:50;3000|400,200|50:50"

	children, err := expandForgeScenario("burst-recovery", defaultBaseCfg())
	if err != nil {
		t.Fatalf("expandForgeScenario: %v", err)
	}
	if len(children) != 10 {
		t.Fatalf("len = %d, want 10", len(children))
	}
	for rnd := 1; rnd <= 5; rnd++ {
		burst := children[(rnd-1)*2]
		probe := children[(rnd-1)*2+1]

		// Burst
		if burst.VariantLabel != "round"+itoa(rnd)+"-burst" {
			t.Errorf("round%d burst VariantLabel = %q", rnd, burst.VariantLabel)
		}
		if burst.Phase != "burst" {
			t.Errorf("round%d burst Phase = %q, want burst", rnd, burst.Phase)
		}
		if burst.Round != rnd {
			t.Errorf("round%d burst Round = %d, want %d", rnd, burst.Round, rnd)
		}
		if burst.Config.Concurrency != 200 {
			t.Errorf("round%d burst Concurrency = %d, want 200", rnd, burst.Config.Concurrency)
		}
		if burst.Config.NumRequests != 400 {
			t.Errorf("round%d burst NumRequests = %d, want 400", rnd, burst.Config.NumRequests)
		}
		if burst.Config.SeqDist != wantBurstSeqDist {
			t.Errorf("round%d burst SeqDist = %q, want %q", rnd, burst.Config.SeqDist, wantBurstSeqDist)
		}

		// Probe
		if probe.VariantLabel != "round"+itoa(rnd)+"-probe" {
			t.Errorf("round%d probe VariantLabel = %q", rnd, probe.VariantLabel)
		}
		if probe.Phase != "probe" {
			t.Errorf("round%d probe Phase = %q, want probe", rnd, probe.Phase)
		}
		if probe.Config.Concurrency != 25 {
			t.Errorf("round%d probe Concurrency = %d, want 25", rnd, probe.Config.Concurrency)
		}
		if probe.Config.NumRequests != 50 {
			t.Errorf("round%d probe NumRequests = %d, want 50", rnd, probe.Config.NumRequests)
		}
		if probe.Config.ISL != 256 {
			t.Errorf("round%d probe ISL = %d, want 256", rnd, probe.Config.ISL)
		}
		if probe.Config.ISLStddev != 50 {
			t.Errorf("round%d probe ISLStddev = %d, want 50", rnd, probe.Config.ISLStddev)
		}
		if probe.Config.OSL != 64 {
			t.Errorf("round%d probe OSL = %d, want 64", rnd, probe.Config.OSL)
		}
	}
}

// ---------------------------------------------------------------------------
// expandForgeScenario — base field merging
// ---------------------------------------------------------------------------

func TestExpandForgeScenario_BaseMerge_ModelOverride(t *testing.T) {
	base := defaultBaseCfg()
	base.Model = "my-custom-model"
	children, err := expandForgeScenario("baseline", base)
	if err != nil {
		t.Fatalf("expandForgeScenario: %v", err)
	}
	for i, ch := range children {
		if ch.Config.Model != "my-custom-model" {
			t.Errorf("[%d] Model = %q, want %q", i, ch.Config.Model, "my-custom-model")
		}
	}
}

func TestExpandForgeScenario_BaseMerge_ZeroModelKeepsScenarioDefault(t *testing.T) {
	// When base.Model is empty, the scenario's syntheticModel should be preserved.
	base := defaultBaseCfg()
	base.Model = ""
	children, err := expandForgeScenario("baseline", base)
	if err != nil {
		t.Fatalf("expandForgeScenario: %v", err)
	}
	for i, ch := range children {
		if ch.Config.Model != syntheticModel {
			t.Errorf("[%d] Model = %q, want %q (scenario default)", i, ch.Config.Model, syntheticModel)
		}
	}
}

// ---------------------------------------------------------------------------
// Registry completeness
// ---------------------------------------------------------------------------

func TestForgeScenarioRegistry_AllSyntheticKeys(t *testing.T) {
	wantKeys := []string{
		"baseline", "high-concurrency", "mixed-workload", "multi-turn",
		"prefix-cache", "bimodal", "sustained-load", "burst-recovery",
	}
	for _, key := range wantKeys {
		s, ok := forgeScenarioByKey(key)
		if !ok {
			t.Errorf("key %q not found in registry", key)
			continue
		}
		if s.TraceDriven {
			t.Errorf("key %q must not be trace-driven (synthetic scenario)", key)
		}
	}
}

func TestForgeScenarioRegistry_MooncakePresent(t *testing.T) {
	s, ok := forgeScenarioByKey("mooncake")
	if !ok {
		t.Error("mooncake not in registry (shape must be stubbed for WS-C2)")
	}
	if !s.TraceDriven {
		t.Error("mooncake must have TraceDriven=true")
	}
}

// ---------------------------------------------------------------------------
// synthetic base invariants — streaming + extra_inputs on all synthetic children
// ---------------------------------------------------------------------------

func TestScenario_SyntheticBase_AllChildren(t *testing.T) {
	syntheticKeys := []string{
		"baseline", "high-concurrency", "mixed-workload", "multi-turn",
		"prefix-cache", "bimodal", "sustained-load", "burst-recovery",
	}
	for _, key := range syntheticKeys {
		t.Run(key, func(t *testing.T) {
			children, err := expandForgeScenario(key, defaultBaseCfg())
			if err != nil {
				t.Fatalf("expandForgeScenario: %v", err)
			}
			for i, ch := range children {
				if !ch.Config.Streaming {
					t.Errorf("[%d] Streaming must be true (synthetic base invariant)", i)
				}
				assertExtraInputsContain(t, ch.Config.ExtraInputs, "ignore_eos:true", i)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// rcMin formula
// ---------------------------------------------------------------------------

func TestRcMin(t *testing.T) {
	cases := []struct{ c, want int }{
		{1, 20}, // floor: max(5, 20) = 20
		{3, 20}, // max(15, 20) = 20
		{4, 20}, // max(20, 20) = 20
		{5, 25}, // max(25, 20) = 25
		{50, 250},
		{100, 500},
		{150, 750},
		{200, 1000},
	}
	for _, tc := range cases {
		got := rcMin(tc.c)
		if got != tc.want {
			t.Errorf("rcMin(%d) = %d, want %d", tc.c, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// forgeScenarioConfigJSON
// ---------------------------------------------------------------------------

func TestForgeScenarioConfigJSON_KnownKey(t *testing.T) {
	m := forgeScenarioConfigJSON("baseline", "my-model", "10.0.10.100")
	if m["scenario_key"] != "baseline" {
		t.Errorf("scenario_key = %v, want baseline", m["scenario_key"])
	}
	if m["model"] != "my-model" {
		t.Errorf("model = %v, want my-model", m["model"])
	}
}

func TestForgeScenarioConfigJSON_UnknownKey(t *testing.T) {
	m := forgeScenarioConfigJSON("no-such", "model", "10.0.0.1")
	if m["scenario_key"] != "no-such" {
		t.Errorf("scenario_key = %v, want no-such", m["scenario_key"])
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// defaultBaseCfg returns a zero-value base config suitable for tests that do
// not need a specific model/tokenizer override.
func defaultBaseCfg() jumphost.AiperfConfig {
	return jumphost.AiperfConfig{}
}

// itoa is a minimal int-to-string helper so tests do not import strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := [20]byte{}
	pos := len(buf)
	for n >= 10 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	pos--
	buf[pos] = byte('0' + n)
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// assertExtraInputsContain fails the test if want is not in inputs.
func assertExtraInputsContain(t *testing.T, inputs []string, want string, idx int) {
	t.Helper()
	for _, v := range inputs {
		if v == want {
			return
		}
	}
	t.Errorf("[%d] ExtraInputs %v does not contain %q", idx, inputs, want)
}

// TestExpandForgeScenario_Poisson pins the open-loop sweep to forge's
// benchmark_scenarios.py values: rates, labels, durations and timeouts.
func TestExpandForgeScenario_Poisson(t *testing.T) {
	children, err := expandForgeScenario("poisson-rate", jumphost.AiperfConfig{Model: "llama3", Timeout: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 5 || children[0].VariantLabel != "2rps" || children[4].VariantLabel != "32rps" {
		t.Fatalf("steps = %+v", children)
	}
	c := children[0].Config
	if c.RequestRate != 2 || c.ArrivalPattern != "poisson" || c.BenchmarkDuration != 150 || c.WarmupDuration != 30 ||
		c.Concurrency != 0 || c.NumRequests != 0 || c.Model != "llama3" || c.RandomSeed != 42 {
		t.Errorf("2 rps step = %+v", c)
	}
	if want := (30+150+30)*time.Second + 5*time.Minute; c.Timeout != want {
		t.Errorf("timeout = %v, want %v (warmup + duration + grace + start-up)", c.Timeout, want)
	}
	prefix, _ := expandForgeScenario("poisson-rate-prefix", jumphost.AiperfConfig{})
	if p := prefix[0].Config; p.RequestRate != 1 || p.PrefixPromptLength != 4000 || p.NumPrefixPrompts != 20 || p.BenchmarkDuration != 300 {
		t.Errorf("prefix 1 rps step = %+v", p)
	}
}

func TestExpandForgeScenario_InferenceXAgentXMvp(t *testing.T) {
	base := jumphost.AiperfConfig{
		Model:      "llama3",
		Tokenizer:  "NousResearch/Meta-Llama-3-8B-Instruct",
		HostHeader: "gkebnkctl-aiinference.local",
		Timeout:    5 * time.Minute,
	}
	children, err := expandForgeScenario("inferencex-agentx-mvp", base)
	if err != nil {
		t.Fatalf("expandForgeScenario(inferencex-agentx-mvp): %v", err)
	}
	if len(children) != 4 {
		t.Fatalf("len(children) = %d, want 4", len(children))
	}
	wantRates := []float64{2, 4, 8, 16}
	wantLabels := []string{"2rps", "4rps", "8rps", "16rps"}
	for i, ch := range children {
		if ch.VariantLabel != wantLabels[i] {
			t.Errorf("[%d] VariantLabel = %q, want %q", i, ch.VariantLabel, wantLabels[i])
		}
		cfg := ch.Config
		if cfg.Model != "llama3" {
			t.Errorf("[%d] Model = %q, want llama3", i, cfg.Model)
		}
		if cfg.Scenario != "inferencex-agentx-mvp" {
			t.Errorf("[%d] Scenario = %q, want inferencex-agentx-mvp", i, cfg.Scenario)
		}
		if cfg.PublicDataset != "custom_multi_turn" {
			t.Errorf("[%d] PublicDataset = %q, want custom_multi_turn", i, cfg.PublicDataset)
		}
		if cfg.RequestRate != wantRates[i] {
			t.Errorf("[%d] RequestRate = %v, want %v", i, cfg.RequestRate, wantRates[i])
		}
		if !cfg.UseServerTokenCount {
			t.Errorf("[%d] UseServerTokenCount = false, want true", i)
		}
		if cfg.MaxContextLength != 32768 {
			t.Errorf("[%d] MaxContextLength = %d, want 32768", i, cfg.MaxContextLength)
		}
		if len(cfg.UnsafeOverride) != 1 || cfg.UnsafeOverride[0] != "dataset.conversation_num=30" {
			t.Errorf("[%d] UnsafeOverride = %v, want [dataset.conversation_num=30]", i, cfg.UnsafeOverride)
		}
		if cfg.Tokenizer != "" {
			t.Errorf("[%d] Tokenizer = %q, want empty (native scenario uses server token count)", i, cfg.Tokenizer)
		}
		if cfg.HostHeader != "gkebnkctl-aiinference.local" {
			t.Errorf("[%d] HostHeader = %q, want gkebnkctl-aiinference.local", i, cfg.HostHeader)
		}
	}
}

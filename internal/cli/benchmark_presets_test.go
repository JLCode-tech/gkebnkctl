package cli

import (
	"testing"
)

// ---------------------------------------------------------------------------
// benchmarkPreset table
// ---------------------------------------------------------------------------

func TestBenchmarkPresets_AllNamesDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range benchmarkPresets {
		if seen[p.Name] {
			t.Errorf("duplicate preset name %q", p.Name)
		}
		seen[p.Name] = true
	}
}

func TestBenchmarkPresets_ExpectedNames(t *testing.T) {
	want := []string{"latency", "throughput", "long-context", "streaming"}
	for _, name := range want {
		found := false
		for _, p := range benchmarkPresets {
			if p.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected preset %q not found in table", name)
		}
	}
}

// TestBenchmarkPresets_ParameterSpot spot-checks a few preset values.
func TestBenchmarkPresets_ParameterSpot(t *testing.T) {
	cases := []struct {
		name        string
		concurrency int
		numRequests int
		isl         int
		osl         int
		streaming   bool
	}{
		{"latency", 1, 50, 512, 128, true},
		{"throughput", 32, 500, 512, 128, false},
		{"long-context", 4, 50, 4096, 512, true},
		{"streaming", 8, 200, 512, 256, true},
	}
	for _, tc := range cases {
		p, err := benchmarkPresetByName(tc.name)
		if err != nil {
			t.Errorf("preset %q: %v", tc.name, err)
			continue
		}
		if p.Config.Concurrency != tc.concurrency {
			t.Errorf("preset %q: Concurrency = %d, want %d", tc.name, p.Config.Concurrency, tc.concurrency)
		}
		if p.Config.NumRequests != tc.numRequests {
			t.Errorf("preset %q: NumRequests = %d, want %d", tc.name, p.Config.NumRequests, tc.numRequests)
		}
		if p.Config.ISL != tc.isl {
			t.Errorf("preset %q: ISL = %d, want %d", tc.name, p.Config.ISL, tc.isl)
		}
		if p.Config.OSL != tc.osl {
			t.Errorf("preset %q: OSL = %d, want %d", tc.name, p.Config.OSL, tc.osl)
		}
		if p.Config.Streaming != tc.streaming {
			t.Errorf("preset %q: Streaming = %v, want %v", tc.name, p.Config.Streaming, tc.streaming)
		}
	}
}

// ---------------------------------------------------------------------------
// benchmarkPresetByName
// ---------------------------------------------------------------------------

func TestBenchmarkPresetByName_UnknownReturnsError(t *testing.T) {
	_, err := benchmarkPresetByName("does-not-exist")
	if err == nil {
		t.Error("expected error for unknown preset name, got nil")
	}
}

func TestBenchmarkPresetByName_KnownReturnsPreset(t *testing.T) {
	p, err := benchmarkPresetByName("latency")
	if err != nil {
		t.Fatalf("benchmarkPresetByName(latency): %v", err)
	}
	if p.Name != "latency" {
		t.Errorf("Name = %q, want %q", p.Name, "latency")
	}
}

// ---------------------------------------------------------------------------
// resolveBenchmarkScenarios
// ---------------------------------------------------------------------------

func TestResolveBenchmarkScenarios_EmptyReturnsNil(t *testing.T) {
	presets, err := resolveBenchmarkScenarios("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(presets) != 0 {
		t.Errorf("expected nil/empty slice for empty flag, got %d items", len(presets))
	}
}

func TestResolveBenchmarkScenarios_AllExpandsToAllPresets(t *testing.T) {
	presets, err := resolveBenchmarkScenarios("all")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(presets) != len(benchmarkPresets) {
		t.Errorf("'all' expanded to %d presets, want %d", len(presets), len(benchmarkPresets))
	}
	for i, p := range presets {
		if p.Name != benchmarkPresets[i].Name {
			t.Errorf("presets[%d].Name = %q, want %q", i, p.Name, benchmarkPresets[i].Name)
		}
	}
}

func TestResolveBenchmarkScenarios_CommaSeparated(t *testing.T) {
	presets, err := resolveBenchmarkScenarios("latency,throughput")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(presets) != 2 {
		t.Fatalf("expected 2 presets, got %d", len(presets))
	}
	if presets[0].Name != "latency" {
		t.Errorf("presets[0].Name = %q, want %q", presets[0].Name, "latency")
	}
	if presets[1].Name != "throughput" {
		t.Errorf("presets[1].Name = %q, want %q", presets[1].Name, "throughput")
	}
}

func TestResolveBenchmarkScenarios_UnknownNameErrors(t *testing.T) {
	_, err := resolveBenchmarkScenarios("latency,bad-preset")
	if err == nil {
		t.Error("expected error for unknown preset name in comma list, got nil")
	}
}

func TestResolveBenchmarkScenarios_SpacesAroundNames(t *testing.T) {
	presets, err := resolveBenchmarkScenarios(" latency , streaming ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(presets) != 2 {
		t.Fatalf("expected 2 presets, got %d", len(presets))
	}
}

func TestResolveBenchmarkScenarios_SinglePreset(t *testing.T) {
	presets, err := resolveBenchmarkScenarios("long-context")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(presets) != 1 || presets[0].Name != "long-context" {
		t.Errorf("unexpected result: %v", presets)
	}
}

// ---------------------------------------------------------------------------
// presetRunLabel
// ---------------------------------------------------------------------------

func TestPresetRunLabel_WithBase(t *testing.T) {
	got := presetRunLabel("nightly-ci", "latency")
	want := "nightly-ci-latency"
	if got != want {
		t.Errorf("presetRunLabel(%q, %q) = %q, want %q", "nightly-ci", "latency", got, want)
	}
}

func TestPresetRunLabel_EmptyBase(t *testing.T) {
	got := presetRunLabel("", "throughput")
	want := "throughput"
	if got != want {
		t.Errorf("presetRunLabel(%q, %q) = %q, want %q", "", "throughput", got, want)
	}
}

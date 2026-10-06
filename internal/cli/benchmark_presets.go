package cli

import (
	"fmt"
	"strings"

	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
)

// benchmarkPreset holds the fixed run parameters for a named benchmark scenario.
type benchmarkPreset struct {
	// Name is the canonical preset identifier used with --scenarios.
	Name string
	// Description is a short human-readable label forwarded to forge as the run_label suffix.
	Description string
	// Config is the aiperf configuration for this preset.
	Config jumphost.AiperfConfig
}

// benchmarkPresets is the canonical table of named presets for --scenarios.
// Each preset maps a well-known name to a set of aiperf parameters that
// exercises a distinct performance dimension of the BNK Gateway VIP.
//
// Preset descriptions:
//
//	latency      — single-user TTFT/latency baseline
//	throughput   — max sustained request throughput
//	long-context — long-context memory and decode performance
//	streaming    — mid-load streaming token delivery
var benchmarkPresets = []benchmarkPreset{
	{
		Name:        "latency",
		Description: "single-user latency / TTFT",
		Config: jumphost.AiperfConfig{
			Concurrency: 1,
			NumRequests: 50,
			ISL:         512,
			OSL:         128,
			Streaming:   true,
		},
	},
	{
		Name:        "throughput",
		Description: "max throughput",
		Config: jumphost.AiperfConfig{
			Concurrency: 32,
			NumRequests: 500,
			ISL:         512,
			OSL:         128,
			Streaming:   false,
		},
	},
	{
		Name:        "long-context",
		Description: "long-context",
		Config: jumphost.AiperfConfig{
			Concurrency: 4,
			NumRequests: 50,
			ISL:         4096,
			OSL:         512,
			Streaming:   true,
		},
	},
	{
		Name:        "streaming",
		Description: "streaming mid-load",
		Config: jumphost.AiperfConfig{
			Concurrency: 8,
			NumRequests: 200,
			ISL:         512,
			OSL:         256,
			Streaming:   true,
		},
	},
}

// benchmarkPresetByName returns the preset with the given name, or an error if
// the name is not in the table.
func benchmarkPresetByName(name string) (benchmarkPreset, error) {
	for _, p := range benchmarkPresets {
		if p.Name == name {
			return p, nil
		}
	}
	names := make([]string, len(benchmarkPresets))
	for i, p := range benchmarkPresets {
		names[i] = p.Name
	}
	return benchmarkPreset{}, fmt.Errorf("unknown preset %q (valid: %s)", name, strings.Join(names, ", "))
}

// resolveBenchmarkScenarios parses the --scenarios flag value into a validated
// slice of presets. Accepts comma-separated preset names or the literal "all".
// Returns an error on the first unknown name.
func resolveBenchmarkScenarios(flag string) ([]benchmarkPreset, error) {
	flag = strings.TrimSpace(flag)
	if flag == "" {
		return nil, nil
	}
	if flag == "all" {
		return append([]benchmarkPreset(nil), benchmarkPresets...), nil
	}
	parts := strings.Split(flag, ",")
	out := make([]benchmarkPreset, 0, len(parts))
	for _, raw := range parts {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		p, err := benchmarkPresetByName(name)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// presetRunLabel derives the per-preset run-label from the shared --run-label.
// If the base label is non-empty, the result is "<base>-<preset>"; otherwise
// it is just "<preset>".
func presetRunLabel(base, preset string) string {
	if base == "" {
		return preset
	}
	return base + "-" + preset
}

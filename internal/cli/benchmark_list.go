package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var benchmarkListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available native forge scenarios and smoke presets",
	Long: `List all built-in benchmark scenarios and presets available for
'gkebnkctl benchmark run --scenario <key>' or '--scenarios <presets>'.

Native forge scenarios expand into structured multi-run sweeps or trace replays
matching forge's benchmark engine. Smoke presets provide lightweight, single-dimension
verification workloads.`,
	RunE: runBenchmarkList,
}

// scenarioListItem is the output structure for JSON / text display.
type scenarioListItem struct {
	Type        string   `json:"type"` // "native" or "preset"
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	TraceDriven bool     `json:"trace_driven,omitempty"`
}

func runBenchmarkList(cmd *cobra.Command, _ []string) error {
	var items []scenarioListItem

	// 1. Native Forge Scenarios
	for _, s := range forgeScenarioRegistry {
		items = append(items, scenarioListItem{
			Type:        "native",
			Key:         s.Key,
			Name:        s.Name,
			Description: s.Description,
			Tags:        s.Tags,
			TraceDriven: s.TraceDriven,
		})
	}

	// 2. Smoke Presets
	for _, p := range benchmarkPresets {
		items = append(items, scenarioListItem{
			Type:        "preset",
			Key:         p.Name,
			Name:        p.Name,
			Description: p.Description,
			Tags:        []string{"smoke-preset"},
		})
	}

	if flagOutput == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"schema":    "gkebnkctl.benchmark.list.v1",
			"scenarios": items,
		})
	}

	// Tabular text output
	fmt.Println("NATIVE FORGE SCENARIOS (use with --scenario <key>):")
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "  KEY\tNAME\tTAGS\tDESCRIPTION")
	fmt.Fprintln(tw, "  ---\t----\t----\t-----------")
	for _, it := range items {
		if it.Type != "native" {
			continue
		}
		tags := strings.Join(it.Tags, ", ")
		desc := it.Description
		if len(desc) > 65 {
			desc = desc[:62] + "..."
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", it.Key, it.Name, tags, desc)
	}
	_ = tw.Flush()

	fmt.Println("\nSMOKE PRESETS (use with --scenarios <name>):")
	tw2 := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw2, "  NAME\tDESCRIPTION")
	fmt.Fprintln(tw2, "  ----\t-----------")
	for _, it := range items {
		if it.Type != "preset" {
			continue
		}
		fmt.Fprintf(tw2, "  %s\t%s\n", it.Key, it.Description)
	}
	return tw2.Flush()
}

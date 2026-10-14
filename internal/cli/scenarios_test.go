package cli

import (
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/scenarios"
)

// fakeScenarioCLI is a minimal test double for topo-sort tests.
type fakeScenarioCLI struct {
	name string
	deps []string
}

func (f *fakeScenarioCLI) Name() string                                   { return f.name }
func (f *fakeScenarioCLI) Title() string                                  { return "Fake: " + f.name }
func (f *fakeScenarioCLI) Rating() scenarios.Rating                       { return scenarios.Green }
func (f *fakeScenarioCLI) Description() string                            { return "fake" }
func (f *fakeScenarioCLI) Dependencies() []string                         { return f.deps }
func (f *fakeScenarioCLI) Manifests(*scenarios.Context) ([]string, error) { return nil, nil }
func (f *fakeScenarioCLI) Apply(*scenarios.Context) error                 { return nil }
func (f *fakeScenarioCLI) Verify(*scenarios.Context) scenarios.Result     { return scenarios.Result{} }
func (f *fakeScenarioCLI) Cleanup(*scenarios.Context) error               { return nil }
func (f *fakeScenarioCLI) Namespace(*scenarios.Context) string            { return "fake-ns" }

func TestTopoSort_NoDeps(t *testing.T) {
	a := &fakeScenarioCLI{name: "a"}
	b := &fakeScenarioCLI{name: "b"}
	sorted, err := topoSort([]scenarios.Scenario{a, b})
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	if len(sorted) != 2 {
		t.Errorf("sorted len = %d, want 2", len(sorted))
	}
}

func TestTopoSort_DepOrdering(t *testing.T) {
	// b depends on a -> a must come first.
	a := &fakeScenarioCLI{name: "a"}
	b := &fakeScenarioCLI{name: "b", deps: []string{"a"}}
	sorted, err := topoSort([]scenarios.Scenario{b, a}) // intentionally reversed input
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	if len(sorted) != 2 {
		t.Fatalf("sorted len = %d, want 2", len(sorted))
	}
	if sorted[0].Name() != "a" || sorted[1].Name() != "b" {
		t.Errorf("order = [%s, %s], want [a, b]", sorted[0].Name(), sorted[1].Name())
	}
}

func TestTopoSort_DepCycle_Errors(t *testing.T) {
	a := &fakeScenarioCLI{name: "a", deps: []string{"b"}}
	b := &fakeScenarioCLI{name: "b", deps: []string{"a"}}
	_, err := topoSort([]scenarios.Scenario{a, b})
	if err == nil {
		t.Fatal("expected error for dep cycle, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error %q does not mention 'cycle'", err.Error())
	}
}

func TestTopoSort_SingleScenario(t *testing.T) {
	a := &fakeScenarioCLI{name: "only"}
	sorted, err := topoSort([]scenarios.Scenario{a})
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	if len(sorted) != 1 || sorted[0].Name() != "only" {
		t.Errorf("sorted = %v, want [only]", sorted)
	}
}

func TestTopoSort_CoreFileCollectionSortedLast(t *testing.T) {
	core := &fakeScenarioCLI{name: "core-file-collection"}
	a := &fakeScenarioCLI{name: "http-routing-e2e"}
	b := &fakeScenarioCLI{name: "cluster-wide-watch"}
	// Put core-file-collection first in input slice
	sorted, err := topoSort([]scenarios.Scenario{core, a, b})
	if err != nil {
		t.Fatalf("topoSort: %v", err)
	}
	if len(sorted) != 3 {
		t.Fatalf("sorted len = %d, want 3", len(sorted))
	}
	if sorted[2].Name() != "core-file-collection" {
		t.Errorf("last scenario = %s, want core-file-collection", sorted[2].Name())
	}
}

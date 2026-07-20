package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhase36FLO_DryRun_NoClientsNeeded(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	if err := Phase36FLO(context.Background(), cl, st, nil, true); err != nil {
		t.Fatalf("Phase36FLO(dryRun=true): %v", err)
	}
}

func TestPhase36FLO_LiveMode_NilClientsErrors(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	err := Phase36FLO(context.Background(), cl, st, &gcp.Clients{}, false)
	if err == nil {
		t.Fatal("expected an error when Clients.Dynamic is nil in live mode")
	}
	if !strings.Contains(err.Error(), "AttachK8s") {
		t.Errorf("error = %q, want a hint to call AttachK8s first", err.Error())
	}
}

// TestFLOCRDs_IncludesAllFiveContractCRDs proves the CRD wait list matches
// docs/SPEC-go-tracer.md §3 Phase36 exactly.
func TestFLOCRDs_IncludesAllFiveContractCRDs(t *testing.T) {
	want := map[string]bool{
		"cneinstances.k8s.f5.com":         false,
		"f5spkvlans.k8s.f5net.com":        false,
		"f5spkstaticroutes.k8s.f5net.com": false,
		"f5spkegresses.k8s.f5net.com":     false,
		"licenses.k8s.f5net.com":          false,
	}
	for _, c := range floCRDs {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for crd, found := range want {
		if !found {
			t.Errorf("expected floCRDs to include %s, got %v", crd, floCRDs)
		}
	}
}

// TestFLOManifest_IsEmbeddedAndParses proves the ~3 MB FLO manifest is
// embedded and parses as a multi-document YAML stream — the apply path
// itself (server-side SSA on a manifest this large) is exercised only by
// dry-run/live-cluster runs, per docs/SPEC-go-tracer.md §7.
func TestFLOManifest_IsEmbeddedAndParses(t *testing.T) {
	data := readManifestForTest(t, floManifestPath)
	if len(data) < 1_000_000 {
		t.Errorf("expected the FLO manifest to be ~3 MB, got %d bytes — did the embed pick up the real file?", len(data))
	}
	names := parseManifestNamesForTest(t, floManifestPath, "CustomResourceDefinition")
	if len(names) == 0 {
		t.Error("expected at least one CustomResourceDefinition in the FLO manifest")
	}
}

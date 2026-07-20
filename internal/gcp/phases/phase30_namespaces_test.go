package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhase30Namespaces_DryRun_NoClientsNeeded(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	// A nil *gcp.Clients (the GKEBNKCTL_SKIP_AUTH=1 dry-run path) must never
	// be dereferenced before the dry-run return.
	if err := Phase30Namespaces(context.Background(), cl, st, nil, true); err != nil {
		t.Fatalf("Phase30Namespaces(dryRun=true): %v", err)
	}
}

// TestPhase30Namespaces_LiveMode_NilClientsErrors proves the live-mode path
// fails fast (rather than nil-panicking) when Clients.Dynamic/RESTMapper
// aren't attached.
func TestPhase30Namespaces_LiveMode_NilClientsErrors(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	err := Phase30Namespaces(context.Background(), cl, st, &gcp.Clients{}, false)
	if err == nil {
		t.Fatal("expected an error when Clients.Dynamic is nil in live mode")
	}
	if !strings.Contains(err.Error(), "AttachK8s") {
		t.Errorf("error = %q, want a hint to call AttachK8s first", err.Error())
	}
}

// TestNamespacesManifest_ParsesToThreeNamespaces proves the embedded static
// manifest this phase applies parses to exactly f5-bnk, f5-utils,
// egress-test — a regression guard independent of the SSA apply path (which
// dynamicfake cannot exercise — see docs/SPEC-go-tracer.md §7).
func TestNamespacesManifest_ParsesToThreeNamespaces(t *testing.T) {
	names := parseManifestNamesForTest(t, namespacesManifestPath, "Namespace")
	want := map[string]bool{"f5-bnk": false, "f5-utils": false, "egress-test": false}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected namespace %s in %s, got names=%v", name, namespacesManifestPath, names)
		}
	}
}

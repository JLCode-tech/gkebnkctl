package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhase34Multus_DryRun_NoClientsNeeded(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	if err := Phase34Multus(context.Background(), cl, st, nil, true); err != nil {
		t.Fatalf("Phase34Multus(dryRun=true): %v", err)
	}
}

func TestPhase34Multus_LiveMode_NilClientsErrors(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	err := Phase34Multus(context.Background(), cl, st, &gcp.Clients{}, false)
	if err == nil {
		t.Fatal("expected an error when Clients.Dynamic is nil in live mode")
	}
	if !strings.Contains(err.Error(), "AttachK8s") {
		t.Errorf("error = %q, want a hint to call AttachK8s first", err.Error())
	}
}

// TestMultusManifestPaths_OrderIsRBACThenCRDThenConfigMapThenDS proves the
// apply order matches the provenance note on multus/02-multus-rbac.yaml —
// the DaemonSet alone fails without RBAC/CRD/ConfigMap applied first.
func TestMultusManifestPaths_OrderIsRBACThenCRDThenConfigMapThenDS(t *testing.T) {
	want := []string{
		"multus/02-multus-rbac.yaml",
		"multus/02-multus-crd.yaml",
		"multus/02-multus-configmap.yaml",
		"multus/02-multus-ds.yaml",
	}
	if len(multusManifestPaths) != len(want) {
		t.Fatalf("got %d paths, want %d: %v", len(multusManifestPaths), len(want), multusManifestPaths)
	}
	for i, w := range want {
		if multusManifestPaths[i] != w {
			t.Errorf("multusManifestPaths[%d] = %q, want %q", i, multusManifestPaths[i], w)
		}
	}
}

func TestMultusManifests_ParseCleanly(t *testing.T) {
	for _, path := range multusManifestPaths {
		// A DaemonSet/ServiceAccount/ClusterRole/etc. name check per file
		// is overkill here; ParseYAMLStream returning without error is the
		// meaningful assertion (the SSA apply path itself is a known
		// dynamicfake boundary — see docs/SPEC-go-tracer.md §7).
		if names := parseManifestNamesForTest(t, path, "DaemonSet"); path == "multus/02-multus-ds.yaml" && len(names) != 1 {
			t.Errorf("expected exactly 1 DaemonSet in %s, got %v", path, names)
		}
	}
}

func TestMultusDS_NodeSelectorTargetsDataplanePools(t *testing.T) {
	data := readManifestForTest(t, "multus/02-multus-ds.yaml")
	if !strings.Contains(string(data), `f5-dataplane: 'true'`) {
		t.Error("expected multus DS nodeSelector f5-dataplane: 'true' — matches Phase20's tmm-pool/agent-pool node labels")
	}
}

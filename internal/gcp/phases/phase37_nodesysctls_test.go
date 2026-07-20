package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhase37NodeSysctls_DryRun_NoClientsNeeded(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	if err := Phase37NodeSysctls(context.Background(), cl, st, nil, true); err != nil {
		t.Fatalf("Phase37NodeSysctls(dryRun=true): %v", err)
	}
}

func TestPhase37NodeSysctls_LiveMode_NilClientsErrors(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	err := Phase37NodeSysctls(context.Background(), cl, st, &gcp.Clients{}, false)
	if err == nil {
		t.Fatal("expected an error when Clients.Dynamic is nil in live mode")
	}
	if !strings.Contains(err.Error(), "AttachK8s") {
		t.Errorf("error = %q, want a hint to call AttachK8s first", err.Error())
	}
}

func TestNodeSysctlsManifest_TargetsF5TMMNodes(t *testing.T) {
	dsNames := parseManifestNamesForTest(t, nodeSysctlsManifestPath, "DaemonSet")
	if len(dsNames) != 1 || dsNames[0] != "bnk-node-sysctls" {
		t.Fatalf("expected exactly 1 DaemonSet named bnk-node-sysctls, got %v", dsNames)
	}
	data := readManifestForTest(t, nodeSysctlsManifestPath)
	if !strings.Contains(string(data), `"app": "f5-tmm"`) {
		t.Error("expected nodeSelector app: f5-tmm on bnk-node-sysctls")
	}
}

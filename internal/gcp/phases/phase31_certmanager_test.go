package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhase31CertManager_DryRun_NoClientsNeeded(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	if err := Phase31CertManager(context.Background(), cl, st, nil, true); err != nil {
		t.Fatalf("Phase31CertManager(dryRun=true): %v", err)
	}
}

func TestPhase31CertManager_LiveMode_NilClientsErrors(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	err := Phase31CertManager(context.Background(), cl, st, &gcp.Clients{}, false)
	if err == nil {
		t.Fatal("expected an error when Clients.Dynamic is nil in live mode")
	}
	if !strings.Contains(err.Error(), "AttachK8s") {
		t.Errorf("error = %q, want a hint to call AttachK8s first", err.Error())
	}
}

func TestCertManagerDeployments_ControllerAndWebhook(t *testing.T) {
	want := map[string]bool{"cert-manager": false, "cert-manager-webhook": false}
	for _, d := range certManagerDeployments {
		if _, ok := want[d]; ok {
			want[d] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected certManagerDeployments to include %s, got %v", name, certManagerDeployments)
		}
	}
}

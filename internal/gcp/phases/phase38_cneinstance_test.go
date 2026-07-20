package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhase38CNEInstance_DryRun_NoClientsNeeded(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	if err := Phase38CNEInstance(context.Background(), cl, st, nil, true); err != nil {
		t.Fatalf("Phase38CNEInstance(dryRun=true): %v", err)
	}
}

func TestPhase38CNEInstance_LiveMode_RendersBeforeFailingOnNilClients(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	st.Set("TMM_EXT_PCI", "0000:00:05.0")
	st.Set("TMM_INT_PCI", "0000:00:06.0")

	err := Phase38CNEInstance(context.Background(), cl, st, &gcp.Clients{}, false)
	if err == nil {
		t.Fatal("expected an error when Clients.Dynamic is nil in live mode")
	}
	if strings.Contains(err.Error(), "rendering") {
		t.Errorf("expected the render step to succeed and the apply step to fail, got a render error: %v", err)
	}
	if !strings.Contains(err.Error(), "AttachK8s") {
		t.Errorf("error = %q, want a hint to call AttachK8s first", err.Error())
	}
}

package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhase35NADs_DryRun_NoClientsNeeded(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	if err := Phase35NADs(context.Background(), cl, st, nil, true); err != nil {
		t.Fatalf("Phase35NADs(dryRun=true): %v", err)
	}
}

// TestPhase35NADs_LiveMode_RendersBeforeFailingOnNilClients proves the
// template renders successfully off state values before the (expected,
// since Clients.Dynamic is nil here) apply failure — i.e. a render bug
// would surface as a "rendering NADs" error, not an opaque apply error.
func TestPhase35NADs_LiveMode_RendersBeforeFailingOnNilClients(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	st.Set("TMM_EXT_IFACE", "ens5")
	st.Set("TMM_EXT_SELFIP", "10.31.0.50")
	st.Set("TMM_INT_IFACE", "ens6")
	st.Set("TMM_INT_SELFIP", "10.51.0.50")

	err := Phase35NADs(context.Background(), cl, st, &gcp.Clients{}, false)
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

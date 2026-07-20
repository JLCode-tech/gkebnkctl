package phases

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhase40License_DryRun_NoClientsOrJwtNeeded(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	// JwtPath is intentionally left empty (testCluster()'s default) — the
	// dry-run branch must return before ever reading cl.Bnk.JwtPath.
	if err := Phase40License(context.Background(), cl, st, nil, true); err != nil {
		t.Fatalf("Phase40License(dryRun=true): %v", err)
	}
}

func TestPhase40License_MissingJwtFileErrors(t *testing.T) {
	cl := testCluster()
	cl.Bnk.JwtPath = filepath.Join(t.TempDir(), "does-not-exist.jwt")
	st := testState(t)
	err := Phase40License(context.Background(), cl, st, &gcp.Clients{}, false)
	if err == nil {
		t.Fatal("expected an error for a missing JWT file")
	}
	if !strings.Contains(err.Error(), "reading JWT") {
		t.Errorf("error = %q, want a JWT-read error", err.Error())
	}
}

// TestPhase40License_LiveMode_RendersBeforeFailingOnNilClients proves the
// JWT is read + trimmed + rendered successfully before the (expected, since
// Clients.Dynamic is nil here) apply failure.
func TestPhase40License_LiveMode_RendersBeforeFailingOnNilClients(t *testing.T) {
	cl := testCluster()
	cl.Bnk.JwtPath = writeTempFile(t, "  eyFAKE.JWT.TOKEN  \n")
	st := testState(t)

	err := Phase40License(context.Background(), cl, st, &gcp.Clients{}, false)
	if err == nil {
		t.Fatal("expected an error when Clients.Dynamic is nil in live mode")
	}
	if strings.Contains(err.Error(), "reading JWT") || strings.Contains(err.Error(), "rendering") {
		t.Errorf("expected JWT read + render to succeed and the apply step to fail, got: %v", err)
	}
	if !strings.Contains(err.Error(), "AttachK8s") {
		t.Errorf("error = %q, want a hint to call AttachK8s first", err.Error())
	}
}

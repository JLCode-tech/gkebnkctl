package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhase32Issuers_DryRun_NoClientsNeeded(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	if err := Phase32Issuers(context.Background(), cl, st, nil, true); err != nil {
		t.Fatalf("Phase32Issuers(dryRun=true): %v", err)
	}
}

func TestPhase32Issuers_LiveMode_NilClientsErrors(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	err := Phase32Issuers(context.Background(), cl, st, &gcp.Clients{}, false)
	if err == nil {
		t.Fatal("expected an error when Clients.Dynamic is nil in live mode")
	}
	if !strings.Contains(err.Error(), "AttachK8s") {
		t.Errorf("error = %q, want a hint to call AttachK8s first", err.Error())
	}
}

func TestIssuersManifest_ParsesToExpectedObjects(t *testing.T) {
	issuers := parseManifestNamesForTest(t, issuersManifestPath, "ClusterIssuer")
	want := map[string]bool{"selfsigned-cluster-issuer": false, "ext-ca-cluster-issuer": false}
	for _, n := range issuers {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected ClusterIssuer %s, got issuers=%v", name, issuers)
		}
	}

	certs := parseManifestNamesForTest(t, issuersManifestPath, "Certificate")
	if len(certs) != 1 || certs[0] != extCACertName {
		t.Errorf("expected exactly 1 Certificate named %s, got %v", extCACertName, certs)
	}
}

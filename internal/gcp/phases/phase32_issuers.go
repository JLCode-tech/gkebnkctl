package phases

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
)

const (
	issuersManifestPath = "static/04-issuers.yaml"
	extCACertNamespace  = "cert-manager"
	extCACertName       = "ext-ca"
	extCAWaitTimeout    = 120 * time.Second
)

func renderClusterCAIssuer(clusterName string) []byte {
	if clusterName == "" {
		return nil
	}
	return []byte(fmt.Sprintf(`apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: %s-ca-cluster-issuer
spec:
  ca:
    secretName: ext-ca
`, clusterName))
}

// Phase32Issuers applies the selfsigned-cluster-issuer + ext-ca Certificate +
// ext-ca-cluster-issuer + <cluster>-ca-cluster-issuer, then waits (best-effort/tolerant,
// matching Phase31's cert-manager wait — see docs/SPEC-go-tracer.md §3) for the ext-ca
// Certificate to report Ready.
func Phase32Issuers(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 32] issuers: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 32] would apply selfsigned-cluster-issuer, ext-ca Certificate, ext-ca-cluster-issuer, %s-ca-cluster-issuer\n", cl.Metadata.Name)
		return nil
	}

	if err := applyEmbeddedManifest(ctx, clients, issuersManifestPath, false); err != nil {
		return fmt.Errorf("phase32: %w", err)
	}
	if cl != nil && cl.Metadata.Name != "" {
		if err := applyManifestBytes(ctx, clients, renderClusterCAIssuer(cl.Metadata.Name), false); err != nil {
			return fmt.Errorf("phase32: applying %s-ca-cluster-issuer: %w", cl.Metadata.Name, err)
		}
	}

	if clients.Dynamic == nil {
		fmt.Fprintln(os.Stderr, "[phase 32] warn: Clients.Dynamic is nil — skipping ext-ca Certificate-ready wait")
		return nil
	}
	fmt.Fprintf(os.Stderr, "[phase 32] waiting for Certificate %s/%s (up to %s)\n", extCACertNamespace, extCACertName, extCAWaitTimeout)
	if err := k8s.WaitForCertificateReady(ctx, clients.Dynamic, extCACertNamespace, extCACertName, extCAWaitTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "[phase 32] warn: Certificate %s/%s not Ready within %s (%v) — continuing (wait is best-effort)\n",
			extCACertNamespace, extCACertName, extCAWaitTimeout, err)
	}

	fmt.Fprintln(os.Stderr, "[phase 32] issuers OK")
	return nil
}

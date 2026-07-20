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

// Phase32Issuers applies the selfsigned-cluster-issuer + ext-ca Certificate +
// ext-ca-cluster-issuer, then waits (best-effort/tolerant, matching Phase31's
// cert-manager wait — see docs/SPEC-go-tracer.md §3) for the ext-ca
// Certificate to report Ready.
func Phase32Issuers(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 32] issuers: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 32] would apply selfsigned-cluster-issuer, ext-ca Certificate, ext-ca-cluster-issuer")
		return nil
	}

	if err := applyEmbeddedManifest(ctx, clients, issuersManifestPath, false); err != nil {
		return fmt.Errorf("phase32: %w", err)
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

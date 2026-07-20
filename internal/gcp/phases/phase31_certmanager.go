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
	certManagerManifestPath = "cert-manager/cert-manager-v1.16.1.yaml"
	certManagerNamespace    = "cert-manager"
	certManagerWaitTimeout  = 5 * time.Minute
)

// certManagerDeployments are the two Deployments Phase31 waits on — the
// controller and its admission webhook — matching the bash seed's
// `helm upgrade --install cert-manager ... --wait --timeout 5m`
// (reference/bash-seed/bin/30-bnk-install.sh), reproduced here as an
// explicit post-apply wait since gkebnkctl has no helm shellout
// (docs/SPEC-go-tracer.md §9.7).
var certManagerDeployments = []string{"cert-manager", "cert-manager-webhook"}

// Phase31CertManager server-side-applies the vendored cert-manager v1.16.1
// static install YAML (no helm shellout — docs/SPEC-go-tracer.md §9.7), then
// waits (best-effort/tolerant — see docs/SPEC-go-tracer.md §3 "wait
// tolerant") for the controller + webhook Deployments to become ready.
func Phase31CertManager(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 31] cert-manager: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 31] would apply cert-manager v1.16.1 (vendored, no helm) into ns cert-manager")
		return nil
	}

	if err := applyEmbeddedManifest(ctx, clients, certManagerManifestPath, false); err != nil {
		return fmt.Errorf("phase31: %w", err)
	}

	if clients.K8s == nil {
		fmt.Fprintln(os.Stderr, "[phase 31] warn: Clients.K8s is nil — skipping deployment-ready wait")
		return nil
	}
	for _, dep := range certManagerDeployments {
		fmt.Fprintf(os.Stderr, "[phase 31] waiting for deployment %s/%s (up to %s)\n", certManagerNamespace, dep, certManagerWaitTimeout)
		if err := k8s.WaitForDeploymentReady(ctx, clients.K8s, certManagerNamespace, dep, certManagerWaitTimeout); err != nil {
			fmt.Fprintf(os.Stderr, "[phase 31] warn: deployment %s/%s not ready within %s (%v) — continuing (wait is best-effort)\n",
				certManagerNamespace, dep, certManagerWaitTimeout, err)
		}
	}

	fmt.Fprintln(os.Stderr, "[phase 31] cert-manager OK")
	return nil
}

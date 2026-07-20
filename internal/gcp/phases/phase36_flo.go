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
	floManifestPath = "flo/flo-manifest.yaml"
	floCRDWait      = 3 * time.Minute
)

// floCRDs are the CRDs Phase36 waits (best-effort/tolerant, per
// docs/SPEC-go-tracer.md §3 "wait tolerant") to become Established after
// applying the FLO operator manifest — the phases downstream of Phase36
// (35/38/40/41) apply CRs against these.
var floCRDs = []string{
	"cneinstances.k8s.f5.com",
	"f5spkvlans.k8s.f5net.com",
	"f5spkstaticroutes.k8s.f5net.com",
	"f5spkegresses.k8s.f5net.com",
	"licenses.k8s.f5net.com",
}

// Phase36FLO applies the embedded FLO operator manifest (~3 MB, 23 CRDs)
// via SERVER-SIDE dynamic SSA — client-side apply exceeds the CRD annotation
// size limit, per docs/SPEC-go-tracer.md §3 Phase36 / §9.8 — then waits for
// the F5 CRDs to establish.
func Phase36FLO(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 36] FLO: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 36] would server-side-apply FLO operator manifest (~3 MB, 23 CRDs)")
		return nil
	}

	// Force SSA (Force: true) instead of plain kubectl apply --server-side.
	// Client-side apply would exceed CRD annotation size limits (23 CRDs, ~3 MB manifest).
	// Force ensures idempotent application even if the manifest has been partially applied.
	if err := applyEmbeddedManifest(ctx, clients, floManifestPath, true); err != nil {
		return fmt.Errorf("phase36: %w", err)
	}

	if clients.Dynamic == nil {
		fmt.Fprintln(os.Stderr, "[phase 36] warn: Clients.Dynamic is nil — skipping CRD-established wait")
		return nil
	}
	for _, crd := range floCRDs {
		fmt.Fprintf(os.Stderr, "[phase 36] waiting for CRD %s to establish (up to %s)\n", crd, floCRDWait)
		if err := k8s.WaitForCRDEstablished(ctx, clients.Dynamic, crd, floCRDWait); err != nil {
			fmt.Fprintf(os.Stderr, "[phase 36] warn: CRD %s did not establish within %s (%v) — check FLO operator pods (kubectl get pods -n f5-bnk); continuing (wait is best-effort)\n",
				crd, floCRDWait, err)
		}
	}

	fmt.Fprintln(os.Stderr, "[phase 36] FLO OK")
	return nil
}

package phases

import (
	"context"
	"fmt"
	"os"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

// namespacesManifestPath is the embedded static manifest applied by
// Phase30Namespaces (f5-bnk, f5-utils, egress-test).
const namespacesManifestPath = "static/01-namespaces.yaml"

// Phase30Namespaces applies the f5-bnk / f5-utils / egress-test namespaces
// via dynamic SSA of the embedded static manifest — declarative-idempotent,
// no existence check needed (docs/SPEC-go-tracer.md §3 Phase30).
func Phase30Namespaces(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 30] namespaces: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 30] would apply namespaces: f5-bnk, f5-utils, egress-test")
		return nil
	}

	if err := applyEmbeddedManifest(ctx, clients, namespacesManifestPath, false); err != nil {
		return fmt.Errorf("phase30: %w", err)
	}
	fmt.Fprintln(os.Stderr, "[phase 30] namespaces OK")
	return nil
}

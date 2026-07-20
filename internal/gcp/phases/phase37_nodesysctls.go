package phases

import (
	"context"
	"fmt"
	"os"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

// nodeSysctlsManifestPath is the embedded static manifest applied by
// Phase37NodeSysctls — the bnk-node-sysctls DaemonSet (core_pattern
// reconciler, nodeSelector app=f5-tmm).
const nodeSysctlsManifestPath = "static/07-node-sysctls.json"

// Phase37NodeSysctls applies the bnk-node-sysctls DaemonSet via dynamic SSA
// — declarative-idempotent, no existence check needed
// (docs/SPEC-go-tracer.md §3 Phase37).
func Phase37NodeSysctls(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 37] node-sysctls: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 37] would apply bnk-node-sysctls DaemonSet (nodeSelector app=f5-tmm)")
		return nil
	}

	if err := applyEmbeddedManifest(ctx, clients, nodeSysctlsManifestPath, false); err != nil {
		return fmt.Errorf("phase37: %w", err)
	}
	fmt.Fprintln(os.Stderr, "[phase 37] node-sysctls OK")
	return nil
}

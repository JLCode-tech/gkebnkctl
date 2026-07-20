package phases

import (
	"context"
	"fmt"
	"os"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

// multusManifestPaths are the four embedded Multus bundle files, applied in
// this exact order — the DaemonSet alone fails without the RBAC/CRD/
// ConfigMap ahead of it ("sa multus not found"), per
// reference/bash-seed/bin/30-bnk-install.sh and the provenance comment on
// multus/02-multus-rbac.yaml (docs/SPEC-go-tracer.md §9.2).
var multusManifestPaths = []string{
	"multus/02-multus-rbac.yaml",
	"multus/02-multus-crd.yaml",
	"multus/02-multus-configmap.yaml",
	"multus/02-multus-ds.yaml",
}

// Phase34Multus applies the Multus SA/ClusterRole/CRB/ConfigMap/NAD-CRD +
// thick DaemonSet via dynamic SSA, in multusManifestPaths order.
func Phase34Multus(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 34] multus: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 34] would apply Multus SA+ClusterRole+CRB+ConfigMap+NAD-CRD, then DaemonSet")
		return nil
	}

	if err := applyEmbeddedManifests(ctx, clients, multusManifestPaths, false); err != nil {
		return fmt.Errorf("phase34: %w", err)
	}
	fmt.Fprintln(os.Stderr, "[phase 34] multus OK")
	return nil
}

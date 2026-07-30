package phases

import (
	"context"
	"fmt"
	"os"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/render"
)

// namespacesTemplatePath is the embedded template applied by Phase30Namespaces,
// rendering f5-bnk, f5-utils, and the configurable egress-test namespace.
const namespacesTemplatePath = "templates/namespaces.yaml.tmpl"

// Phase30Namespaces applies the f5-bnk / f5-utils / egress-test namespaces
// via dynamic SSA of a rendered template — declarative-idempotent,
// no existence check needed (docs/SPEC-go-tracer.md §3 Phase30).
// The egress test namespace is templated to support custom values from cluster.yaml.
func Phase30Namespaces(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 30] namespaces: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 30] would apply namespaces: f5-bnk, f5-utils, %s\n", cl.Egress.TestNamespace)
		return nil
	}

	tmplBytes, err := manifests.FS.ReadFile(namespacesTemplatePath)
	if err != nil {
		return fmt.Errorf("phase30: reading embedded template: %w", err)
	}
	rendered, err := render.RenderNamespaces(tmplBytes, render.NamespacesVars{
		TestNamespace: cl.Egress.TestNamespace,
	})
	if err != nil {
		return fmt.Errorf("phase30: rendering namespaces: %w", err)
	}
	if err := applyManifestBytes(ctx, clients, rendered, false); err != nil {
		return fmt.Errorf("phase30: %w", err)
	}
	fmt.Fprintln(os.Stderr, "[phase 30] namespaces OK")
	return nil
}

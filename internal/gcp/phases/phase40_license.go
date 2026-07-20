package phases

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/render"
)

// licenseCRTemplatePath is the embedded template Phase40License renders.
const licenseCRTemplatePath = "templates/license-cr.yaml.tmpl"

// Phase40License reads the JWT at cl.Bnk.JwtPath, trims whitespace, renders
// the License/bnk-license CR (connected mode, raw un-encoded JWT — live-
// proven, do NOT base64-encode it) entirely in memory, and applies it via
// dynamic SSA. The JWT content is never written to disk — it is read at
// phase entry and inlined directly into the render call
// (docs/SPEC-go-tracer.md §2 "Secrets constraint").
func Phase40License(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 40] License: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 40] would apply License/bnk-license in f5-utils (connected mode, JWT never written to disk)")
		return nil
	}

	jwtRaw, err := os.ReadFile(cl.Bnk.JwtPath) //nolint:gosec -- path is operator-supplied via cluster.yaml, existence-validated at intent.Load
	if err != nil {
		return fmt.Errorf("phase40: reading JWT %s: %w", cl.Bnk.JwtPath, err)
	}
	jwt := strings.TrimSpace(string(jwtRaw))

	tmplBytes, err := manifests.FS.ReadFile(licenseCRTemplatePath)
	if err != nil {
		return fmt.Errorf("phase40: reading embedded license-cr template: %w", err)
	}
	rendered, err := render.RenderLicenseCR(tmplBytes, render.LicenseCRVars{JWT: jwt})
	if err != nil {
		return fmt.Errorf("phase40: rendering license-cr: %w", err)
	}

	if err := applyManifestBytes(ctx, clients, rendered, false); err != nil {
		return fmt.Errorf("phase40: applying License CR: %w", err)
	}
	fmt.Fprintln(os.Stderr, "[phase 40] License OK")
	return nil
}

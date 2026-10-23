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

// nadsTemplatePath is the embedded template Phase35NADs renders against
// Phase25-discovered state (docs/SPEC-go-tracer.md §5).
const nadsTemplatePath = "templates/nads-hostdevice.yaml.tmpl"

// Phase35NADs renders and applies the tmm-ext/tmm-int host-device
// NetworkAttachmentDefinitions in f5-cne-system, using the iface names + self-IPs
// Phase25Discover wrote to state. Needs a live cluster (Phase25's discovered
// values only exist once the tmm-pool nodes are up) — docs/SPEC-go-tracer.md
// §3 Phase35.
func Phase35NADs(ctx context.Context, cl *intent.Cluster, st *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 35] NADs: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 35] would apply host-device NADs tmm-ext, tmm-int in f5-cne-system (needs live cluster)")
		return nil
	}

	tmplBytes, err := manifests.FS.ReadFile(nadsTemplatePath)
	if err != nil {
		return fmt.Errorf("phase35: reading embedded NADs template: %w", err)
	}
	rendered, err := render.RenderNADs(tmplBytes, render.NADsVars{
		ExtIface:  st.Get("TMM_EXT_IFACE"),
		ExtSelfIP: st.Get("TMM_EXT_SELFIP"),
		IntIface:  st.Get("TMM_INT_IFACE"),
		IntSelfIP: st.Get("TMM_INT_SELFIP"),
	})
	if err != nil {
		return fmt.Errorf("phase35: rendering NADs: %w", err)
	}

	if err := applyManifestBytes(ctx, clients, rendered, false); err != nil {
		return fmt.Errorf("phase35: applying NADs: %w", err)
	}
	fmt.Fprintln(os.Stderr, "[phase 35] NADs OK")
	return nil
}

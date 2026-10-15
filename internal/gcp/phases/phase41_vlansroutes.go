package phases

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/render"
)

// vlansRoutesTemplatePath is the embedded template Phase41VlansRoutes
// renders against cluster.yaml + Phase25-discovered state + intent-derived
// route-CIDR splits (docs/SPEC-go-tracer.md §5).
const vlansRoutesTemplatePath = "templates/vlans-routes.yaml.tmpl"

// Phase41VlansRoutes renders and applies the external+internal F5SPKVlan +
// 6 F5SPKStaticRoutes (ext-gw, int-gw, ext-underlay, cluster-pods,
// cluster-svcs, default-egress) in f5-cne-system. route-int-gw
// (10.50.0.1/32, interface internal) is the item that made egress work
// where the reference cluster failed — do not drop it (docs/SPEC-go-tracer.md
// "Notes for Builder"). Needs a live cluster — docs/SPEC-go-tracer.md §3
// Phase41.
func Phase41VlansRoutes(ctx context.Context, cl *intent.Cluster, st *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 41] F5SPKVlans + static routes: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 41] would apply F5SPKVlan (external, internal) + 6 F5SPKStaticRoutes incl. route-int-gw in f5-cne-system (needs live cluster)")
		return nil
	}

	tmplBytes, err := manifests.FS.ReadFile(vlansRoutesTemplatePath)
	if err != nil {
		return fmt.Errorf("phase41: reading embedded vlans-routes template: %w", err)
	}
	rendered, err := render.RenderVlansRoutes(tmplBytes, render.VlansRoutesVars{
		TmmMtu:               cl.Bnk.TmmMtu,
		ExtSelfIP:            st.Get("TMM_EXT_SELFIP"),
		IntSelfIP:            st.Get("TMM_INT_SELFIP"),
		ExtGwIP:              cl.ExtGwIP,
		IntGwIP:              cl.IntGwIP,
		VxlanPort:            cl.Egress.VxlanPort,
		ExtUnderlayDest:      cl.ExtUnderlayDest,
		ExtUnderlayPrefixLen: cl.ExtUnderlayPrefixLen,
		MainPodsDest:         cl.MainPodsDest,
		MainPodsPrefixLen:    cl.MainPodsPrefixLen,
		MainSvcsDest:         cl.MainSvcsDest,
		MainSvcsPrefixLen:    cl.MainSvcsPrefixLen,
		TmmPodGW:             st.Get("TMM_POD_GW"),
	})
	if err != nil {
		return fmt.Errorf("phase41: rendering vlans-routes: %w", err)
	}

	const retryAttempts = 24
	const retryInterval = 5 * time.Second
	var lastErr error
	for attempt := 1; attempt <= retryAttempts; attempt++ {
		lastErr = applyStaticRouteManifestBytes(ctx, clients, rendered, false)
		if lastErr == nil {
			break
		}
		if !strings.Contains(lastErr.Error(), "f5-validation-svc") &&
			!strings.Contains(lastErr.Error(), "failed calling webhook") &&
			!strings.Contains(lastErr.Error(), "connection refused") {
			return fmt.Errorf("phase41: applying F5SPKVlans/routes: %w", lastErr)
		}
		if attempt < retryAttempts {
			fmt.Fprintf(os.Stderr, "[phase 41] webhook f5-validation-svc not ready yet (attempt %d/%d) — retrying in %s\n", attempt, retryAttempts, retryInterval)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryInterval):
			}
		}
	}
	if lastErr != nil {
		return fmt.Errorf("phase41: applying F5SPKVlans/routes: %w", lastErr)
	}
	fmt.Fprintln(os.Stderr, "[phase 41] F5SPKVlans + static routes OK")
	return nil
}

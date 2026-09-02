package phases

import (
	"context"
	"fmt"
	"os"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/render"
)

// cneInstanceTemplatePath is the embedded template Phase38CNEInstance
// renders against cluster.yaml + Phase25-discovered state
// (docs/SPEC-go-tracer.md §5).
const cneInstanceTemplatePath = "templates/cneinstance.yaml.tmpl"

// Phase38CNEInstance renders and applies the CNEInstance/instance CR in
// f5-bnk. Carries the mandatory host-device env
// (reference/TRIAL-NOTES.md contract item 3): TMM_MAPRES_ADDL_VETHS_ON_DP
// =TRUE + PCIDEVICE_INTEL_COM_EXT/INT from Phase25-discovered PCI addresses.
// Needs a live cluster — docs/SPEC-go-tracer.md §3 Phase38.
func Phase38CNEInstance(ctx context.Context, cl *intent.Cluster, st *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 38] CNEInstance: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 38] would apply CNEInstance/instance in f5-bnk (needs live cluster)")
		return nil
	}

	tmplBytes, err := manifests.FS.ReadFile(cneInstanceTemplatePath)
	if err != nil {
		return fmt.Errorf("phase38: reading embedded CNEInstance template: %w", err)
	}
	rendered, err := render.RenderCNEInstance(tmplBytes, render.CNEInstanceVars{
		TmmMtu:          cl.Bnk.TmmMtu,
		ManifestVersion: cl.Bnk.ManifestVersion,
		ExtPCI:          st.Get("TMM_EXT_PCI"),
		IntPCI:          st.Get("TMM_INT_PCI"),
		ImageRegistry:   cl.Bnk.ImageRegistry,
	})
	if err != nil {
		return fmt.Errorf("phase38: rendering CNEInstance: %w", err)
	}

	if err := applyManifestBytes(ctx, clients, rendered, false); err != nil {
		return fmt.Errorf("phase38: applying CNEInstance: %w", err)
	}

	// Applying the CNEInstance is what makes FLO spawn its crd-installer Job,
	// which registers the k8s.f5net.com CRDs. Everything downstream applies CRs
	// against those kinds (Phase40 License, Phase41 F5SPKVlan/StaticRoute,
	// Phase50 F5SPKEgress, Phase51 StaticRoute), so wait here rather than
	// letting Phase40 fail with `no matches for kind "License"` — which is
	// exactly what happened on a greenfield run before this wait existed
	// (2026-07-21, cluster bnk-scratch).
	//
	// Tolerant like Phase36's wait: a timeout warns and continues rather than
	// aborting, so a slow crd-installer degrades to the pre-existing
	// "re-run up" behaviour instead of a hard failure.
	if clients.Dynamic == nil {
		fmt.Fprintln(os.Stderr, "[phase 38] warn: Clients.Dynamic is nil — skipping FLO-installed CRD wait")
	} else {
		for _, crd := range floInstalledCRDs {
			fmt.Fprintf(os.Stderr, "[phase 38] waiting for FLO-installed CRD %s to establish (up to %s)\n", crd, floCRDWait)
			if err := k8s.WaitForCRDEstablished(ctx, clients.Dynamic, crd, floCRDWait); err != nil {
				fmt.Fprintf(os.Stderr, "[phase 38] warn: CRD %s did not establish within %s (%v) — check FLO's crd-installer job (kubectl get jobs -n f5-utils); continuing (wait is best-effort)\n",
					crd, floCRDWait, err)
			}
		}
	}

	fmt.Fprintln(os.Stderr, "[phase 38] CNEInstance OK")
	return nil
}

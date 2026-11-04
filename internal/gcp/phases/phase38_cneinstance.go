package phases

import (
	"context"
	"fmt"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

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
const (
	cneInstanceTemplatePath       = "templates/cneinstance.yaml.tmpl"
	cneControllerRBACTemplatePath = "templates/cne-controller-rbac.yaml.tmpl"
)

// Phase38CNEInstance renders and applies the CNEInstance/instance CR in
// f5-cne-system along with the supplemental cne-controller / cwc RBAC.
// Carries the mandatory host-device env
// (reference/TRIAL-NOTES.md contract item 3): TMM_MAPRES_ADDL_VETHS_ON_DP
// =TRUE + PCIDEVICE_INTEL_COM_EXT/INT from Phase25-discovered PCI addresses.
// Needs a live cluster — docs/SPEC-go-tracer.md §3 Phase38.
func Phase38CNEInstance(ctx context.Context, cl *intent.Cluster, st *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 38] CNEInstance: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 38] would apply supplemental cne-controller RBAC + CNEInstance/instance in f5-cne-system (needs live cluster)")
		return nil
	}

	rbacTmplBytes, err := manifests.FS.ReadFile(cneControllerRBACTemplatePath)
	if err != nil {
		return fmt.Errorf("phase38: reading embedded cne-controller RBAC template: %w", err)
	}
	rbacRendered, err := render.RenderCNEControllerRBAC(rbacTmplBytes, cl)
	if err != nil {
		return fmt.Errorf("phase38: rendering cne-controller RBAC: %w", err)
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
		Telemetry:       cl.Bnk.Telemetry,
		AIGateway:       cl.Bnk.AIGateway != nil && cl.Bnk.AIGateway.Enabled,
		ServiceCIDR:     cl.Network.Main.SvcsCIDR,
		PodCIDR:         cl.Network.Main.PodsCIDR,
		MainCIDR:        cl.Network.Main.CIDR,
	})
	if err != nil {
		return fmt.Errorf("phase38: rendering CNEInstance: %w", err)
	}

	if err := applyManifestBytes(ctx, clients, rbacRendered, true); err != nil {
		return fmt.Errorf("phase38: applying cne-controller RBAC: %w", err)
	}
	if err := applyManifestBytes(ctx, clients, rendered, true); err != nil {
		return fmt.Errorf("phase38: applying CNEInstance: %w", err)
	}

	resetFailedCRDInstaller(ctx, clients)

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
				fmt.Fprintf(os.Stderr, "[phase 38] warn: CRD %s did not establish within %s (%v) — check FLO's crd-installer job (kubectl get jobs -n f5-cne-core); continuing (wait is best-effort)\n",
					crd, floCRDWait, err)
			}
		}
		if crdBytes, err := manifests.FS.ReadFile(bnkGatewayCRDsPath); err == nil {
			_ = applySupplementalBNKGatewayCRDs(ctx, clients, crdBytes)
		}
	}

	fmt.Fprintln(os.Stderr, "[phase 38] CNEInstance OK")
	return nil
}

func resetFailedCRDInstaller(ctx context.Context, clients *gcp.Clients) {
	if clients == nil || clients.Dynamic == nil {
		return
	}
	crdInstGVR := schema.GroupVersionResource{
		Group:    "k8s.f5.com",
		Version:  "v1",
		Resource: "crdinstallers",
	}
	inst, err := clients.Dynamic.Resource(crdInstGVR).Namespace(FLONamespace).Get(ctx, "crd-installer", metav1.GetOptions{})
	if err != nil || inst == nil {
		return
	}
	conds, _, _ := unstructured.NestedSlice(inst.Object, "status", "conditions")
	failed := false
	for _, c := range conds {
		if cm, ok := c.(map[string]interface{}); ok {
			if cm["reason"] == "Failed" && cm["status"] == "False" {
				failed = true
				break
			}
		}
	}
	if !failed {
		return
	}
	fmt.Fprintln(os.Stderr, "[phase 38] resetting failed crd-installer in f5-cne-core so FLO re-reconciles CRDs")
	if clients.K8s != nil {
		bg := metav1.DeletePropagationBackground
		_ = clients.K8s.BatchV1().Jobs(FLONamespace).Delete(ctx, "crd-installer", metav1.DeleteOptions{PropagationPolicy: &bg})
	}
	_ = clients.Dynamic.Resource(crdInstGVR).Namespace(FLONamespace).Delete(ctx, "crd-installer", metav1.DeleteOptions{})
}

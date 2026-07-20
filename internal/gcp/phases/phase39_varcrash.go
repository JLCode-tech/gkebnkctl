package phases

import (
	"context"
	"fmt"
	"os"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

// varCrashNamespace is the namespace both patched Deployments live in.
const varCrashNamespace = "f5-bnk"

// varCrashDeployments are the two Deployments Phase39 patches with a
// /var/crash emptyDir — freshly-pulled f5-cne-controller/f5-crdconversion
// image digests carry a crashagent that hard-dies without /var/crash and
// s6-tears-down the main service (reference/TRIAL-NOTES.md item 5).
var varCrashDeployments = []string{"f5-cne-controller", "f5-crdconversion"}

// varCrashVolumeName is the emptyDir volume name added to both Deployments.
const varCrashVolumeName = "var-crash"

// Phase39VarCrashPatches strategic-merge-patches both varCrashDeployments
// with a /var/crash emptyDir volume + mount, looking up the container name
// dynamically (it isn't guaranteed to match the Deployment name) — matching
// reference/bash-seed/bin/30-bnk-install.sh's bnk::patch_var_crash.
//
// A Deployment not found yet is a non-fatal warn+skip (FLO may still be
// reconciling); a Deployment that already has the var-crash volume is
// skipped. Neither condition is an error, per docs/SPEC-go-tracer.md §3
// Phase39's idempotency column.
func Phase39VarCrashPatches(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 39] var-crash patches: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 39] would patch /var/crash emptyDir onto %v in %s (if present, if not already patched)\n",
			varCrashDeployments, varCrashNamespace)
		return nil
	}

	if clients.K8s == nil {
		return fmt.Errorf("phase39: Clients.K8s is nil — Phase20 must attach the k8s client first")
	}

	for _, deploy := range varCrashDeployments {
		if err := patchVarCrash(ctx, clients.K8s, deploy); err != nil {
			return fmt.Errorf("phase39: %s: %w", deploy, err)
		}
	}

	fmt.Fprintln(os.Stderr, "[phase 39] var-crash patches OK")
	return nil
}

// patchVarCrash implements the per-Deployment patch logic. Never returns an
// error for "deployment not found yet" — only for a real API error.
func patchVarCrash(ctx context.Context, client kubernetes.Interface, deploy string) error {
	d, err := client.AppsV1().Deployments(varCrashNamespace).Get(ctx, deploy, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			fmt.Fprintf(os.Stderr, "[phase 39] %s not found in %s yet — skipping /var/crash patch (FLO may still be reconciling); re-run up once it exists\n",
				deploy, varCrashNamespace)
			return nil
		}
		return fmt.Errorf("getting deployment %s: %w", deploy, err)
	}

	for _, v := range d.Spec.Template.Spec.Volumes {
		if v.Name == varCrashVolumeName {
			fmt.Fprintf(os.Stderr, "[phase 39] %s already has /var/crash volume — skipping\n", deploy)
			return nil
		}
	}
	if len(d.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("deployment %s has no containers to mount /var/crash into", deploy)
	}
	container := d.Spec.Template.Spec.Containers[0].Name

	fmt.Fprintf(os.Stderr, "[phase 39] patching %s (container=%s) with /var/crash emptyDir\n", deploy, container)
	patch := fmt.Sprintf(`{"spec":{"template":{"spec":{
  "volumes":[{"name":%q,"emptyDir":{}}],
  "containers":[{"name":%q,"volumeMounts":[{"name":%q,"mountPath":"/var/crash"}]}]
}}}}`, varCrashVolumeName, container, varCrashVolumeName)

	if _, err := client.AppsV1().Deployments(varCrashNamespace).Patch(ctx, deploy, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("patching deployment %s: %w", deploy, err)
	}
	return nil
}

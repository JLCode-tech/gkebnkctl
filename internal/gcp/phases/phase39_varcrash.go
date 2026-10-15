package phases

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

// varCrashTarget identifies a Deployment that requires the /var/crash emptyDir patch.
type varCrashTarget struct {
	Namespace  string
	Deployment string
}

// varCrashTargets are the Deployments Phase39 patches with a
// /var/crash emptyDir — freshly-pulled f5-cne-controller/f5-crdconversion
// image digests carry a crashagent that hard-dies without /var/crash and
// s6-tears-down the main service (reference/TRIAL-NOTES.md item 5).
var varCrashTargets = []varCrashTarget{
	{Namespace: "f5-cne-system", Deployment: "f5-cne-controller"},
	{Namespace: "f5-utils", Deployment: "f5-crdconversion"},
}

// varCrashVolumeName is the emptyDir volume name added to both Deployments.
const varCrashVolumeName = "var-crash"

// varCrashDeployWait bounds how long Phase39 waits for FLO to create the
// Deployments it patches. They are created by FLO reconciling the CNEInstance
// that Phase38 just applied, so on a greenfield run they briefly do not exist.
// Skipping immediately (the old behaviour) left f5-crdconversion permanently in
// CrashLoopBackOff — its crashagent hard-dies without /var/crash — and the
// "re-run up" hint was silently easy to miss (live-hit 2026-07-21 on cluster
// bnk-scratch).
const varCrashDeployWait = 3 * time.Minute

// Phase39VarCrashPatches strategic-merge-patches both varCrashTargets
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
		fmt.Fprintf(os.Stderr, "[phase 39] would patch /var/crash emptyDir onto %+v (if present, if not already patched)\n",
			varCrashTargets)
		return nil
	}

	if clients.K8s == nil {
		return fmt.Errorf("phase39: Clients.K8s is nil — Phase20 must attach the k8s client first")
	}

	for _, target := range varCrashTargets {
		if err := waitForDeploymentExists(ctx, clients.K8s, target.Namespace, target.Deployment, varCrashDeployWait); err != nil {
			fmt.Fprintf(os.Stderr, "[phase 39] warn: %s did not appear in %s within %s (%v) — skipping its /var/crash patch; re-run up once FLO has created it\n",
				target.Deployment, target.Namespace, varCrashDeployWait, err)
			continue
		}
		if err := patchVarCrash(ctx, clients.K8s, target.Namespace, target.Deployment); err != nil {
			return fmt.Errorf("phase39: %s/%s: %w", target.Namespace, target.Deployment, err)
		}
	}

	fmt.Fprintln(os.Stderr, "[phase 39] var-crash patches OK")
	return nil
}

// patchVarCrash implements the per-Deployment patch logic. Never returns an
// error for "deployment not found yet" — only for a real API error.
func patchVarCrash(ctx context.Context, client kubernetes.Interface, ns, deploy string) error {
	d, err := client.AppsV1().Deployments(ns).Get(ctx, deploy, metav1.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			fmt.Fprintf(os.Stderr, "[phase 39] %s not found in %s yet — skipping /var/crash patch (FLO may still be reconciling); re-run up once it exists\n",
				deploy, ns)
			return nil
		}
		return fmt.Errorf("getting deployment %s/%s: %w", ns, deploy, err)
	}

	for _, v := range d.Spec.Template.Spec.Volumes {
		if v.Name == varCrashVolumeName {
			fmt.Fprintf(os.Stderr, "[phase 39] %s/%s already has /var/crash volume — skipping\n", ns, deploy)
			return nil
		}
	}
	if len(d.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("deployment %s/%s has no containers to mount /var/crash into", ns, deploy)
	}
	container := d.Spec.Template.Spec.Containers[0].Name

	fmt.Fprintf(os.Stderr, "[phase 39] patching %s/%s (container=%s) with /var/crash emptyDir\n", ns, deploy, container)
	patch := fmt.Sprintf(`{"spec":{"template":{"spec":{
  "volumes":[{"name":%q,"emptyDir":{}}],
  "containers":[{"name":%q,"volumeMounts":[{"name":%q,"mountPath":"/var/crash"}]}]
}}}}`, varCrashVolumeName, container, varCrashVolumeName)

	if _, err := client.AppsV1().Deployments(ns).Patch(ctx, deploy, types.StrategicMergePatchType, []byte(patch), metav1.PatchOptions{}); err != nil {
		return fmt.Errorf("patching deployment %s/%s: %w", ns, deploy, err)
	}
	return nil
}

// waitForDeploymentExists blocks until ns/name exists (it need not be Ready —
// the patch itself triggers the rollout), or until timeout. Returns nil as soon
// as the Deployment is observed.
func waitForDeploymentExists(ctx context.Context, client kubernetes.Interface, ns, name string, timeout time.Duration) error {
	err := gcp.PollUntil(ctx, 3*time.Second, timeout, func(ctx context.Context) (bool, error) {
		_, err := client.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			return true, nil
		}
		if !k8serrors.IsNotFound(err) {
			return false, fmt.Errorf("getting deployment %s/%s: %w", ns, name, err)
		}
		return false, nil
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("timed out after %s waiting for deployment %s/%s to be created", timeout, ns, name)
		}
		return err
	}
	return nil
}

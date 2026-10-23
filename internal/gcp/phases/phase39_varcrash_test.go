package phases

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakek8s "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhase39VarCrashPatches_DryRun_NoClientsNeeded(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	if err := Phase39VarCrashPatches(context.Background(), cl, st, nil, true); err != nil {
		t.Fatalf("Phase39VarCrashPatches(dryRun=true): %v", err)
	}
}

func TestPhase39VarCrashPatches_LiveMode_NilK8sErrors(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	if err := Phase39VarCrashPatches(context.Background(), cl, st, &gcp.Clients{}, false); err == nil {
		t.Fatal("expected an error when Clients.K8s is nil in live mode")
	}
}

func deploymentFixture(ns, name, container string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: container}},
				},
			},
		},
	}
}

// TestPhase39VarCrashPatches_PatchesBothDeployments proves both
// f5-cne-controller (f5-cne-system) and f5-crdconversion (f5-utils) get the var-crash emptyDir +
// volumeMount, using the container[0] name looked up dynamically.
func TestPhase39VarCrashPatches_PatchesBothDeployments(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	k8sClient := fakek8s.NewClientset(
		deploymentFixture("f5-cne-system", "f5-cne-controller", "cne-controller"),
		deploymentFixture("f5-utils", "f5-crdconversion", "crdconversion"),
	)
	clients := &gcp.Clients{K8s: k8sClient}

	if err := Phase39VarCrashPatches(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase39VarCrashPatches: %v", err)
	}

	for _, target := range varCrashTargets {
		d, err := k8sClient.AppsV1().Deployments(target.Namespace).Get(context.Background(), target.Deployment, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("getting patched deployment %s/%s: %v", target.Namespace, target.Deployment, err)
		}
		foundVolume := false
		for _, v := range d.Spec.Template.Spec.Volumes {
			if v.Name == varCrashVolumeName && v.EmptyDir != nil {
				foundVolume = true
			}
		}
		if !foundVolume {
			t.Errorf("%s/%s: expected a var-crash emptyDir volume, got volumes=%+v", target.Namespace, target.Deployment, d.Spec.Template.Spec.Volumes)
		}
		foundMount := false
		for _, c := range d.Spec.Template.Spec.Containers {
			for _, m := range c.VolumeMounts {
				if m.Name == varCrashVolumeName && m.MountPath == "/var/crash" {
					foundMount = true
				}
			}
		}
		if !foundMount {
			t.Errorf("%s/%s: expected a var-crash volumeMount at /var/crash, got containers=%+v", target.Namespace, target.Deployment, d.Spec.Template.Spec.Containers)
		}
	}
}

// TestPatchVarCrash_SkipsWhenVolumeAlreadyPresent proves the idempotency
// check — a Deployment that already has the var-crash volume is not
// re-patched.
func TestPatchVarCrash_SkipsWhenVolumeAlreadyPresent(t *testing.T) {
	d := deploymentFixture("f5-cne-system", "f5-cne-controller", "cne-controller")
	d.Spec.Template.Spec.Volumes = []corev1.Volume{
		{Name: varCrashVolumeName, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
	}
	k8sClient := fakek8s.NewClientset(d)

	if err := patchVarCrash(context.Background(), k8sClient, "f5-cne-system", "f5-cne-controller"); err != nil {
		t.Fatalf("patchVarCrash: %v", err)
	}
	// No error is the meaningful assertion — a second Patch attempt against
	// a fixture with no volumeMounts set would be harmless either way, but
	// asserting the container's VolumeMounts stayed nil proves the patch
	// path was never reached.
	got, _ := k8sClient.AppsV1().Deployments("f5-cne-system").Get(context.Background(), "f5-cne-controller", metav1.GetOptions{})
	if len(got.Spec.Template.Spec.Containers[0].VolumeMounts) != 0 {
		t.Error("expected no volumeMounts to be added — the volume-presence skip should have short-circuited before the patch")
	}
}

// TestPatchVarCrash_DeploymentNotFoundIsNonFatal proves a missing Deployment
// (FLO still reconciling) is a warn+skip, not an error.
func TestPatchVarCrash_DeploymentNotFoundIsNonFatal(t *testing.T) {
	k8sClient := fakek8s.NewClientset()
	if err := patchVarCrash(context.Background(), k8sClient, "f5-cne-system", "f5-cne-controller"); err != nil {
		t.Fatalf("patchVarCrash: expected nil error for a not-yet-existing deployment, got: %v", err)
	}
}

package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/render"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func healDeps(objs ...runtime.Object) *HealDeps {
	return &HealDeps{Clients: &Clients{K8s: k8sfake.NewClientset(objs...)}}
}

func controllerDeploy(sa string, mounts ...corev1.VolumeMount) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: h4DeploymentName, Namespace: InstanceNamespace},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "f5-cne-controller"}},
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				ServiceAccountName: sa,
				Containers:         []corev1.Container{{Name: "f5-cne-controller"}, {Name: h4ContainerName, VolumeMounts: mounts}},
			}},
		},
	}
}

var grpcMount = corev1.VolumeMount{Name: "tls-f5ingress-grpc-clt-volume", MountPath: "/tls/f5ingress/grpc/clt"}

func controllerPod(name string, pmReady bool, restarts int32, waiting string) *corev1.Pod {
	cs := corev1.ContainerStatus{Name: h4ContainerName, Ready: pmReady, RestartCount: restarts}
	if waiting != "" {
		cs.State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: waiting}}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: InstanceNamespace, Labels: map[string]string{"app": "f5-cne-controller"}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{cs}},
	}
}

// An empty cluster is healthy everywhere except the test namespace, which is created;
// nothing else is fixed and nothing errors.
func TestRunHeal_EmptyClusterDetectsOnly(t *testing.T) {
	d := healDeps()
	d.Clients.Dynamic = nil
	out := RunHeal(context.Background(), d, nil)
	if len(out) != len(Repairs) {
		t.Fatalf("got %d statuses, want %d", len(out), len(Repairs))
	}
	for _, s := range out {
		switch s.Name {
		case "tmm-k8s-routes", "epp-namespaces":
			if s.Error == "" { // no dynamic client -> detect error, never a fix
				t.Errorf("%s: expected an error without a dynamic client: %+v", s.Name, s)
			}
		case "test-namespace":
			if !s.Changed || !s.Healthy || !strings.Contains(s.Detail, "created") {
				t.Errorf("test-namespace: expected the namespace to be created: %+v", s)
			}
		default:
			if !s.Healthy || s.Changed || s.Error != "" {
				t.Errorf("%s: %+v", s.Name, s)
			}
		}
	}
}

func TestRunHeal_OnlyAndDryRun(t *testing.T) {
	d := healDeps(multusDaemonSetForHeal())
	d.DryRun = true
	out := RunHeal(context.Background(), d, []string{"multus-token-watch"})
	if len(out) != 1 || out[0].Name != "multus-token-watch" {
		t.Fatalf("out=%+v", out)
	}
	if out[0].Healthy || out[0].Changed || !strings.HasPrefix(out[0].Detail, "would repair:") {
		t.Errorf("dry-run status %+v", out[0])
	}
	d.DryRun = false
	out = RunHeal(context.Background(), d, []string{"multus-token-watch"})
	if !out[0].Changed || !out[0].Healthy || !strings.Contains(out[0].Detail, "DaemonSet rolled") {
		t.Errorf("fix status %+v", out[0])
	}
}

func multusDaemonSetForHeal() *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "kube-multus-ds", Namespace: "kube-system", Generation: 1},
		Spec: appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name: "kube-multus", Args: []string{"--multus-conf-file=auto"},
		}}}}},
		Status: appsv1.DaemonSetStatus{ObservedGeneration: 1, DesiredNumberScheduled: 1, UpdatedNumberScheduled: 1, NumberReady: 1, NumberAvailable: 1},
	}
}

func TestDetectPodManager(t *testing.T) {
	ctx := context.Background()
	t.Run("missing mount", func(t *testing.T) {
		d := healDeps(controllerDeploy("f5-cne-controller"))
		ok, detail, err := detectPodManager(ctx, d.Clients)
		if err != nil || ok || !strings.Contains(detail, "/tls/f5ingress/grpc/clt") {
			t.Errorf("ok=%v detail=%q err=%v", ok, detail, err)
		}
	})
	t.Run("crash loop", func(t *testing.T) {
		d := healDeps(controllerDeploy("f5-cne-controller", grpcMount), controllerPod("c-1", false, 3, "CrashLoopBackOff"))
		ok, detail, err := detectPodManager(ctx, d.Clients)
		if err != nil || ok || !strings.Contains(detail, "CrashLoopBackOff") {
			t.Errorf("ok=%v detail=%q err=%v", ok, detail, err)
		}
	})
	t.Run("healthy", func(t *testing.T) {
		d := healDeps(controllerDeploy("f5-cne-controller", grpcMount), controllerPod("c-1", true, 1, ""))
		ok, _, err := detectPodManager(ctx, d.Clients)
		if err != nil || !ok {
			t.Errorf("ok=%v err=%v", ok, err)
		}
	})
}

func TestDetectControllerRBAC(t *testing.T) {
	ctx := context.Background()
	if ok, detail, _ := detectControllerRBAC(ctx, healDeps(controllerDeploy("f5-cne-controller-x-serviceaccount")).Clients); !ok || !strings.Contains(detail, "2.3") {
		t.Errorf("2.3 SA must not need the role: ok=%v %q", ok, detail)
	}
	if ok, detail, _ := detectControllerRBAC(ctx, healDeps(controllerDeploy("f5-cne-controller")).Clients); ok || !strings.Contains(detail, "EndpointSlice") {
		t.Errorf("no binding: ok=%v %q", ok, detail)
	}
	role := &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: "x-cne-controller-endpointslices"}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{"discovery.k8s.io"}, Resources: []string{"endpointslices"}, Verbs: []string{"get", "list"}}}}
	crb := &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "x-cne-controller-endpointslices"}, RoleRef: rbacv1.RoleRef{Kind: "ClusterRole", Name: role.Name}, Subjects: []rbacv1.Subject{{Kind: "ServiceAccount", Namespace: InstanceNamespace, Name: "f5-cne-controller"}}}
	if ok, _, err := detectControllerRBAC(ctx, healDeps(controllerDeploy("f5-cne-controller"), role, crb).Clients); !ok || err != nil {
		t.Errorf("bound role: ok=%v err=%v", ok, err)
	}
}

func TestDSSMOverlayKeys(t *testing.T) {
	cm := &corev1.ConfigMap{Data: map[string]string{
		"readiness_probe.sh": "redis-cli --tls -p 6379 ping",
		"liveness_probe.sh":  "redis-cli --tls --insecure -p 6379 ping",
		"init.sh":            "echo hello",
	}}
	toPatch, done := dssmOverlayKeys(cm)
	if len(toPatch) != 1 || toPatch[0] != "readiness_probe.sh" || done != 1 {
		t.Errorf("toPatch=%v done=%d", toPatch, done)
	}
}

func TestSidecarServiceNetworkEvidence(t *testing.T) {
	log := "[2026/09/15 02:23:37] [ info] [task] task_id=99 still running\n[2026/09/15 02:23:37.278793012] [ warn] [net] getaddrinfo(host='f5-toda-fluentd.f5-cne-core.svc.cluster.local.', err=12): Timeout while contacting DNS servers\n[2026/09/15 02:23:37] [error] [output:forward:forward.1] no upstream connections available\n"
	got := sidecarServiceNetworkEvidence(log)
	if !strings.Contains(got, "Timeout while contacting DNS servers") || !strings.HasPrefix(got, "f5-fluentbit: [2026/09/15 02:23:37.278793012]") {
		t.Errorf("evidence %q", got)
	}
	if sidecarServiceNetworkEvidence("[info] [output:forward:forward.1] worker #0 started\n") != "" {
		t.Error("healthy log must give no evidence")
	}
}

func TestRepairTestNamespace(t *testing.T) {
	d := healDeps()
	out := RunHeal(context.Background(), d, []string{"test-namespace"})
	if len(out) != 1 || !out[0].Changed || !out[0].Healthy {
		t.Fatalf("first run should create the namespace: %+v", out)
	}
	if _, err := d.Clients.K8s.CoreV1().Namespaces().Get(context.Background(), K8sTestNamespace, metav1.GetOptions{}); err != nil {
		t.Fatalf("namespace not created: %v", err)
	}
	out = RunHeal(context.Background(), d, []string{"test-namespace"})
	if out[0].Changed || !out[0].Healthy || !strings.Contains(out[0].Detail, "present") {
		t.Errorf("second run should detect only: %+v", out[0])
	}
	det := DetectAll(context.Background(), healDeps())
	for _, s := range det {
		if s.Name == "test-namespace" && (s.Healthy || !strings.Contains(s.Detail, "missing")) {
			t.Errorf("DetectAll on an empty cluster: %+v", s)
		}
	}
}

func cneForHeal(manifestVersion string, env []any) *unstructured.Unstructured {
	spec := map[string]any{
		"manifestVersion": manifestVersion,
	}
	if env != nil {
		spec["advanced"] = map[string]any{
			"tmm": map[string]any{
				"env": env,
			},
		}
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "k8s.f5.com/v1",
			"kind":       "CNEInstance",
			"metadata": map[string]any{
				"name":      "instance",
				"namespace": InstanceNamespace,
			},
			"spec": spec,
		},
	}
}

func fakeDynamicCNE(cne *unstructured.Unstructured) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(
		schema.GroupVersionKind{Group: "k8s.f5.com", Version: "v1", Kind: "CNEInstance"},
		&unstructured.Unstructured{},
	)
	scheme.AddKnownTypeWithName(
		schema.GroupVersionKind{Group: "k8s.f5.com", Version: "v1", Kind: "CNEInstanceList"},
		&unstructured.UnstructuredList{},
	)
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{
			healCNEInstanceGVR: "CNEInstanceList",
		},
	)
	if cne != nil {
		_, _ = dyn.Resource(healCNEInstanceGVR).Namespace(InstanceNamespace).Create(
			context.Background(), cne, metav1.CreateOptions{},
		)
	}
	return dyn
}

func TestDetectTMMK8sRoutes(t *testing.T) {
	ctx := context.Background()

	t.Run("no CNEInstance", func(t *testing.T) {
		d := healDeps()
		d.Clients.Dynamic = fakeDynamicCNE(nil)
		ok, detail, err := detectTMMK8sRoutes(ctx, d)
		if err != nil || !ok || !strings.Contains(detail, "no CNEInstance") {
			t.Errorf("expected healthy with no CNEInstance, got ok=%v, detail=%q, err=%v", ok, detail, err)
		}
	})

	t.Run("2.4 without routes", func(t *testing.T) {
		cne := cneForHeal("2.4.0", nil)
		d := healDeps()
		d.Clients.Dynamic = fakeDynamicCNE(cne)
		ok, detail, err := detectTMMK8sRoutes(ctx, d)
		if err != nil || ok || !strings.Contains(detail, "has no "+render.TMMK8sRoutesEnv) {
			t.Errorf("expected unhealthy without routes, got ok=%v, detail=%q, err=%v", ok, detail, err)
		}
	})

	t.Run("2.4 with duplicate routes", func(t *testing.T) {
		cne := cneForHeal("2.4.0", []any{
			map[string]any{"name": render.TMMK8sRoutesEnv, "value": "10.96.0.0/16"},
			map[string]any{"name": render.TMMK8sRoutesEnv, "value": "10.96.0.0/16"},
		})
		d := healDeps()
		d.Clients.Dynamic = fakeDynamicCNE(cne)
		ok, detail, err := detectTMMK8sRoutes(ctx, d)
		if err != nil || ok || !strings.Contains(detail, "duplicate") {
			t.Errorf("expected unhealthy with duplicate routes, got ok=%v, detail=%q, err=%v", ok, detail, err)
		}
	})

	t.Run("2.4 with Service CIDR 10.96.0.0/16", func(t *testing.T) {
		cne := cneForHeal("2.4.0", []any{
			map[string]any{"name": render.TMMK8sRoutesEnv, "value": "10.96.0.0/16"},
		})
		d := healDeps()
		d.Clients.Dynamic = fakeDynamicCNE(cne)
		ok, detail, err := detectTMMK8sRoutes(ctx, d)
		if err != nil || !ok || !strings.Contains(detail, render.TMMK8sRoutesEnv+"=10.96.0.0/16") {
			t.Errorf("expected healthy with 10.96.0.0/16, got ok=%v, detail=%q, err=%v", ok, detail, err)
		}
	})
}

func TestFixTMMK8sRoutes(t *testing.T) {
	ctx := context.Background()

	t.Run("empty state defaults to 10.96.0.0/16", func(t *testing.T) {
		cne := cneForHeal("2.4.0", []any{
			map[string]any{"name": "TMM_CALICO_ROUTER", "value": "default"},
		})
		st, err := state.Load(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		d := healDeps()
		d.State = st
		dyn := fakeDynamicCNE(cne)
		d.Clients.Dynamic = dyn

		detail, err := fixTMMK8sRoutes(ctx, d)
		if err != nil {
			t.Fatalf("fixTMMK8sRoutes failed: %v", err)
		}
		if !strings.Contains(detail, "10.96.0.0/16") {
			t.Errorf("expected detail to mention 10.96.0.0/16, got %q", detail)
		}
		if st.Get(ServiceCIDRStateKey) != "10.96.0.0/16" {
			t.Errorf("expected state to have 10.96.0.0/16, got %q", st.Get(ServiceCIDRStateKey))
		}

		patched, err := dyn.Resource(healCNEInstanceGVR).Namespace(InstanceNamespace).Get(ctx, "instance", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		vals := tmmEnvValues(patched, render.TMMK8sRoutesEnv)
		if len(vals) != 1 || vals[0] != "10.96.0.0/16" {
			t.Errorf("expected [10.96.0.0/16], got %v", vals)
		}
		if v, _ := tmmEnvValue(patched, "TMM_CALICO_ROUTER"); v != "default" {
			t.Errorf("expected other env to be preserved, got %q", v)
		}
	})
}

package k8s

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	fakek8s "k8s.io/client-go/kubernetes/fake"
)

// waitTestTimeout keeps every success-path Wait* test well under Go's
// default test timeout — success is a single immediate poll
// (wait.PollUntilContextTimeout's immediate=true), so the timeout value
// itself is never actually waited out.
const waitTestTimeout = 2 * time.Second

func TestIsCertificateReady(t *testing.T) {
	cases := []struct {
		name string
		obj  map[string]interface{}
		want bool
	}{
		{"ready true", map[string]interface{}{"status": map[string]interface{}{
			"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}},
		}}, true},
		{"ready false", map[string]interface{}{"status": map[string]interface{}{
			"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "False"}},
		}}, false},
		{"no status", map[string]interface{}{}, false},
		{"no conditions", map[string]interface{}{"status": map[string]interface{}{}}, false},
		{"other condition type only", map[string]interface{}{"status": map[string]interface{}{
			"conditions": []interface{}{map[string]interface{}{"type": "Issuing", "status": "True"}},
		}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsCertificateReady(tc.obj); got != tc.want {
				t.Errorf("IsCertificateReady(%v) = %v, want %v", tc.obj, got, tc.want)
			}
		})
	}
}

func TestIsCRDEstablished(t *testing.T) {
	cases := []struct {
		name string
		obj  map[string]interface{}
		want bool
	}{
		{"established true", map[string]interface{}{"status": map[string]interface{}{
			"conditions": []interface{}{map[string]interface{}{"type": "Established", "status": "True"}},
		}}, true},
		{"established false", map[string]interface{}{"status": map[string]interface{}{
			"conditions": []interface{}{map[string]interface{}{"type": "Established", "status": "False"}},
		}}, false},
		{"no status", map[string]interface{}{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCRDEstablished(tc.obj); got != tc.want {
				t.Errorf("isCRDEstablished(%v) = %v, want %v", tc.obj, got, tc.want)
			}
		})
	}
}

func TestSplitDotPath(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"status", []string{"status"}},
		{"status.state", []string{"status", "state"}},
		{"a.b.c", []string{"a", "b", "c"}},
	}
	for _, tc := range cases {
		got := splitDotPath(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("splitDotPath(%q) = %v, want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitDotPath(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestWaitForDeploymentReady_AlreadyReady(t *testing.T) {
	cs := fakek8s.NewClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "d", Namespace: "ns"},
		Status:     appsv1.DeploymentStatus{Replicas: 2, AvailableReplicas: 2},
	})
	if err := WaitForDeploymentReady(context.Background(), cs, "ns", "d", waitTestTimeout); err != nil {
		t.Fatalf("WaitForDeploymentReady: %v", err)
	}
}

func TestWaitForDaemonSetReady_AlreadyReady(t *testing.T) {
	cs := fakek8s.NewClientset(&appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "ds", Namespace: "ns"},
		Status:     appsv1.DaemonSetStatus{DesiredNumberScheduled: 3, NumberReady: 3},
	})
	if err := WaitForDaemonSetReady(context.Background(), cs, "ns", "ds", waitTestTimeout); err != nil {
		t.Fatalf("WaitForDaemonSetReady: %v", err)
	}
}

func TestWaitForPodReady_AlreadyReady(t *testing.T) {
	cs := fakek8s.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Status: corev1.PodStatus{Conditions: []corev1.PodCondition{
			{Type: corev1.PodReady, Status: corev1.ConditionTrue},
		}},
	})
	if err := WaitForPodReady(context.Background(), cs, "ns", "p", waitTestTimeout); err != nil {
		t.Fatalf("WaitForPodReady: %v", err)
	}
}

func TestWaitForPodReady_FailedPhaseErrorsImmediately(t *testing.T) {
	cs := fakek8s.NewClientset(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Status:     corev1.PodStatus{Phase: corev1.PodFailed},
	})
	err := WaitForPodReady(context.Background(), cs, "ns", "p", waitTestTimeout)
	if err == nil {
		t.Fatal("expected an error for a Failed pod, got nil")
	}
}

func TestDeploymentReplicaStatus(t *testing.T) {
	cs := fakek8s.NewClientset(&appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "d", Namespace: "ns"},
		Status:     appsv1.DeploymentStatus{Replicas: 3, AvailableReplicas: 2},
	})
	available, desired, err := DeploymentReplicaStatus(context.Background(), cs, "ns", "d")
	if err != nil {
		t.Fatalf("DeploymentReplicaStatus: %v", err)
	}
	if available != 2 || desired != 3 {
		t.Errorf("got available=%d desired=%d, want 2/3", available, desired)
	}
}

func TestDeploymentReplicaStatus_NotFound(t *testing.T) {
	cs := fakek8s.NewClientset()
	if _, _, err := DeploymentReplicaStatus(context.Background(), cs, "ns", "missing"); err == nil {
		t.Fatal("expected an error for a missing deployment, got nil")
	}
}

// newCertificateObj builds an unstructured cert-manager.io/v1 Certificate
// with a Ready=True condition.
func newCertificateObj(ns, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata":   map[string]interface{}{"name": name, "namespace": ns},
		"status": map[string]interface{}{
			"conditions": []interface{}{map[string]interface{}{"type": "Ready", "status": "True"}},
		},
	}}
}

func TestWaitForCertificateReady_AlreadyReady(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), newCertificateObj("ns", "ext-ca"))
	if err := WaitForCertificateReady(context.Background(), dyn, "ns", "ext-ca", waitTestTimeout); err != nil {
		t.Fatalf("WaitForCertificateReady: %v", err)
	}
}

// newCRDObj builds an unstructured apiextensions.k8s.io/v1
// CustomResourceDefinition with an Established=True condition.
func newCRDObj(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]interface{}{"name": name},
		"status": map[string]interface{}{
			"conditions": []interface{}{map[string]interface{}{"type": "Established", "status": "True"}},
		},
	}}
}

func TestWaitForCRDEstablished_AlreadyEstablished(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), newCRDObj("cneinstances.k8s.f5.com"))
	if err := WaitForCRDEstablished(context.Background(), dyn, "cneinstances.k8s.f5.com", waitTestTimeout); err != nil {
		t.Fatalf("WaitForCRDEstablished: %v", err)
	}
}

func TestWaitForUnstructuredCondition_AlreadyMatches(t *testing.T) {
	gvr := CertificateGVR
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata":   map[string]interface{}{"name": "x", "namespace": "ns"},
		"status":     map[string]interface{}{"state": "Active"},
	}}
	dyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), obj)
	err := WaitForUnstructuredCondition(context.Background(), dyn, gvr, "ns", "x", "status.state", "Active", waitTestTimeout)
	if err != nil {
		t.Fatalf("WaitForUnstructuredCondition: %v", err)
	}
}

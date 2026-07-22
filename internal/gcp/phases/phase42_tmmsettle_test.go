package phases

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	fakek8s "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

const testTMMSettlePollInterval = time.Millisecond

// cneInstance builds a CNEInstance with F5TmmAvailable set to the given status.
// The fake dynamic client derives the GVR from the Kind
// (UnsafeGuessKindToResource: "CNEInstance" -> "cneinstances"), which matches
// cneInstanceGVR.
func cneInstance(tmmAvailable string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "k8s.f5.com/v1",
		"kind":       "CNEInstance",
		"metadata":   map[string]interface{}{"name": cneInstanceName, "namespace": tmmSettleNamespace},
		"status": map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{"type": "Accepted", "status": "True"},
				map[string]interface{}{"type": tmmAvailableCondition, "status": tmmAvailable},
			},
		},
	}}
}

func dynWithCNE(obj *unstructured.Unstructured) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	if obj == nil {
		return dynamicfake.NewSimpleDynamicClient(scheme)
	}
	return dynamicfake.NewSimpleDynamicClient(scheme, obj)
}

func tmmPod(name, node string, terminating bool, phase corev1.PodPhase) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: tmmSettleNamespace,
			Labels:    map[string]string{"app": "f5-tmm"},
		},
		Spec:   corev1.PodSpec{NodeName: node},
		Status: corev1.PodStatus{Phase: phase},
	}
	if terminating {
		now := metav1.Now()
		p.DeletionTimestamp = &now
		p.Finalizers = []string{"kubernetes"}
	}
	return p
}

func TestPhase42TMMSettle_DryRun_NoClientsNeeded(t *testing.T) {
	if err := Phase42TMMSettle(context.Background(), testCluster(), testState(t), nil, true); err != nil {
		t.Fatalf("Phase42TMMSettle(dryRun=true): %v", err)
	}
}

// With no live clients (GKEBNKCTL_SKIP_AUTH / dry-run infra) the phase cannot
// observe or intervene, so it degrades to a warn+skip rather than failing.
func TestPhase42TMMSettle_NilClientsSkips(t *testing.T) {
	if err := Phase42TMMSettle(context.Background(), testCluster(), testState(t), &gcp.Clients{}, false); err != nil {
		t.Fatalf("nil-clients path must skip, not error, got: %v", err)
	}
}

// The gate returns immediately once the CNEInstance reports F5TmmAvailable=True.
func TestSettleAndGateTMM_ReturnsWhenTMMAvailable(t *testing.T) {
	k8sClient := fakek8s.NewClientset(tmmPod("tmm-1", "node-a", false, corev1.PodRunning))
	dyn := dynWithCNE(cneInstance("True"))

	if err := settleAndGateTMM(context.Background(), k8sClient, dyn, testTMMSettlePollInterval, 5*time.Second); err != nil {
		t.Fatalf("expected success when F5TmmAvailable=True, got: %v", err)
	}
}

// The critical contract change: `up` must NOT proceed over a dead dataplane.
// When TMM never becomes available the gate returns an error rather than the
// old silent success.
func TestSettleAndGateTMM_ErrorsWhenTMMNeverAvailable(t *testing.T) {
	k8sClient := fakek8s.NewClientset(tmmPod("tmm-old", "node-a", true, corev1.PodRunning))
	dyn := dynWithCNE(cneInstance("False"))

	err := settleAndGateTMM(context.Background(), k8sClient, dyn, testTMMSettlePollInterval, 20*time.Millisecond)
	if err == nil {
		t.Fatal("expected an error when TMM never becomes available")
	}
	if !strings.Contains(err.Error(), "did not become available") {
		t.Errorf("error = %q, want a TMM-not-available message", err)
	}
}

// A missing CNEInstance is treated as "not yet" and still times out with an
// error, not a panic or a false success.
func TestSettleAndGateTMM_ErrorsWhenCNEInstanceAbsent(t *testing.T) {
	k8sClient := fakek8s.NewClientset()
	dyn := dynWithCNE(nil)

	if err := settleAndGateTMM(context.Background(), k8sClient, dyn, testTMMSettlePollInterval, 20*time.Millisecond); err == nil {
		t.Fatal("expected a timeout error when the CNEInstance is absent")
	}
}

// The intervention force-deletes the Terminating NIC-holder and leaves other
// pods alone. Re-listing fresh (rather than deleting a captured snapshot) is
// what fixes the stale-name no-op the live run hit.
func TestCycleStuckTMMPods_DeletesTerminatingKeepsOthers(t *testing.T) {
	k8sClient := fakek8s.NewClientset(
		tmmPod("tmm-old", "node-a", true, corev1.PodRunning),
		tmmPod("tmm-new", "node-a", false, corev1.PodPending),
	)

	cycleStuckTMMPods(context.Background(), k8sClient)

	if _, err := k8sClient.CoreV1().Pods(tmmSettleNamespace).Get(context.Background(), "tmm-old", metav1.GetOptions{}); err == nil {
		t.Error("expected the terminating pod tmm-old to be force-deleted")
	}
	if _, err := k8sClient.CoreV1().Pods(tmmSettleNamespace).Get(context.Background(), "tmm-new", metav1.GetOptions{}); err != nil {
		t.Errorf("expected the pending pod tmm-new to survive, got: %v", err)
	}
}

// No Terminating pod → no Delete calls, immediate return.
func TestCycleStuckTMMPods_NoOpWhenNoTerminating(t *testing.T) {
	k8sClient := fakek8s.NewClientset(tmmPod("tmm-1", "node-a", false, corev1.PodRunning))
	cycleStuckTMMPods(context.Background(), k8sClient)
	if _, err := k8sClient.CoreV1().Pods(tmmSettleNamespace).Get(context.Background(), "tmm-1", metav1.GetOptions{}); err != nil {
		t.Errorf("running pod must be untouched, got: %v", err)
	}
}

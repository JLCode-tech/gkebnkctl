package phases

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
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

func fakeLicense(ns, name, state string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "k8s.f5net.com/v1",
			"kind":       "License",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": ns,
			},
			"status": map[string]interface{}{
				"state": state,
			},
		},
	}
}

func dynWithResources(objs ...*unstructured.Unstructured) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	var runtimeObjs []runtime.Object
	for _, obj := range objs {
		if obj != nil {
			runtimeObjs = append(runtimeObjs, obj)
		}
	}
	return dynamicfake.NewSimpleDynamicClient(scheme, runtimeObjs...)
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
	tmmDS := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: tmmDaemonSetName, Namespace: tmmSettleNamespace},
	}
	k8sClient := fakek8s.NewClientset(tmmDS, tmmPod("tmm-1", "node-a", false, corev1.PodRunning))
	dyn := dynWithCNE(cneInstance("True"))

	if err := settleAndGateTMM(context.Background(), k8sClient, dyn, testTMMSettlePollInterval, 5*time.Second); err != nil {
		t.Fatalf("expected success when F5TmmAvailable=True, got: %v", err)
	}

	gotDS, err := k8sClient.AppsV1().DaemonSets(tmmSettleNamespace).Get(context.Background(), tmmDaemonSetName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get f5-tmm DaemonSet: %v", err)
	}
	if gotDS.Spec.UpdateStrategy.RollingUpdate == nil ||
		gotDS.Spec.UpdateStrategy.RollingUpdate.MaxSurge == nil ||
		gotDS.Spec.UpdateStrategy.RollingUpdate.MaxSurge.IntVal != 0 ||
		gotDS.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable == nil ||
		gotDS.Spec.UpdateStrategy.RollingUpdate.MaxUnavailable.IntVal != 1 {
		t.Errorf("expected DaemonSet/f5-tmm rollingUpdate {maxSurge:0, maxUnavailable:1}, got %+v", gotDS.Spec.UpdateStrategy)
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

func TestSettleAndGateTMM_WithActiveLicense(t *testing.T) {
	k8sClient := fakek8s.NewClientset(tmmPod("tmm-1", "node-a", false, corev1.PodRunning))
	dyn := dynWithResources(
		cneInstance("True"),
		fakeLicense("f5-utils", "bnk-license", "Active"),
	)

	if err := settleAndGateTMM(context.Background(), k8sClient, dyn, testTMMSettlePollInterval, 5*time.Second); err != nil {
		t.Fatalf("expected success with active license, got: %v", err)
	}
}

func TestSettleAndGateTMM_AlertsOnLicenseFailed(t *testing.T) {
	k8sClient := fakek8s.NewClientset(tmmPod("tmm-old", "node-a", true, corev1.PodRunning))
	dyn := dynWithResources(
		cneInstance("False"),
		fakeLicense("f5-utils", "bnk-license", "Failed"),
	)

	err := settleAndGateTMM(context.Background(), k8sClient, dyn, testTMMSettlePollInterval, 20*time.Millisecond)
	if err == nil {
		t.Fatal("expected error when license is Failed and TMM never available")
	}
	if !strings.Contains(err.Error(), "License: state=Failed") {
		t.Errorf("expected error to mention License: state=Failed, got: %v", err)
	}
}

func TestSettleAndGateTMM_CWCCrashLoop_And_EnrichedTimeoutError(t *testing.T) {
	cwcPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cwc-test-1",
			Namespace: "f5-utils",
			Labels:    map[string]string{"app": "cwc"},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:         "f5-spk-cwc",
					Ready:        false,
					RestartCount: 5,
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "CrashLoopBackOff",
							Message: "Back-off 5m0s restarting failed container",
						},
					},
				},
			},
		},
	}

	stuckTMMPod := tmmPod("tmm-stuck", "node-a", false, corev1.PodPending)

	k8sClient := fakek8s.NewClientset(cwcPod, stuckTMMPod)
	dyn := dynWithResources(
		cneInstance("False"),
		fakeLicense("f5-utils", "bnk-license", "Active"),
	)

	err := settleAndGateTMM(context.Background(), k8sClient, dyn, testTMMSettlePollInterval, 20*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "License: state=Active") {
		t.Errorf("expected error to include License: state=Active, got: %s", errStr)
	}
	if !strings.Contains(errStr, "Stuck pod diagnostics:") {
		t.Errorf("expected error to include Stuck pod diagnostics, got: %s", errStr)
	}
	if !strings.Contains(errStr, "pod f5-utils/cwc-test-1") {
		t.Errorf("expected error to include cwc pod diagnostic, got: %s", errStr)
	}
	if !strings.Contains(errStr, "pod f5-cne-system/tmm-stuck") {
		t.Errorf("expected error to include stuck TMM pod diagnostic, got: %s", errStr)
	}
}

func TestIsLicenseFailed(t *testing.T) {
	cases := []struct {
		state string
		want  bool
	}{
		{"Active", false},
		{"Pending", false},
		{"NotFound", false},
		{"Failed", true},
		{"Invalid", true},
		{"Error", true},
		{"failed-quota-exceeded", true},
	}
	for _, tc := range cases {
		if got := isLicenseFailed(tc.state); got != tc.want {
			t.Errorf("isLicenseFailed(%q) = %v, want %v", tc.state, got, tc.want)
		}
	}
}

func TestCheckCWCCrashLoop(t *testing.T) {
	podNormal := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "cwc-ok", Namespace: "f5-utils", Labels: map[string]string{"app": "cwc"}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "cwc", Ready: true, RestartCount: 0},
			},
		},
	}
	clientOk := fakek8s.NewClientset(podNormal)
	crash, _ := checkCWCCrashLoop(context.Background(), clientOk)
	if crash {
		t.Error("expected crash=false for healthy pod")
	}

	podCrash := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "cwc-broken", Namespace: "f5-utils", Labels: map[string]string{"app": "cwc"}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "f5-spk-cwc", Ready: false, RestartCount: 4},
			},
		},
	}
	clientCrash := fakek8s.NewClientset(podCrash)
	crash, detail := checkCWCCrashLoop(context.Background(), clientCrash)
	if !crash {
		t.Error("expected crash=true for restarting pod")
	}
	if !strings.Contains(detail, "cwc-broken") {
		t.Errorf("expected detail to mention cwc-broken, got: %s", detail)
	}
}

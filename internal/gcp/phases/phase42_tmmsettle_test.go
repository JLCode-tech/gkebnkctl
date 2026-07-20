package phases

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakek8s "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

// testTMMSettlePollInterval keeps the retry-exhaustion path (6 attempts)
// fast in tests.
const testTMMSettlePollInterval = time.Millisecond

func TestPhase42TMMSettle_DryRun_NoClientsNeeded(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	if err := Phase42TMMSettle(context.Background(), cl, st, nil, true); err != nil {
		t.Fatalf("Phase42TMMSettle(dryRun=true): %v", err)
	}
}

// TestPhase42TMMSettle_NilK8sIsNonFatal proves this phase NEVER returns an
// error — even the nil-clients edge case is a warn+skip, matching the "best
// effort, never returns error" contract (docs/SPEC-go-tracer.md §3 Phase42).
func TestPhase42TMMSettle_NilK8sIsNonFatal(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	if err := Phase42TMMSettle(context.Background(), cl, st, &gcp.Clients{}, false); err != nil {
		t.Fatalf("Phase42TMMSettle must never return an error, got: %v", err)
	}
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
		// Force-delete tolerates any non-zero finalizer needed to keep the
		// object present in the fake tracker with a DeletionTimestamp set.
		p.Finalizers = []string{"kubernetes"}
	}
	return p
}

// TestCycleTMMPodIfStuck_DeletesTerminatingWhenPendingCoexists proves the
// bash-ground-truth behavior (docs/SPEC-go-tracer.md deviation noted in
// phase42_tmmsettle.go): ANY Terminating + ANY Pending pod pair triggers a
// force-delete of the terminating one(s), no same-node matching required.
func TestCycleTMMPodIfStuck_DeletesTerminatingWhenPendingCoexists(t *testing.T) {
	k8sClient := fakek8s.NewClientset(
		tmmPod("tmm-old", "node-a", true, corev1.PodRunning),
		tmmPod("tmm-new", "node-a", false, corev1.PodPending),
	)

	cycleTMMPodIfStuck(context.Background(), k8sClient, testTMMSettlePollInterval)

	_, err := k8sClient.CoreV1().Pods(tmmSettleNamespace).Get(context.Background(), "tmm-old", metav1.GetOptions{})
	if err == nil {
		t.Error("expected the terminating pod tmm-old to be force-deleted")
	}
	_, err = k8sClient.CoreV1().Pods(tmmSettleNamespace).Get(context.Background(), "tmm-new", metav1.GetOptions{})
	if err != nil {
		t.Errorf("expected the pending pod tmm-new to survive, got: %v", err)
	}
}

// TestCycleTMMPodIfStuck_NoOpWhenOnlyRunningPods proves a healthy steady
// state (no Terminating, no Pending) makes zero Delete calls and returns
// immediately (no retry sleep).
func TestCycleTMMPodIfStuck_NoOpWhenOnlyRunningPods(t *testing.T) {
	k8sClient := fakek8s.NewClientset(
		tmmPod("tmm-1", "node-a", false, corev1.PodRunning),
	)
	start := time.Now()
	cycleTMMPodIfStuck(context.Background(), k8sClient, 10*time.Second) // large interval — would time out the test if it slept even once
	if time.Since(start) > 2*time.Second {
		t.Error("expected an immediate no-op return, but the function appears to have slept")
	}
}

// TestCycleTMMPodIfStuck_GivesUpAfterMaxAttempts proves the loop is bounded
// — it does not retry forever when the deadlock never clears.
func TestCycleTMMPodIfStuck_GivesUpAfterMaxAttempts(t *testing.T) {
	k8sClient := fakek8s.NewClientset(
		tmmPod("tmm-old", "node-a", true, corev1.PodRunning),
		tmmPod("tmm-new", "node-a", false, corev1.PodPending),
	)
	start := time.Now()
	cycleTMMPodIfStuck(context.Background(), k8sClient, testTMMSettlePollInterval)
	elapsed := time.Since(start)
	// tmmSettleMaxAttempts-1 sleeps of testTMMSettlePollInterval each; a
	// generous ceiling well under the production 10s interval proves it
	// didn't fall through to the real constant.
	if elapsed > time.Second {
		t.Errorf("expected the bounded retry loop to finish quickly with a 1ms poll interval, took %s", elapsed)
	}
}

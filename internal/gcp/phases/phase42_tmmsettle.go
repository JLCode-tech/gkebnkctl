package phases

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
)

const (
	tmmSettleNamespace    = "f5-bnk"
	tmmSettleLabelSel     = "app=f5-tmm"
	tmmSettlePollInterval = 10 * time.Second

	// tmmSettleTimeout bounds how long Phase42 both breaks the NIC-handoff
	// deadlock AND waits for TMM to actually come up. The old value was
	// effectively 60s (6 × 10s), far shorter than the sustained manual
	// settling the deadlock needed during the 2026-07-20 trial — the
	// automation gave up while TMM was still churning and `up` then declared
	// success over a dead dataplane. 12 minutes matches the order of magnitude
	// of a cold host-device TMM bring-up.
	tmmSettleTimeout = 12 * time.Minute

	// cneInstanceName is the singleton CNEInstance Phase38 applies.
	cneInstanceName = "instance"
	// tmmAvailableCondition is the CNEInstance status condition that flips True
	// once TMM is actually serving — the authoritative readiness signal, more
	// meaningful than pod-Ready alone.
	tmmAvailableCondition = "F5TmmAvailable"
)

// cneInstanceGVR resolves the CNEInstance CR (k8s.f5.com/v1) directly rather
// than through the RESTMapper — its plural is regular ("cneinstances"), so no
// discovery is needed.
var cneInstanceGVR = schema.GroupVersionResource{
	Group:    "k8s.f5.com",
	Version:  "v1",
	Resource: "cneinstances",
}

// Phase42TMMSettle breaks the host-device DS NIC-handoff deadlock AND gates on
// TMM actually coming up. It is BOTH the settle intervention and the TMM
// readiness gate.
//
// The deadlock: on a rollout a new TMM pod can't bind the node NICs while the
// old pod still holds them, so the DS rollout wedges — the old pod stays
// Terminating and the new one stays Pending/ContainerCreating (whose sandbox
// then fails because multus loses API reachability during the handoff). The
// bash seed's bnk::cycle_tmm_pod_if_stuck broke this by force-deleting the
// stuck old pod; a human watched and repeated until TMM stuck.
//
// This automates that watch. It loops until the CNEInstance reports
// F5TmmAvailable=True or tmmSettleTimeout elapses, and on every iteration
// re-lists the TMM pods FRESH and force-deletes any Terminating one (grace=0).
// Two deliberate hardenings over the old port, both driven by a greenfield run
// that exited 0 over a dead dataplane (2026-07-21, cluster bnk-scratch2):
//
//   - It no longer returns the instant a Terminating+Pending pair is absent.
//     TMM churn is bursty — at any single poll the pair may not coexist even
//     though TMM is nowhere near up — so the old early-return handed control
//     back to `up` mid-churn. The loop now exits only on real readiness or
//     timeout.
//   - Names are re-fetched every iteration instead of deleting a snapshot
//     captured earlier. The old code logged
//     `deleting pod f5-tmm-... : not found` because the pod had already churned
//     to a new name before the delete landed, so the intervention was a no-op.
//
// It force-deletes any Terminating TMM pod while TMM is not yet Available
// (broader than the bash terminating+pending pairing) because a Terminating
// pod holding the NICs is precisely the blocker, and the readiness gate below
// means we never touch pods once TMM is up.
//
// Contract change: unlike the old best-effort port, this RETURNS AN ERROR if
// TMM is not Available by the timeout. `up` must not report success over a
// cluster whose dataplane never came up. When the k8s/dynamic clients are
// absent (GKEBNKCTL_SKIP_AUTH=1 / dry-run) it degrades to a skip, since it
// cannot observe or intervene.
func Phase42TMMSettle(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 42] TMM settle: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 42] would break any TMM NIC-handoff deadlock and wait for CNEInstance F5TmmAvailable=True")
		return nil
	}

	if clients.K8s == nil || clients.Dynamic == nil {
		fmt.Fprintln(os.Stderr, "[phase 42] warn: k8s/dynamic client unavailable — skipping TMM settle+gate")
		return nil
	}

	return settleAndGateTMM(ctx, clients.K8s, clients.Dynamic, tmmSettlePollInterval, tmmSettleTimeout)
}

// settleAndGateTMM implements the loop. pollInterval/timeout are injectable so
// tests stay fast. Returns nil once F5TmmAvailable=True, or an error if the
// timeout elapses first.
func settleAndGateTMM(ctx context.Context, client kubernetes.Interface, dyn dynamic.Interface, pollInterval, timeout time.Duration) error {
	err := gcp.PollUntil(ctx, pollInterval, timeout, func(ctx context.Context) (bool, error) {
		if tmmAvailable(ctx, dyn) {
			fmt.Fprintln(os.Stderr, "[phase 42] CNEInstance F5TmmAvailable=True — TMM is up")
			return true, nil
		}

		cycleStuckTMMPods(ctx, client)

		return false, nil
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("phase42: TMM did not become available within %s (CNEInstance %s/%s %s != True) — "+
				"check `kubectl get pods -n %s -l %s` and `kubectl describe cneinstance -n %s %s`",
				timeout, tmmSettleNamespace, cneInstanceName, tmmAvailableCondition,
				tmmSettleNamespace, tmmSettleLabelSel, tmmSettleNamespace, cneInstanceName)
		}
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("phase42: cancelled while waiting for TMM to become available: %w", err)
		}
		return err
	}
	return nil
}

// tmmAvailable reports whether the CNEInstance's F5TmmAvailable condition is
// True. A missing CR or a read error reads as "not yet" (false) — the loop
// simply polls again — so a transient API blip never aborts the gate.
func tmmAvailable(ctx context.Context, dyn dynamic.Interface) bool {
	obj, err := dyn.Resource(cneInstanceGVR).Namespace(tmmSettleNamespace).Get(ctx, cneInstanceName, metav1.GetOptions{})
	if err != nil {
		return false
	}
	return k8s.UnstructuredConditionIsTrue(obj.Object, tmmAvailableCondition)
}

// cycleStuckTMMPods force-deletes (grace=0) every Terminating app=f5-tmm pod
// it currently sees, re-listing fresh so it never acts on a stale name. Every
// error is logged and swallowed — the caller's loop and readiness gate decide
// success, not this single sweep.
func cycleStuckTMMPods(ctx context.Context, client kubernetes.Interface) {
	pods, err := client.CoreV1().Pods(tmmSettleNamespace).List(ctx, metav1.ListOptions{LabelSelector: tmmSettleLabelSel})
	if err != nil {
		fmt.Fprintf(os.Stderr, "[phase 42] warn: listing %s pods in %s: %v\n", tmmSettleLabelSel, tmmSettleNamespace, err)
		return
	}

	var terminating, pending []string
	for _, p := range pods.Items {
		switch {
		case p.DeletionTimestamp != nil:
			terminating = append(terminating, p.Name)
		case p.Status.Phase == "Pending":
			pending = append(pending, p.Name)
		}
	}
	if len(terminating) == 0 {
		return
	}

	fmt.Fprintf(os.Stderr, "[phase 42] TMM not yet available; terminating=%v pending=%v — force-deleting stuck pod(s)\n", terminating, pending)
	grace := int64(0)
	for _, name := range terminating {
		if err := client.CoreV1().Pods(tmmSettleNamespace).Delete(ctx, name, metav1.DeleteOptions{GracePeriodSeconds: &grace}); err != nil {
			fmt.Fprintf(os.Stderr, "[phase 42] warn: deleting pod %s: %v\n", name, err)
		}
	}
}

package phases

import (
	"context"
	"fmt"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

const (
	tmmSettleNamespace    = "f5-bnk"
	tmmSettleLabelSel     = "app=f5-tmm"
	tmmSettleMaxAttempts  = 6
	tmmSettlePollInterval = 10 * time.Second
)

// Phase42TMMSettle detects the host-device DS rolling-update NIC-handoff
// deadlock (a new TMM pod can't bind the NICs while the old pod still holds
// them, so the rollout hangs forever unless the old pod is deleted) and
// intervenes: if BOTH a Terminating pod and a Pending pod exist among the
// app=f5-tmm pods in f5-bnk, the Terminating one(s) are force-deleted
// (grace=0) and the check retries after a short wait, up to
// tmmSettleMaxAttempts times.
//
// Deviation from docs/SPEC-go-tracer.md §3 Phase42 (noted per the task's
// "follow bash's live-proven behavior on ambiguity" instruction): the spec
// table paraphrases this as "old-Terminating + new-Pending pods ON THE SAME
// NODE", but the bash ground truth
// (reference/bash-seed/bin/30-bnk-install.sh's bnk::cycle_tmm_pod_if_stuck)
// has no same-node matching — it intervenes whenever ANY Terminating pod and
// ANY Pending pod coexist. This port follows the bash behavior exactly.
//
// Reactive, best-effort, and NEVER returns an error — matching bash, this
// step must never fail `up` even if the deadlock never clears.
func Phase42TMMSettle(ctx context.Context, cl *intent.Cluster, _ *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 42] TMM settle: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 42] would check for TMM pod NIC-handoff deadlock in f5-bnk (app=f5-tmm)")
		return nil
	}

	if clients.K8s == nil {
		fmt.Fprintln(os.Stderr, "[phase 42] warn: Clients.K8s is nil — skipping TMM settle check")
		return nil
	}

	cycleTMMPodIfStuck(ctx, clients.K8s, tmmSettlePollInterval)
	fmt.Fprintln(os.Stderr, "[phase 42] TMM settle done")
	return nil
}

// cycleTMMPodIfStuck implements the retry loop. pollInterval is injectable
// so tests stay fast. Every failure along the way (List error, Delete error)
// is logged and swallowed — this function never returns an error, matching
// Phase42TMMSettle's contract.
func cycleTMMPodIfStuck(ctx context.Context, client kubernetes.Interface, pollInterval time.Duration) {
	for attempt := 1; attempt <= tmmSettleMaxAttempts; attempt++ {
		pods, err := client.CoreV1().Pods(tmmSettleNamespace).List(ctx, metav1.ListOptions{LabelSelector: tmmSettleLabelSel})
		if err != nil {
			fmt.Fprintf(os.Stderr, "[phase 42] warn: listing %s pods in %s: %v — giving up\n", tmmSettleLabelSel, tmmSettleNamespace, err)
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

		if len(terminating) == 0 || len(pending) == 0 {
			return
		}

		fmt.Fprintf(os.Stderr, "[phase 42] TMM pod NIC-handoff deadlock detected — deleting old pod(s): %v\n", terminating)
		grace := int64(0)
		for _, name := range terminating {
			if err := client.CoreV1().Pods(tmmSettleNamespace).Delete(ctx, name, metav1.DeleteOptions{GracePeriodSeconds: &grace}); err != nil {
				fmt.Fprintf(os.Stderr, "[phase 42] warn: deleting pod %s: %v\n", name, err)
			}
		}

		if attempt == tmmSettleMaxAttempts {
			fmt.Fprintln(os.Stderr, "[phase 42] warn: TMM pod state still unsettled after retries — check manually: kubectl get pods -n f5-bnk -l app=f5-tmm")
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pollInterval):
		}
	}
}

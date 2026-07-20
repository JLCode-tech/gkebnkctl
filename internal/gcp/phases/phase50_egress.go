package phases

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/gkebnkctl/internal/k8s/render"
)

const (
	egressTemplatePath           = "templates/egress.yaml.tmpl"
	agentRouteSetterTemplatePath = "templates/agent-route-setter.yaml.tmpl"
	egressClientPodName          = "egress-client"
	egressClientPodTimeout       = 120 * time.Second

	// verifyEgressEnvVar gates Phase50's best-effort pkts_in/curl
	// verification pass. Phase signatures are frozen
	// (docs/SPEC-go-tracer.md §3), so an env var is the extension point for
	// "gated behind a flag, default-skip for tracer" (§8 Task D) — matching
	// the GKEBNKCTL_SKIP_AUTH precedent in internal/cli/lifecycle.go.
	verifyEgressEnvVar = "GKEBNKCTL_VERIFY_EGRESS"

	egressVerifyCurlTimeout = 10 * time.Second
	egressTraceURL          = "https://1.1.1.1/cdn-cgi/trace"
)

// Phase50Egress renders and applies the F5SPKEgress pseudo-CNI vxlan CR, the
// bnk-agent-route-setter DaemonSet, and the egress-client test pod, then
// waits for egress-client to be Ready (docs/SPEC-go-tracer.md §3 Phase50).
// Needs a live cluster.
//
// Verification (tmctl clientside.pkts_in delta + a curl trace through
// egress-client, mirroring reference/bash-seed/bin/40-egress.sh) runs only
// when GKEBNKCTL_VERIFY_EGRESS=1 is set — default-skip for the tracer
// milestone (build-first, §8 Task D). It is best-effort: a verification
// failure is logged but NEVER fails `up` (§3 Phase50: "verification never
// blocks"), unlike the bash script's `exit 1` on FAIL.
func Phase50Egress(ctx context.Context, cl *intent.Cluster, st *state.State, clients *gcp.Clients, dryRun bool) error {
	fmt.Fprintf(os.Stderr, "[phase 50] egress: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 50] would apply F5SPKEgress/egress-agents + bnk-agent-route-setter DS + egress-client pod in %s (needs live cluster)\n", cl.Egress.TestNamespace)
		return nil
	}

	if err := applyEgress(ctx, clients, cl, st); err != nil {
		return err
	}
	if err := applyAgentRouteSetter(ctx, clients, cl, st); err != nil {
		return err
	}

	if clients == nil || clients.K8s == nil {
		return fmt.Errorf("phase50: Clients.K8s is nil — AttachK8s must run first (after Phase20)")
	}
	fmt.Fprintln(os.Stderr, "[phase 50] waiting for egress-client pod to be Ready")
	if err := k8s.WaitForPodReady(ctx, clients.K8s, cl.Egress.TestNamespace, egressClientPodName, egressClientPodTimeout); err != nil {
		return fmt.Errorf("phase50: waiting for egress-client pod: %w", err)
	}

	runEgressVerification(ctx, cl, clients)

	fmt.Fprintln(os.Stderr, "[phase 50] egress OK")
	return nil
}

// applyEgress renders templates/egress.yaml.tmpl (F5SPKEgress +
// egress-client pod) against cluster.yaml + Phase25-discovered
// AGENT_EXT_IFACE, then applies it.
func applyEgress(ctx context.Context, clients *gcp.Clients, cl *intent.Cluster, st *state.State) error {
	tmplBytes, err := manifests.FS.ReadFile(egressTemplatePath)
	if err != nil {
		return fmt.Errorf("phase50: reading embedded egress template: %w", err)
	}
	rendered, err := render.RenderEgress(tmplBytes, render.EgressVars{
		TestNamespace: cl.Egress.TestNamespace,
		VxlanKey:      cl.Egress.VxlanKey,
		VxlanMtu:      cl.Egress.VxlanMtu,
		AgentExtIface: st.Get("AGENT_EXT_IFACE"),
		VxlanPort:     cl.Egress.VxlanPort,
		AgentPoolName: cl.ClusterSpec.AgentPool.Name,
	})
	if err != nil {
		return fmt.Errorf("phase50: rendering egress: %w", err)
	}
	if err := applyManifestBytes(ctx, clients, rendered, false); err != nil {
		return fmt.Errorf("phase50: applying F5SPKEgress/egress-client: %w", err)
	}
	return nil
}

// applyAgentRouteSetter renders templates/agent-route-setter.yaml.tmpl (the
// bnk-agent-route-setter DaemonSet) against cluster.yaml +
// Phase25-discovered AGENT_EXT_IFACE, then applies it.
func applyAgentRouteSetter(ctx context.Context, clients *gcp.Clients, cl *intent.Cluster, st *state.State) error {
	tmplBytes, err := manifests.FS.ReadFile(agentRouteSetterTemplatePath)
	if err != nil {
		return fmt.Errorf("phase50: reading embedded agent-route-setter template: %w", err)
	}
	rendered, err := render.RenderAgentRouteSetter(tmplBytes, render.AgentRouteSetterVars{
		AgentPoolName: cl.ClusterSpec.AgentPool.Name,
		ExtPodsCidr:   cl.Network.External.PodsCIDR,
		ExtGwIP:       cl.ExtGwIP,
		AgentExtIface: st.Get("AGENT_EXT_IFACE"),
	})
	if err != nil {
		return fmt.Errorf("phase50: rendering agent-route-setter: %w", err)
	}
	if err := applyManifestBytes(ctx, clients, rendered, false); err != nil {
		return fmt.Errorf("phase50: applying bnk-agent-route-setter: %w", err)
	}
	return nil
}

// egressExecFunc executes command inside ns/pod (scoped to container, if
// non-empty) and returns its captured output. The real implementation
// (defaultEgressExecFunc) wraps k8s.ExecCapture's SPDY exec, which needs a
// real upgradeable connection and so cannot be faked directly (matches
// awsbnkctl's internal/k8s/exec_test.go boundary). Tests inject a canned
// egressExecFunc instead to exercise verifyEgress's decision logic (pkts_in
// delta, egress-IP parse, PASS/FAIL) without a live cluster.
type egressExecFunc func(ctx context.Context, ns, pod, container string, command []string) (string, error)

// defaultEgressExecFunc builds the real egressExecFunc bound to clients'
// already-authenticated rest.Config + typed clientset (populated by
// gcp.Clients.AttachK8s after Phase20).
func defaultEgressExecFunc(clients *gcp.Clients) egressExecFunc {
	return func(ctx context.Context, ns, pod, container string, command []string) (string, error) {
		stdout, stderr, err := k8s.ExecCapture(ctx, clients.RestConfig, clients.K8s, ns, pod, container, command)
		if err != nil {
			return stdout + stderr, err
		}
		return stdout, nil
	}
}

// runEgressVerification is the gated, best-effort entry point Phase50Egress
// calls after egress-client is Ready. It never returns an error — a
// verification failure is logged, matching "verification never blocks"
// (docs/SPEC-go-tracer.md §3 Phase50).
func runEgressVerification(ctx context.Context, cl *intent.Cluster, clients *gcp.Clients) {
	if os.Getenv(verifyEgressEnvVar) != "1" {
		fmt.Fprintf(os.Stderr, "[phase 50] verification skipped (set %s=1 to enable; best-effort, mirrors reference/bash-seed/bin/40-egress.sh)\n", verifyEgressEnvVar)
		return
	}
	if clients == nil || clients.RestConfig == nil || clients.K8s == nil {
		fmt.Fprintln(os.Stderr, "[phase 50] warn: Clients.RestConfig/K8s is nil — skipping verification")
		return
	}

	result := verifyEgress(ctx, clients.K8s, cl.Egress.TestNamespace, defaultEgressExecFunc(clients))
	logEgressVerification(result)
}

// egressVerifyResult carries the outcome of verifyEgress for logging.
type egressVerifyResult struct {
	skippedNoTMMPod bool
	tmmPod          string
	pktsInBefore    int64
	pktsInAfter     int64
	traceOutput     string
	egressIP        string
	passed          bool
}

// verifyEgress ports reference/bash-seed/bin/40-egress.sh's verification
// loop: find the first app=f5-tmm pod, read clientside.pkts_in before,
// curl 1.1.1.1/cdn-cgi/trace through egress-client, read pkts_in after, and
// PASS iff an egress source IP was parsed AND pkts_in increased.
// Exec failures on the tmctl/curl steps are tolerated (bash `|| true`) —
// they surface as empty output, not as a verifyEgress error.
func verifyEgress(ctx context.Context, k8sClient kubernetes.Interface, testNamespace string, exec egressExecFunc) egressVerifyResult {
	pods, err := k8sClient.CoreV1().Pods(tmmSettleNamespace).List(ctx, metav1.ListOptions{LabelSelector: tmmSettleLabelSel})
	if err != nil || len(pods.Items) == 0 {
		return egressVerifyResult{skippedNoTMMPod: true}
	}
	tmmPod := pods.Items[0].Name

	beforeOut, _ := exec(ctx, tmmSettleNamespace, tmmPod, "tmm", []string{"tmctl", "-a", "clientside.pkts_in"})
	before := sumLastFields(beforeOut)

	traceOut, _ := exec(ctx, testNamespace, egressClientPodName, "", []string{
		"curl", "-sk", "--max-time", strconv.Itoa(int(egressVerifyCurlTimeout.Seconds())), egressTraceURL,
	})

	afterOut, _ := exec(ctx, tmmSettleNamespace, tmmPod, "tmm", []string{"tmctl", "-a", "clientside.pkts_in"})
	after := sumLastFields(afterOut)

	egressIP := parseEgressIP(traceOut)

	return egressVerifyResult{
		tmmPod:       tmmPod,
		pktsInBefore: before,
		pktsInAfter:  after,
		traceOutput:  traceOut,
		egressIP:     egressIP,
		passed:       egressIP != "" && after > before,
	}
}

// sumLastFields sums the last whitespace-delimited field of every line,
// mirroring bash `awk '{sum+=$NF} END{print sum+0}'` — a tmctl output line
// whose last field doesn't parse as an integer contributes 0, exactly as
// awk's implicit numeric-string coercion would.
func sumLastFields(output string) int64 {
	var sum int64
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseInt(fields[len(fields)-1], 10, 64)
		if err != nil {
			continue
		}
		sum += n
	}
	return sum
}

// parseEgressIP extracts the value of the "ip=" line from a
// cdn-cgi/trace response body, mirroring bash
// `grep '^ip=' | cut -d= -f2`. Returns "" if no such line is present.
func parseEgressIP(traceOutput string) string {
	for _, line := range strings.Split(traceOutput, "\n") {
		if rest, ok := strings.CutPrefix(line, "ip="); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// logEgressVerification writes the PASS/FAIL/SKIP summary to stderr,
// mirroring reference/bash-seed/bin/40-egress.sh's log lines. Never returns
// an error — the caller (runEgressVerification) already guarantees
// verification is non-fatal.
func logEgressVerification(r egressVerifyResult) {
	if r.skippedNoTMMPod {
		fmt.Fprintln(os.Stderr, "[phase 50] verify: no TMM pod found (app=f5-tmm) — skipping pkts_in check")
		return
	}
	fmt.Fprintf(os.Stderr, "[phase 50] verify: clientside.pkts_in before=%d after=%d, egress source IP=%q\n",
		r.pktsInBefore, r.pktsInAfter, r.egressIP)
	if r.passed {
		fmt.Fprintf(os.Stderr, "[phase 50] verify: PASS — egress verified: pkts_in %d -> %d, egress source IP %s\n",
			r.pktsInBefore, r.pktsInAfter, r.egressIP)
		return
	}
	fmt.Fprintf(os.Stderr, "[phase 50] verify: FAIL (non-fatal) — pkts_in delta=%d, trace ip=%q; check: kubectl get f5spkegress,f5spkvlan -n f5-bnk; kubectl logs -n f5-bnk %s -c tmm\n",
		r.pktsInAfter-r.pktsInBefore, r.egressIP, r.tmmPod)
}

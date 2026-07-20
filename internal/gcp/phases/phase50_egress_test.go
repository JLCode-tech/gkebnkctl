package phases

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakek8s "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhase50Egress_DryRun_NoClientsNeeded(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	if err := Phase50Egress(context.Background(), cl, st, nil, true); err != nil {
		t.Fatalf("Phase50Egress(dryRun=true): %v", err)
	}
}

// TestPhase50Egress_LiveMode_RendersBeforeFailingOnNilClients proves both
// templates render successfully off state values before the (expected,
// since Clients.Dynamic is nil here) apply failure — a render bug would
// surface as a "rendering" error, not an opaque apply error. Mirrors
// phase35_nads_test.go's split.
func TestPhase50Egress_LiveMode_RendersBeforeFailingOnNilClients(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	st.Set("AGENT_EXT_IFACE", "ens5")

	err := Phase50Egress(context.Background(), cl, st, &gcp.Clients{}, false)
	if err == nil {
		t.Fatal("expected an error when Clients.Dynamic is nil in live mode")
	}
	if strings.Contains(err.Error(), "rendering") {
		t.Errorf("expected the render step to succeed and the apply step to fail, got a render error: %v", err)
	}
	if !strings.Contains(err.Error(), "AttachK8s") {
		t.Errorf("error = %q, want a hint to call AttachK8s first", err.Error())
	}
}

func tmmPodForVerify(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: tmmSettleNamespace,
			Labels:    map[string]string{"app": "f5-tmm"},
		},
	}
}

// TestVerifyEgress_NoTMMPod_Skips proves the no-TMM-pod case is reported as
// skipped, not as a failure — matching bash's "no TMM pod found — skipping"
// warn-and-continue.
func TestVerifyEgress_NoTMMPod_Skips(t *testing.T) {
	k8sClient := fakek8s.NewClientset()
	execFn := func(context.Context, string, string, string, []string) (string, error) {
		t.Fatal("exec must not be called when no TMM pod exists")
		return "", nil
	}

	result := verifyEgress(context.Background(), k8sClient, "egress-test", execFn)
	if !result.skippedNoTMMPod {
		t.Error("expected skippedNoTMMPod=true when no app=f5-tmm pod exists")
	}
}

// TestVerifyEgress_Pass proves the PASS path: an increasing pkts_in delta
// plus a parsed egress IP.
func TestVerifyEgress_Pass(t *testing.T) {
	k8sClient := fakek8s.NewClientset(tmmPodForVerify("tmm-0"))
	calls := 0
	execFn := func(_ context.Context, ns, pod, container string, command []string) (string, error) {
		calls++
		switch {
		case len(command) > 0 && command[0] == "tmctl":
			if calls == 1 {
				return "clientside.pkts_in 100\n", nil
			}
			return "clientside.pkts_in 142\n", nil
		case len(command) > 0 && command[0] == "curl":
			if ns != "egress-test" || pod != egressClientPodName {
				t.Errorf("curl exec targeted %s/%s, want egress-test/%s", ns, pod, egressClientPodName)
			}
			return "ip=34.61.234.229\nport=443\n", nil
		default:
			t.Fatalf("unexpected exec command: ns=%s pod=%s container=%s cmd=%v", ns, pod, container, command)
			return "", nil
		}
	}

	result := verifyEgress(context.Background(), k8sClient, "egress-test", execFn)
	if result.skippedNoTMMPod {
		t.Fatal("expected a TMM pod to be found")
	}
	if !result.passed {
		t.Errorf("expected passed=true, got pktsInBefore=%d pktsInAfter=%d egressIP=%q",
			result.pktsInBefore, result.pktsInAfter, result.egressIP)
	}
	if result.tmmPod != "tmm-0" {
		t.Errorf("tmmPod = %q, want tmm-0", result.tmmPod)
	}
	if result.pktsInBefore != 100 || result.pktsInAfter != 142 {
		t.Errorf("pktsInBefore/After = %d/%d, want 100/142", result.pktsInBefore, result.pktsInAfter)
	}
	if result.egressIP != "34.61.234.229" {
		t.Errorf("egressIP = %q, want 34.61.234.229", result.egressIP)
	}
}

// TestVerifyEgress_FailsOnNoDelta proves a flat pkts_in count (no traffic
// observed) fails verification even when an egress IP was parsed.
func TestVerifyEgress_FailsOnNoDelta(t *testing.T) {
	k8sClient := fakek8s.NewClientset(tmmPodForVerify("tmm-0"))
	execFn := func(_ context.Context, _, _, _ string, command []string) (string, error) {
		if command[0] == "tmctl" {
			return "clientside.pkts_in 50\n", nil
		}
		return "ip=34.61.234.229\n", nil
	}

	result := verifyEgress(context.Background(), k8sClient, "egress-test", execFn)
	if result.passed {
		t.Error("expected passed=false when pkts_in did not increase")
	}
}

// TestVerifyEgress_FailsOnNoEgressIP proves a missing "ip=" line (curl
// failed or the trace format changed) fails verification even when pkts_in
// increased.
func TestVerifyEgress_FailsOnNoEgressIP(t *testing.T) {
	k8sClient := fakek8s.NewClientset(tmmPodForVerify("tmm-0"))
	calls := 0
	execFn := func(_ context.Context, _, _, _ string, command []string) (string, error) {
		if command[0] == "tmctl" {
			calls++
			if calls == 1 {
				return "clientside.pkts_in 50\n", nil
			}
			return "clientside.pkts_in 90\n", nil
		}
		return "", errors.New("curl: connection timed out")
	}

	result := verifyEgress(context.Background(), k8sClient, "egress-test", execFn)
	if result.passed {
		t.Error("expected passed=false when no egress IP was parsed from the trace output")
	}
	if result.egressIP != "" {
		t.Errorf("egressIP = %q, want empty", result.egressIP)
	}
}

// TestVerifyEgress_TmctlExecErrorTolerated proves an exec error on the
// tmctl step is tolerated (bash `|| echo 0`) rather than aborting
// verification outright — it contributes a 0 pkts_in reading instead.
func TestVerifyEgress_TmctlExecErrorTolerated(t *testing.T) {
	k8sClient := fakek8s.NewClientset(tmmPodForVerify("tmm-0"))
	execFn := func(_ context.Context, _, _, _ string, command []string) (string, error) {
		if command[0] == "tmctl" {
			return "", errors.New("exec: pod not found")
		}
		return "ip=34.61.234.229\n", nil
	}

	result := verifyEgress(context.Background(), k8sClient, "egress-test", execFn)
	if result.pktsInBefore != 0 || result.pktsInAfter != 0 {
		t.Errorf("expected 0/0 pkts_in on tmctl exec error, got %d/%d", result.pktsInBefore, result.pktsInAfter)
	}
	if result.passed {
		t.Error("expected passed=false when pkts_in never moved off 0")
	}
}

func TestSumLastFields(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int64
	}{
		{"empty", "", 0},
		{"single line", "clientside.pkts_in 42\n", 42},
		{"multi line sums", "a 10\nb 32\n", 42},
		{"non-numeric last field contributes zero", "a foo\nb 5\n", 5},
		{"blank lines skipped", "\n\na 7\n\n", 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sumLastFields(tc.in); got != tc.want {
				t.Errorf("sumLastFields(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseEgressIP(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"present", "fl=123abc\nip=34.61.234.229\nport=443\n", "34.61.234.229"},
		{"absent", "fl=123abc\nport=443\n", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseEgressIP(tc.in); got != tc.want {
				t.Errorf("parseEgressIP(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRunEgressVerification_SkippedByDefault proves the env-var gate
// defaults to skip — runEgressVerification must not panic or dereference
// clients.RestConfig/K8s when GKEBNKCTL_VERIFY_EGRESS is unset, even with a
// nil *gcp.Clients (the GKEBNKCTL_SKIP_AUTH=1 dry-run shape).
func TestRunEgressVerification_SkippedByDefault(t *testing.T) {
	t.Setenv(verifyEgressEnvVar, "")
	cl := testCluster()
	runEgressVerification(context.Background(), cl, nil)
}

// TestRunEgressVerification_EnabledButNilClients_NoPanic proves that even
// with verification explicitly enabled, a nil Clients.RestConfig/K8s is a
// warn+skip, not a panic.
func TestRunEgressVerification_EnabledButNilClients_NoPanic(t *testing.T) {
	t.Setenv(verifyEgressEnvVar, "1")
	cl := testCluster()
	runEgressVerification(context.Background(), cl, &gcp.Clients{})
}

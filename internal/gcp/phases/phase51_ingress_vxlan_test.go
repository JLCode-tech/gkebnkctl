package phases

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakek8s "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhase51IngressVxlanRoutes_DryRun_NoClientsNeeded(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	if err := Phase51IngressVxlanRoutes(context.Background(), cl, st, nil, true); err != nil {
		t.Fatalf("Phase51IngressVxlanRoutes(dryRun=true): %v", err)
	}
}

func TestPhase51IngressVxlanRoutes_NilK8sClientErrors(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	if err := Phase51IngressVxlanRoutes(context.Background(), cl, st, &gcp.Clients{}, false); err == nil {
		t.Fatal("expected an error when Clients.K8s is nil")
	} else if !strings.Contains(err.Error(), "AttachK8s") {
		t.Errorf("error = %q, want a hint to call AttachK8s first", err.Error())
	}
}

// TestDiscoverIngressVxlanRoutes_NoNodesInPoolErrors proves the phase fails
// loudly (not silently, zero routes) when the agent pool itself has no
// nodes registered yet — that's a "run phase 20 first" ordering bug, not
// the expected "no egress pod scheduled here yet" case.
func TestDiscoverIngressVxlanRoutes_NoNodesInPoolErrors(t *testing.T) {
	k8sClient := fakek8s.NewClientset()
	if _, err := discoverIngressVxlanRoutes(context.Background(), k8sClient, "agent-pool"); err == nil {
		t.Fatal("expected an error when no nodes match the agent pool label")
	}
}

// TestDiscoverIngressVxlanRoutes_NoPodCIDRErrors proves a node registered
// without Spec.PodCIDR set (shouldn't happen on a real GKE cluster past
// Phase20, but defends against a nil-derived destination/prefixLen) is a
// hard error rather than a silently-skipped/zero-value route.
func TestDiscoverIngressVxlanRoutes_NoPodCIDRErrors(t *testing.T) {
	k8sClient := fakek8s.NewClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "agent-1", Labels: map[string]string{gkeNodepoolLabel: "agent-pool"}}},
	)
	if _, err := discoverIngressVxlanRoutes(context.Background(), k8sClient, "agent-pool"); err == nil {
		t.Fatal("expected an error when a node has no Spec.PodCIDR")
	}
}

// TestIngressVxlanRouteName_DeterministicPerCIDR mirrors the golden-tested
// trial values (podCIDR 10.66.4.0/24 -> route-ingress-vxlan-10-66-4-0,
// proven live on cluster bnk-hostdev 2026-07-21) and proves distinct pod
// CIDRs never collide on the derived name.
func TestIngressVxlanRouteName_DeterministicPerCIDR(t *testing.T) {
	cases := []struct {
		addr string
		want string
	}{
		{"10.66.4.0", "route-ingress-vxlan-10-66-4-0"},
		{"10.66.5.0", "route-ingress-vxlan-10-66-5-0"},
		{"10.0.0.0", "route-ingress-vxlan-10-0-0-0"},
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		got := ingressVxlanRouteName(tc.addr)
		if got != tc.want {
			t.Errorf("ingressVxlanRouteName(%q) = %q, want %q", tc.addr, got, tc.want)
		}
		if seen[got] {
			t.Errorf("name %q collided across distinct pod CIDRs", got)
		}
		seen[got] = true
	}
}

// TestHostIPFromCIDR is a table test over hostIPFromCIDR — the parser for
// the discovery script's raw "<ip>/<prefixlen>" output line, including the
// exact trial value (10.0.64.1/17, proven live on cluster bnk-hostdev
// 2026-07-21).
func TestHostIPFromCIDR(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"trial value", "10.0.64.1/17", "10.0.64.1", false},
		{"different prefix", "10.0.0.1/17", "10.0.0.1", false},
		{"no slash", "10.0.64.1", "", true},
		{"empty", "", "", true},
		{"not an IP", "not-an-ip/17", "", true},
		{"IPv6 rejected", "fe80::1/64", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := hostIPFromCIDR(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("hostIPFromCIDR(%q) = %q, want an error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("hostIPFromCIDR(%q): unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("hostIPFromCIDR(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestBuildVxlanDiscoveryPod_PrivilegedHostPIDNsenterVxlan100 mirrors
// phase25_discover_test.go's TestBuildDiscoveryPod_PrivilegedHostPIDNsenter
// shape and additionally asserts the script targets vxlan100 (not the
// iface-dump script phase25 uses).
func TestBuildVxlanDiscoveryPod_PrivilegedHostPIDNsenterVxlan100(t *testing.T) {
	pod := buildVxlanDiscoveryPod("agent-node-1")
	if pod.Spec.NodeName != "agent-node-1" {
		t.Errorf("NodeName = %q, want agent-node-1", pod.Spec.NodeName)
	}
	if !pod.Spec.HostPID {
		t.Error("expected HostPID=true")
	}
	if pod.Namespace != vxlanDiscoverNamespace {
		t.Errorf("Namespace = %q, want %q", pod.Namespace, vxlanDiscoverNamespace)
	}
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("expected exactly 1 container, got %d", len(pod.Spec.Containers))
	}
	c := pod.Spec.Containers[0]
	if c.SecurityContext == nil || c.SecurityContext.Privileged == nil || !*c.SecurityContext.Privileged {
		t.Error("expected a privileged container")
	}
	foundNsenter := false
	foundVxlan100 := false
	for _, arg := range c.Command {
		if arg == "nsenter" {
			foundNsenter = true
		}
		if strings.Contains(arg, vxlanIfaceName) {
			foundVxlan100 = true
		}
	}
	if !foundNsenter {
		t.Errorf("Command %v does not use nsenter", c.Command)
	}
	if !foundVxlan100 {
		t.Errorf("Command %v does not target %s", c.Command, vxlanIfaceName)
	}
}

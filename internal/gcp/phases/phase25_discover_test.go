package phases

import (
	"context"
	"strings"
	"testing"

	computepb "cloud.google.com/go/compute/apiv1/computepb"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakek8s "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhase25Discover_DryRun_SetsPlaceholderStateAndNoMutations(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	instances := &fakeInstances{}
	clients := &gcp.Clients{Instances: instances}

	if err := Phase25Discover(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase25Discover(dryRun=true): %v", err)
	}
	for _, k := range discoverStateKeys {
		want := "dry-run-" + strings.ToLower(k)
		if got := st.Get(k); got != want {
			t.Errorf("state[%s] = %q, want %q", k, got, want)
		}
	}
}

func TestPhase25Discover_NilK8sClientErrors(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	clients := &gcp.Clients{Instances: &fakeInstances{}}

	if err := Phase25Discover(context.Background(), cl, st, clients, false); err == nil {
		t.Fatal("expected an error when Clients.K8s is nil")
	}
}

func TestFirstNodeInPool_ReturnsFirstMatch(t *testing.T) {
	k8sClient := fakek8s.NewClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "tmm-1", Labels: map[string]string{gkeNodepoolLabel: "tmm-pool"}}},
	)
	name, err := firstNodeInPool(context.Background(), k8sClient, "tmm-pool")
	if err != nil {
		t.Fatalf("firstNodeInPool: %v", err)
	}
	if name != "tmm-1" {
		t.Errorf("name = %q, want tmm-1", name)
	}
}

func TestFirstNodeInPool_NoMatchErrors(t *testing.T) {
	k8sClient := fakek8s.NewClientset()
	if _, err := firstNodeInPool(context.Background(), k8sClient, "tmm-pool"); err == nil {
		t.Fatal("expected an error when no node matches the pool label")
	}
}

func TestNicForVPC_MatchesBySelfLinkSuffix(t *testing.T) {
	inst := &computepb.Instance{
		Name: strPtr("tmm-1"),
		NetworkInterfaces: []*computepb.NetworkInterface{
			{Network: strPtr("projects/p/global/networks/bnkhd-ext"), NetworkIP: strPtr("10.30.0.5")},
			{Network: strPtr("projects/p/global/networks/bnkhd-int"), NetworkIP: strPtr("10.50.0.5")},
		},
	}
	nic, err := nicForVPC(inst, "bnkhd-ext")
	if err != nil {
		t.Fatalf("nicForVPC: %v", err)
	}
	if nic.GetNetworkIP() != "10.30.0.5" {
		t.Errorf("NetworkIP = %q, want 10.30.0.5", nic.GetNetworkIP())
	}
}

func TestNicForVPC_NoMatchErrors(t *testing.T) {
	inst := &computepb.Instance{Name: strPtr("tmm-1")}
	if _, err := nicForVPC(inst, "bnkhd-ext"); err == nil {
		t.Fatal("expected an error when no NIC matches the vpc")
	}
}

func TestAliasCIDRForRange_MatchesByRangeName(t *testing.T) {
	nic := &computepb.NetworkInterface{
		AliasIpRanges: []*computepb.AliasIpRange{
			{SubnetworkRangeName: strPtr("tmm-ext-pods"), IpCidrRange: strPtr("10.31.0.48/28")},
		},
	}
	cidr, err := aliasCIDRForRange(nic, "tmm-ext-pods")
	if err != nil {
		t.Fatalf("aliasCIDRForRange: %v", err)
	}
	if cidr != "10.31.0.48/28" {
		t.Errorf("cidr = %q, want 10.31.0.48/28", cidr)
	}
}

func TestAliasCIDRForRange_NoMatchErrors(t *testing.T) {
	nic := &computepb.NetworkInterface{}
	if _, err := aliasCIDRForRange(nic, "tmm-ext-pods"); err == nil {
		t.Fatal("expected an error when no alias range matches")
	}
}

// TestFieldForMAC mirrors the bash discover::field_for_mac contract: match a
// dump line by its MAC= field (case-insensitive) and return the requested
// field from that same line.
func TestFieldForMAC(t *testing.T) {
	dump := "IFACE=eth0 MAC=42:01:0a:1e:00:31 PCI=0000:00:04.0\n" +
		"IFACE=eth1 MAC=42:01:0a:32:00:31 PCI=0000:00:05.0\n"

	if got := fieldForMAC(dump, "42:01:0a:1e:00:31", "IFACE"); got != "eth0" {
		t.Errorf("IFACE for ext MAC = %q, want eth0", got)
	}
	if got := fieldForMAC(dump, "42:01:0a:1e:00:31", "PCI"); got != "0000:00:04.0" {
		t.Errorf("PCI for ext MAC = %q, want 0000:00:04.0", got)
	}
	if got := fieldForMAC(dump, "42:01:0A:1E:00:31", "IFACE"); got != "eth0" {
		t.Errorf("expected case-insensitive MAC match, got %q", got)
	}
	if got := fieldForMAC(dump, "42:01:ff:ff:ff:ff", "IFACE"); got != "" {
		t.Errorf("expected no match for unknown MAC, got %q", got)
	}
}

func TestBuildDiscoveryPod_PrivilegedHostPIDNsenter(t *testing.T) {
	pod := buildDiscoveryPod("tmm-node-1")
	if pod.Spec.NodeName != "tmm-node-1" {
		t.Errorf("NodeName = %q, want tmm-node-1", pod.Spec.NodeName)
	}
	if !pod.Spec.HostPID {
		t.Error("expected HostPID=true")
	}
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("expected exactly 1 container, got %d", len(pod.Spec.Containers))
	}
	c := pod.Spec.Containers[0]
	if c.SecurityContext == nil || c.SecurityContext.Privileged == nil || !*c.SecurityContext.Privileged {
		t.Error("expected a privileged container")
	}
	found := false
	for _, arg := range c.Command {
		if arg == "nsenter" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected command to invoke nsenter, got %v", c.Command)
	}
}

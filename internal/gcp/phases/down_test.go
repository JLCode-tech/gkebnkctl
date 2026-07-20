package phases

import (
	"context"
	"testing"

	containerpb "cloud.google.com/go/container/apiv1/containerpb"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func TestPhaseDownCluster_DeletesWhenPresent(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	gke := &fakeGKE{clusters: map[string]*containerpb.Cluster{
		"bnk-hostdev": {Name: "bnk-hostdev", Status: containerpb.Cluster_RUNNING},
	}}
	clients := &gcp.Clients{GKE: gke}

	if err := PhaseDownCluster(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("PhaseDownCluster: %v", err)
	}
	if len(gke.deleteClusterCalls) != 1 || gke.deleteClusterCalls[0] != "bnk-hostdev" {
		t.Errorf("expected 1 DeleteCluster call for bnk-hostdev, got %v", gke.deleteClusterCalls)
	}
}

func TestPhaseDownCluster_ToleratesAlreadyGone(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	gke := &fakeGKE{} // no clusters registered -> GetCluster returns errNotFound
	clients := &gcp.Clients{GKE: gke}

	if err := PhaseDownCluster(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("PhaseDownCluster: expected already-gone to be tolerated, got: %v", err)
	}
	if len(gke.deleteClusterCalls) != 0 {
		t.Errorf("expected 0 DeleteCluster calls when cluster already gone, got %v", gke.deleteClusterCalls)
	}
}

// TestPhaseDownNetworks_NeverReferencesSharedVPCs is the down-asymmetry
// requirement from docs/SPEC-go-tracer.md §3/§7: PhaseDownNetworks must
// never touch (or even Get) network.external/network.internal by name.
func TestPhaseDownNetworks_NeverReferencesSharedVPCs(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	networks := &fakeNetworks{existing: map[string]bool{"bnkhd-vpc": true}}
	subnetworks := &fakeSubnetworks{existing: map[string]bool{"bnkhd-subnet": true}}
	firewalls := &fakeFirewalls{existing: map[string]bool{"bnkhd-vpc-allow-internal": true}}
	clients := &gcp.Clients{Networks: networks, Subnetworks: subnetworks, Firewalls: firewalls}

	if err := PhaseDownNetworks(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("PhaseDownNetworks: %v", err)
	}

	forbidden := []string{cl.Network.External.VPC, cl.Network.Internal.VPC, cl.Network.External.Subnet, cl.Network.Internal.Subnet}
	allCalls := append(append([]string{}, networks.deleteCalls...), subnetworks.deleteCalls...)
	for _, call := range allCalls {
		for _, f := range forbidden {
			if call == f {
				t.Errorf("PhaseDownNetworks referenced a shared dataplane resource %q — must never touch network.external/network.internal", call)
			}
		}
	}
	if len(networks.deleteCalls) != 1 || networks.deleteCalls[0] != "bnkhd-vpc" {
		t.Errorf("expected exactly 1 Networks.Delete call for the main VPC, got %v", networks.deleteCalls)
	}
	if len(subnetworks.deleteCalls) != 1 || subnetworks.deleteCalls[0] != "bnkhd-subnet" {
		t.Errorf("expected exactly 1 Subnetworks.Delete call for the main subnet, got %v", subnetworks.deleteCalls)
	}
	if len(firewalls.deleteCalls) != 1 || firewalls.deleteCalls[0] != "bnkhd-vpc-allow-internal" {
		t.Errorf("expected exactly 1 Firewalls.Delete call for the main allow-internal rule, got %v", firewalls.deleteCalls)
	}
}

func TestPhaseDownNetworks_ToleratesAlreadyGone(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	clients := &gcp.Clients{
		Networks:    &fakeNetworks{existing: map[string]bool{}},
		Subnetworks: &fakeSubnetworks{existing: map[string]bool{}},
		Firewalls:   &fakeFirewalls{existing: map[string]bool{}},
	}
	if err := PhaseDownNetworks(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("PhaseDownNetworks: expected already-gone to be tolerated, got: %v", err)
	}
}

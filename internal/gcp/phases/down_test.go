package phases

import (
	"context"
	"testing"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	containerpb "cloud.google.com/go/container/apiv1/containerpb"
	gax "github.com/googleapis/gax-go/v2"

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
	networks := &fakeNetworks{existing: map[string]string{"bnkhd-vpc": ""}}
	subnetworks := &fakeSubnetworks{existing: map[string]string{"bnkhd-subnet": ""}}
	firewalls := &fakeFirewalls{existing: map[string]string{"bnkhd-vpc-allow-internal": ""}}
	routers := &fakeRouters{existing: map[string]*computepb.Router{}}
	clients := &gcp.Clients{Networks: networks, Subnetworks: subnetworks, Firewalls: firewalls, Routers: routers}

	if err := PhaseDownNetworks(context.Background(), cl, st, clients, false); err != nil {
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
		Networks:    &fakeNetworks{existing: map[string]string{}},
		Subnetworks: &fakeSubnetworks{existing: map[string]string{}},
		Firewalls:   &fakeFirewalls{existing: map[string]string{}},
		Routers:     &fakeRouters{existing: map[string]*computepb.Router{}},
	}
	if err := PhaseDownNetworks(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("PhaseDownNetworks: expected already-gone to be tolerated, got: %v", err)
	}
}

type mockRouterAPI struct {
	gcp.RoutersAPI
	routers []*computepb.Router
	deleted []string
}

func (m *mockRouterAPI) ListAll(_ context.Context, _, _ string) ([]*computepb.Router, error) {
	return m.routers, nil
}
func (m *mockRouterAPI) Delete(_ context.Context, req *computepb.DeleteRouterRequest, _ ...gax.CallOption) (*compute.Operation, error) {
	m.deleted = append(m.deleted, req.GetRouter())
	return nil, nil
}

type mockSubnetAPI struct {
	gcp.SubnetworksAPI
	subnets []*computepb.Subnetwork
	deleted []string
}

func (m *mockSubnetAPI) ListAll(_ context.Context, _, _ string) ([]*computepb.Subnetwork, error) {
	return m.subnets, nil
}
func (m *mockSubnetAPI) Delete(_ context.Context, req *computepb.DeleteSubnetworkRequest, _ ...gax.CallOption) (*compute.Operation, error) {
	m.deleted = append(m.deleted, req.GetSubnetwork())
	return nil, nil
}

type mockFirewallAPI struct {
	gcp.FirewallsAPI
	firewalls []*computepb.Firewall
	deleted   []string
}

func (m *mockFirewallAPI) ListAll(_ context.Context, _ string) ([]*computepb.Firewall, error) {
	return m.firewalls, nil
}
func (m *mockFirewallAPI) Delete(_ context.Context, req *computepb.DeleteFirewallRequest, _ ...gax.CallOption) (*compute.Operation, error) {
	m.deleted = append(m.deleted, req.GetFirewall())
	return nil, nil
}

func TestPhaseDownNetworks_TeardownDataplaneResources(t *testing.T) {
	cl := testCluster()
	vpcSelfLink := networkSelfLink(cl.Metadata.Project, cl.Network.External.VPC)

	// Subnets: one owned by us, one by another cluster, one unmarked
	subnets := &mockSubnetAPI{
		subnets: []*computepb.Subnetwork{
			{Name: strPtr("sn-us"), Network: strPtr(vpcSelfLink), Description: strPtr("gkebnkctl-cluster=bnk-hostdev gkebnkctl-managed=true")},
			{Name: strPtr("sn-them"), Network: strPtr(vpcSelfLink), Description: strPtr("gkebnkctl-cluster=other-cluster gkebnkctl-managed=true")},
			{Name: strPtr("sn-unmarked"), Network: strPtr(vpcSelfLink)},
		},
	}
	routers := &mockRouterAPI{
		routers: []*computepb.Router{
			{Name: strPtr("rt-us"), Network: strPtr(vpcSelfLink), Description: strPtr("gkebnkctl-cluster=bnk-hostdev gkebnkctl-managed=true")},
			{Name: strPtr("rt-them"), Network: strPtr(vpcSelfLink), Description: strPtr("gkebnkctl-cluster=other-cluster gkebnkctl-managed=true")},
			{Name: strPtr("rt-unmarked"), Network: strPtr(vpcSelfLink)},
		},
	}
	firewalls := &mockFirewallAPI{
		firewalls: []*computepb.Firewall{
			{Name: strPtr("fw-us"), Network: strPtr(vpcSelfLink), Description: strPtr("gkebnkctl-cluster=bnk-hostdev gkebnkctl-managed=true")},
			{Name: strPtr("fw-them"), Network: strPtr(vpcSelfLink), Description: strPtr("gkebnkctl-cluster=other-cluster gkebnkctl-managed=true")},
			{Name: strPtr("fw-unmarked"), Network: strPtr(vpcSelfLink)},
		},
	}

	clients := &gcp.Clients{
		Networks:    &fakeNetworks{},
		Subnetworks: subnets,
		Firewalls:   firewalls,
		Routers:     routers,
	}

	// Because another cluster has a subnet, router should NOT be deleted, but our subnet SHOULD be deleted.
	err := teardownDataplaneResources(context.Background(), cl.Metadata.Project, cl.Region, cl.Network.External.VPC, cl.Metadata.Name, clients)
	if err != nil {
		t.Fatalf("teardownDataplaneResources: %v", err)
	}
	if len(subnets.deleted) != 1 || subnets.deleted[0] != "sn-us" {
		t.Errorf("expected only sn-us to be deleted, got %v", subnets.deleted)
	}
	if len(routers.deleted) != 0 {
		t.Errorf("expected router to be kept because another cluster exists, got %v deleted", routers.deleted)
	}
	if len(firewalls.deleted) != 0 {
		t.Errorf("expected firewall to be kept because another cluster exists, got %v deleted", firewalls.deleted)
	}

	// Now remove the other cluster's subnet, simulating that we are the last tenant out.
	// But leave the other cluster's marked routers and firewalls to prove that we sweep them.
	subnets.subnets = []*computepb.Subnetwork{
		{Name: strPtr("sn-us"), Network: strPtr(vpcSelfLink), Description: strPtr("gkebnkctl-cluster=bnk-hostdev gkebnkctl-managed=true")},
		{Name: strPtr("sn-unmarked"), Network: strPtr(vpcSelfLink)},
	}
	subnets.deleted = nil
	routers.deleted = nil
	firewalls.deleted = nil

	err = teardownDataplaneResources(context.Background(), cl.Metadata.Project, cl.Region, cl.Network.External.VPC, cl.Metadata.Name, clients)
	if err != nil {
		t.Fatalf("teardownDataplaneResources: %v", err)
	}
	if len(subnets.deleted) != 1 || subnets.deleted[0] != "sn-us" {
		t.Errorf("expected only sn-us to be deleted, got %v", subnets.deleted)
	}

	// We are the last tenant out. Both our marked resources and the other cluster's marked resources
	// should be deleted. Unmarked resources should be spared.
	if len(routers.deleted) != 2 {
		t.Errorf("expected both marked routers to be deleted because we are the last cluster, got %v", routers.deleted)
	} else {
		for _, name := range routers.deleted {
			if name != "rt-us" && name != "rt-them" {
				t.Errorf("unexpected router deleted: %s", name)
			}
		}
	}

	if len(firewalls.deleted) != 2 {
		t.Errorf("expected both marked firewalls to be deleted because we are the last cluster, got %v", firewalls.deleted)
	} else {
		for _, name := range firewalls.deleted {
			if name != "fw-us" && name != "fw-them" {
				t.Errorf("unexpected firewall deleted: %s", name)
			}
		}
	}
}

package phases

import (
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"context"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/labels"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"testing"
)

func TestParseOwner(t *testing.T) {
	marker := labels.OwnerMarker("test-cluster")
	cl, managed := labels.ParseOwner(marker)
	if !managed || cl != "test-cluster" {
		t.Errorf("ParseOwner failed: got %s, %v", cl, managed)
	}

	cl, managed = labels.ParseOwner("gkebnkctl-managed=true gkebnkctl-cluster=other")
	if !managed || cl != "other" {
		t.Errorf("ParseOwner failed: got %s, %v", cl, managed)
	}

	cl, managed = labels.ParseOwner("some other description")
	if managed || cl != "" {
		t.Errorf("ParseOwner failed on unmarked: got %s, %v", cl, managed)
	}
}

func TestPhase10Networks_MarkerAndAdopt(t *testing.T) {
	cl := &intent.Cluster{}
	cl.Metadata.Name = "c1"
	cl.Metadata.Project = "p1"
	cl.Region = "us-east1"
	cl.Network.Main.VPC = "main-vpc"
	cl.Network.External.VPC = "ext-vpc"
	cl.Network.Internal.VPC = "int-vpc"

	netFake := &fakeNetworks{
		existing: map[string]string{
			"main-vpc": "", // unlabelled, should adopt
		},
	}
	subFake := &fakeSubnetworks{existing: map[string]string{}}
	fwFake := &fakeFirewalls{existing: map[string]string{}}
	routerFake := &fakeRouters{existing: map[string]*computepb.Router{}}

	clients := &gcp.Clients{
		Networks:    netFake,
		Subnetworks: subFake,
		Firewalls:   fwFake,
		Routers:     routerFake,
	}

	err := Phase10Networks(context.Background(), cl, nil, clients, false)
	if err != nil {
		t.Fatalf("Phase10Networks failed: %v", err)
	}

	if len(netFake.patchCalls) == 0 || netFake.patchCalls[0] != "main-vpc" {
		t.Errorf("expected adopt patch on main-vpc, got %v", netFake.patchCalls)
	}
	if len(netFake.insertCalls) == 0 {
		t.Errorf("expected inserts for ext and int")
	}
}

func TestDiscoverDataplaneResources(t *testing.T) {
	subFake := &fakeSubnetworks{
		existing: map[string]string{
			"sub1": labels.OwnerMarker("c1"),
			"sub2": labels.OwnerMarker("c2"),
			"sub3": "unrelated",
		},
	}
	fwFake := &fakeFirewalls{
		existing: map[string]string{
			"fw1": labels.OwnerMarker("c1"),
		},
	}
	routerFake := &fakeRouters{
		existing: map[string]*computepb.Router{
			"r1": {Description: strPtr(labels.OwnerMarker("c2")), Network: strPtr("projects/p1/global/networks/vpc1")},
		},
	}

	clients := &gcp.Clients{
		Subnetworks: subFake,
		Firewalls:   fwFake,
		Routers:     routerFake,
	}

	byCluster, err := DiscoverDataplaneResources(context.Background(), clients, "p1", "us-east1", "vpc1")
	if err != nil {
		t.Fatalf("DiscoverDataplaneResources failed: %v", err)
	}

	if len(byCluster["c1"].Subnets) != 1 || len(byCluster["c1"].Firewalls) != 1 {
		t.Errorf("expected 1 subnet and 1 fw for c1, got %v", byCluster["c1"])
	}
	if len(byCluster["c2"].Subnets) != 1 || len(byCluster["c2"].Routers) != 1 {
		t.Errorf("expected 1 subnet and 1 router for c2")
	}
	if len(byCluster["unmarked"].Subnets) != 1 {
		t.Errorf("expected 1 unmarked subnet")
	}
}

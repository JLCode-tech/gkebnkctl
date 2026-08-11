package phases

import (
	"context"
	"errors"
	"strings"
	"testing"

	computepb "cloud.google.com/go/compute/apiv1/computepb"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp"
)

func testClients10() (*gcp.Clients, *fakeNetworks, *fakeSubnetworks, *fakeFirewalls, *fakeRouters) {
	networks := &fakeNetworks{existing: map[string]string{}}
	subnetworks := &fakeSubnetworks{existing: map[string]string{}}
	firewalls := &fakeFirewalls{existing: map[string]string{}}
	routers := &fakeRouters{existing: map[string]*computepb.Router{}}
	return &gcp.Clients{
		Networks:    networks,
		Subnetworks: subnetworks,
		Firewalls:   firewalls,
		Routers:     routers,
	}, networks, subnetworks, firewalls, routers
}

func TestPhase10Networks_CreatesWhenAbsent(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	clients, networks, subnetworks, firewalls, routers := testClients10()

	if err := Phase10Networks(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase10Networks: %v", err)
	}

	for _, want := range []string{"bnkhd-vpc", "bnkhd-ext", "bnkhd-int"} {
		found := false
		for _, got := range networks.insertCalls {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected VPC %s to be created; insertCalls=%v", want, networks.insertCalls)
		}
	}
	for _, want := range []string{"bnkhd-subnet", "bnkhd-ext", "bnkhd-int"} {
		found := false
		for _, got := range subnetworks.insertCalls {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected subnet %s to be created; insertCalls=%v", want, subnetworks.insertCalls)
		}
	}
	for _, want := range []string{"bnkhd-vpc-allow-internal", "bnkhd-ext-allow-internal", "bnkhd-int-allow-internal"} {
		found := false
		for _, got := range firewalls.insertCalls {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected firewall %s to be created; insertCalls=%v", want, firewalls.insertCalls)
		}
	}
	for _, want := range []string{"bnkhd-ext-router", "bnkhd-int-router"} {
		found := false
		for _, got := range routers.insertCalls {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected router %s to be created; insertCalls=%v", want, routers.insertCalls)
		}
	}
}

// TestPhase10Networks_SkipsWhenPresent proves Get-returns-existing skips
// Insert entirely for every resource type — the idempotency requirement
// from docs/SPEC-go-tracer.md §7.
func TestPhase10Networks_SkipsWhenPresent(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	clients, networks, subnetworks, firewalls, routers := testClients10()

	networks.existing["bnkhd-vpc"] = ""
	networks.existing["bnkhd-ext"] = ""
	networks.existing["bnkhd-int"] = ""
	subnetworks.existing["bnkhd-subnet"] = ""
	subnetworks.existing["bnkhd-ext"] = ""
	subnetworks.existing["bnkhd-int"] = ""
	firewalls.existing["bnkhd-vpc-allow-internal"] = ""
	firewalls.existing["bnkhd-ext-allow-internal"] = ""
	firewalls.existing["bnkhd-int-allow-internal"] = ""
	natName := "bnkhd-ext-nat"
	routers.existing["bnkhd-ext-router"] = &computepb.Router{Description: strPtr("gkebnkctl-managed=true gkebnkctl-cluster=test-cluster"), Nats: []*computepb.RouterNat{{Name: &natName}}}
	natNameInt := "bnkhd-int-nat"
	routers.existing["bnkhd-int-router"] = &computepb.Router{Description: strPtr("gkebnkctl-managed=true gkebnkctl-cluster=test-cluster"), Nats: []*computepb.RouterNat{{Name: &natNameInt}}}

	if err := Phase10Networks(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase10Networks: %v", err)
	}

	if len(networks.insertCalls) != 0 {
		t.Errorf("expected 0 Insert calls on Networks when all exist, got %v", networks.insertCalls)
	}
	if len(subnetworks.insertCalls) != 0 {
		t.Errorf("expected 0 Insert calls on Subnetworks when all exist, got %v", subnetworks.insertCalls)
	}
	if len(firewalls.insertCalls) != 0 {
		t.Errorf("expected 0 Insert calls on Firewalls when all exist, got %v", firewalls.insertCalls)
	}
	if len(routers.insertCalls) != 0 {
		t.Errorf("expected 0 Insert calls on Routers when both routers+NATs exist, got %v", routers.insertCalls)
	}
	if len(routers.patchCalls) != 0 {
		t.Errorf("expected 0 Patch calls on Routers when NATs already present, got %v", routers.patchCalls)
	}
}

// TestPhase10Networks_DryRun_MakesZeroMutations proves --dry-run never
// calls Insert/Get on any client.
func TestPhase10Networks_DryRun_MakesZeroMutations(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	clients, networks, subnetworks, firewalls, routers := testClients10()

	if err := Phase10Networks(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase10Networks(dryRun=true): %v", err)
	}
	if len(networks.insertCalls) != 0 || len(subnetworks.insertCalls) != 0 ||
		len(firewalls.insertCalls) != 0 || len(routers.insertCalls) != 0 {
		t.Error("dry-run must make zero GCP mutations")
	}
}

// TestEnsureRouterNAT_403Tolerated proves a 403 on router Insert (compute.
// routers.create denied) is swallowed with a warning, not returned as an
// error — matching bash net::create_nat (docs/SPEC-go-tracer.md §9.5). No
// NAT-attach Patch is attempted since the router was never created.
func TestEnsureRouterNAT_403Tolerated(t *testing.T) {
	routers := &fakeRouters{
		existing:  map[string]*computepb.Router{},
		insertErr: map[string]error{"bnkhd-ext-router": errPermissionDenied},
	}
	err := ensureRouterNAT(context.Background(), routers, "my-gcp-project", "us-central1", "bnkhd-ext", "test-cluster")
	if err != nil {
		t.Fatalf("ensureRouterNAT: expected 403 to be tolerated (nil error), got: %v", err)
	}
	if len(routers.insertCalls) != 1 {
		t.Errorf("expected exactly 1 Insert attempt, got %d", len(routers.insertCalls))
	}
	if len(routers.patchCalls) != 0 {
		t.Errorf("expected 0 NAT-attach Patch calls when router create was denied, got %d", len(routers.patchCalls))
	}
}

// TestEnsureRouterNAT_OtherErrorsPropagate proves a non-403 error from the
// router Insert call is NOT swallowed (router creation itself is fail-fast,
// unlike the NAT-attach step — see TestEnsureRouterNAT_NATAttachErrorNonFatal).
func TestEnsureRouterNAT_OtherErrorsPropagate(t *testing.T) {
	routers := &fakeRouters{
		existing:  map[string]*computepb.Router{},
		insertErr: map[string]error{"bnkhd-ext-router": errNotFound}, // stand-in for an unrelated 500-class error
	}
	if err := ensureRouterNAT(context.Background(), routers, "my-gcp-project", "us-central1", "bnkhd-ext", "test-cluster"); err == nil {
		t.Fatal("expected a non-403 Insert error to propagate")
	}
}

// TestEnsureRouterNAT_FreshCreateSplitsRouterAndNAT proves the fresh-create
// path issues a bare router Insert (no embedded Nats) followed by a separate
// NAT-attach Patch — matching bash's two-step net::create_nat rather than the
// old single combined Insert.
func TestEnsureRouterNAT_FreshCreateSplitsRouterAndNAT(t *testing.T) {
	routers := &fakeRouters{existing: map[string]*computepb.Router{}}
	if err := ensureRouterNAT(context.Background(), routers, "p", "us-central1", "bnkhd-ext", "test-cluster"); err != nil {
		t.Fatalf("ensureRouterNAT: %v", err)
	}
	if len(routers.insertCalls) != 1 || routers.insertCalls[0] != "bnkhd-ext-router" {
		t.Errorf("expected exactly 1 router Insert call, got %v", routers.insertCalls)
	}
	if len(routers.patchCalls) != 1 || routers.patchCalls[0] != "bnkhd-ext-router" {
		t.Errorf("expected exactly 1 NAT-attach Patch call following router create, got %v", routers.patchCalls)
	}
}

func TestEnsureRouterNAT_AddsNATToExistingRouter(t *testing.T) {
	routers := &fakeRouters{existing: map[string]*computepb.Router{
		"bnkhd-ext-router": {Description: strPtr("gkebnkctl-managed=true gkebnkctl-cluster=test-cluster"), Nats: nil},
	}}
	if err := ensureRouterNAT(context.Background(), routers, "p", "us-central1", "bnkhd-ext", "test-cluster"); err != nil {
		t.Fatalf("ensureRouterNAT: %v", err)
	}
	if len(routers.patchCalls) != 1 {
		t.Errorf("expected 1 Patch call to add NAT to existing router, got %d", len(routers.patchCalls))
	}
	if len(routers.insertCalls) != 0 {
		t.Errorf("expected 0 Insert calls when router already exists, got %d", len(routers.insertCalls))
	}
}

// TestEnsureRouterNAT_NATAttachErrorNonFatal proves ANY error from the
// NAT-attach Patch call (not just a 403) is swallowed with a warning —
// matching bash's `|| { log::warn ...; }` on the NAT create call
// (reference/bash-seed/bin/10-networks.sh:140-142), which is unconditional
// unlike the 403-only tolerance on router create.
func TestEnsureRouterNAT_NATAttachErrorNonFatal(t *testing.T) {
	routers := &fakeRouters{
		existing: map[string]*computepb.Router{"bnkhd-ext-router": {Description: strPtr("gkebnkctl-managed=true gkebnkctl-cluster=test-cluster"), Nats: nil}},
		patchErr: map[string]error{"bnkhd-ext-router": errors.New("transient NAT attach failure")},
	}
	if err := ensureRouterNAT(context.Background(), routers, "p", "us-central1", "bnkhd-ext", "test-cluster"); err != nil {
		t.Fatalf("ensureRouterNAT: expected NAT-attach failure to be non-fatal, got: %v", err)
	}
	if len(routers.patchCalls) != 1 {
		t.Errorf("expected exactly 1 Patch attempt, got %d", len(routers.patchCalls))
	}
}

func TestHasHTTPStatus_GoogleAPIErrorSubstring(t *testing.T) {
	if !strings.Contains(errPermissionDenied.Error(), "403") {
		t.Fatal("test fixture errPermissionDenied must contain \"403\"")
	}
}

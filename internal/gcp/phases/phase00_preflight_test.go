package phases

import (
	"context"
	"testing"

	computepb "cloud.google.com/go/compute/apiv1/computepb"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

func testCluster() *intent.Cluster {
	cl := &intent.Cluster{
		Metadata: intent.Metadata{Name: "bnk-hostdev", Project: "my-gcp-project", Zone: "us-central1-a"},
		Pattern:  intent.PatternHostDevice,
		Network: intent.Network{
			Main: intent.MainNetworkSpec{
				VPC: "bnkhd-vpc", Subnet: "bnkhd-subnet", CIDR: "10.60.0.0/24",
				PodsRangeName: "bnkhd-pods", PodsCIDR: "10.66.0.0/16",
				SvcsRangeName: "bnkhd-svcs", SvcsCIDR: "10.67.0.0/20",
			},
			External: intent.DataplaneSpec{
				VPC: "bnkhd-ext", Subnet: "bnkhd-ext", CIDR: "10.30.0.0/24", MTU: 8896,
				PodsRangeName: "tmm-ext-pods", PodsCIDR: "10.31.0.0/24", UnderlayCIDR: "10.30.0.0/16",
			},
			Internal: intent.DataplaneSpec{
				VPC: "bnkhd-int", Subnet: "bnkhd-int", CIDR: "10.50.0.0/24", MTU: 8896,
				PodsRangeName: "tmm-int-pods", PodsCIDR: "10.51.0.0/24",
			},
			AllowInternalCidr: "10.0.0.0/8",
		},
		ClusterSpec: intent.ClusterSpec{
			KubernetesVersion: "1.31",
			DefaultPool:       intent.NodePoolSpec{MachineType: "e2-standard-4", NodeCount: 3},
			TmmPool:           intent.NodePoolSpec{Name: "tmm-pool", MachineType: "e2-standard-8", NodeCount: 1, MaxPodsPerNode: 8},
			AgentPool:         intent.NodePoolSpec{Name: "agent-pool", MachineType: "e2-standard-4", NodeCount: 1, MaxPodsPerNode: 8},
		},
		Region: "us-central1",
	}
	return cl
}

func testState(t *testing.T) *state.State {
	t.Helper()
	st, err := state.Load(t.TempDir())
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	return st
}

func TestPhase00Preflight_NilClients_SkipsChecks(t *testing.T) {
	cl := testCluster()
	st := testState(t)
	if err := Phase00Preflight(context.Background(), cl, st, nil, false); err != nil {
		t.Fatalf("Phase00Preflight(clients=nil): unexpected error: %v", err)
	}
}

// Note: Phase00Preflight's non-nil-clients path also calls
// google.FindDefaultCredentials, which requires live Application Default
// Credentials — not available in a sandboxed test environment (see report).
// checkAPIEnabled / warnRegionQuota are tested directly below since they're
// the parts of Phase00 that don't depend on ADC.

func TestCheckAPIEnabled_Disabled(t *testing.T) {
	su := &fakeServiceUsage{states: map[string]string{
		"projects/my-gcp-project/services/compute.googleapis.com": "DISABLED",
	}}
	enabled, err := checkAPIEnabled(context.Background(), su, "my-gcp-project", "compute.googleapis.com")
	if err != nil {
		t.Fatalf("checkAPIEnabled: unexpected error: %v", err)
	}
	if enabled {
		t.Error("expected compute.googleapis.com to be reported as not enabled")
	}
}

func TestCheckAPIEnabled_Enabled(t *testing.T) {
	su := &fakeServiceUsage{states: map[string]string{
		"projects/p/services/compute.googleapis.com": "ENABLED",
	}}
	enabled, err := checkAPIEnabled(context.Background(), su, "p", "compute.googleapis.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !enabled {
		t.Error("expected enabled=true")
	}
}

func TestWarnRegionQuota_NilRegionsNoPanic(t *testing.T) {
	warnRegionQuota(context.Background(), nil, "p", "us-central1")
}

func TestWarnRegionQuota_LowHeadroomDoesNotError(t *testing.T) {
	limit := 16.0
	usage := 15.0
	metric := "CPUS"
	regions := &fakeRegions{region: &computepb.Region{
		Quotas: []*computepb.Quota{{Metric: &metric, Limit: &limit, Usage: &usage}},
	}}
	// warnRegionQuota has no return value — this proves it doesn't panic on
	// a low-headroom quota (the whole point is it's a warning, never fatal).
	warnRegionQuota(context.Background(), regions, "p", "us-central1")
}

func TestWarnRegionQuota_GetErrorDoesNotPanic(t *testing.T) {
	regions := &fakeRegions{err: errNotFound}
	warnRegionQuota(context.Background(), regions, "p", "us-central1")
}

func TestHasHTTPStatus_StringFallback(t *testing.T) {
	if !isNotFound(errNotFound) {
		t.Error("expected errNotFound to match isNotFound via string fallback")
	}
	if !isPermissionDenied(errPermissionDenied) {
		t.Error("expected errPermissionDenied to match isPermissionDenied via string fallback")
	}
	if isNotFound(nil) || isPermissionDenied(nil) {
		t.Error("nil error must never match a status")
	}
}

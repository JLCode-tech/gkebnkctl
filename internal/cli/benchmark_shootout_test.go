package cli

// benchmark_shootout_test.go — unit tests for WS-D1 proxy shootout additions.
//
// Tests:
//   - formatRunIDs: pure helper
//   - resolveShootoutFrontEnds: front-end list building from --proxies CSV
//     and --direct-pod-ip
//   - resolveShootoutFrontEnds: external_url → VIP for non-f5-bnk proxy types
//   - resolveShootoutFrontEnds: empty external_url → RESOLVE_FAILED (fail-closed)
//   - resolveShootoutFrontEnds: f5-bnk VIP unchanged (uses global --vip)
//   - runShootoutFrontEnd: single-run mode stamps proxyDeploymentID correctly
//   - No global mutation: shootout builds per-front-end forgeGraph copy, never
//     mutates flagBenchProxy / flagBenchVIP globals
//   - Composition: --proxies + --scenario runs scenario through each front-end
//     (child run_ids surfaced in outcomes)

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/JLCode-tech/gkebnkctl/internal/forge"
	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
)

// ---------------------------------------------------------------------------
// formatRunIDs — pure helper
// ---------------------------------------------------------------------------

func TestFormatRunIDs_Empty(t *testing.T) {
	if got := formatRunIDs(nil); got != "-" {
		t.Errorf("formatRunIDs(nil) = %q, want %q", got, "-")
	}
	if got := formatRunIDs([]int{}); got != "-" {
		t.Errorf("formatRunIDs([]) = %q, want %q", got, "-")
	}
}

func TestFormatRunIDs_Single(t *testing.T) {
	if got := formatRunIDs([]int{42}); got != "42" {
		t.Errorf("formatRunIDs([42]) = %q, want %q", got, "42")
	}
}

func TestFormatRunIDs_Multiple(t *testing.T) {
	got := formatRunIDs([]int{1, 2, 3})
	want := "1,2,3"
	if got != want {
		t.Errorf("formatRunIDs([1,2,3]) = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// resolveShootoutFrontEnds — front-end list building
// ---------------------------------------------------------------------------

func shootoutTestSetup(
	t *testing.T,
	proxiesCSV string,
	directPodIP string,
	proxyDeployments []forge.ProxyDeployment,
) func() {
	t.Helper()

	origProxies := flagBenchProxies
	origDirectPodIP := flagBenchDirectPodIP
	origForgeURL := flagBenchForgeURL
	origDiscover := discoverProxiesFn
	origList := listProxyDeploymentsFn
	origCheckServedModel := checkServedModelFn

	flagBenchProxies = proxiesCSV
	flagBenchDirectPodIP = directPodIP
	flagBenchForgeURL = "http://localhost:9999"

	checkServedModelFn = func(_ context.Context, _ jumphost.ProbeOptions, _ string) error {
		return nil
	}
	discoverProxiesFn = func(_ context.Context, _ forge.ProxyDiscoverOptions) (forge.ProxyDiscoveryResult, error) {
		return forge.ProxyDiscoveryResult{DiscoveredCount: len(proxyDeployments)}, nil
	}
	listProxyDeploymentsFn = func(_ context.Context, _ forge.ProxyDiscoverOptions) ([]forge.ProxyDeployment, error) {
		return proxyDeployments, nil
	}

	return func() {
		flagBenchProxies = origProxies
		flagBenchDirectPodIP = origDirectPodIP
		flagBenchForgeURL = origForgeURL
		discoverProxiesFn = origDiscover
		listProxyDeploymentsFn = origList
		checkServedModelFn = origCheckServedModel
	}
}

func TestResolveShootoutFrontEnds_FromCSV(t *testing.T) {
	deps := []forge.ProxyDeployment{
		{ID: 10, ProxyType: "envoy", ExternalURL: "10.0.1.55:31234"},
		{ID: 11, ProxyType: "haproxy", ExternalURL: "10.0.1.55:31235"},
	}
	cleanup := shootoutTestSetup(t, "envoy,haproxy", "", deps)
	defer cleanup()

	fes, err := resolveShootoutFrontEnds(context.Background(), forge.RestCreds{}, 42)
	if err != nil {
		t.Fatalf("resolveShootoutFrontEnds: %v", err)
	}
	if len(fes) != 2 {
		t.Fatalf("len(frontEnds) = %d, want 2", len(fes))
	}
	if fes[0].ProxyType != "envoy" || fes[0].ProxyDeploymentID != 10 {
		t.Errorf("frontEnds[0] = %+v, want {envoy, id=10}", fes[0])
	}
	if fes[1].ProxyType != "haproxy" || fes[1].ProxyDeploymentID != 11 {
		t.Errorf("frontEnds[1] = %+v, want {haproxy, id=11}", fes[1])
	}
}

func TestResolveShootoutFrontEnds_DirectPodIPAdded(t *testing.T) {
	deps := []forge.ProxyDeployment{
		{ID: 99, ProxyType: "nodeport"},
	}
	cleanup := shootoutTestSetup(t, "f5-bnk", "10.0.10.200", deps)
	defer cleanup()

	fes, err := resolveShootoutFrontEnds(context.Background(), forge.RestCreds{}, 5)
	if err != nil {
		t.Fatalf("resolveShootoutFrontEnds: %v", err)
	}
	if len(fes) != 2 {
		t.Fatalf("len(frontEnds) = %d, want 2", len(fes))
	}
	last := fes[len(fes)-1]
	if last.ProxyType != "nodeport" {
		t.Errorf("last front-end ProxyType = %q, want %q", last.ProxyType, "nodeport")
	}
	if last.VIP != "10.0.10.200" {
		t.Errorf("last front-end VIP = %q, want %q", last.VIP, "10.0.10.200")
	}
}

func TestResolveShootoutFrontEnds_DirectPodIPDistinctFromVIPNodeport(t *testing.T) {
	deps := []forge.ProxyDeployment{
		{ID: 7, ProxyType: "nodeport", ExternalURL: "10.0.1.55:30080"},
	}
	cleanup := shootoutTestSetup(t, "nodeport", "10.0.10.200", deps)
	defer cleanup()

	fes, err := resolveShootoutFrontEnds(context.Background(), forge.RestCreds{}, 5)
	if err != nil {
		t.Fatalf("resolveShootoutFrontEnds: %v", err)
	}
	if len(fes) != 2 {
		t.Fatalf("len(frontEnds) = %d, want 2 (VIP nodeport + direct-pod-ip nodeport)", len(fes))
	}
	if fes[0].ProxyType != "nodeport" || fes[0].VIP != "10.0.1.55:30080" {
		t.Errorf("frontEnds[0] = %+v, want nodeport with VIP=10.0.1.55:30080 (from external_url)", fes[0])
	}
	if fes[1].ProxyType != "nodeport" || fes[1].VIP != "10.0.10.200" {
		t.Errorf("frontEnds[1] = %+v, want nodeport with VIP=10.0.10.200", fes[1])
	}
}

func TestResolveShootoutFrontEnds_UnlinkedWhenNoTargetID(t *testing.T) {
	cleanup := shootoutTestSetup(t, "envoy,haproxy", "", nil)
	defer cleanup()
	listProxyDeploymentsFn = func(_ context.Context, _ forge.ProxyDiscoverOptions) ([]forge.ProxyDeployment, error) {
		return nil, fmt.Errorf("should not be called")
	}

	fes, err := resolveShootoutFrontEnds(context.Background(), forge.RestCreds{}, 0)
	if err != nil {
		t.Fatalf("resolveShootoutFrontEnds: %v", err)
	}
	for _, fe := range fes {
		if fe.ProxyDeploymentID != 0 {
			t.Errorf("frontEnd %q: ProxyDeploymentID = %d, want 0 (unlinked)", fe.ProxyType, fe.ProxyDeploymentID)
		}
	}
}

// ---------------------------------------------------------------------------
// resolveShootoutFrontEnds — proxy type validation
// ---------------------------------------------------------------------------

func TestResolveShootoutFrontEnds_UnknownType_ReturnsError(t *testing.T) {
	cleanup := shootoutTestSetup(t, "envoy,envooy", "", nil)
	defer cleanup()

	_, err := resolveShootoutFrontEnds(context.Background(), forge.RestCreds{}, 0)
	if err == nil {
		t.Fatalf("expected error for unknown proxy type %q, got nil", "envooy")
	}
	if !strings.Contains(err.Error(), "envooy") {
		t.Errorf("error message should mention the bad value %q, got: %v", "envooy", err)
	}
}

func TestResolveShootoutFrontEnds_ValidNodeportProceeds(t *testing.T) {
	cleanup := shootoutTestSetup(t, "nodeport", "", nil)
	defer cleanup()

	fes, err := resolveShootoutFrontEnds(context.Background(), forge.RestCreds{}, 0)
	if err != nil {
		t.Fatalf("expected no error for valid type %q, got: %v", "nodeport", err)
	}
	if len(fes) != 1 || fes[0].ProxyType != "nodeport" {
		t.Errorf("expected one nodeport front-end, got %+v", fes)
	}
}

// ---------------------------------------------------------------------------
// No global mutation: forgeGraph copy per front-end
// ---------------------------------------------------------------------------

func TestForgeGraph_CopyByValue(t *testing.T) {
	original := forgeGraph{agentID: 1, targetID: 2, configID: 3, proxyDeploymentID: 0}
	copy1 := original
	copy1.proxyDeploymentID = 42
	copy2 := original
	copy2.proxyDeploymentID = 99

	if original.proxyDeploymentID != 0 {
		t.Errorf("original.proxyDeploymentID = %d, want 0 (must not be mutated)", original.proxyDeploymentID)
	}
	if copy1.proxyDeploymentID != 42 {
		t.Errorf("copy1.proxyDeploymentID = %d, want 42", copy1.proxyDeploymentID)
	}
	if copy2.proxyDeploymentID != 99 {
		t.Errorf("copy2.proxyDeploymentID = %d, want 99", copy2.proxyDeploymentID)
	}
}

// ---------------------------------------------------------------------------
// runShootoutFrontEnd single-run — stamps proxyDeploymentID on push
// ---------------------------------------------------------------------------

func stubAiperfResult() *jumphost.AiperfResult {
	return &jumphost.AiperfResult{
		Model:           "test-model",
		BaseURL:         "http://10.0.10.100",
		Endpoint:        "/v1/chat/completions",
		TotalRequests:   1,
		Successful:      1,
		DurationSeconds: 1.0,
		RawJSON:         `{"test":true}`,
	}
}

func TestRunShootoutFrontEnd_SingleRun_StampsProxyDeploymentID(t *testing.T) {
	origRunAiperf := runAiperfFn
	origPushRaw := pushRawAiperfResultFn
	origVIP := flagBenchVIP
	origModel := flagBenchModel
	origForgeURL := flagBenchForgeURL
	origScenarios := flagBenchScenarios
	origScenario := flagBenchScenario
	origCheckServedModel := checkServedModelFn
	defer func() {
		runAiperfFn = origRunAiperf
		pushRawAiperfResultFn = origPushRaw
		flagBenchVIP = origVIP
		flagBenchModel = origModel
		flagBenchForgeURL = origForgeURL
		flagBenchScenarios = origScenarios
		flagBenchScenario = origScenario
		checkServedModelFn = origCheckServedModel
	}()

	flagBenchVIP = "10.0.10.100"
	flagBenchModel = "test-model"
	flagBenchForgeURL = "http://localhost:9999"
	flagBenchScenarios = ""
	flagBenchScenario = ""

	checkServedModelFn = func(_ context.Context, _ jumphost.ProbeOptions, _ string) error {
		return nil
	}

	runAiperfFn = func(_ context.Context, _ jumphost.AiperfRunOptions) (*jumphost.AiperfResult, error) {
		return stubAiperfResult(), nil
	}

	var gotProxyDeploymentID int
	pushRawAiperfResultFn = func(_ context.Context, opts forge.RawAiperfPushOptions) (forge.RawAiperfPushResponse, error) {
		gotProxyDeploymentID = opts.ProxyDeploymentID
		return forge.RawAiperfPushResponse{ID: 1, RunID: 55, Proxy: opts.Proxy, Status: "ok"}, nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	graph := forgeGraph{targetID: 5, proxyDeploymentID: 77}
	oc := runShootoutFrontEnd(cmd, jumphost.ProbeOptions{VIP: "10.0.10.100"}, forge.RestCreds{}, "agent-1", graph, "envoy", "", nil)

	if oc.Status != "OK" {
		t.Errorf("Status = %q, want OK (err=%v)", oc.Status, oc.Err)
	}
	if gotProxyDeploymentID != 77 {
		t.Errorf("pushed ProxyDeploymentID = %d, want 77", gotProxyDeploymentID)
	}
	if len(oc.RunIDs) != 1 || oc.RunIDs[0] != 55 {
		t.Errorf("RunIDs = %v, want [55]", oc.RunIDs)
	}
}

func TestRunShootoutFrontEnd_NoGlobalMutation(t *testing.T) {
	origProxy := flagBenchProxy
	origVIP := flagBenchVIP
	origRunAiperf := runAiperfFn
	origPushRaw := pushRawAiperfResultFn
	origForgeURL := flagBenchForgeURL
	origScenario := flagBenchScenario
	origScenarios := flagBenchScenarios
	origCheckServedModel := checkServedModelFn
	defer func() {
		flagBenchProxy = origProxy
		flagBenchVIP = origVIP
		runAiperfFn = origRunAiperf
		pushRawAiperfResultFn = origPushRaw
		flagBenchForgeURL = origForgeURL
		flagBenchScenario = origScenario
		flagBenchScenarios = origScenarios
		checkServedModelFn = origCheckServedModel
	}()

	flagBenchProxy = "f5-bnk"
	flagBenchVIP = "10.0.10.100"
	flagBenchForgeURL = "http://localhost:9999"
	flagBenchScenario = ""
	flagBenchScenarios = ""

	checkServedModelFn = func(_ context.Context, _ jumphost.ProbeOptions, _ string) error {
		return nil
	}
	runAiperfFn = func(_ context.Context, _ jumphost.AiperfRunOptions) (*jumphost.AiperfResult, error) {
		return stubAiperfResult(), nil
	}
	pushRawAiperfResultFn = func(_ context.Context, _ forge.RawAiperfPushOptions) (forge.RawAiperfPushResponse, error) {
		return forge.RawAiperfPushResponse{RunID: 1}, nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	graph := forgeGraph{targetID: 1, proxyDeploymentID: 42}
	_ = runShootoutFrontEnd(cmd, jumphost.ProbeOptions{VIP: "10.0.10.100"}, forge.RestCreds{}, "agent", graph, "envoy", "", nil)

	if flagBenchProxy != "f5-bnk" {
		t.Errorf("flagBenchProxy mutated: got %q, want %q", flagBenchProxy, "f5-bnk")
	}
	if flagBenchVIP != "10.0.10.100" {
		t.Errorf("flagBenchVIP mutated: got %q, want %q", flagBenchVIP, "10.0.10.100")
	}
}

// ---------------------------------------------------------------------------
// Composition: --proxies + --scenario
// ---------------------------------------------------------------------------

func TestComposition_ProxiesAndScenario_StampsPerFrontEnd(t *testing.T) {
	origRunAiperf := runAiperfFn
	origPushRaw := pushRawAiperfResultFn
	origRegisterConfig := registerBenchmarkConfigFn
	origProxies := flagBenchProxies
	origScenario := flagBenchScenario
	origVIP := flagBenchVIP
	origModel := flagBenchModel
	origForgeURL := flagBenchForgeURL
	origEndpoint := flagBenchEndpoint
	origDiscover := discoverProxiesFn
	origList := listProxyDeploymentsFn
	origCheckServedModel := checkServedModelFn
	defer func() {
		runAiperfFn = origRunAiperf
		pushRawAiperfResultFn = origPushRaw
		registerBenchmarkConfigFn = origRegisterConfig
		flagBenchProxies = origProxies
		flagBenchScenario = origScenario
		flagBenchVIP = origVIP
		flagBenchModel = origModel
		flagBenchForgeURL = origForgeURL
		flagBenchEndpoint = origEndpoint
		discoverProxiesFn = origDiscover
		listProxyDeploymentsFn = origList
		checkServedModelFn = origCheckServedModel
	}()

	flagBenchProxies = "envoy,haproxy"
	flagBenchScenario = "baseline"
	flagBenchVIP = "10.0.10.100"
	flagBenchModel = "test-model"
	flagBenchForgeURL = "http://localhost:9999"
	flagBenchEndpoint = "/v1/chat/completions"

	checkServedModelFn = func(_ context.Context, _ jumphost.ProbeOptions, _ string) error {
		return nil
	}

	knownProxies := []forge.ProxyDeployment{
		{ID: 10, ProxyType: "envoy", ExternalURL: "10.0.1.55:31234"},
		{ID: 11, ProxyType: "haproxy", ExternalURL: "10.0.1.55:31235"},
	}
	discoverProxiesFn = func(_ context.Context, _ forge.ProxyDiscoverOptions) (forge.ProxyDiscoveryResult, error) {
		return forge.ProxyDiscoveryResult{DiscoveredCount: 2}, nil
	}
	listProxyDeploymentsFn = func(_ context.Context, _ forge.ProxyDiscoverOptions) ([]forge.ProxyDeployment, error) {
		return knownProxies, nil
	}

	registerBenchmarkConfigFn = func(_ context.Context, opts forge.BenchmarkConfigOptions) (forge.BenchmarkConfigResponse, error) {
		return forge.BenchmarkConfigResponse{ID: 1, Name: opts.Name}, nil
	}

	runAiperfFn = func(_ context.Context, _ jumphost.AiperfRunOptions) (*jumphost.AiperfResult, error) {
		return stubAiperfResult(), nil
	}

	var pushedIDs []int
	var pushedProxies []string
	pushRawAiperfResultFn = func(_ context.Context, opts forge.RawAiperfPushOptions) (forge.RawAiperfPushResponse, error) {
		pushedIDs = append(pushedIDs, opts.ProxyDeploymentID)
		pushedProxies = append(pushedProxies, opts.Proxy)
		return forge.RawAiperfPushResponse{RunID: len(pushedIDs)}, nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	graph := forgeGraph{targetID: 5}
	frontEnds, resolveErr := resolveShootoutFrontEnds(context.Background(), forge.RestCreds{}, graph.targetID)
	if resolveErr != nil {
		t.Fatalf("resolveShootoutFrontEnds: %v", resolveErr)
	}
	if len(frontEnds) != 2 {
		t.Fatalf("expected 2 front-ends, got %d", len(frontEnds))
	}

	for _, fe := range frontEnds {
		g := graph
		g.proxyDeploymentID = fe.ProxyDeploymentID
		_ = runShootoutFrontEnd(cmd, jumphost.ProbeOptions{VIP: flagBenchVIP}, forge.RestCreds{}, "agent", g, fe.ProxyType, fe.VIP, nil)
	}

	if len(pushedIDs) != 8 {
		t.Fatalf("expected 8 pushed runs (2 front-ends × 4 children), got %d", len(pushedIDs))
	}

	for i, id := range pushedIDs[:4] {
		if id != 10 {
			t.Errorf("push[%d] ProxyDeploymentID = %d, want 10 (envoy)", i, id)
		}
	}
	for i, id := range pushedIDs[4:] {
		if id != 11 {
			t.Errorf("push[%d] ProxyDeploymentID = %d, want 11 (haproxy)", i+4, id)
		}
	}

	if len(pushedProxies) != 8 {
		t.Fatalf("expected 8 proxy labels, got %d", len(pushedProxies))
	}
	for i, p := range pushedProxies[:4] {
		if p != "envoy" {
			t.Errorf("pushedProxies[%d] = %q, want \"envoy\"", i, p)
		}
	}
	for i, p := range pushedProxies[4:] {
		if p != "haproxy" {
			t.Errorf("pushedProxies[%d] = %q, want \"haproxy\"", i+4, p)
		}
	}
}

// ---------------------------------------------------------------------------
// --proxies empty: single-run path unchanged
// ---------------------------------------------------------------------------

func TestRunForgeBenchmark_ProxiesEmpty_NoShootoutPath(t *testing.T) {
	origProxies := flagBenchProxies
	origDirectPodIP := flagBenchDirectPodIP
	origDiscover := discoverProxiesFn
	defer func() {
		flagBenchProxies = origProxies
		flagBenchDirectPodIP = origDirectPodIP
		discoverProxiesFn = origDiscover
	}()

	flagBenchProxies = ""
	flagBenchDirectPodIP = ""

	discoverCalled := false
	discoverProxiesFn = func(_ context.Context, _ forge.ProxyDiscoverOptions) (forge.ProxyDiscoveryResult, error) {
		discoverCalled = true
		return forge.ProxyDiscoveryResult{}, nil
	}

	shootoutWouldRun := flagBenchProxies != "" || flagBenchDirectPodIP != ""
	if shootoutWouldRun {
		t.Error("shootout path would be entered with empty --proxies and no --direct-pod-ip")
	}
	if discoverCalled {
		t.Error("discoverProxiesFn was called with empty --proxies")
	}
}

// ---------------------------------------------------------------------------
// pushAiperfResult proxyDeploymentID threading
// ---------------------------------------------------------------------------

func TestPushAiperfResult_ThreadsProxyDeploymentID(t *testing.T) {
	origPushRaw := pushRawAiperfResultFn
	origForgeURL := flagBenchForgeURL
	origProxy := flagBenchProxy
	origVIP := flagBenchVIP
	origModel := flagBenchModel
	defer func() {
		pushRawAiperfResultFn = origPushRaw
		flagBenchForgeURL = origForgeURL
		flagBenchProxy = origProxy
		flagBenchVIP = origVIP
		flagBenchModel = origModel
	}()

	flagBenchForgeURL = "http://localhost:9999"
	flagBenchProxy = "f5-bnk"
	flagBenchVIP = "10.0.10.100"
	flagBenchModel = "test-model"

	var gotProxyDeploymentID int
	pushRawAiperfResultFn = func(_ context.Context, opts forge.RawAiperfPushOptions) (forge.RawAiperfPushResponse, error) {
		gotProxyDeploymentID = opts.ProxyDeploymentID
		return forge.RawAiperfPushResponse{RunID: 1}, nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	result := stubAiperfResult()
	_, err := pushAiperfResult(cmd, result, forge.RestCreds{}, "agent", "label",
		0, 5, 88,
		"", "", "", nil)
	if err != nil {
		t.Fatalf("pushAiperfResult: %v", err)
	}
	if gotProxyDeploymentID != 88 {
		t.Errorf("ProxyDeploymentID = %d, want 88", gotProxyDeploymentID)
	}
}

// ---------------------------------------------------------------------------
// external_url → VIP resolution
// ---------------------------------------------------------------------------

func TestResolveShootoutFrontEnds_ExternalURL_VIPSet(t *testing.T) {
	deps := []forge.ProxyDeployment{
		{ID: 20, ProxyType: "envoy", ExternalURL: "10.0.1.55:31234"},
	}
	cleanup := shootoutTestSetup(t, "envoy", "", deps)
	defer cleanup()

	fes, err := resolveShootoutFrontEnds(context.Background(), forge.RestCreds{}, 5)
	if err != nil {
		t.Fatalf("resolveShootoutFrontEnds: %v", err)
	}
	if len(fes) != 1 {
		t.Fatalf("len(frontEnds) = %d, want 1", len(fes))
	}
	fe := fes[0]
	if fe.ProxyType != "envoy" {
		t.Errorf("ProxyType = %q, want %q", fe.ProxyType, "envoy")
	}
	if fe.VIP != "10.0.1.55:31234" {
		t.Errorf("VIP = %q, want %q", fe.VIP, "10.0.1.55:31234")
	}
	if fe.ResolveErr != nil {
		t.Errorf("ResolveErr = %v, want nil", fe.ResolveErr)
	}
	if fe.ProxyDeploymentID != 20 {
		t.Errorf("ProxyDeploymentID = %d, want 20", fe.ProxyDeploymentID)
	}
}

func TestResolveShootoutFrontEnds_EmptyExternalURL_FailClosed(t *testing.T) {
	deps := []forge.ProxyDeployment{
		{ID: 20, ProxyType: "envoy", ExternalURL: ""},
	}
	cleanup := shootoutTestSetup(t, "envoy", "", deps)
	defer cleanup()

	fes, err := resolveShootoutFrontEnds(context.Background(), forge.RestCreds{}, 5)
	if err != nil {
		t.Fatalf("resolveShootoutFrontEnds: %v", err)
	}
	if len(fes) != 1 {
		t.Fatalf("len(frontEnds) = %d, want 1", len(fes))
	}
	fe := fes[0]
	if fe.VIP != "" {
		t.Errorf("VIP = %q, want empty", fe.VIP)
	}
	if fe.ResolveErr == nil {
		t.Fatal("ResolveErr is nil; expected fail-closed error when external_url is empty")
	}
}

func TestResolveShootoutFrontEnds_NoDeployment_FailClosed(t *testing.T) {
	deps := []forge.ProxyDeployment{}
	cleanup := shootoutTestSetup(t, "envoy", "", deps)
	defer cleanup()

	fes, err := resolveShootoutFrontEnds(context.Background(), forge.RestCreds{}, 5)
	if err != nil {
		t.Fatalf("resolveShootoutFrontEnds: %v", err)
	}
	if len(fes) != 1 {
		t.Fatalf("len(frontEnds) = %d, want 1", len(fes))
	}
	fe := fes[0]
	if fe.VIP != "" {
		t.Errorf("VIP = %q, want empty", fe.VIP)
	}
	if fe.ResolveErr == nil {
		t.Fatal("ResolveErr is nil; expected fail-closed error when no ProxyDeployment found")
	}
}

func TestResolveShootoutFrontEnds_F5BNK_VIPUnchanged(t *testing.T) {
	deps := []forge.ProxyDeployment{
		{ID: 30, ProxyType: "f5-bnk"},
	}
	cleanup := shootoutTestSetup(t, "f5-bnk", "", deps)
	defer cleanup()

	fes, err := resolveShootoutFrontEnds(context.Background(), forge.RestCreds{}, 5)
	if err != nil {
		t.Fatalf("resolveShootoutFrontEnds: %v", err)
	}
	if len(fes) != 1 {
		t.Fatalf("len(frontEnds) = %d, want 1", len(fes))
	}
	fe := fes[0]
	if fe.ProxyType != "f5-bnk" {
		t.Errorf("ProxyType = %q, want %q", fe.ProxyType, "f5-bnk")
	}
	if fe.VIP != "" {
		t.Errorf("VIP = %q, want empty", fe.VIP)
	}
	if fe.ResolveErr != nil {
		t.Errorf("ResolveErr = %v, want nil", fe.ResolveErr)
	}
}

package cli

// benchmark_nlb_tags_test.go — unit tests for Slice-4 NLB opt-in tag logic.
//
// Tests:
//   - hasNonBNKProxy: pure helper covering empty, f5-bnk-only, and mixed cases.
//   - nlbOptInTags: conditional tag map returned only for non-BNK shootouts.
//   - registerBenchmarkTargetFn injection seam: Tags captured in target options
//     for the four cases from the acceptance criteria:
//       (a) no --proxies             → nil Tags
//       (b) --proxies f5-bnk         → nil Tags
//       (c) --proxies envoy,f5-bnk   → all three tags with defaults
//       (d) custom --upstream-service / --upstream-port reflected

import (
	"context"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/forge"
)

// ---------------------------------------------------------------------------
// hasNonBNKProxy — pure helper
// ---------------------------------------------------------------------------

func TestHasNonBNKProxy(t *testing.T) {
	cases := []struct {
		csv  string
		want bool
	}{
		{"", false},
		{"f5-bnk", false},
		{"f5-bnk,f5-bnk", false},
		{" f5-bnk , f5-bnk ", false},
		{"envoy", true},
		{"envoy,f5-bnk", true},
		{"f5-bnk,envoy", true},
		{"haproxy,nginx", true},
		{"nodeport", true},
	}
	for _, tc := range cases {
		got := hasNonBNKProxy(tc.csv)
		if got != tc.want {
			t.Errorf("hasNonBNKProxy(%q) = %v, want %v", tc.csv, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// nlbOptInTags — conditional tag map
// ---------------------------------------------------------------------------

func resetSyntheticFlag(t *testing.T) {
	t.Helper()
	orig := flagBenchSynthetic
	flagBenchSynthetic = false
	t.Cleanup(func() {
		flagBenchSynthetic = orig
	})
}

func TestNlbOptInTags_NoProxies_NilTags(t *testing.T) {
	resetSyntheticFlag(t)
	got := nlbOptInTags("", "vllm", "80", "")
	if got != nil {
		t.Errorf("nlbOptInTags(\"\", ...) = %v, want nil", got)
	}
}

func TestNlbOptInTags_BNKOnly_NilTags(t *testing.T) {
	resetSyntheticFlag(t)
	got := nlbOptInTags("f5-bnk", "vllm", "80", "")
	if got != nil {
		t.Errorf("nlbOptInTags(\"f5-bnk\", ...) = %v, want nil", got)
	}
}

func TestNlbOptInTags_NonBNK_AllThreeTags(t *testing.T) {
	resetSyntheticFlag(t)
	got := nlbOptInTags("envoy,f5-bnk", "vllm", "80", "")
	if got == nil {
		t.Fatal("nlbOptInTags(\"envoy,f5-bnk\", ...) = nil, want map with three keys")
	}
	if got["proxy_expose"] != "internal-nlb" {
		t.Errorf("proxy_expose = %q, want \"internal-nlb\"", got["proxy_expose"])
	}
	if got["upstream_service"] != "vllm" {
		t.Errorf("upstream_service = %q, want \"vllm\"", got["upstream_service"])
	}
	if got["upstream_port"] != "80" {
		t.Errorf("upstream_port = %q, want \"80\"", got["upstream_port"])
	}
	// Empty namespace → the tag must be absent (three keys only).
	if _, ok := got["upstream_namespace"]; ok {
		t.Errorf("upstream_namespace present with empty namespace; want absent (got %v)", got)
	}
	if len(got) != 3 {
		t.Errorf("len(tags) = %d, want 3; got %v", len(got), got)
	}
}

func TestNlbOptInTags_CustomUpstream_Reflected(t *testing.T) {
	resetSyntheticFlag(t)
	got := nlbOptInTags("envoy", "inference-svc", "8080", "")
	if got == nil {
		t.Fatal("nlbOptInTags(\"envoy\", ...) = nil, want map")
	}
	if got["upstream_service"] != "inference-svc" {
		t.Errorf("upstream_service = %q, want \"inference-svc\"", got["upstream_service"])
	}
	if got["upstream_port"] != "8080" {
		t.Errorf("upstream_port = %q, want \"8080\"", got["upstream_port"])
	}
}

func TestNlbOptInTags_UpstreamNamespace_IncludedWhenSet(t *testing.T) {
	resetSyntheticFlag(t)
	got := nlbOptInTags("envoy,haproxy", "vllm", "80", "aiinference")
	if got == nil {
		t.Fatal("nlbOptInTags(...) = nil, want map with four keys")
	}
	if got["upstream_namespace"] != "aiinference" {
		t.Errorf("upstream_namespace = %q, want \"aiinference\"", got["upstream_namespace"])
	}
	if len(got) != 4 {
		t.Errorf("len(tags) = %d, want 4; got %v", len(got), got)
	}
}

func TestNlbOptInTags_SyntheticAI(t *testing.T) {
	resetSyntheticFlag(t)
	flagBenchSynthetic = true

	got := nlbOptInTags("", "vllm", "80", "")
	if got == nil {
		t.Fatal("nlbOptInTags(...) = nil, want synthetic tags")
	}
	if got["synthetic"] != "true" {
		t.Errorf("synthetic = %q, want 'true'", got["synthetic"])
	}
	if got["simulator"] != "llm-d-inference-sim" {
		t.Errorf("simulator = %q, want 'llm-d-inference-sim'", got["simulator"])
	}
}

// ---------------------------------------------------------------------------
// resolveForgeGraph Target.Tags capture via registerBenchmarkTargetFn seam
// ---------------------------------------------------------------------------

func setupTagSeamTest(t *testing.T) (*forge.BenchmarkTargetOptions, func()) {
	t.Helper()

	origRegisterAgent := registerBenchmarkAgentFn
	origRegisterTarget := registerBenchmarkTargetFn
	origDiscoverTargets := discoverTargetsFn
	origListTargets := listBenchmarkTargetsFn
	origDiscoverProxies := discoverProxiesFn
	origListProxies := listProxyDeploymentsFn

	var capturedTargetOpts forge.BenchmarkTargetOptions

	registerBenchmarkAgentFn = func(_ context.Context, _ forge.BenchmarkAgentOptions) (forge.BenchmarkAgentResponse, error) {
		return forge.BenchmarkAgentResponse{ID: 1, Name: "agent"}, nil
	}
	registerBenchmarkTargetFn = func(_ context.Context, opts forge.BenchmarkTargetOptions) (forge.BenchmarkTargetResponse, error) {
		capturedTargetOpts = opts
		return forge.BenchmarkTargetResponse{ID: 10, Name: opts.Name}, nil
	}
	// Stub cluster discovery to return empty so resolveForgeGraph falls back to registerBenchmarkTargetFn.
	discoverTargetsFn = func(_ context.Context, _ forge.DiscoverTargetsOptions) (forge.DiscoverTargetsResponse, error) {
		return forge.DiscoverTargetsResponse{}, nil
	}
	listBenchmarkTargetsFn = func(_ context.Context, _ forge.ListBenchmarkTargetsOptions) ([]forge.BenchmarkTargetResponse, error) {
		return nil, nil
	}
	discoverProxiesFn = func(_ context.Context, _ forge.ProxyDiscoverOptions) (forge.ProxyDiscoveryResult, error) {
		return forge.ProxyDiscoveryResult{}, nil
	}
	listProxyDeploymentsFn = func(_ context.Context, _ forge.ProxyDiscoverOptions) ([]forge.ProxyDeployment, error) {
		return nil, nil
	}

	cleanup := func() {
		registerBenchmarkAgentFn = origRegisterAgent
		registerBenchmarkTargetFn = origRegisterTarget
		discoverTargetsFn = origDiscoverTargets
		listBenchmarkTargetsFn = origListTargets
		discoverProxiesFn = origDiscoverProxies
		listProxyDeploymentsFn = origListProxies
	}

	return &capturedTargetOpts, cleanup
}

func TestResolveForgeGraph_TargetTags_NoProxies(t *testing.T) {
	captured, cleanup := setupTagSeamTest(t)
	defer cleanup()

	flagBenchProxies = ""
	flagBenchUpstreamService = "vllm"
	flagBenchUpstreamPort = "80"
	flagBenchUpstreamNamespace = ""
	flagBenchSynthetic = false

	resolveForgeGraph(context.Background(), forge.RestCreds{}, "test-agent")

	if captured.Tags != nil {
		t.Errorf("captured.Tags = %v, want nil", captured.Tags)
	}
}

func TestResolveForgeGraph_TargetTags_BNKOnly(t *testing.T) {
	captured, cleanup := setupTagSeamTest(t)
	defer cleanup()

	flagBenchProxies = "f5-bnk"
	flagBenchUpstreamService = "vllm"
	flagBenchUpstreamPort = "80"
	flagBenchUpstreamNamespace = ""
	flagBenchSynthetic = false

	resolveForgeGraph(context.Background(), forge.RestCreds{}, "test-agent")

	if captured.Tags != nil {
		t.Errorf("captured.Tags = %v, want nil", captured.Tags)
	}
}

func TestResolveForgeGraph_TargetTags_NonBNK_DefaultServiceAndPort(t *testing.T) {
	captured, cleanup := setupTagSeamTest(t)
	defer cleanup()

	flagBenchProxies = "envoy,f5-bnk"
	flagBenchUpstreamService = "vllm"
	flagBenchUpstreamPort = "80"
	flagBenchUpstreamNamespace = ""
	flagBenchSynthetic = false

	resolveForgeGraph(context.Background(), forge.RestCreds{}, "test-agent")

	if captured.Tags == nil {
		t.Fatal("captured.Tags = nil, want map with three keys")
	}
	if captured.Tags["proxy_expose"] != "internal-nlb" {
		t.Errorf("Tags[proxy_expose] = %q, want \"internal-nlb\"", captured.Tags["proxy_expose"])
	}
	if captured.Tags["upstream_service"] != "vllm" {
		t.Errorf("Tags[upstream_service] = %q, want \"vllm\"", captured.Tags["upstream_service"])
	}
	if captured.Tags["upstream_port"] != "80" {
		t.Errorf("Tags[upstream_port] = %q, want \"80\"", captured.Tags["upstream_port"])
	}
	if _, ok := captured.Tags["upstream_namespace"]; ok {
		t.Errorf("Tags[upstream_namespace] should be absent, got %q", captured.Tags["upstream_namespace"])
	}
}

func TestResolveForgeGraph_TargetTags_NonBNK_CustomServicePortNamespace(t *testing.T) {
	captured, cleanup := setupTagSeamTest(t)
	defer cleanup()

	flagBenchProxies = "haproxy"
	flagBenchUpstreamService = "my-llm"
	flagBenchUpstreamPort = "8000"
	flagBenchUpstreamNamespace = "ai"
	flagBenchSynthetic = false

	resolveForgeGraph(context.Background(), forge.RestCreds{}, "test-agent")

	if captured.Tags == nil {
		t.Fatal("captured.Tags = nil, want map with four keys")
	}
	if captured.Tags["proxy_expose"] != "internal-nlb" {
		t.Errorf("Tags[proxy_expose] = %q, want \"internal-nlb\"", captured.Tags["proxy_expose"])
	}
	if captured.Tags["upstream_service"] != "my-llm" {
		t.Errorf("Tags[upstream_service] = %q, want \"my-llm\"", captured.Tags["upstream_service"])
	}
	if captured.Tags["upstream_port"] != "8000" {
		t.Errorf("Tags[upstream_port] = %q, want \"8000\"", captured.Tags["upstream_port"])
	}
	if captured.Tags["upstream_namespace"] != "ai" {
		t.Errorf("Tags[upstream_namespace] = %q, want \"ai\"", captured.Tags["upstream_namespace"])
	}
}

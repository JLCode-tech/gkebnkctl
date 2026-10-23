package externalresourcepool_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
	"github.com/JLCode-tech/gkebnkctl/internal/scenarios"
	"github.com/JLCode-tech/gkebnkctl/internal/scenarios/externalresourcepool"
)

const fakeBackendIP = "10.0.10.222"

func readFileHelper(path string) ([]byte, error) {
	return os.ReadFile(path) // #nosec G304 — test helper, path from WriteManifest
}

func makeCluster() *intent.Cluster {
	return &intent.Cluster{
		Metadata: intent.Metadata{
			Name:    "test-cluster",
			Project: "test-proj",
			Zone:    "us-central1-a",
		},
		Region: "us-central1",
		Network: intent.Network{
			External: intent.DataplaneSpec{CIDR: "10.0.10.0/24"},
			Internal: intent.DataplaneSpec{CIDR: "10.0.20.0/24"},
		},
		Pattern: "host-device",
	}
}

// stateWithBackendIP returns a state.State seeded with the jumphost ENI IP so
// the EndpointSlice manifest renders a concrete backend address.
func stateWithBackendIP(t *testing.T) *state.State {
	t.Helper()
	st, err := state.Load(t.TempDir())
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	st.Set("JUMPHOST_INSTANCE_ID", "i-test")
	st.Set("JUMPHOST_BNK_EXT_ENI_IP", fakeBackendIP)
	return st
}

func TestRegistered(t *testing.T) {
	s := scenarios.Find("external-resource-pool")
	if s == nil {
		t.Fatal("external-resource-pool not registered — init() not called?")
	}
	if s.Rating() != scenarios.Green {
		t.Errorf("rating = %q, want green", s.Rating())
	}
	if len(s.Dependencies()) != 0 {
		t.Errorf("dependencies = %v, want empty", s.Dependencies())
	}
}

func TestManifestsRendered(t *testing.T) {
	dir := t.TempDir()
	cl := makeCluster()

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		State:        stateWithBackendIP(t),
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	s := scenarios.Find("external-resource-pool")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}
	if len(paths) != 5 {
		t.Errorf("expected 5 manifest paths, got %d: %v", len(paths), paths)
	}

	for _, p := range paths {
		if p == "" {
			t.Error("empty path in manifest list")
			continue
		}
		rawContent, readErr := readFileHelper(p)
		if readErr != nil {
			t.Fatalf("reading %s: %v", p, readErr)
		}
		content := string(rawContent)
		// No leftover template directives.
		if strings.Contains(content, "{{") || strings.Contains(content, "}}") {
			t.Errorf("manifest %s still contains template directives:\n%s", p, content)
		}
		// The external-backend manifest must carry both a selectorless Service
		// and an EndpointSlice with the rendered backend IP. BNK 2.3 has no
		// Pool CRD, so it must NOT mention one.
		if strings.HasSuffix(p, "05-external-backend.yaml") {
			if !strings.Contains(content, "kind: Service") {
				t.Errorf("05-external-backend.yaml missing 'kind: Service':\n%s", content)
			}
			if !strings.Contains(content, "kind: EndpointSlice") {
				t.Errorf("05-external-backend.yaml missing 'kind: EndpointSlice':\n%s", content)
			}
			if !strings.Contains(content, fakeBackendIP) {
				t.Errorf("05-external-backend.yaml missing backend IP %q:\n%s", fakeBackendIP, content)
			}
			if strings.Contains(content, "kind: Pool") {
				t.Errorf("05-external-backend.yaml must NOT contain 'kind: Pool' (BNK 2.3 has no Pool CRD):\n%s", content)
			}
		}
		// The HTTPRoute must reference the Service by name, not a Pool CR.
		if strings.HasSuffix(p, "06-httproute.yaml") {
			if !strings.Contains(content, "name: ext-backend") {
				t.Errorf("06-httproute.yaml missing 'name: ext-backend' backendRef:\n%s", content)
			}
			if strings.Contains(content, "kind: Pool") {
				t.Errorf("06-httproute.yaml must NOT contain 'kind: Pool' backendRef (BNK 2.3 has no Pool CRD):\n%s", content)
			}
		}
	}
}

// TestManifestsMissingBackendIP asserts Manifests errors clearly when the
// jumphost ENI IP is absent from state.
func TestManifestsMissingBackendIP(t *testing.T) {
	t.Setenv("GKEBNKCTL_FORCE_IAP", "1")
	st, err := state.Load(t.TempDir())
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      makeCluster(),
		State:        st,
		Out:          io.Discard,
		WorkspaceDir: t.TempDir(),
		Options:      map[string]string{},
	}
	s := scenarios.Find("external-resource-pool")
	if _, err := s.Manifests(sctx); err == nil {
		t.Fatal("expected error when JUMPHOST_BNK_EXT_ENI_IP is empty, got nil")
	}
}

// TestVerifyCallOrder guards the load-bearing step order in Verify:
//
//	StartHTTPResponder → waitCondition(Programmed) →
//	waitHTTPRouteCondition(Accepted) → waitHTTPRouteCondition(ResolvedRefs) →
//	ResyncHTTPRoutes → RunBodyProbes
func TestVerifyCallOrder(t *testing.T) {
	var calls []string

	deps := externalresourcepool.VerifyDeps{
		StartHTTPResponderFn: func(_ context.Context, _ *scenarios.Context, _ int, _ string) error {
			calls = append(calls, "StartHTTPResponder")
			return nil
		},
		WaitConditionFn: func(_ context.Context, _ *scenarios.Context, _ schema.GroupVersionResource, _, _, condType string, _ time.Duration) error {
			calls = append(calls, "waitCondition("+condType+")")
			return nil
		},
		WaitHTTPRouteConditionFn: func(_ context.Context, _ *scenarios.Context, _, _, condType string, _ time.Duration) error {
			calls = append(calls, "waitHTTPRouteCondition("+condType+")")
			return nil
		},
		ResyncHTTPRoutesFn: func(_ context.Context, _ *scenarios.Context, _ string) error {
			calls = append(calls, "ResyncHTTPRoutes")
			return nil
		},
		RunBodyProbesFn: func(_ context.Context, _ *scenarios.Context, _, _, _ string, _ int, _ time.Duration) (bool, string) {
			calls = append(calls, "RunBodyProbes")
			return true, "recorded"
		},
	}

	s := externalresourcepool.NewScenarioForTest(deps)
	cl := makeCluster()
	dir := t.TempDir()
	st := stateWithBackendIP(t)

	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      cl,
		State:        st,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{"vip": "10.0.10.100"},
	}

	res := s.Verify(sctx)
	if !res.AllPassed() {
		t.Errorf("expected all assertions to pass with stubbed deps, summary: %s", res.Summary)
	}

	want := []string{
		"StartHTTPResponder",
		"waitCondition(Programmed)",
		"waitHTTPRouteCondition(Accepted)",
		"waitHTTPRouteCondition(ResolvedRefs)",
		"ResyncHTTPRoutes",
		"RunBodyProbes",
	}

	if len(calls) != len(want) {
		t.Fatalf("call sequence = %v, want %v", calls, want)
	}
	for i, got := range calls {
		if got != want[i] {
			t.Errorf("calls[%d] = %q, want %q", i, got, want[i])
		}
	}
}

// TestVerifyResponderFailShortCircuits asserts a responder start failure adds
// the failed assertion and stops before any control-plane waits.
func TestVerifyResponderFailShortCircuits(t *testing.T) {
	var calls []string
	deps := externalresourcepool.VerifyDeps{
		StartHTTPResponderFn: func(_ context.Context, _ *scenarios.Context, _ int, _ string) error {
			calls = append(calls, "StartHTTPResponder")
			return os.ErrPermission
		},
		WaitConditionFn: func(_ context.Context, _ *scenarios.Context, _ schema.GroupVersionResource, _, _, _ string, _ time.Duration) error {
			calls = append(calls, "waitCondition")
			return nil
		},
	}
	s := externalresourcepool.NewScenarioForTest(deps)
	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      makeCluster(),
		State:        stateWithBackendIP(t),
		Out:          io.Discard,
		WorkspaceDir: t.TempDir(),
		Options:      map[string]string{"vip": "10.0.10.100"},
	}
	res := s.Verify(sctx)
	if res.AllPassed() {
		t.Error("expected failure when responder start errors")
	}
	if len(calls) != 1 || calls[0] != "StartHTTPResponder" {
		t.Errorf("expected only StartHTTPResponder called, got %v", calls)
	}
}

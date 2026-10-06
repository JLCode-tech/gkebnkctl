package forge_test

// cleanup_test.go — unit tests for forge benchmark artifact cleanup.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JLCode-tech/gkebnkctl/internal/forge"
)

// ---------------------------------------------------------------------------
// Mock server for cleanup tests
// ---------------------------------------------------------------------------

type cleanupServer struct {
	// Configuration — 0 means use the default success code.
	listTargetsStatus   int
	listProxiesStatus   int
	deleteTargetStatus  int // 0 = 204
	deleteProxyStatus   int // 0 = 202
	deleteAgentStatus   int // 0 = 204
	deleteConfigStatus  int // 0 = 204
	deleteSSHCredStatus int // 0 = 204
	loginStatus         int // 0 = 200

	// Partial-failure control: if set, this proxy ID returns an error.
	proxyErrorID int
	proxyErrCode int // HTTP status code for the erroring proxy

	// State for enumeration responses.
	targets  []map[string]any
	proxies  map[int][]map[string]any // keyed by target ID
	agents   []map[string]any
	sshCreds []map[string]any
	configs  []map[string]any

	// Recorded DELETE calls.
	deletedTargetIDs  []int
	deletedProxyIDs   []int
	deletedAgentIDs   []int
	deletedSSHCredIDs []int
	deletedConfigIDs  []int
	// Track all DELETE paths to assert no runs/results are deleted.
	allDeletedPaths []string
}

func (s *cleanupServer) status(configured, successCode int) int {
	if configured != 0 {
		return configured
	}
	return successCode
}

func (s *cleanupServer) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Track every DELETE call.
	if r.Method == http.MethodDelete {
		s.allDeletedPaths = append(s.allDeletedPaths, r.URL.Path)
	}

	switch {
	// ── Auth ────────────────────────────────────────────────────────────────
	case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
		code := s.status(s.loginStatus, http.StatusOK)
		w.WriteHeader(code)
		if code == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "test-token"})
		}

	// ── Targets: GET /api/benchmarks/targets ────────────────────────────────
	case r.Method == http.MethodGet && r.URL.Path == forge.BenchmarkTargetEndpoint:
		code := s.status(s.listTargetsStatus, http.StatusOK)
		w.WriteHeader(code)
		if code == http.StatusOK {
			list := s.targets
			if list == nil {
				list = []map[string]any{}
			}
			_ = json.NewEncoder(w).Encode(list)
		}

	// ── Proxies: GET /api/benchmarks/targets/{id}/proxies ───────────────────
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/proxies") &&
		!strings.Contains(r.URL.Path, "/proxies/"):
		var targetID int
		fmt.Sscanf(strings.TrimPrefix(r.URL.Path, forge.BenchmarkTargetEndpoint+"/"), "%d/proxies", &targetID)
		code := s.status(s.listProxiesStatus, http.StatusOK)
		w.WriteHeader(code)
		if code == http.StatusOK {
			list := s.proxies[targetID]
			if list == nil {
				list = []map[string]any{}
			}
			_ = json.NewEncoder(w).Encode(list)
		}

	// ── DELETE proxy: DELETE /api/benchmarks/targets/{id}/proxies/{pid} ─────
	case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/proxies/"):
		var targetID, proxyID int
		fmt.Sscanf(strings.TrimPrefix(r.URL.Path, forge.BenchmarkTargetEndpoint+"/"), "%d/proxies/%d", &targetID, &proxyID)
		s.deletedProxyIDs = append(s.deletedProxyIDs, proxyID)
		if s.proxyErrorID != 0 && proxyID == s.proxyErrorID && s.proxyErrCode != 0 {
			w.WriteHeader(s.proxyErrCode)
			return
		}
		code := s.status(s.deleteProxyStatus, http.StatusAccepted)
		w.WriteHeader(code)

	// ── DELETE target: DELETE /api/benchmarks/targets/{id} ──────────────────
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, forge.BenchmarkTargetEndpoint+"/"):
		var id int
		fmt.Sscanf(strings.TrimPrefix(r.URL.Path, forge.BenchmarkTargetEndpoint+"/"), "%d", &id)
		s.deletedTargetIDs = append(s.deletedTargetIDs, id)
		code := s.status(s.deleteTargetStatus, http.StatusNoContent)
		w.WriteHeader(code)

	// ── Agents: GET /api/benchmarks/agents ──────────────────────────────────
	case r.Method == http.MethodGet && r.URL.Path == forge.BenchmarkAgentEndpoint:
		list := s.agents
		if list == nil {
			list = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(list)

	// ── DELETE agent: DELETE /api/benchmarks/agents/{id} ────────────────────
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, forge.BenchmarkAgentEndpoint+"/"):
		var id int
		fmt.Sscanf(strings.TrimPrefix(r.URL.Path, forge.BenchmarkAgentEndpoint+"/"), "%d", &id)
		s.deletedAgentIDs = append(s.deletedAgentIDs, id)
		code := s.status(s.deleteAgentStatus, http.StatusNoContent)
		w.WriteHeader(code)

	// ── SSH credentials: GET /api/ssh-credentials ───────────────────────────
	case r.Method == http.MethodGet && r.URL.Path == forge.SSHCredentialEndpoint:
		list := s.sshCreds
		if list == nil {
			list = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(list)

	// ── DELETE ssh-credential: DELETE /api/ssh-credentials/{id} ────────────
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, forge.SSHCredentialEndpoint+"/"):
		var id int
		fmt.Sscanf(strings.TrimPrefix(r.URL.Path, forge.SSHCredentialEndpoint+"/"), "%d", &id)
		s.deletedSSHCredIDs = append(s.deletedSSHCredIDs, id)
		code := s.status(s.deleteSSHCredStatus, http.StatusNoContent)
		w.WriteHeader(code)

	// ── Configs: GET /api/benchmarks/configs ────────────────────────────────
	case r.Method == http.MethodGet && r.URL.Path == forge.BenchmarkConfigEndpoint:
		list := s.configs
		if list == nil {
			list = []map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(list)

	// ── DELETE config: DELETE /api/benchmarks/configs/{id} ──────────────────
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, forge.BenchmarkConfigEndpoint+"/"):
		var id int
		fmt.Sscanf(strings.TrimPrefix(r.URL.Path, forge.BenchmarkConfigEndpoint+"/"), "%d", &id)
		s.deletedConfigIDs = append(s.deletedConfigIDs, id)
		code := s.status(s.deleteConfigStatus, http.StatusNoContent)
		w.WriteHeader(code)

	default:
		http.NotFound(w, r)
	}
}

func startCleanupServer(s *cleanupServer) (*httptest.Server, func()) {
	ts := httptest.NewServer(http.HandlerFunc(s.handler))
	origDo := *forge.BenchmarkHTTPDoFn
	*forge.BenchmarkHTTPDoFn = ts.Client().Do
	return ts, func() {
		*forge.BenchmarkHTTPDoFn = origDo
		ts.Close()
	}
}

// ---------------------------------------------------------------------------
// Tests — down-path (DeleteClusterBenchmarkArtifacts)
// ---------------------------------------------------------------------------

func TestDeleteClusterBenchmarkArtifacts_HappyPath(t *testing.T) {
	const thisInstance = "i-THIS"
	const otherInstance = "i-OTHER"
	s := &cleanupServer{
		targets: []map[string]any{
			{"id": float64(10), "name": "gkebnkctl-ai-rig-llama3", "cluster_id": float64(7)},
			{"id": float64(11), "name": "other-cluster-target", "cluster_id": float64(99)},
		},
		proxies: map[int][]map[string]any{
			10: {{"id": float64(20)}},
		},
		agents: []map[string]any{
			{"id": float64(3), "name": "gkebnkctl-jumphost-" + thisInstance},
			{"id": float64(4), "name": "gkebnkctl-jumphost-" + otherInstance},
			{"id": float64(2), "name": "forge-local"},
		},
		sshCreds: []map[string]any{
			{"id": float64(5), "name": "gkebnkctl-jumphost-" + thisInstance},
			{"id": float64(6), "name": "gkebnkctl-jumphost-" + otherInstance},
		},
		configs: []map[string]any{
			{"id": float64(8), "name": "gkebnkctl-latency"},
			{"id": float64(9), "name": "gkebnkctl-throughput"},
			{"id": float64(1), "name": "some-other-config"},
		},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	err := forge.DeleteClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7, thisInstance)
	if err != nil {
		t.Fatalf("DeleteClusterBenchmarkArtifacts: %v", err)
	}

	if len(s.deletedProxyIDs) != 1 || s.deletedProxyIDs[0] != 20 {
		t.Errorf("deletedProxyIDs = %v, want [20]", s.deletedProxyIDs)
	}
	if len(s.deletedTargetIDs) != 1 || s.deletedTargetIDs[0] != 10 {
		t.Errorf("deletedTargetIDs = %v, want [10]", s.deletedTargetIDs)
	}
	if len(s.deletedAgentIDs) != 1 || s.deletedAgentIDs[0] != 3 {
		t.Errorf("deletedAgentIDs = %v, want [3] (exact match only)", s.deletedAgentIDs)
	}
	if len(s.deletedSSHCredIDs) != 1 || s.deletedSSHCredIDs[0] != 5 {
		t.Errorf("deletedSSHCredIDs = %v, want [5] (exact match only)", s.deletedSSHCredIDs)
	}
	if len(s.deletedConfigIDs) != 0 {
		t.Errorf("down-path deleted configs %v — configs must not be deleted on down", s.deletedConfigIDs)
	}

	for _, path := range s.allDeletedPaths {
		if strings.Contains(path, "/runs") || strings.Contains(path, "/results") {
			t.Errorf("DELETE issued against runs/results path: %s", path)
		}
	}
}

func TestDeleteClusterBenchmarkArtifacts_EmptyInstanceID(t *testing.T) {
	s := &cleanupServer{
		targets: []map[string]any{
			{"id": float64(10), "name": "gkebnkctl-target", "cluster_id": float64(7)},
		},
		proxies: map[int][]map[string]any{10: {{"id": float64(20)}}},
		agents: []map[string]any{
			{"id": float64(3), "name": "gkebnkctl-jumphost-i-abc"},
		},
		sshCreds: []map[string]any{
			{"id": float64(5), "name": "gkebnkctl-jumphost-i-abc"},
		},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	err := forge.DeleteClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(s.deletedProxyIDs) != 1 || s.deletedProxyIDs[0] != 20 {
		t.Errorf("deletedProxyIDs = %v, want [20]", s.deletedProxyIDs)
	}
	if len(s.deletedTargetIDs) != 1 || s.deletedTargetIDs[0] != 10 {
		t.Errorf("deletedTargetIDs = %v, want [10]", s.deletedTargetIDs)
	}
	if len(s.deletedAgentIDs) != 0 {
		t.Errorf("agent deleted when instanceID is empty: %v", s.deletedAgentIDs)
	}
	if len(s.deletedSSHCredIDs) != 0 {
		t.Errorf("ssh-cred deleted when instanceID is empty: %v", s.deletedSSHCredIDs)
	}
}

func TestDeleteClusterBenchmarkArtifacts_404Idempotent(t *testing.T) {
	s := &cleanupServer{
		deleteTargetStatus:  http.StatusNotFound,
		deleteProxyStatus:   http.StatusNotFound,
		deleteAgentStatus:   http.StatusNotFound,
		deleteSSHCredStatus: http.StatusNotFound,
		targets: []map[string]any{
			{"id": float64(10), "name": "gkebnkctl-ai-rig-llama3", "cluster_id": float64(7)},
		},
		proxies: map[int][]map[string]any{
			10: {{"id": float64(20)}},
		},
		agents: []map[string]any{
			{"id": float64(3), "name": "gkebnkctl-jumphost-i-001"},
		},
		sshCreds: []map[string]any{
			{"id": float64(5), "name": "gkebnkctl-jumphost-i-001"},
		},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	if err := forge.DeleteClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7, "i-001"); err != nil {
		t.Errorf("expected nil on all-404, got: %v", err)
	}
}

func TestDeleteClusterBenchmarkArtifacts_NoArtifacts(t *testing.T) {
	s := &cleanupServer{
		targets: nil,
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	if err := forge.DeleteClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 99, "i-000"); err != nil {
		t.Errorf("expected nil for empty cluster, got: %v", err)
	}
	if len(s.deletedTargetIDs)+len(s.deletedAgentIDs)+len(s.deletedConfigIDs)+len(s.deletedSSHCredIDs) != 0 {
		t.Error("expected no deletes for empty cluster with no matching records")
	}
}

func TestDeleteClusterBenchmarkArtifacts_401SoftFail(t *testing.T) {
	s := &cleanupServer{loginStatus: http.StatusUnauthorized}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	err := forge.DeleteClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7, "i-001")
	if err == nil {
		t.Error("expected error on 401 login, got nil")
	}
}

func TestDeleteClusterBenchmarkArtifacts_NoRunsOrResultsDeleted(t *testing.T) {
	s := &cleanupServer{
		targets: []map[string]any{
			{"id": float64(5), "name": "gkebnkctl-target", "cluster_id": float64(7)},
		},
		proxies:  map[int][]map[string]any{5: {{"id": float64(6)}}},
		agents:   []map[string]any{{"id": float64(7), "name": "gkebnkctl-jumphost-i-x"}},
		sshCreds: []map[string]any{{"id": float64(9), "name": "gkebnkctl-jumphost-i-x"}},
		configs:  []map[string]any{{"id": float64(8), "name": "gkebnkctl-latency"}},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	_ = forge.DeleteClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7, "i-x")

	for _, path := range s.allDeletedPaths {
		if strings.Contains(path, "/runs") || strings.Contains(path, "/results") {
			t.Errorf("DELETE issued to runs/results path: %s", path)
		}
	}
}

func TestDeleteClusterBenchmarkArtifacts_PartialDeleteError(t *testing.T) {
	s := &cleanupServer{
		proxyErrorID: 20,
		proxyErrCode: http.StatusInternalServerError,
		targets: []map[string]any{
			{"id": float64(10), "name": "gkebnkctl-ai-rig", "cluster_id": float64(7)},
		},
		proxies: map[int][]map[string]any{
			10: {{"id": float64(20)}, {"id": float64(21)}},
		},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	err := forge.DeleteClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7, "")
	if err == nil {
		t.Fatal("expected error when proxy delete fails, got nil")
	}
	if !strings.Contains(err.Error(), "20") {
		t.Errorf("error should mention failing proxy id 20, got: %v", err)
	}

	found21 := false
	for _, id := range s.deletedProxyIDs {
		if id == 21 {
			found21 = true
		}
	}
	if !found21 {
		t.Errorf("proxy 21 was not deleted after proxy 20 failed; deletedProxyIDs=%v", s.deletedProxyIDs)
	}

	if len(s.deletedTargetIDs) != 1 || s.deletedTargetIDs[0] != 10 {
		t.Errorf("target 10 was not deleted after proxy error; deletedTargetIDs=%v", s.deletedTargetIDs)
	}
}

func TestDeleteClusterBenchmarkArtifacts_SSHCred409SoftWarning(t *testing.T) {
	s := &cleanupServer{
		deleteSSHCredStatus: http.StatusConflict,
		targets: []map[string]any{
			{"id": float64(10), "name": "gkebnkctl-target", "cluster_id": float64(7)},
		},
		proxies:  map[int][]map[string]any{10: {{"id": float64(20)}}},
		agents:   []map[string]any{{"id": float64(3), "name": "gkebnkctl-jumphost-i-001"}},
		sshCreds: []map[string]any{{"id": float64(5), "name": "gkebnkctl-jumphost-i-001"}},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	err := forge.DeleteClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7, "i-001")
	if err == nil {
		t.Error("expected soft-warning error on 409 ssh-cred delete, got nil")
	}
	if !strings.Contains(err.Error(), "ssh-credential") {
		t.Errorf("error should mention ssh-credential, got: %v", err)
	}

	if len(s.deletedTargetIDs) != 1 {
		t.Errorf("target not deleted despite ssh-cred 409; deletedTargetIDs=%v", s.deletedTargetIDs)
	}
	if len(s.deletedProxyIDs) != 1 {
		t.Errorf("proxy not deleted despite ssh-cred 409; deletedProxyIDs=%v", s.deletedProxyIDs)
	}
	if len(s.deletedAgentIDs) != 1 || s.deletedAgentIDs[0] != 3 {
		t.Errorf("agent not deleted despite ssh-cred 409; deletedAgentIDs=%v", s.deletedAgentIDs)
	}
}

// ---------------------------------------------------------------------------
// Tests — full-path (DeleteAllClusterBenchmarkArtifacts)
// ---------------------------------------------------------------------------

func TestDeleteAllClusterBenchmarkArtifacts_HappyPath(t *testing.T) {
	s := &cleanupServer{
		targets: []map[string]any{
			{"id": float64(10), "name": "gkebnkctl-ai-rig-llama3", "cluster_id": float64(7)},
		},
		proxies: map[int][]map[string]any{
			10: {{"id": float64(20)}},
		},
		agents: []map[string]any{
			{"id": float64(3), "name": "gkebnkctl-jumphost-i-001"},
			{"id": float64(2), "name": "forge-local"},
		},
		sshCreds: []map[string]any{
			{"id": float64(5), "name": "gkebnkctl-jumphost-i-001"},
			{"id": float64(6), "name": "some-external-host"},
		},
		configs: []map[string]any{
			{"id": float64(8), "name": "gkebnkctl-latency"},
			{"id": float64(9), "name": "gkebnkctl-throughput"},
			{"id": float64(1), "name": "some-other-config"},
		},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	err := forge.DeleteAllClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7)
	if err != nil {
		t.Fatalf("DeleteAllClusterBenchmarkArtifacts: %v", err)
	}

	if len(s.deletedProxyIDs) != 1 || s.deletedProxyIDs[0] != 20 {
		t.Errorf("deletedProxyIDs = %v, want [20]", s.deletedProxyIDs)
	}
	if len(s.deletedTargetIDs) != 1 || s.deletedTargetIDs[0] != 10 {
		t.Errorf("deletedTargetIDs = %v, want [10]", s.deletedTargetIDs)
	}
	if len(s.deletedAgentIDs) != 1 || s.deletedAgentIDs[0] != 3 {
		t.Errorf("deletedAgentIDs = %v, want [3]", s.deletedAgentIDs)
	}
	if len(s.deletedSSHCredIDs) != 1 || s.deletedSSHCredIDs[0] != 5 {
		t.Errorf("deletedSSHCredIDs = %v, want [5]", s.deletedSSHCredIDs)
	}
	if len(s.deletedConfigIDs) != 2 {
		t.Errorf("deletedConfigIDs = %v, want [8 9]", s.deletedConfigIDs)
	}

	for _, path := range s.allDeletedPaths {
		if strings.Contains(path, "/runs") || strings.Contains(path, "/results") {
			t.Errorf("DELETE issued against runs/results path: %s", path)
		}
	}
}

func TestDeleteAllClusterBenchmarkArtifacts_BuiltinAgentPreserved(t *testing.T) {
	s := &cleanupServer{
		targets: nil,
		agents: []map[string]any{
			{"id": float64(2), "name": "forge-local"},
			{"id": float64(99), "name": "some-other-agent"},
		},
		sshCreds: []map[string]any{
			{"id": float64(10), "name": "customer-provided-host"},
		},
		configs: []map[string]any{},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	if err := forge.DeleteAllClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(s.deletedAgentIDs) != 0 {
		t.Errorf("non-gkebnkctl agents were deleted: %v", s.deletedAgentIDs)
	}
	if len(s.deletedSSHCredIDs) != 0 {
		t.Errorf("non-gkebnkctl ssh-credentials were deleted: %v", s.deletedSSHCredIDs)
	}
}

func TestDeleteAllClusterBenchmarkArtifacts_NoRunsOrResultsDeleted(t *testing.T) {
	s := &cleanupServer{
		targets: []map[string]any{
			{"id": float64(5), "name": "gkebnkctl-target", "cluster_id": float64(7)},
		},
		proxies:  map[int][]map[string]any{5: {{"id": float64(6)}}},
		agents:   []map[string]any{{"id": float64(7), "name": "gkebnkctl-jumphost-x"}},
		sshCreds: []map[string]any{{"id": float64(9), "name": "gkebnkctl-jumphost-x"}},
		configs:  []map[string]any{{"id": float64(8), "name": "gkebnkctl-latency"}},
	}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	_ = forge.DeleteAllClusterBenchmarkArtifacts(context.Background(), ts.URL, forge.RestCreds{}, 7)

	for _, path := range s.allDeletedPaths {
		if strings.Contains(path, "/runs") || strings.Contains(path, "/results") {
			t.Errorf("DELETE issued to runs/results path: %s", path)
		}
	}
}

// ---------------------------------------------------------------------------
// Individual-helper tests
// ---------------------------------------------------------------------------

func TestDeleteBenchmarkTarget_404Idempotent(t *testing.T) {
	s := &cleanupServer{deleteTargetStatus: http.StatusNotFound}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	if err := forge.DeleteBenchmarkTarget(context.Background(), ts.URL, forge.RestCreds{}, 999); err != nil {
		t.Errorf("expected nil on 404, got: %v", err)
	}
}

func TestDeleteBenchmarkAgent_404Idempotent(t *testing.T) {
	s := &cleanupServer{deleteAgentStatus: http.StatusNotFound}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	if err := forge.DeleteBenchmarkAgent(context.Background(), ts.URL, forge.RestCreds{}, 999); err != nil {
		t.Errorf("expected nil on 404, got: %v", err)
	}
}

func TestDeleteBenchmarkConfig_404Idempotent(t *testing.T) {
	s := &cleanupServer{deleteConfigStatus: http.StatusNotFound}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	if err := forge.DeleteBenchmarkConfig(context.Background(), ts.URL, forge.RestCreds{}, 999); err != nil {
		t.Errorf("expected nil on 404, got: %v", err)
	}
}

func TestDeleteProxyDeployment_404Idempotent(t *testing.T) {
	s := &cleanupServer{deleteProxyStatus: http.StatusNotFound}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	if err := forge.DeleteProxyDeployment(context.Background(), ts.URL, forge.RestCreds{}, 10, 999); err != nil {
		t.Errorf("expected nil on 404, got: %v", err)
	}
}

func TestDeleteSSHCredential_404Idempotent(t *testing.T) {
	s := &cleanupServer{deleteSSHCredStatus: http.StatusNotFound}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	if err := forge.DeleteSSHCredential(context.Background(), ts.URL, forge.RestCreds{}, 999); err != nil {
		t.Errorf("expected nil on 404, got: %v", err)
	}
}

func TestDeleteSSHCredential_409SoftWarning(t *testing.T) {
	s := &cleanupServer{deleteSSHCredStatus: http.StatusConflict}
	ts, cleanup := startCleanupServer(s)
	defer cleanup()

	err := forge.DeleteSSHCredential(context.Background(), ts.URL, forge.RestCreds{}, 5)
	if err == nil {
		t.Error("expected error on 409, got nil")
	}
	if !strings.Contains(err.Error(), "5") {
		t.Errorf("error should mention credential id 5, got: %v", err)
	}
}

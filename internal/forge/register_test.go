package forge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type scriptedForge struct {
	mu        sync.Mutex
	callOrder []string
	responses map[string]string
}

func newScriptedForge() *scriptedForge {
	return &scriptedForge{
		responses: map[string]string{
			"create_project": `{"project":{"id":11,"name":"gkebnkctl-default"},"success":true}`,
			"create_cluster": `{"cluster":{"id":99,"name":"bnk-prod"},"success":true}`,
			"get_cluster":    `{"id":99,"name":"bnk-prod","status":"connected"}`,
			"delete_cluster": `{"success":true}`,
			"delete_project": `{"success":true}`,
			"scan_cluster":   `{"namespaces":[]}`,
			"bnk_health":     `{"healthy":true}`,
			"system_version": `{"version":"3.1.0"}`,
		},
	}
}

func (s *scriptedForge) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int64           `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	if req.ID == 0 && req.Method == "notifications/initialized" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	type rpcRespOK struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Result  any    `json:"result"`
	}

	switch req.Method {
	case "initialize":
		_ = json.NewEncoder(w).Encode(rpcRespOK{"2.0", req.ID, map[string]any{"protocolVersion": "2024-11-05"}})
		return
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		s.mu.Lock()
		s.callOrder = append(s.callOrder, p.Name)
		text := s.responses[p.Name]
		s.mu.Unlock()
		if text == "" {
			text = `{}`
		}
		result := map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
		}
		_ = json.NewEncoder(w).Encode(rpcRespOK{"2.0", req.ID, result})
		return
	}
	http.Error(w, "unknown method", 400)
}

func (s *scriptedForge) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.callOrder))
	copy(out, s.callOrder)
	return out
}

func TestRegister_HappyPath_NoScan(t *testing.T) {
	f := newScriptedForge()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()

	c := NewClient(srv.URL + "/mcp/")
	dir := t.TempDir()
	res, err := Register(context.Background(), c, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  dir,
		ClusterName:   "bnk-prod",
		Region:        "us-central1",
		Kubeconfig:    []byte("apiVersion: v1\nkind: Config\n"),
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if res.Link == nil || res.Link.ProjectID != 11 || res.Link.ClusterID != 99 {
		t.Errorf("link = %+v", res.Link)
	}
	if _, err := ReadLink(dir); err != nil {
		t.Errorf("link file not written: %v", err)
	}
	got := f.calls()
	want := []string{"create_project", "create_cluster"}
	if !equalSlices(got, want) {
		t.Errorf("call order = %v, want %v", got, want)
	}
}

func TestRegister_Idempotent(t *testing.T) {
	f := newScriptedForge()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()

	c := NewClient(srv.URL + "/mcp/")
	dir := t.TempDir()
	req := RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  dir,
		ClusterName:   "bnk-prod",
		Region:        "us-central1",
		Kubeconfig:    []byte("apiVersion: v1\nkind: Config\n"),
	}
	if _, err := Register(context.Background(), c, req); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if _, err := Register(context.Background(), c, req); err != nil {
		t.Fatalf("second Register: %v", err)
	}
	got := f.calls()
	want := []string{"create_project", "create_cluster", "get_cluster"}
	if !equalSlices(got, want) {
		t.Errorf("call order = %v, want %v", got, want)
	}
}

func TestRegister_WithScan(t *testing.T) {
	f := newScriptedForge()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()

	c := NewClient(srv.URL + "/mcp/")
	dir := t.TempDir()
	res, err := Register(context.Background(), c, RegisterRequest{
		WorkspaceName:    "default",
		WorkspaceDir:     dir,
		ClusterName:      "bnk-prod",
		Region:           "us-central1",
		Kubeconfig:       []byte("apiVersion: v1\nkind: Config\n"),
		PostRegisterScan: true,
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !strings.Contains(res.ScanOutput, "namespaces") {
		t.Errorf("scan output missing payload: %s", res.ScanOutput)
	}
	if !strings.Contains(res.HealthCheck, "healthy") {
		t.Errorf("health output missing payload: %s", res.HealthCheck)
	}
	got := f.calls()
	want := []string{"create_project", "create_cluster", "scan_cluster", "bnk_health"}
	if !equalSlices(got, want) {
		t.Errorf("call order = %v, want %v", got, want)
	}
}

func TestUnregister_PreservesProjectByDefault(t *testing.T) {
	f := newScriptedForge()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()

	c := NewClient(srv.URL + "/mcp/")
	dir := t.TempDir()
	if err := WriteLink(dir, &Link{ProjectID: 11, ClusterID: 99}); err != nil {
		t.Fatal(err)
	}
	if err := Unregister(context.Background(), c, dir, false); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	got := f.calls()
	want := []string{"delete_cluster"}
	if !equalSlices(got, want) {
		t.Errorf("call order = %v, want %v", got, want)
	}
	if _, err := ReadLink(dir); err == nil {
		t.Error("link file still present after Unregister")
	}
}

func TestUnregister_PurgeAlsoDeletesProject(t *testing.T) {
	f := newScriptedForge()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()

	c := NewClient(srv.URL + "/mcp/")
	dir := t.TempDir()
	if err := WriteLink(dir, &Link{ProjectID: 11, ClusterID: 99}); err != nil {
		t.Fatal(err)
	}
	if err := Unregister(context.Background(), c, dir, true); err != nil {
		t.Fatalf("Unregister --purge: %v", err)
	}
	got := f.calls()
	want := []string{"delete_cluster", "delete_project"}
	if !equalSlices(got, want) {
		t.Errorf("call order = %v, want %v", got, want)
	}
}

func TestStatus_HappyPath(t *testing.T) {
	f := newScriptedForge()
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()

	c := NewClient(srv.URL + "/mcp/")
	dir := t.TempDir()
	if err := WriteLink(dir, &Link{ProjectID: 11, ClusterID: 99, Workspace: "default"}); err != nil {
		t.Fatal(err)
	}
	st, err := Status(context.Background(), c, dir)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Reachable {
		t.Error("Reachable = false, want true")
	}
	if st.Link.ClusterID != 99 {
		t.Errorf("link.ClusterID = %d", st.Link.ClusterID)
	}
	if !strings.Contains(st.ForgeVersion, "3.1.0") {
		t.Errorf("ForgeVersion missing 3.1.0: %s", st.ForgeVersion)
	}
}

func TestRegister_PendingLinkFallsThrough(t *testing.T) {
	f := newScriptedForge()
	f.responses["create_project"] = `{"success":true,"project_id":11,"name":"gkebnkctl-default","message":"Project created successfully"}`
	f.responses["create_cluster"] = `{"id":99,"name":"bnk-prod","context":"gke_proj_us-central1_bnk-prod","api_server":"https://example.com","cloud_provider":"gcp","region":"us-central1","status":"active","project_id":11,"default_namespace":"default"}`
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()

	c := NewClient(srv.URL + "/mcp/")
	dir := t.TempDir()

	pendingLink := &Link{
		ProjectID:   0,
		ClusterID:   0,
		ProjectName: "",
		ClusterName: "bnk-prod",
		Workspace:   "default",
		Status:      "pending",
	}
	if err := WriteLink(dir, pendingLink); err != nil {
		t.Fatal(err)
	}

	req := RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  dir,
		ClusterName:   "bnk-prod",
		Region:        "us-central1",
		Kubeconfig:    []byte("apiVersion: v1\nkind: Config\n"),
	}
	res, err := Register(context.Background(), c, req)
	if err != nil {
		t.Fatalf("Register with pending link: %v", err)
	}
	if res.Link == nil {
		t.Fatal("result.Link is nil")
	}
	if res.Link.ProjectID != 11 {
		t.Errorf("ProjectID = %d, want 11", res.Link.ProjectID)
	}
	if res.Link.ClusterID != 99 {
		t.Errorf("ClusterID = %d, want 99", res.Link.ClusterID)
	}
	got := f.calls()
	want := []string{"create_project", "create_cluster"}
	if !equalSlices(got, want) {
		t.Errorf("call order = %v, want %v", got, want)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

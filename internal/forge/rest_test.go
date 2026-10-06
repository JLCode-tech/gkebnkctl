package forge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// forgeRESTServer is a minimal httptest server that serves the forge REST
// endpoints needed by RegisterREST and UnregisterREST.
type forgeRESTServer struct {
	authFail      bool
	projectFail   bool
	clusterFail   bool
	calls         []string
	projectBodies []map[string]any
}

func (s *forgeRESTServer) handler(w http.ResponseWriter, r *http.Request) {
	s.calls = append(s.calls, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")

	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
		if s.authFail {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "test-token"})

	case r.Method == http.MethodPost && r.URL.Path == "/api/projects":
		if s.projectFail {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.projectBodies = append(s.projectBodies, body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"project": map[string]any{"id": 11, "name": "gkebnkctl-default"},
			"success": true,
		})

	case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/k8s/clusters"):
		if s.clusterFail {
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cluster": map[string]any{"id": 99, "name": "bnk-prod"},
			"success": true,
		})

	case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/k8s/clusters/"):
		w.WriteHeader(http.StatusNoContent)

	case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/api/projects/"):
		w.WriteHeader(http.StatusNoContent)

	default:
		http.NotFound(w, r)
	}
}

func TestRegisterREST_HappyPath(t *testing.T) {
	srv := &forgeRESTServer{}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	dir := t.TempDir()
	res, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  dir,
		ClusterName:   "bnk-prod",
		Region:        "us-central1",
		Kubeconfig:    []byte("apiVersion: v1\nkind: Config\n"),
	}, RestCreds{})
	if err != nil {
		t.Fatalf("RegisterREST: %v", err)
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
	if res.Link.Status != "registered" {
		t.Errorf("Status = %q, want %q", res.Link.Status, "registered")
	}
	if _, err := ReadLink(dir); err != nil {
		t.Errorf("link file not written: %v", err)
	}
}

func TestRegisterREST_FlatProjectIDShape(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
	})
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success":    true,
			"project_id": 30,
			"name":       "gkebnkctl-default",
			"message":    "Project created successfully",
		})
	})
	mux.HandleFunc("/api/projects/30/k8s/clusters", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success":    true,
			"cluster_id": 77,
			"name":       "bnk-prod",
			"message":    "Cluster created successfully",
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	dir := t.TempDir()
	res, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  dir,
		ClusterName:   "bnk-prod",
		Region:        "us-central1",
		Kubeconfig:    []byte("apiVersion: v1\nkind: Config\n"),
	}, RestCreds{})
	if err != nil {
		t.Fatalf("RegisterREST (flat project_id shape): %v", err)
	}
	if res.Link.ProjectID != 30 {
		t.Errorf("ProjectID = %d, want 30", res.Link.ProjectID)
	}
	if res.Link.ClusterID != 77 {
		t.Errorf("ClusterID = %d, want 77", res.Link.ClusterID)
	}
}

func TestRegisterREST_AuthFailure(t *testing.T) {
	srv := &forgeRESTServer{authFail: true}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	_, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  t.TempDir(),
		ClusterName:   "bnk-prod",
		Kubeconfig:    []byte("k"),
	}, RestCreds{})
	if err == nil {
		t.Fatal("expected error on auth failure, got nil")
	}
	if !strings.Contains(err.Error(), "login") {
		t.Errorf("expected login error, got: %v", err)
	}
}

func TestRegisterREST_ProjectCreationFailure(t *testing.T) {
	srv := &forgeRESTServer{projectFail: true}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	_, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  t.TempDir(),
		ClusterName:   "bnk-prod",
		Kubeconfig:    []byte("k"),
	}, RestCreds{})
	if err == nil {
		t.Fatal("expected error on project failure, got nil")
	}
}

func TestRegisterREST_ClusterCreationFailure(t *testing.T) {
	srv := &forgeRESTServer{clusterFail: true}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	_, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  t.TempDir(),
		ClusterName:   "bnk-prod",
		Kubeconfig:    []byte("k"),
	}, RestCreds{})
	if err == nil {
		t.Fatal("expected error on cluster failure, got nil")
	}
}

func TestUnregisterREST_404Tolerated(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth/login" {
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	link := &Link{ProjectID: 11, ClusterID: 99}
	if err := UnregisterREST(context.Background(), ts.URL, link, RestCreds{}); err != nil {
		t.Fatalf("UnregisterREST should tolerate 404, got: %v", err)
	}
}

func TestUnregisterREST_PurgesProject(t *testing.T) {
	srv := &forgeRESTServer{}
	ts := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer ts.Close()

	link := &Link{ProjectID: 11, ClusterID: 99}
	if err := UnregisterREST(context.Background(), ts.URL, link, RestCreds{}); err != nil {
		t.Fatalf("UnregisterREST: %v", err)
	}

	var clusterDeleted, projectDeleted bool
	for _, c := range srv.calls {
		switch c {
		case "DELETE /api/projects/11/k8s/clusters/99":
			clusterDeleted = true
		case "DELETE /api/projects/11":
			projectDeleted = true
		}
	}
	if !clusterDeleted {
		t.Errorf("cluster DELETE not received; calls: %v", srv.calls)
	}
	if !projectDeleted {
		t.Errorf("project DELETE not received — project shell left hanging; calls: %v", srv.calls)
	}
}

func unregisterByNameServer(t *testing.T, projects []map[string]any, clusters []map[string]any, deletes *[]string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects":
			_ = json.NewEncoder(w).Encode(map[string]any{"projects": projects})
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/45/k8s/clusters":
			_ = json.NewEncoder(w).Encode(map[string]any{"clusters": clusters})
		case r.Method == http.MethodDelete:
			mu.Lock()
			*deletes = append(*deletes, "DELETE "+r.URL.Path)
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestUnregisterRESTByName_DeletesClusterAndProject(t *testing.T) {
	var deletes []string
	ts := unregisterByNameServer(t,
		[]map[string]any{
			{"id": 7, "name": "some-other-project"},
			{"id": 45, "name": "gkebnkctl-tracer"},
		},
		[]map[string]any{
			{"id": 29, "name": "tracer"},
		},
		&deletes)
	defer ts.Close()

	if err := UnregisterRESTByName(context.Background(), ts.URL, "tracer", RestCreds{}); err != nil {
		t.Fatalf("UnregisterRESTByName: %v", err)
	}
	want := []string{"DELETE /api/projects/45/k8s/clusters/29", "DELETE /api/projects/45"}
	if len(deletes) != 2 || deletes[0] != want[0] || deletes[1] != want[1] {
		t.Errorf("deletes = %v, want %v", deletes, want)
	}
}

func TestUnregisterRESTByName_NotRegistered(t *testing.T) {
	var deletes []string
	ts := unregisterByNameServer(t,
		[]map[string]any{
			{"id": 7, "name": "some-other-project"},
		},
		nil,
		&deletes)
	defer ts.Close()

	err := UnregisterRESTByName(context.Background(), ts.URL, "tracer", RestCreds{})
	if err == nil {
		t.Fatal("expected error for unregistered cluster, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error does not wrap os.ErrNotExist: %v", err)
	}
	if len(deletes) != 0 {
		t.Errorf("expected no DELETEs, got %v", deletes)
	}
}

func TestUnregisterRESTByName_ClusterAlreadyGone(t *testing.T) {
	var deletes []string
	ts := unregisterByNameServer(t,
		[]map[string]any{
			{"id": 45, "name": "gkebnkctl-tracer"},
		},
		[]map[string]any{},
		&deletes)
	defer ts.Close()

	if err := UnregisterRESTByName(context.Background(), ts.URL, "tracer", RestCreds{}); err != nil {
		t.Fatalf("UnregisterRESTByName: %v", err)
	}
	want := "DELETE /api/projects/45"
	if len(deletes) != 1 || deletes[0] != want {
		t.Errorf("deletes = %v, want [%s]", deletes, want)
	}
}

func TestIsMCPCatalogGap(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"tool not found: create_project", true},
		{"unknown tool create_cluster", true},
		{"method not found", true},
		{"no tool named foo", true},
		{"tool_not_found", true},
		{"connection refused", false},
		{"http 500: internal server error", false},
		{"", false},
	}
	for _, tc := range cases {
		var err error
		if tc.msg != "" {
			err = &testErr{tc.msg}
		}
		if got := IsMCPCatalogGapErr(err); got != tc.want {
			t.Errorf("IsMCPCatalogGapErr(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

func TestRegisterREST_ProjectConflict_UpsertReuses(t *testing.T) {
	const orphanProjectID = 42

	var (
		clusterCreated   bool
		kubeconfigUpload string
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
	})
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			http.Error(w, `{"error":"project name already exists"}`, http.StatusConflict)
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"projects": []map[string]any{
					{"id": 1, "name": "some-other-project"},
					{"id": orphanProjectID, "name": "gkebnkctl-default"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/api/projects/42/k8s/clusters", func(w http.ResponseWriter, r *http.Request) {
		clusterCreated = true
		var body struct {
			Kubeconfig string `json:"kubeconfig"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		kubeconfigUpload = body.Kubeconfig
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cluster": map[string]any{"id": 555, "name": "bnk-prod"},
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	dir := t.TempDir()
	res, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  dir,
		ClusterName:   "bnk-prod",
		Region:        "us-central1",
		Kubeconfig:    []byte("apiVersion: v1\nkind: Config\n"),
	}, RestCreds{})
	if err != nil {
		t.Fatalf("RegisterREST upsert: %v", err)
	}
	if res.Link.ProjectID != orphanProjectID {
		t.Errorf("ProjectID = %d, want %d (orphan reuse)", res.Link.ProjectID, orphanProjectID)
	}
	if res.Link.ClusterID != 555 {
		t.Errorf("ClusterID = %d, want 555", res.Link.ClusterID)
	}
	if !clusterCreated {
		t.Error("cluster create endpoint was not called after project upsert")
	}
	if kubeconfigUpload == "" {
		t.Error("kubeconfig was not uploaded to forge after project upsert")
	}
}

func TestRegisterREST_ClusterConflict_PutsFreshKubeconfig(t *testing.T) {
	const orphanProjectID = 42
	const orphanClusterID = 700

	var putKubeconfig string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
	})
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			http.Error(w, `{"error":"project exists"}`, http.StatusConflict)
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"projects": []map[string]any{
					{"id": orphanProjectID, "name": "gkebnkctl-default"},
				},
			})
		}
	})
	mux.HandleFunc("/api/projects/42/k8s/clusters", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			http.Error(w, `{"error":"cluster name already used in this project"}`, http.StatusConflict)
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"clusters": []map[string]any{
					{"id": orphanClusterID, "name": "bnk-prod"},
				},
			})
		}
	})
	mux.HandleFunc("/api/k8s/clusters/700", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "wrong method", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Kubeconfig string `json:"kubeconfig"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		putKubeconfig = body.Kubeconfig
		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	dir := t.TempDir()
	res, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  dir,
		ClusterName:   "bnk-prod",
		Region:        "us-central1",
		Kubeconfig:    []byte("apiVersion: v1\nkind: Config\nfresh: true\n"),
	}, RestCreds{})
	if err != nil {
		t.Fatalf("RegisterREST cluster upsert: %v", err)
	}
	if res.Link.ClusterID != orphanClusterID {
		t.Errorf("ClusterID = %d, want %d (orphan reuse)", res.Link.ClusterID, orphanClusterID)
	}
	if putKubeconfig == "" {
		t.Fatal("PUT /api/k8s/clusters/{id} was never called — kubeconfig was NOT refreshed")
	}
	if !strings.HasPrefix(putKubeconfig, "YXBpVmVyc2lvbjog") {
		t.Errorf("kubeconfig PUT body did not look base64-encoded: prefix=%q", firstN(putKubeconfig, 24))
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func TestRestCreds_DefaultsWhenZero(t *testing.T) {
	c := RestCreds{}
	if got := c.restUsername(); got != "admin" {
		t.Errorf("restUsername() = %q, want %q", got, "admin")
	}
	if got := c.restPassword(); got != "changeme" {
		t.Errorf("restPassword() = %q, want %q", got, "changeme")
	}
}

func TestRestCreds_ExplicitValues(t *testing.T) {
	c := RestCreds{Username: "operator", Password: "s3cr3t"}
	if got := c.restUsername(); got != "operator" {
		t.Errorf("restUsername() = %q, want %q", got, "operator")
	}
	if got := c.restPassword(); got != "s3cr3t" {
		t.Errorf("restPassword() = %q, want %q", got, "s3cr3t")
	}
}

func TestRegisterREST_CredsThreadedToLogin(t *testing.T) {
	var capturedUser, capturedPass string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		capturedUser = body.Username
		capturedPass = body.Password
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
	})
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "name": "gkebnkctl-default"})
	})
	mux.HandleFunc("/api/projects/1/k8s/clusters", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 2, "name": "bnk-prod"})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	creds := RestCreds{Username: "myuser", Password: "mypass"}
	_, err := RegisterREST(context.Background(), ts.URL, RegisterRequest{
		WorkspaceName: "default",
		WorkspaceDir:  t.TempDir(),
		ClusterName:   "bnk-prod",
		Region:        "us-central1",
		Kubeconfig:    []byte("apiVersion: v1\nkind: Config\n"),
	}, creds)
	if err != nil {
		t.Fatalf("RegisterREST: %v", err)
	}
	if capturedUser != "myuser" {
		t.Errorf("login username = %q, want %q", capturedUser, "myuser")
	}
	if capturedPass != "mypass" {
		t.Errorf("login password = %q, want %q", capturedPass, "mypass")
	}
}

func TestUnregisterREST_CredsThreadedToLogin(t *testing.T) {
	var capturedUser, capturedPass string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/auth/login" {
			var body struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			capturedUser = body.Username
			capturedPass = body.Password
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok"})
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()

	creds := RestCreds{Username: "svc", Password: "hunter2"}
	link := &Link{ProjectID: 5, ClusterID: 10}
	if err := UnregisterREST(context.Background(), ts.URL, link, creds); err != nil {
		t.Fatalf("UnregisterREST: %v", err)
	}
	if capturedUser != "svc" {
		t.Errorf("login username = %q, want %q", capturedUser, "svc")
	}
	if capturedPass != "hunter2" {
		t.Errorf("login password = %q, want %q", capturedPass, "hunter2")
	}
}

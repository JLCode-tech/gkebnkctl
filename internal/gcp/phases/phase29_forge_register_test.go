package phases

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/JLCode-tech/gkebnkctl/internal/forge/mcp"
	"github.com/JLCode-tech/gkebnkctl/internal/gcp/state"
	"github.com/JLCode-tech/gkebnkctl/internal/intent"
)

func TestPhase29ForgeRegister(t *testing.T) {
	ctx := context.Background()

	origBackoffs := forgeRegisterBackoffs
	forgeRegisterBackoffs = []time.Duration{1 * time.Millisecond, 1 * time.Millisecond}
	defer func() { forgeRegisterBackoffs = origBackoffs }()

	t.Run("skipped when Forge disabled", func(t *testing.T) {
		cl := &intent.Cluster{
			Metadata: intent.Metadata{Name: "test-gke", Project: "my-proj", Zone: "us-central1-a"},
			Forge:    &intent.ForgeSpec{Enabled: false},
		}
		st, _ := state.Load(t.TempDir())
		if err := Phase29ForgeRegister(ctx, cl, st, nil, false); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("dry run returns nil without mutation", func(t *testing.T) {
		cl := &intent.Cluster{
			Metadata: intent.Metadata{Name: "test-gke", Project: "my-proj", Zone: "us-central1-a"},
			Forge:    &intent.ForgeSpec{Enabled: true, URL: "http://localhost:8000"},
		}
		st, _ := state.Load(t.TempDir())
		if err := Phase29ForgeRegister(ctx, cl, st, nil, true); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("successful MCP registration", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			method, _ := req["method"].(string)
			id := req["id"]

			if method == "initialize" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      id,
					"result": map[string]any{
						"protocolVersion": mcp.ProtocolVersion,
						"serverInfo":      map[string]string{"name": "test-forge", "version": "1.0"},
					},
				})
				return
			}
			if method == "notifications/initialized" {
				w.WriteHeader(http.StatusOK)
				return
			}

			params, _ := req["params"].(map[string]any)
			toolName, _ := params["name"].(string)

			var resText string
			switch toolName {
			case "create_project":
				resText = `{"project_id":50,"name":"gkebnkctl-test-gke","success":true}`
			case "create_cluster":
				resText = `{"cluster_id":200,"name":"test-gke","project_id":50,"success":true}`
			case "scan_cluster":
				resText = `{"status":"scanned"}`
			case "bnk_health":
				resText = `{"status":"healthy"}`
			}

			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"result": map[string]any{
					"content": []map[string]string{
						{"type": "text", "text": resText},
					},
				},
			})
		}))
		defer srv.Close()

		tmpDir := t.TempDir()
		origWD, _ := os.Getwd()
		_ = os.Chdir(tmpDir)
		defer func() { _ = os.Chdir(origWD) }()

		wsDir := filepath.Join(tmpDir, ".gkebnkctl", "test-gke")
		_ = os.MkdirAll(wsDir, 0o750)

		kubePath := filepath.Join(wsDir, "kubeconfig")
		_ = os.WriteFile(kubePath, []byte("apiVersion: v1\nclusters: []"), 0o600)

		cl := &intent.Cluster{
			Metadata: intent.Metadata{Name: "test-gke", Project: "my-proj", Zone: "us-central1-a"},
			Region:   "us-central1",
			Forge: &intent.ForgeSpec{
				Enabled: true,
				MCPURL:  srv.URL,
			},
		}

		st, _ := state.Load(wsDir)
		st.Set("KUBECONFIG_PATH", kubePath)

		if err := Phase29ForgeRegister(ctx, cl, st, nil, false); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if st.Get("FORGE_REGISTERED") != "true" {
			t.Errorf("expected FORGE_REGISTERED=true in state")
		}
	})

	t.Run("soft-fail when Forge offline", func(t *testing.T) {
		tmpDir := t.TempDir()
		origWD, _ := os.Getwd()
		_ = os.Chdir(tmpDir)
		defer func() { _ = os.Chdir(origWD) }()

		wsDir := filepath.Join(tmpDir, ".gkebnkctl", "test-offline")
		_ = os.MkdirAll(wsDir, 0o750)

		kubePath := filepath.Join(wsDir, "kubeconfig")
		_ = os.WriteFile(kubePath, []byte("apiVersion: v1\nclusters: []"), 0o600)

		cl := &intent.Cluster{
			Metadata: intent.Metadata{Name: "test-offline", Project: "my-proj", Zone: "us-central1-a"},
			Region:   "us-central1",
			Forge: &intent.ForgeSpec{
				Enabled: true,
				MCPURL:  "http://127.0.0.1:59999/mcp/",
				URL:     "http://127.0.0.1:59999",
			},
		}
		st, _ := state.Load(wsDir)
		st.Set("KUBECONFIG_PATH", kubePath)

		if err := Phase29ForgeRegister(ctx, cl, st, nil, false); err != nil {
			t.Fatalf("expected soft-fail nil error, got: %v", err)
		}
	})
}

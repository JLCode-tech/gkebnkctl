package bnkforge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBNKForgeRegisterAndStatus(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/clusters", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		var p ClusterPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if p.Name != "test-cluster" {
			http.Error(w, "unexpected cluster name", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
	})

	mux.HandleFunc("/api/v1/clusters/test-cluster", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			st := ClusterStatus{
				Name:         "test-cluster",
				Provider:     "gke",
				Status:       "ACTIVE",
				RegisteredAt: "2026-09-02T00:00:00Z",
			}
			_ = json.NewEncoder(w).Encode(st)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := NewClient(srv.URL)
	ctx := context.Background()

	payload := ClusterPayload{
		Name:     "test-cluster",
		Provider: "gke",
		Status:   "ACTIVE",
	}

	if err := client.Register(ctx, payload); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	st, err := client.GetStatus(ctx, "test-cluster")
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}
	if st.Status != "ACTIVE" {
		t.Errorf("status mismatch: %s", st.Status)
	}

	if err := client.Unregister(ctx, "test-cluster"); err != nil {
		t.Fatalf("Unregister failed: %v", err)
	}
}

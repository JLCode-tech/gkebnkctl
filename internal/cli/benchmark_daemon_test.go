package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JLCode-tech/gkebnkctl/internal/jumphost"
	"github.com/gorilla/websocket"
)

func TestBenchmarkDaemon_ExecuteDispatchedRun(t *testing.T) {
	origRunAiperf := runAiperfFn
	defer func() { runAiperfFn = origRunAiperf }()

	var capturedOpts jumphost.AiperfRunOptions
	runAiperfFn = func(ctx context.Context, opts jumphost.AiperfRunOptions) (*jumphost.AiperfResult, error) {
		capturedOpts = opts
		return &jumphost.AiperfResult{
			Model:                 opts.Config.Model,
			RequestThroughput:     12.34,
			OutputTokenThroughput: 56.78,
			RawJSON:               `{"custom_metric": 99.9, "request_throughput_rps": 12.34}`,
		}, nil
	}

	config := map[string]any{
		"model":                       "meta-llama/Llama-3-8B-Instruct",
		"url":                         "http://10.10.10.100:8000/v1/chat/completions",
		"concurrency":                 float64(10),
		"request_count":               float64(50),
		"synthetic_input_tokens_mean": float64(128),
		"output_tokens_mean":          float64(64),
		"streaming":                   true,
	}

	res, err := executeDispatchedRun(context.Background(), 202, config)
	if err != nil {
		t.Fatalf("executeDispatchedRun failed: %v", err)
	}

	if capturedOpts.Config.Model != "meta-llama/Llama-3-8B-Instruct" {
		t.Errorf("unexpected model: %s", capturedOpts.Config.Model)
	}
	if capturedOpts.ProbeOptions.VIP != "10.10.10.100:8000" {
		t.Errorf("unexpected VIP: %s", capturedOpts.ProbeOptions.VIP)
	}
	if capturedOpts.Config.Concurrency != 10 {
		t.Errorf("unexpected concurrency: %d", capturedOpts.Config.Concurrency)
	}
	if capturedOpts.Config.NumRequests != 50 {
		t.Errorf("unexpected requests: %d", capturedOpts.Config.NumRequests)
	}
	if res["custom_metric"] != 99.9 {
		t.Errorf("expected custom_metric 99.9, got %v", res["custom_metric"])
	}
}

func TestBenchmarkDaemon_EndToEndMock(t *testing.T) {
	var upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	var mu sync.Mutex
	var receivedHeartbeats int
	var receivedCompleted map[string]any
	var wsConn *websocket.Conn

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/auth/login":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token": "fake-jwt-token",
			})
		case r.URL.Path == "/api/benchmarks/agents":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":     99,
				"name":   "daemon-test-agent",
				"status": "connected",
			})
		case r.URL.Path == "/api/benchmarks/agents/99/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      "daemon-agent-token",
				"expires_at": time.Now().Add(24 * time.Hour).Format(time.RFC3339),
			})
		case strings.HasPrefix(r.URL.Path, "/ws/benchmarks/agents/"):
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("WebSocket upgrade failed: %v", err)
				return
			}
			mu.Lock()
			wsConn = conn
			mu.Unlock()

			go func() {
				defer conn.Close() // #nosec G104
				for {
					_, msg, err := conn.ReadMessage()
					if err != nil {
						return
					}
					var payload map[string]any
					if err := json.Unmarshal(msg, &payload); err != nil {
						continue
					}
					mu.Lock()
					if payload["type"] == "heartbeat" {
						receivedHeartbeats++
					} else if payload["type"] == "run_completed" {
						receivedCompleted = payload
					}
					mu.Unlock()
				}
			}()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	origRunAiperf := runAiperfFn
	defer func() {
		runAiperfFn = origRunAiperf
		flagBenchAgentToken = ""
	}()

	runAiperfFn = func(ctx context.Context, opts jumphost.AiperfRunOptions) (*jumphost.AiperfResult, error) {
		return &jumphost.AiperfResult{
			Model:                 opts.Config.Model,
			RequestThroughput:     25.5,
			OutputTokenThroughput: 120.0,
			RawJSON:               `{"status": "completed", "throughput_rps": 25.5}`,
		}, nil
	}

	flagBenchForgeURL = server.URL
	flagBenchForgeUser = "admin"
	flagBenchForgePass = "changeme"
	flagBenchAgentName = "daemon-test-agent"
	flagDaemonHeartbeatInterval = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := benchmarkDaemonCmd
	cmd.SetContext(ctx)

	errCh := make(chan error, 1)
	go func() {
		// Run daemon in background
		errCh <- cmd.RunE(cmd, []string{})
	}()

	// Wait for connection with polling
	var conn *websocket.Conn
	connDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(connDeadline) {
		mu.Lock()
		conn = wsConn
		mu.Unlock()
		if conn != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if conn == nil {
		t.Fatalf("WebSocket did not connect to mock server within 3s")
	}

	// Trigger a run from mock Forge
	err := conn.WriteJSON(map[string]any{
		"type":   "run",
		"run_id": 303,
		"config": map[string]any{
			"model": "meta-llama/Llama-3-8B-Instruct",
			"url":   "http://10.10.10.100:8000/v1/chat/completions",
		},
	})
	if err != nil {
		t.Fatalf("WriteJSON run failed: %v", err)
	}

	// Wait for run completion to reach mock Forge
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	hbCount := receivedHeartbeats
	completed := receivedCompleted
	mu.Unlock()

	if hbCount == 0 {
		t.Errorf("expected heartbeats, got 0")
	}
	if completed == nil {
		t.Fatalf("expected run_completed message, got nil")
	}
	if completed["run_id"] != float64(303) {
		t.Errorf("expected run_id 303, got %v", completed["run_id"])
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Errorf("unexpected daemon exit error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Errorf("daemon did not exit cleanly")
	}
}

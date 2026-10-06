package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeMCPServer wires up a minimal MCP-over-HTTP backend that echoes
// the request body for assertions. It handles initialize/notifications
// transparently and lets the test prescribe the tools/call result.
type fakeMCPServer struct {
	t          *testing.T
	toolResult ToolCallResult // returned for tools/call
	toolErr    *rpcErr        // if non-nil, returned as rpc error
	calls      atomic.Int32   // number of POSTs received
	lastTool   atomic.Value   // string — the most recent tools/call name
	lastArgs   atomic.Value   // map[string]any — the args of the most recent call
}

func (f *fakeMCPServer) handler(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	body, _ := io.ReadAll(r.Body)
	var req rpcReq
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}

	// Notifications have no id and expect no response body.
	if req.ID == 0 && req.Method == "notifications/initialized" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	resp := rpcResp{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = json.RawMessage(`{"protocolVersion":"2024-11-05"}`)
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		f.lastTool.Store(p.Name)
		f.lastArgs.Store(p.Arguments)
		if f.toolErr != nil {
			resp.Error = f.toolErr
			break
		}
		b, _ := json.Marshal(f.toolResult)
		resp.Result = b
	default:
		resp.Error = &rpcErr{Code: -32601, Message: "method not found"}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func TestCallTool_Success(t *testing.T) {
	f := &fakeMCPServer{
		t: t,
		toolResult: ToolCallResult{
			Content: []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{
				{Type: "text", Text: `{"project":{"id":42,"name":"gkebnkctl-default"}}`},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()

	c := New(srv.URL+"/mcp/", 5*time.Second)
	res, err := c.CallTool(context.Background(), "create_project", map[string]any{
		"name":           "gkebnkctl-default",
		"cloud_provider": "gcp",
	})
	if err != nil {
		t.Fatalf("CallTool: unexpected error: %v", err)
	}
	got := f.lastTool.Load().(string)
	if got != "create_project" {
		t.Errorf("lastTool = %q, want create_project", got)
	}
	args := f.lastArgs.Load().(map[string]any)
	if args["name"] != "gkebnkctl-default" {
		t.Errorf("args.name = %v, want gkebnkctl-default", args["name"])
	}
	if !strings.Contains(res.Text(), `"id":42`) {
		t.Errorf("res.Text missing id=42: %s", res.Text())
	}

	// Each CallTool should trigger 3 POSTs: initialize + notification + tools/call.
	if got := f.calls.Load(); got != 3 {
		t.Errorf("calls = %d, want 3", got)
	}
}

func TestCallTool_ServerRPCError(t *testing.T) {
	f := &fakeMCPServer{
		t: t,
		toolErr: &rpcErr{
			Code:    -32602,
			Message: "Invalid params: name is required",
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()

	c := New(srv.URL+"/mcp/", 5*time.Second)
	_, err := c.CallTool(context.Background(), "create_project", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Invalid params") {
		t.Errorf("err = %v, want it to surface the server message", err)
	}
}

func TestCallTool_ToolIsErrorFlag(t *testing.T) {
	f := &fakeMCPServer{
		t: t,
		toolResult: ToolCallResult{
			IsError: true,
			Content: []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{
				{Type: "text", Text: "validation failed: region required"},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()

	c := New(srv.URL+"/mcp/", 5*time.Second)
	_, err := c.CallTool(context.Background(), "create_project", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "validation failed") {
		t.Errorf("err = %v, want it to surface tool text", err)
	}
}

func TestCallTool_EmptyToolName(t *testing.T) {
	c := New("http://unused/mcp/", time.Second)
	if _, err := c.CallTool(context.Background(), "", nil); err == nil {
		t.Fatal("expected error for empty tool name")
	}
}

func TestUnmarshalToolJSON(t *testing.T) {
	r := ToolCallResult{
		Content: []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{
			{Type: "text", Text: `{"x":1,"y":"two"}`},
		},
	}
	var got struct {
		X int    `json:"x"`
		Y string `json:"y"`
	}
	if err := r.UnmarshalToolJSON(&got); err != nil {
		t.Fatal(err)
	}
	if got.X != 1 || got.Y != "two" {
		t.Errorf("got %+v", got)
	}
}

func TestUnmarshalToolJSON_Empty(t *testing.T) {
	r := ToolCallResult{}
	var dst map[string]any
	if err := r.UnmarshalToolJSON(&dst); err == nil {
		t.Fatal("expected error on empty result")
	}
}

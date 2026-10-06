// Package mcp is a minimal MCP (Model Context Protocol) client that speaks
// JSON-RPC 2.0 over Streamable HTTP — enough to talk to the BNK-Forge MCP
// server in its stateless mode (FastMCP with stateless_http=True,
// json_response=True).
//
// Each Call performs a fresh initialize → notifications/initialized →
// tools/call cycle, because the server is stateless. That's wasteful for
// chatty workloads but acceptable for the gkebnkctl→forge handoff, which
// makes one call per orchestration step.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// ProtocolVersion is the MCP protocol revision gkebnkctl targets. The
// forge MCP server (FastMCP) accepts any version it knows; updating this
// is a deliberate compatibility decision, not a tracking concern.
const ProtocolVersion = "2024-11-05"

// Client is a minimal MCP-over-HTTP client.
type Client struct {
	endpoint  string
	http      *http.Client
	clientID  string
	idCounter atomic.Int64
}

// New returns a Client targeting the given MCP endpoint
// (e.g. "http://localhost:8081/mcp/"). The trailing slash matches
// FastMCP's default route.
func New(endpoint string, timeout time.Duration) *Client {
	return &Client{
		endpoint: endpoint,
		http:     &http.Client{Timeout: timeout},
		clientID: "gkebnkctl",
	}
}

// rpcReq is one JSON-RPC 2.0 request.
type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResp is one JSON-RPC 2.0 response.
type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcErr) Error() string {
	return fmt.Sprintf("mcp rpc error %d: %s", e.Code, e.Message)
}

// ToolCallResult is the shape of an MCP tools/call result.
// content is normally a single text block containing JSON.
type ToolCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}

// Text returns the concatenated text of all text-typed content blocks.
// Most forge MCP tools return a single JSON-stringified payload in one
// text block.
func (r ToolCallResult) Text() string {
	var b bytes.Buffer
	for _, c := range r.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// CallTool performs init+notification+tools/call and returns the tool's
// result. Errors are flattened: transport errors, JSON-RPC errors, and
// MCP-reported tool errors (isError=true) all surface as a Go error.
//
// args may be nil to call a tool with no arguments.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (ToolCallResult, error) {
	if name == "" {
		return ToolCallResult{}, errors.New("tool name is required")
	}

	// 1) initialize
	initParams := map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": c.clientID, "version": "1"},
	}
	if _, err := c.rpc(ctx, "initialize", initParams); err != nil {
		return ToolCallResult{}, fmt.Errorf("mcp initialize: %w", err)
	}

	// 2) notifications/initialized (no response expected; fire and ignore)
	_ = c.notify(ctx, "notifications/initialized", nil)

	// 3) tools/call
	if args == nil {
		args = map[string]any{}
	}
	params := map[string]any{"name": name, "arguments": args}
	raw, err := c.rpc(ctx, "tools/call", params)
	if err != nil {
		return ToolCallResult{}, fmt.Errorf("mcp tools/call %s: %w", name, err)
	}

	var out ToolCallResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return ToolCallResult{}, fmt.Errorf("decode tool result for %s: %w", name, err)
	}
	if out.IsError {
		return out, fmt.Errorf("mcp tool %s reported isError=true: %s", name, out.Text())
	}
	return out, nil
}

// UnmarshalToolJSON parses the JSON text the forge MCP tool returned into
// the caller's target struct. Forge tools wrap their REST result in a
// JSON-stringified text block; this is the canonical decode helper.
func (r ToolCallResult) UnmarshalToolJSON(target any) error {
	t := r.Text()
	if t == "" {
		return errors.New("tool returned empty content")
	}
	if err := json.Unmarshal([]byte(t), target); err != nil {
		return fmt.Errorf("decode tool JSON: %w", err)
	}
	return nil
}

func (c *Client) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.idCounter.Add(1)
	body := rpcReq{JSONRPC: "2.0", ID: id, Method: method}
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		body.Params = p
	}
	reqBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(reqBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("http %d from MCP endpoint: %s", resp.StatusCode, truncate(string(respBytes), 400))
	}

	var rr rpcResp
	if err := json.Unmarshal(respBytes, &rr); err != nil {
		return nil, fmt.Errorf("decode rpc response: %w (body=%s)", err, truncate(string(respBytes), 200))
	}
	if rr.Error != nil {
		return nil, rr.Error
	}
	return rr.Result, nil
}

// notify sends a JSON-RPC notification (no id, no response expected).
// We do a best-effort POST and intentionally do not parse the response —
// errors are non-fatal because the spec allows transport to ignore them.
func (c *Client) notify(ctx context.Context, method string, params any) error {
	body := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		body["params"] = params
	}
	reqBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(reqBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

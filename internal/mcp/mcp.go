// Package mcp is a minimal Model Context Protocol server over stdio
// (JSON-RPC 2.0, one message per line): enough for tools/list and
// tools/call, with no dependencies.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync"
)

// Tool is one tool the server offers.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Result is a tool's reply.
type Result struct {
	Text    string
	IsError bool
}

// Handler answers tool calls. Tools is called on every tools/list, so the
// list can change (e.g. as skills get proven).
type Handler interface {
	Tools() []Tool
	Call(ctx context.Context, name string, args json.RawMessage) Result
}

// Changer is a Handler whose tool list can change. After each request the
// server asks; on true it tells the client to list the tools again.
type Changer interface {
	Changed() bool
}

// Info names the server.
type Info struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// supported protocol versions, newest first.
var versions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// Serve reads requests from r and writes responses to w until r closes.
// Tool calls run one at a time, so a local model is never sent two at once.
func Serve(ctx context.Context, r io.Reader, w io.Writer, info Info, h Handler) error {
	var mu sync.Mutex
	send := func(resp response) {
		mu.Lock()
		defer mu.Unlock()
		b, _ := json.Marshal(resp)
		w.Write(append(b, '\n'))
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			send(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
			continue
		}
		if len(req.ID) == 0 {
			continue // a notification (e.g. notifications/initialized): no reply
		}
		resp := response{JSONRPC: "2.0", ID: req.ID}
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			v := versions[0]
			for _, s := range versions {
				if s == p.ProtocolVersion {
					v = s
				}
			}
			resp.Result = map[string]any{
				"protocolVersion": v,
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
				"serverInfo":      info,
			}
		case "ping":
			resp.Result = map[string]any{}
		case "tools/list":
			tools := h.Tools()
			if tools == nil {
				tools = []Tool{}
			}
			resp.Result = map[string]any{"tools": tools}
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &p); err != nil {
				resp.Error = &rpcError{-32602, "invalid params"}
				break
			}
			res := h.Call(ctx, p.Name, p.Arguments)
			resp.Result = map[string]any{
				"content": []map[string]string{{"type": "text", "text": res.Text}},
				"isError": res.IsError,
			}
		default:
			resp.Error = &rpcError{-32601, "method not found: " + req.Method}
		}
		send(resp)
		if c, ok := h.(Changer); ok && req.Method != "initialize" && c.Changed() {
			mu.Lock()
			w.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}` + "\n"))
			mu.Unlock()
		}
	}
	return sc.Err()
}

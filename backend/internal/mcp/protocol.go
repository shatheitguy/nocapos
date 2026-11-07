// Package mcp is NoCap MCP: a Model Context Protocol server built into
// NoCapOS, so AI agents installed on this machine (or allowed in from the
// network) can look around and, with the owner's approval, do things.
//
// This file is the protocol side: JSON-RPC 2.0 messages as the MCP spec's
// Streamable HTTP transport carries them (one message per POST, answered
// with application/json). Tools themselves live with the API server.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
)

// LatestProtocol is the newest MCP revision NoCap MCP speaks.
const LatestProtocol = "2025-11-25"

// SupportedProtocols are the revisions a client may ask for, newest first.
var SupportedProtocols = []string{LatestProtocol, "2025-06-18", "2025-03-26", "2024-11-05"}

// SupportedProtocol reports whether v is a revision NoCap MCP speaks.
func SupportedProtocol(v string) bool { return slices.Contains(SupportedProtocols, v) }

// JSON-RPC error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// Message is any JSON-RPC 2.0 message: a request (method + id), a
// notification (method, no id) or a response (id + result/error).
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("%d: %s", e.Code, e.Message) }

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Content is one block of a tool result.
type Content struct {
	Type     string `json:"type"` // text | image
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"` // base64, for images
	MimeType string `json:"mimeType,omitempty"`
}

// Result is what a tool call returns. IsError marks a failure the agent
// should see (and can react to), as opposed to a protocol error.
type Result struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Text is a plain-text result.
func Text(s string) Result { return Result{Content: []Content{{Type: "text", Text: s}}} }

// Errorf is a failed tool call with a message for the agent.
func Errorf(format string, a ...any) Result {
	return Result{Content: []Content{{Type: "text", Text: fmt.Sprintf(format, a...)}}, IsError: true}
}

// JSON is a result holding v as indented JSON text.
func JSON(v any) Result {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return Errorf("couldn't encode the result: %v", err)
	}
	return Text(string(b))
}

// Tool describes one tool as tools/list shows it.
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

// Backend is what one authenticated agent connection can see and call.
type Backend interface {
	// Tools lists the tools this agent may use.
	Tools() []Tool
	// Call runs a tool. ok is false when the agent can't use a tool by that
	// name (a protocol error); failures of the tool itself are results with
	// IsError set.
	Call(ctx context.Context, name string, args map[string]any) (res Result, ok bool)
}

// ServerInfo names this server in the initialize answer.
type ServerInfo struct {
	Name         string
	Title        string
	Version      string
	Instructions string
}

// Handle answers one HTTP body: a single message or (older clients) a
// batch. reply is nil when nothing needs sending back (only notifications
// or responses came in): the transport then answers 202 Accepted.
func Handle(ctx context.Context, b Backend, info ServerInfo, body []byte) (reply []byte) {
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(body, &batch); err != nil {
			return marshal(errorResponse(nil, CodeParseError, "parse error"))
		}
		if len(batch) == 0 {
			return marshal(errorResponse(nil, CodeInvalidRequest, "empty batch"))
		}
		var out []response
		for _, raw := range batch {
			if r := handleOne(ctx, b, info, raw); r != nil {
				out = append(out, *r)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return marshal(out)
	}
	if r := handleOne(ctx, b, info, body); r != nil {
		return marshal(*r)
	}
	return nil
}

func marshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(errorResponse(nil, CodeInternalError, "couldn't encode the response"))
	}
	return b
}

func errorResponse(id json.RawMessage, code int, msg string) response {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return response{JSONRPC: "2.0", ID: id, Error: &RPCError{Code: code, Message: msg}}
}

func validID(id json.RawMessage) bool {
	if len(id) == 0 {
		return false
	}
	switch id[0] {
	case '"':
		var s string
		return json.Unmarshal(id, &s) == nil
	case 'n', '{', '[', 't', 'f':
		return false // null, objects, arrays and booleans aren't ids
	}
	var n json.Number
	return json.Unmarshal(id, &n) == nil
}

func handleOne(ctx context.Context, b Backend, info ServerInfo, raw []byte) *response {
	var m Message
	if err := json.Unmarshal(raw, &m); err != nil {
		r := errorResponse(nil, CodeParseError, "parse error")
		return &r
	}
	if m.JSONRPC != "2.0" {
		r := errorResponse(m.ID, CodeInvalidRequest, `jsonrpc must be "2.0"`)
		return &r
	}
	if m.Method == "" {
		if len(m.ID) > 0 && (m.Result != nil || m.Error != nil) {
			return nil // a response to something we never asked: ignore
		}
		r := errorResponse(m.ID, CodeInvalidRequest, "missing method")
		return &r
	}
	if len(m.ID) == 0 {
		return nil // notifications (initialized, cancelled, ...) need no answer
	}
	if !validID(m.ID) {
		r := errorResponse(nil, CodeInvalidRequest, "id must be a string or a number")
		return &r
	}
	result, rerr := dispatch(ctx, b, info, m)
	if rerr != nil {
		r := errorResponse(m.ID, rerr.Code, rerr.Message)
		return &r
	}
	return &response{JSONRPC: "2.0", ID: m.ID, Result: result}
}

type initializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
}

type callParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func dispatch(ctx context.Context, b Backend, info ServerInfo, m Message) (any, *RPCError) {
	switch m.Method {
	case "initialize":
		var p initializeParams
		if len(m.Params) > 0 {
			if err := json.Unmarshal(m.Params, &p); err != nil {
				return nil, &RPCError{CodeInvalidParams, "bad initialize params"}
			}
		}
		version := LatestProtocol
		if SupportedProtocol(p.ProtocolVersion) {
			version = p.ProtocolVersion
		}
		res := map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": info.Name, "title": info.Title, "version": info.Version},
		}
		if info.Instructions != "" {
			res["instructions"] = info.Instructions
		}
		return res, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		tools := b.Tools()
		if tools == nil {
			tools = []Tool{}
		}
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		var p callParams
		if len(m.Params) == 0 || json.Unmarshal(m.Params, &p) != nil || p.Name == "" {
			return nil, &RPCError{CodeInvalidParams, "tools/call needs a tool name"}
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		var schema map[string]any
		for _, t := range b.Tools() {
			if t.Name == p.Name {
				schema = t.InputSchema
			}
		}
		if schema == nil {
			return nil, &RPCError{CodeInvalidParams, "unknown tool: " + p.Name}
		}
		if err := ValidateArgs(schema, p.Arguments); err != nil {
			return Errorf("invalid arguments: %v", err), nil
		}
		res, ok := b.Call(ctx, p.Name, p.Arguments)
		if !ok {
			return nil, &RPCError{CodeInvalidParams, "unknown tool: " + p.Name}
		}
		if res.Content == nil {
			res.Content = []Content{}
		}
		return res, nil
	}
	return nil, &RPCError{CodeMethodNotFound, "method not found: " + m.Method}
}

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rainea/nexus/internal/tool"
	"github.com/rainea/nexus/pkg/types"
)

// Server implements MCP-style JSON-RPC over HTTP (POST) and optional SSE for session bootstrap.
type Server struct {
	reg            *tool.Registry
	exec           *tool.Executor
	serverInfo     *ServerInfo
	protoVersion   string
	rpcPath        string
	ssePath        string
	initializeHook func(ctx context.Context, params *InitializeParams) error
}

// ServerOption configures Server.
type ServerOption func(*Server)

// WithProtocolVersion sets the advertised MCP protocol version.
func WithProtocolVersion(v string) ServerOption {
	return func(s *Server) {
		if v != "" {
			s.protoVersion = v
		}
	}
}

// WithExecutor sets the tool executor used for tools/call (timeouts, truncation, hooks).
func WithExecutor(e *tool.Executor) ServerOption {
	return func(s *Server) {
		if e != nil {
			s.exec = e
		}
	}
}

// WithPaths overrides HTTP paths for RPC and SSE endpoints.
func WithPaths(rpcPath, ssePath string) ServerOption {
	return func(s *Server) {
		if rpcPath != "" {
			s.rpcPath = rpcPath
		}
		if ssePath != "" {
			s.ssePath = ssePath
		}
	}
}

// WithInitializeHook runs after validating initialize params (optional).
func WithInitializeHook(h func(ctx context.Context, params *InitializeParams) error) ServerOption {
	return func(s *Server) {
		s.initializeHook = h
	}
}

// NewServer builds an MCP server backed by the tool registry and a default Executor.
func NewServer(reg *tool.Registry, opts ...ServerOption) *Server {
	s := &Server{
		reg: reg,
		exec: tool.NewExecutor(reg,
			tool.WithDefaultTimeout(2*time.Minute),
			tool.WithMaxOutputRunes(100_000),
		),
		serverInfo: &ServerInfo{
			Name:    "nexus-mcp",
			Version: "0.1.0",
		},
		protoVersion: "2024-11-05",
		rpcPath:      "/mcp/rpc",
		ssePath:      "/mcp/sse",
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Dispatch processes a single JSON-RPC request body and returns the response JSON (or nil for notifications).
func (s *Server) Dispatch(ctx context.Context, raw []byte) ([]byte, error) {
	var req JSONRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return marshalRPCError(nil, ErrParseError, "parse error", nil)
	}
	if req.JSONRPC != JSONRPCVersion {
		return marshalRPCError(req.ID, ErrInvalidRequest, "jsonrpc must be 2.0", nil)
	}

	if !RequestHasID(req.ID) {
		_ = s.handleNotification(ctx, &req)
		return nil, nil
	}

	var result json.RawMessage
	var err error
	switch req.Method {
	case MethodInitialize:
		result, err = s.handleInitialize(ctx, req.Params)
	case MethodPing:
		result, err = MarshalResult(PingResult{})
	case MethodToolsList:
		result, err = s.handleToolsList(req.Params)
	case MethodToolsCall:
		result, err = s.handleToolsCall(ctx, req.Params)
	default:
		return marshalRPCError(req.ID, ErrMethodNotFound, fmt.Sprintf("method %q not found", req.Method), nil)
	}

	if err != nil {
		return marshalRPCError(req.ID, ErrInternal, err.Error(), nil)
	}
	return marshalRPCResult(req.ID, result)
}

// Handler returns an http.Handler mounting POST rpcPath and GET ssePath.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.rpcPath, s.handleHTTPRPC)
	mux.HandleFunc(s.ssePath, s.handleSSE)
	return mux
}

// RPCPath returns the configured POST path for JSON-RPC.
func (s *Server) RPCPath() string { return s.rpcPath }

// SSEPath returns the GET path for the SSE endpoint.
func (s *Server) SSEPath() string { return s.ssePath }

func (s *Server) handleHTTPRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	if rid := r.Header.Get("X-Request-ID"); rid != "" {
		ctx = tool.WithRequestID(ctx, rid)
	}
	out, err := s.Dispatch(ctx, raw)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if out == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	payload := fmt.Sprintf(`{"postPath":%q,"ssePath":%q}`, s.rpcPath, s.ssePath)
	_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", payload)
	fl.Flush()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = w.Write([]byte(": ping\n\n"))
			fl.Flush()
		}
	}
}

func (s *Server) handleNotification(ctx context.Context, req *JSONRPCRequest) error {
	switch req.Method {
	case MethodInitializedNotification:
		return nil
	default:
		return nil
	}
}

func (s *Server) handleInitialize(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p InitializeParams
	if len(params) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("initialize params: %w", err)
		}
	}
	if s.initializeHook != nil {
		if err := s.initializeHook(ctx, &p); err != nil {
			return nil, err
		}
	}
	res := InitializeResult{
		ProtocolVersion: s.protoVersion,
		ServerInfo:      s.serverInfo,
		Capabilities:    json.RawMessage(`{"tools":{}}`),
	}
	return MarshalResult(res)
}

func (s *Server) handleToolsList(params json.RawMessage) (json.RawMessage, error) {
	var p ToolsListParams
	if len(params) > 0 && string(params) != "null" {
		_ = json.Unmarshal(params, &p)
	}
	_ = p.Cursor

	list := s.reg.List()
	tools := make([]ToolDescriptor, 0, len(list))
	for _, m := range list {
		tools = append(tools, ToolDescriptor{
			Name:        m.Definition.Name,
			Description: m.Definition.Description,
			InputSchema: m.Definition.Parameters,
		})
	}
	return MarshalResult(ToolsListResult{Tools: tools})
}

func (s *Server) handleToolsCall(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p ToolsCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, fmt.Errorf("tools/call params: %w", err)
	}
	if p.Name == "" {
		return nil, fmt.Errorf("tool name is required")
	}
	if s.reg.Get(p.Name) == nil {
		return marshalToolsCallError(fmt.Sprintf("tool %q not found", p.Name))
	}

	var args map[string]interface{}
	if len(p.Arguments) > 0 && string(p.Arguments) != "null" {
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return nil, fmt.Errorf("arguments: %w", err)
		}
	}
	if args == nil {
		args = map[string]interface{}{}
	}

	var res *types.ToolResult
	if s.exec != nil {
		res = s.exec.Execute(ctx, tool.NewToolID(), p.Name, args, 0)
	} else {
		meta := s.reg.Get(p.Name)
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		var err error
		res, err = meta.Handler(callCtx, args)
		if err != nil && res == nil {
			return marshalToolsCallError(err.Error())
		}
		if res == nil {
			res = &types.ToolResult{Name: p.Name, Content: ""}
		}
	}

	if res == nil {
		res = &types.ToolResult{Name: p.Name, Content: ""}
	}

	out := ToolsCallResult{
		IsError: res.IsError,
		Content: []ContentBlock{
			{Type: "text", Text: res.Content},
		},
	}
	return MarshalResult(out)
}

func marshalToolsCallError(msg string) (json.RawMessage, error) {
	return MarshalResult(ToolsCallResult{
		IsError: true,
		Content: []ContentBlock{{Type: "text", Text: msg}},
	})
}

func marshalRPCResult(id json.RawMessage, result json.RawMessage) ([]byte, error) {
	resp := JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Result:  result,
	}
	return json.Marshal(resp)
}

func marshalRPCError(id json.RawMessage, code int, message string, data json.RawMessage) ([]byte, error) {
	resp := JSONRPCResponse{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
	return json.Marshal(resp)
}

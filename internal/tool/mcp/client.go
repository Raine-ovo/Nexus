package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/rainea/nexus/internal/tool"
	"github.com/rainea/nexus/pkg/types"
)

// Client speaks MCP JSON-RPC over a Transport (stdio or HTTP/SSE).
type Client struct {
	tr            Transport
	nextID        atomic.Int64
	info          *InitializeResult
	clientName    string
	clientVersion string
	protoVer      string
}

// NewClient wraps a transport. Call Initialize before ListTools or CallTool.
func NewClient(tr Transport) *Client {
	return &Client{
		tr:            tr,
		clientName:    "nexus-mcp-client",
		clientVersion: "0.1.0",
		protoVer:      "2024-11-05",
	}
}

// WithClientIdentity overrides the name/version sent in initialize.
func (c *Client) WithClientIdentity(name, version string) *Client {
	if name != "" {
		c.clientName = name
	}
	if version != "" {
		c.clientVersion = version
	}
	return c
}

// Initialize performs the MCP handshake (initialize).
func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
	params := InitializeParams{
		ProtocolVersion: c.protoVer,
		ClientInfo: &ClientInfo{
			Name:    c.clientName,
			Version: c.clientVersion,
		},
	}
	raw, err := c.rpc(ctx, MethodInitialize, params)
	if err != nil {
		return nil, err
	}
	var res InitializeResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("initialize result: %w", err)
	}
	c.info = &res
	return &res, nil
}

// SendInitializedNotification sends notifications/initialized (no response expected on stdio; may no-op).
func (c *Client) SendInitializedNotification(ctx context.Context) error {
	return c.notify(ctx, MethodInitializedNotification, map[string]interface{}{})
}

// ListTools calls tools/list.
func (c *Client) ListTools(ctx context.Context) ([]ToolDescriptor, error) {
	raw, err := c.rpc(ctx, MethodToolsList, map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var out ToolsListResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	return out.Tools, nil
}

// CallTool invokes tools/call and returns a ToolResult.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]interface{}) (*types.ToolResult, error) {
	argBytes, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	params := ToolsCallParams{Name: name, Arguments: argBytes}
	raw, err := c.rpc(ctx, MethodToolsCall, params)
	if err != nil {
		return nil, err
	}
	var tres ToolsCallResult
	if err := json.Unmarshal(raw, &tres); err != nil {
		return nil, fmt.Errorf("tools/call result: %w", err)
	}
	text := ""
	for _, b := range tres.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	return &types.ToolResult{
		Name:    name,
		Content: text,
		IsError: tres.IsError,
	}, nil
}

// RegisterDiscoveredTools registers every tool from the remote server into the local registry.
// Existing names are overwritten. The handler proxies to this client.
func (c *Client) RegisterDiscoveredTools(ctx context.Context, reg *tool.Registry) error {
	tools, err := c.ListTools(ctx)
	if err != nil {
		return err
	}
	for _, td := range tools {
		remoteName := td.Name
		params, err := inputSchemaToParameters(td.InputSchema)
		if err != nil {
			return fmt.Errorf("tool %q schema: %w", remoteName, err)
		}
		def := types.ToolDefinition{
			Name:        remoteName,
			Description: td.Description,
			Parameters:  params,
		}
		meta := &types.ToolMeta{
			Definition: def,
			Permission: types.PermRead,
			Source:     "mcp",
			Handler:    c.proxyHandler(remoteName),
		}
		if err := reg.Register(meta); err != nil {
			return err
		}
	}
	return nil
}

// AutoRegisterTools is an alias for RegisterDiscoveredTools (initialize + list + register).
func (c *Client) AutoRegisterTools(ctx context.Context, reg *tool.Registry) error {
	return c.RegisterDiscoveredTools(ctx, reg)
}

func (c *Client) proxyHandler(remoteName string) types.ToolHandler {
	return func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
		return c.CallTool(ctx, remoteName, args)
	}
}

func (c *Client) notify(ctx context.Context, method string, params interface{}) error {
	req := map[string]interface{}{
		"jsonrpc": JSONRPCVersion,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return c.tr.Send(ctx, payload)
}

func (c *Client) rpc(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	req := map[string]interface{}{
		"jsonrpc": JSONRPCVersion,
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if err := c.tr.Send(ctx, payload); err != nil {
		return nil, err
	}
	raw, err := c.tr.Receive(ctx)
	if err != nil {
		return nil, err
	}
	var resp JSONRPCResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

func inputSchemaToParameters(schema interface{}) (map[string]interface{}, error) {
	if schema == nil {
		return map[string]interface{}{"type": "object"}, nil
	}
	if m, ok := schema.(map[string]interface{}); ok {
		return m, nil
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

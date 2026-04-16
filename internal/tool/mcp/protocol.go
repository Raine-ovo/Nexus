// Package mcp implements JSON-RPC message types and helpers for the Model Context Protocol (MCP)
// style tool listing and invocation, plus pluggable transports (stdio, SSE/HTTP).
package mcp

import (
	"encoding/json"
	"fmt"
)

const JSONRPCVersion = "2.0"

// MCP JSON-RPC method names (Model Context Protocol style).
const (
	MethodInitialize              = "initialize"
	MethodInitializedNotification = "notifications/initialized"
	MethodPing                    = "ping"
	MethodToolsList               = "tools/list"
	MethodToolsCall               = "tools/call"
	MethodResourcesList           = "resources/list"
	MethodPromptsList             = "prompts/list"
)

// Standard JSON-RPC 2.0 error codes.
const (
	ErrParseError     = -32700
	ErrInvalidRequest = -32600
	ErrMethodNotFound = -32601
	ErrInvalidParams  = -32602
	ErrInternal       = -32603
)

// MCP-specific error codes (application-defined range).
const (
	ErrMCPToolNotFound = -32001
	ErrMCPToolFailed   = -32002
	ErrMCPTimeout      = -32003
)

// JSONRPCRequest is a JSON-RPC 2.0 request object.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse is a JSON-RPC 2.0 response envelope.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JSONRPCError   `json:"error,omitempty"`
}

// JSONRPCError is a JSON-RPC 2.0 error object.
type JSONRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Error implements error.
func (e *JSONRPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("json-rpc %d: %s", e.Code, e.Message)
}

// InitializeParams is sent with initialize.
type InitializeParams struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities,omitempty"`
	ClientInfo      *ClientInfo     `json:"clientInfo,omitempty"`
}

// ClientInfo identifies the MCP client.
type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult is returned from initialize.
type InitializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities,omitempty"`
	ServerInfo      *ServerInfo     `json:"serverInfo,omitempty"`
}

// ServerInfo identifies the MCP server.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// PingParams is optional metadata for ping (often empty object).
type PingParams struct{}

// PingResult is a trivial successful ping payload.
type PingResult struct{}

// ToolsListParams is optional pagination for tools/list.
type ToolsListParams struct {
	Cursor string `json:"cursor,omitempty"`
}

// ToolsListResult lists tool descriptors.
type ToolsListResult struct {
	Tools      []ToolDescriptor `json:"tools"`
	NextCursor string           `json:"nextCursor,omitempty"`
}

// ToolDescriptor matches MCP tool metadata exposed to clients.
type ToolDescriptor struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema interface{} `json:"inputSchema"`
}

// JSONSchemaProperty describes a single JSON Schema property (subset).
type JSONSchemaProperty struct {
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

// JSONSchemaObject is a minimal object schema for tools (subset of JSON Schema).
type JSONSchemaObject struct {
	Type       string                           `json:"type"`
	Properties map[string]JSONSchemaProperty    `json:"properties,omitempty"`
	Required   []string                         `json:"required,omitempty"`
}

// ToolsCallParams identifies a tool invocation.
type ToolsCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolsCallResult is the outcome of tools/call.
type ToolsCallResult struct {
	Content []ContentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

// ContentBlock is a typed content fragment (text or future binary).
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// NotificationMessage is a JSON-RPC notification (no id).
type NotificationMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// ResourceDescriptor is a placeholder for resources/list (optional future use).
type ResourceDescriptor struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// PromptDescriptor is a placeholder for prompts/list (optional future use).
type PromptDescriptor struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Arguments   interface{} `json:"arguments,omitempty"`
}

// MarshalResult wraps any Go value as json.RawMessage for JSONRPCResponse.Result.
func MarshalResult(v interface{}) (json.RawMessage, error) {
	return json.Marshal(v)
}

// RequestHasID returns true if the raw id field is present and non-null.
func RequestHasID(id json.RawMessage) bool {
	if len(id) == 0 {
		return false
	}
	s := string(id)
	return s != "null"
}

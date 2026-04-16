package types

import "context"

// ToolDefinition describes a tool's schema for LLM function calling.
type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ToolResult is the outcome of executing a tool.
type ToolResult struct {
	ToolID  string `json:"tool_id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// ToolHandler is the function signature for tool execution.
type ToolHandler func(ctx context.Context, args map[string]interface{}) (*ToolResult, error)

// ToolPermission describes what permission level a tool requires.
type ToolPermission string

const (
	PermRead    ToolPermission = "read"
	PermWrite   ToolPermission = "write"
	PermExecute ToolPermission = "execute"
	PermNetwork ToolPermission = "network"
)

// ToolMeta contains registration metadata for a tool.
type ToolMeta struct {
	Definition ToolDefinition `json:"definition"`
	Permission ToolPermission `json:"permission"`
	Source     string         `json:"source"`
	Handler    ToolHandler    `json:"-"`
}

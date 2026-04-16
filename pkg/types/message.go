package types

import "time"

// Role represents a message participant role.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is the fundamental unit of conversation.
type Message struct {
	ID        string                 `json:"id"`
	Role      Role                   `json:"role"`
	Content   string                 `json:"content"`
	ToolCalls []ToolCall             `json:"tool_calls,omitempty"`
	ToolID    string                 `json:"tool_id,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

// ToolCall represents a single tool invocation requested by the model.
type ToolCall struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// InboundMessage is the gateway-normalized input from any channel.
type InboundMessage struct {
	Channel   string                 `json:"channel"`
	User      string                 `json:"user"`
	SessionID string                 `json:"session_id"`
	Text      string                 `json:"text"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// OutboundMessage is the response sent back through the gateway.
type OutboundMessage struct {
	SessionID string                 `json:"session_id"`
	Text      string                 `json:"text"`
	AgentName string                 `json:"agent_name,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// AgentEvent represents an observable event during agent execution.
type AgentEvent struct {
	Type      string                 `json:"type"`
	AgentName string                 `json:"agent_name"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

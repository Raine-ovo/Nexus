package core

import (
	"context"
	"fmt"

	"github.com/rainea/nexus/pkg/types"
)

// DefaultChatModel is a placeholder ChatModel used when constructing specialized agents
// before the orchestrator wires a concrete LLM. Call (*BaseAgent).SetModel before Run.
var DefaultChatModel ChatModel = unconfiguredChatModel{}

type unconfiguredChatModel struct{}

func (unconfiguredChatModel) Generate(ctx context.Context, system string, messages []types.Message, tools []types.ToolDefinition) (*ChatModelResponse, error) {
	_ = ctx
	_ = system
	_ = messages
	_ = tools
	return nil, fmt.Errorf("core: chat model not assigned; call (*BaseAgent).SetModel with a concrete ChatModel before Run")
}

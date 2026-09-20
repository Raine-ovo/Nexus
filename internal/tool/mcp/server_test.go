package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/rainea/nexus/internal/tool"
	"github.com/rainea/nexus/pkg/types"
)

func testRegistry() *tool.Registry {
	reg := tool.NewRegistry()
	_ = reg.Register(&types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "write_file",
			Description: "test write",
			Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		Permission: types.PermWrite,
		Source:     "builtin",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			return &types.ToolResult{Name: "write_file", Content: "wrote"}, nil
		},
	})
	return reg
}

func toolsCallRequest(name string) []byte {
	params, _ := json.Marshal(map[string]interface{}{
		"name":      name,
		"arguments": map[string]interface{}{},
	})
	req := JSONRPCRequest{JSONRPC: JSONRPCVersion, ID: json.RawMessage(`1`), Method: MethodToolsCall, Params: params}
	raw, _ := json.Marshal(req)
	return raw
}

func decodeToolsCallResult(t *testing.T, out []byte) ToolsCallResult {
	t.Helper()
	var resp JSONRPCResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected rpc error: %v", resp.Error)
	}
	var result ToolsCallResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return result
}

// TestServerToolsCallEnforcesExecutorHook verifies that tools/call cannot bypass
// a permission hook injected into the executor (the P0 MCP permission bypass).
func TestServerToolsCallEnforcesExecutorHook(t *testing.T) {
	reg := testRegistry()
	exec := tool.NewExecutor(reg, tool.WithHooks(func(ctx context.Context, h tool.HookContext) error {
		return errors.New("permission denied")
	}, nil))
	srv := NewServer(reg, WithExecutor(exec))

	out, err := srv.Dispatch(context.Background(), toolsCallRequest("write_file"))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	result := decodeToolsCallResult(t, out)
	if !result.IsError {
		t.Fatalf("expected tool error from denied hook, got %+v", result)
	}
	if len(result.Content) == 0 || result.Content[0].Text == "" {
		t.Fatalf("expected error text in result, got %+v", result)
	}
}

func TestServerToolsCallAllowsWhenHookPasses(t *testing.T) {
	reg := testRegistry()
	exec := tool.NewExecutor(reg, tool.WithHooks(func(ctx context.Context, h tool.HookContext) error {
		return nil
	}, nil))
	srv := NewServer(reg, WithExecutor(exec))

	out, err := srv.Dispatch(context.Background(), toolsCallRequest("write_file"))
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	result := decodeToolsCallResult(t, out)
	if result.IsError {
		t.Fatalf("expected success, got %+v", result)
	}
}

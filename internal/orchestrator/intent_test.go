package orchestrator

import (
	"context"
	"testing"

	"github.com/rainea/nexus/internal/core"
	"github.com/rainea/nexus/pkg/types"
)

type stubAgent struct{ name string }

func (s stubAgent) Name() string                                { return s.name }
func (s stubAgent) Description() string                         { return s.name + " description" }
func (s stubAgent) Run(context.Context, string) (string, error) { return "", nil }
func (s stubAgent) GetTools() []*types.ToolMeta                 { return nil }
func (s stubAgent) GetSystemPrompt() string                     { return "" }

var _ core.Agent = stubAgent{}

func newTestRouter() *IntentRouter {
	reg := NewAgentRegistry()
	for _, n := range []string{"code_reviewer", "knowledge", "devops", "planner"} {
		_ = reg.Register(n, stubAgent{name: n})
	}
	return NewIntentRouter(reg)
}

func TestIntentRouterKeywordRoute(t *testing.T) {
	r := newTestRouter()
	agent, conf, err := r.Route(context.Background(), "please review this code diff")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if agent != "code_reviewer" {
		t.Fatalf("agent = %q, want code_reviewer", agent)
	}
	if conf < minRuleConfidenceToSkipLLM {
		t.Fatalf("confidence = %v, want >= %v", conf, minRuleConfidenceToSkipLLM)
	}
}

func TestIntentRouterFallsBackWhenUnknown(t *testing.T) {
	r := newTestRouter()
	agent, _, err := r.Route(context.Background(), "just a random greeting")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if agent != "planner" {
		t.Fatalf("agent = %q, want fallback planner", agent)
	}
}

func TestIntentRouterEmptyInput(t *testing.T) {
	r := newTestRouter()
	agent, _, err := r.Route(context.Background(), "   ")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if agent != "planner" {
		t.Fatalf("agent = %q, want planner", agent)
	}
}

func TestParseClassificationResponse(t *testing.T) {
	agent, conf := parseClassificationResponse(`{"agent_name":"devops","confidence":0.9,"reason":"x"}`)
	if agent != "devops" || conf != 0.9 {
		t.Fatalf("got (%q, %v)", agent, conf)
	}

	agent, conf = parseClassificationResponse("```json\n{\"agent_name\":\"planner\",\"confidence\":0.7,\"reason\":\"x\"}\n```")
	if agent != "planner" || conf != 0.7 {
		t.Fatalf("fenced got (%q, %v)", agent, conf)
	}

	agent, conf = parseClassificationResponse("Agent: code_reviewer")
	if agent != "code_reviewer" || conf != 0.75 {
		t.Fatalf("line fallback got (%q, %v)", agent, conf)
	}

	if a, _ := parseClassificationResponse("garbage"); a != "" {
		t.Fatalf("expected empty agent for garbage, got %q", a)
	}
}

func TestChatCompletionsURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/v1/chat/completions", "https://api.openai.com/v1/chat/completions"},
		{"https://open.bigmodel.cn/api/paas/v4/", "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		{"https://example.com/v1", "https://example.com/v1/chat/completions"},
		{"https://example.com", "https://example.com/v1/chat/completions"},
	}
	for _, c := range cases {
		if got := chatCompletionsURL(c.in); got != c.want {
			t.Errorf("chatCompletionsURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

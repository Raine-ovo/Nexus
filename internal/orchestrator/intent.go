package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/rainea/nexus/configs"
)

const (
	// ruleConfidenceKeyword is assigned when a keyword rule matches.
	ruleConfidenceKeyword = 1.0
	// ruleConfidencePattern is assigned when only a regex pattern matches.
	ruleConfidencePattern = 0.92
	// minRuleConfidenceToSkipLLM is the bar above which Route skips the LLM phase.
	minRuleConfidenceToSkipLLM = 0.88
)

// IntentRouter classifies user intent and maps it to an agent.
type IntentRouter struct {
	registry *AgentRegistry
	mu       sync.RWMutex
	rules    []compiledRoutingRule
	fallback string // default agent name when classification is ambiguous

	modelCfg configs.ModelConfig
	httpDo   func(*http.Request) (*http.Response, error) // injectable for tests
}

type compiledRoutingRule struct {
	RoutingRule
	regexes []*regexp.Regexp // aligned with Patterns; nil if compile failed
}

// RoutingRule is a single routing rule (keywords and/or regexes).
type RoutingRule struct {
	Keywords  []string `json:"keywords"`
	Patterns  []string `json:"patterns"` // regex patterns
	AgentName string   `json:"agent_name"`
	Priority  int      `json:"priority"`
}

// NewIntentRouter builds a router with default rules for the standard agent names.
func NewIntentRouter(registry *AgentRegistry) *IntentRouter {
	r := &IntentRouter{
		registry: registry,
		fallback: "planner",
		httpDo:   http.DefaultClient.Do,
	}
	r.mu.Lock()
	for _, rule := range DefaultRoutingRules() {
		r.addRuleLocked(rule)
	}
	r.mu.Unlock()
	return r
}

// ConfigureModel sets the model configuration used for LLM-based routing (OpenAI-compatible chat API).
func (r *IntentRouter) ConfigureModel(cfg configs.ModelConfig) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.modelCfg = cfg
	r.mu.Unlock()
}

// SetHTTPDo replaces the HTTP round-tripper used for LLM classification (nil restores http.DefaultClient.Do).
func (r *IntentRouter) SetHTTPDo(do func(*http.Request) (*http.Response, error)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if do == nil {
		r.httpDo = http.DefaultClient.Do
	} else {
		r.httpDo = do
	}
	r.mu.Unlock()
}

// SetFallback sets the agent name used when heuristics and LLM cannot pick a target.
func (r *IntentRouter) SetFallback(agentName string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.fallback = agentName
	r.mu.Unlock()
}

// Fallback returns the configured default agent name (may be empty before SetFallback).
func (r *IntentRouter) Fallback() string {
	if r == nil {
		return ""
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.fallback
}

// SnapshotRules returns a copy of configured routing rules in priority order (higher first).
func (r *IntentRouter) SnapshotRules() []RoutingRule {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RoutingRule, len(r.rules))
	for i, cr := range r.rules {
		out[i] = cr.RoutingRule
	}
	return out
}

// DefaultRoutingRules returns built-in rules for code_reviewer, knowledge, devops, and planner.
func DefaultRoutingRules() []RoutingRule {
	return []RoutingRule{
		{
			Keywords: []string{
				"review", "code", "diff", "pr", "pull request", "bug", "lint", "refactor",
				"merge request", "mr", "patch",
			},
			Patterns:  []string{`(?i)\b(code\s*review|pull\s*request|merge\s*request)\b`},
			AgentName: "code_reviewer",
			Priority:  100,
		},
		{
			Keywords: []string{
				"search", "find", "what is", "explain", "document", "documentation",
				"how does", "define", "meaning of", "lookup",
			},
			Patterns:  []string{`(?i)\b(wiki|readme|docs?)\b`},
			AgentName: "knowledge",
			Priority:  90,
		},
		{
			Keywords: []string{
				"deploy", "monitor", "log", "restart", "health", "pod", "cluster",
				"pipeline", "ci", "cd", "kubernetes", "k8s", "tce", "rollback",
			},
			Patterns:  []string{`(?i)\b(slo|sli|on-?call|incident)\b`},
			AgentName: "devops",
			Priority:  80,
		},
		{
			Keywords: []string{
				"plan", "task", "break down", "schedule", "project", "milestone",
				"roadmap", "backlog", "sprint", "estimate",
			},
			Patterns:  []string{`(?i)\b(work\s*breakdown|wbs)\b`},
			AgentName: "planner",
			Priority:  70,
		},
	}
}

// AddRule appends a routing rule. Invalid regex patterns are skipped (rule still applies for keywords).
func (r *IntentRouter) AddRule(rule RoutingRule) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.addRuleLocked(rule)
}

func (r *IntentRouter) addRuleLocked(rule RoutingRule) {
	cr := compiledRoutingRule{RoutingRule: rule}
	if len(rule.Patterns) > 0 {
		cr.regexes = make([]*regexp.Regexp, len(rule.Patterns))
		for i, p := range rule.Patterns {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			c, err := regexp.Compile(p)
			if err != nil {
				cr.regexes[i] = nil
				continue
			}
			cr.regexes[i] = c
		}
	}
	r.rules = append(r.rules, cr)
	sort.SliceStable(r.rules, func(i, j int) bool {
		if r.rules[i].Priority != r.rules[j].Priority {
			return r.rules[i].Priority > r.rules[j].Priority
		}
		return r.rules[i].AgentName < r.rules[j].AgentName
	})
}

// Route uses a two-phase approach: rule matching, then LLM classification if needed.
func (r *IntentRouter) Route(ctx context.Context, input string) (string, float64, error) {
	if r == nil {
		return "", 0, fmt.Errorf("orchestrator: nil IntentRouter")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return r.resolveFallback(0)
	}

	agent, conf, matched := r.matchRules(trimmed)
	if matched && conf >= minRuleConfidenceToSkipLLM {
		if err := r.ensureRegistered(agent); err != nil {
			// Fall through to LLM if configured, else fallback
			if !r.modelConfigured() {
				return r.resolveFallback(0.4)
			}
		} else {
			return agent, conf, nil
		}
	}

	if !r.modelConfigured() {
		if matched {
			if err := r.ensureRegistered(agent); err == nil {
				return agent, conf, nil
			}
		}
		return r.resolveFallback(0.35)
	}

	llmAgent, llmConf, err := r.RouteWithLLM(ctx, trimmed)
	if err != nil {
		if matched {
			if regErr := r.ensureRegistered(agent); regErr == nil {
				return agent, conf * 0.85, nil
			}
		}
		fb, c, fbErr := r.resolveFallback(0.25)
		if fbErr != nil {
			return "", 0, fmt.Errorf("orchestrator: route: %w (llm: %v)", fbErr, err)
		}
		return fb, c, nil
	}
	return llmAgent, llmConf, nil
}

func (r *IntentRouter) modelConfigured() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return strings.TrimSpace(r.modelCfg.APIKey) != ""
}

func (r *IntentRouter) matchRules(input string) (agent string, confidence float64, matched bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lower := strings.ToLower(input)
	bestConf := 0.0
	bestAgent := ""

	for _, rule := range r.rules {
		if rule.AgentName == "" {
			continue
		}
		kwHit := false
		for _, kw := range rule.Keywords {
			kw = strings.TrimSpace(strings.ToLower(kw))
			if kw == "" {
				continue
			}
			if strings.Contains(lower, kw) {
				kwHit = true
				break
			}
		}
		reHit := false
		for _, rx := range rule.regexes {
			if rx == nil {
				continue
			}
			if rx.MatchString(input) {
				reHit = true
				break
			}
		}
		if !kwHit && !reHit {
			continue
		}
		conf := ruleConfidencePattern
		if kwHit {
			conf = ruleConfidenceKeyword
		}
		if conf > bestConf {
			bestConf = conf
			bestAgent = rule.AgentName
			matched = true
		}
	}
	return bestAgent, bestConf, matched
}

func (r *IntentRouter) ensureRegistered(name string) error {
	if r.registry == nil {
		return fmt.Errorf("orchestrator: nil registry")
	}
	if _, ok := r.registry.Get(name); !ok {
		return fmt.Errorf("orchestrator: agent %q not registered", name)
	}
	return nil
}

func (r *IntentRouter) resolveFallback(conf float64) (string, float64, error) {
	r.mu.RLock()
	fb := r.fallback
	reg := r.registry
	r.mu.RUnlock()

	if fb == "" {
		return "", 0, fmt.Errorf("orchestrator: no fallback agent configured")
	}
	if reg == nil {
		return "", 0, fmt.Errorf("orchestrator: nil registry")
	}
	if _, ok := reg.Get(fb); !ok {
		names := reg.Names()
		if len(names) == 1 {
			return names[0], conf, nil
		}
		if len(names) > 0 {
			sort.Strings(names)
			return names[0], conf * 0.8, nil
		}
		return "", 0, fmt.Errorf("orchestrator: fallback agent %q not registered", fb)
	}
	return fb, conf, nil
}

// RouteWithLLM asks the configured model to pick an agent. Confidence is parsed when present; otherwise 0.82.
func (r *IntentRouter) RouteWithLLM(ctx context.Context, input string) (string, float64, error) {
	if r == nil {
		return "", 0, fmt.Errorf("orchestrator: nil IntentRouter")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !r.modelConfigured() {
		return r.resolveFallback(0.3)
	}

	r.mu.RLock()
	cfg := r.modelCfg
	reg := r.registry
	r.mu.RUnlock()

	prompt := r.buildClassificationPrompt(reg, input)
	text, err := completeChat(ctx, cfg, r.httpDo, prompt, 512)
	if err != nil {
		return "", 0, err
	}
	agent, conf := parseClassificationResponse(text)
	if agent == "" {
		return r.resolveFallback(0.4)
	}
	if err := r.ensureRegistered(agent); err != nil {
		fb, c, fbErr := r.resolveFallback(0.35)
		if fbErr != nil {
			return "", 0, fmt.Errorf("%w; llm picked unknown %q", fbErr, agent)
		}
		return fb, c, nil
	}
	if conf <= 0 {
		conf = 0.82
	}
	return agent, conf, nil
}

func (r *IntentRouter) buildClassificationPrompt(reg *AgentRegistry, userInput string) string {
	r.mu.RLock()
	fb := r.fallback
	r.mu.RUnlock()
	return buildAgentSelectionPrompt(reg, userInput, classificationInstruction, fb)
}

const classificationInstruction = `You are an intent router. Choose exactly one agent_name from the list that should handle the user's message.
Respond with a single JSON object only, no markdown fences, in this form:
{"agent_name":"<name>","confidence":0.0-1.0,"reason":"one short phrase"}
If nothing fits, use the fallback agent_name from the list marked as default.`

// buildAgentSelectionPrompt lists agents and appends routing instructions (shared with Supervisor).
func buildAgentSelectionPrompt(reg *AgentRegistry, userInput string, instruction string, defaultAgentHint string) string {
	var b strings.Builder
	b.WriteString(instruction)
	b.WriteString("\n\n## Available agents\n")
	if reg == nil || reg.Count() == 0 {
		b.WriteString("(none registered)\n")
	} else {
		for _, info := range reg.ListInfo() {
			b.WriteString(fmt.Sprintf("- name: %s\n  description: %s\n", info.Name, info.Description))
			if len(info.Capabilities) > 0 {
				b.WriteString(fmt.Sprintf("  capabilities: %s\n", strings.Join(info.Capabilities, ", ")))
			}
		}
	}
	if da := strings.TrimSpace(defaultAgentHint); da != "" {
		b.WriteString(fmt.Sprintf("\nDefault fallback agent (use when intent is unclear): %s\n", da))
	}
	b.WriteString("\n## User message\n")
	b.WriteString(userInput)
	b.WriteString("\n")
	return b.String()
}

type classificationJSON struct {
	AgentName  string  `json:"agent_name"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

func parseClassificationResponse(raw string) (agent string, confidence float64) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", 0
	}
	// Strip markdown code fences if present
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSpace(s)
	if idx := strings.LastIndex(s, "```"); idx >= 0 {
		s = strings.TrimSpace(s[:idx])
	}

	var cj classificationJSON
	if err := json.Unmarshal([]byte(s), &cj); err == nil && cj.AgentName != "" {
		return strings.TrimSpace(cj.AgentName), cj.Confidence
	}

	// Line-oriented fallback: AGENT: name
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "agent_name:") {
			return strings.TrimSpace(line[len("agent_name:"):]), 0.75
		}
		if strings.HasPrefix(lower, "agent:") {
			return strings.TrimSpace(line[len("agent:"):]), 0.75
		}
	}
	return "", 0
}

// --- OpenAI-compatible chat completion (shared by IntentRouter and Supervisor) ---

func effectiveMaxTokens(cfg configs.ModelConfig, requested int) int {
	if requested <= 0 {
		requested = 512
	}
	if cfg.MaxTokens > 0 && requested > cfg.MaxTokens {
		return cfg.MaxTokens
	}
	return requested
}

func completeChat(ctx context.Context, cfg configs.ModelConfig, doer func(*http.Request) (*http.Response, error), userPrompt string, maxTokens int) (string, error) {
	if doer == nil {
		doer = http.DefaultClient.Do
	}
	maxTokens = effectiveMaxTokens(cfg, maxTokens)
	model := strings.TrimSpace(cfg.ModelName)
	if model == "" {
		model = "gpt-4o"
	}
	temp := cfg.Temperature
	if temp <= 0 {
		temp = 0.3
	}

	url := chatCompletionsURL(cfg.BaseURL)
	body := map[string]interface{}{
		"model":       model,
		"temperature": temp,
		"max_tokens":  maxTokens,
		"messages": []map[string]string{
			{"role": "system", "content": "You are a precise routing assistant. Output only valid JSON when asked."},
			{"role": "user", "content": userPrompt},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("orchestrator: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("orchestrator: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.APIKey))

	resp, err := doer(req)
	if err != nil {
		return "", fmt.Errorf("orchestrator: http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", fmt.Errorf("orchestrator: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("orchestrator: chat completion status %d: %s", resp.StatusCode, truncateForErr(respBody, 512))
	}

	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return "", fmt.Errorf("orchestrator: decode response: %w", err)
	}
	if envelope.Error != nil && envelope.Error.Message != "" {
		return "", fmt.Errorf("orchestrator: api error: %s", envelope.Error.Message)
	}
	if len(envelope.Choices) == 0 || strings.TrimSpace(envelope.Choices[0].Message.Content) == "" {
		return "", errors.New("orchestrator: empty completion")
	}
	return strings.TrimSpace(envelope.Choices[0].Message.Content), nil
}

func chatCompletionsURL(base string) string {
	b := strings.TrimSuffix(strings.TrimSpace(base), "/")
	if b == "" {
		return "https://api.openai.com/v1/chat/completions"
	}
	if strings.HasSuffix(b, "/chat/completions") {
		return b
	}
	if strings.HasSuffix(b, "/v1") || strings.HasSuffix(b, "/v4") {
		return b + "/chat/completions"
	}
	return b + "/v1/chat/completions"
}

func truncateForErr(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

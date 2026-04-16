package reflection

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rainea/nexus/internal/core"
)

// Prospector performs pre-execution critique inspired by PreFlect (ICML 2026).
// It analyzes the task BEFORE execution, injects past failure lessons, and identifies
// potential risks — particularly valuable for irreversible operations (file deletion,
// database writes, shell commands) where retrospective correction is insufficient.
type Prospector struct {
	model core.ChatModel
	obs   core.Observer
}

// NewProspector constructs a prospector.
func NewProspector(model core.ChatModel, obs core.Observer) *Prospector {
	return &Prospector{model: model, obs: obs}
}

// Critique generates a pre-execution analysis of the task, incorporating relevant
// past reflections. Returns a ProspectiveCritique that the engine injects into
// the agent's enriched input.
func (p *Prospector) Critique(ctx context.Context, task string, history []Reflection) (ProspectiveCritique, error) {
	if p.model == nil || len(history) == 0 {
		return ProspectiveCritique{}, nil
	}

	prompt := p.buildCritiquePrompt(task, history)
	resp, err := p.model.Generate(ctx, prospectSystemPrompt, toMessages(prompt), nil)
	if err != nil {
		if p.obs != nil {
			p.obs.Warn("reflection/prospector: critique call failed", "err", err.Error())
		}
		return p.fallbackCritique(history), nil
	}

	critique, parseErr := parseCritiqueResponse(resp.Content)
	if parseErr != nil {
		if p.obs != nil {
			p.obs.Warn("reflection/prospector: parse failed, using fallback", "err", parseErr.Error())
		}
		return p.fallbackCritique(history), nil
	}
	return critique, nil
}

const prospectSystemPrompt = `You are a prospective reflection specialist. BEFORE an AI agent executes a task, you analyze it for potential pitfalls by drawing on lessons from past failures.

Your job:
1. Identify 1-3 specific RISKS this task might encounter based on historical failure patterns.
2. Provide 1-3 actionable SUGGESTIONS to avoid those risks.
3. Write a concise INJECTED paragraph (2-4 sentences) that should be prepended to the agent's input to guide it away from known failure modes.

Pay special attention to:
- Irreversible operations (file deletion, database mutations, shell commands)
- Common parameter format errors
- Missing validation or edge cases

Respond ONLY with JSON:
{
  "risks": ["risk1", "risk2"],
  "suggestions": ["suggestion1", "suggestion2"],
  "injected": "Concise guidance paragraph for the agent..."
}`

func (p *Prospector) buildCritiquePrompt(task string, history []Reflection) string {
	var b strings.Builder
	b.WriteString("## Upcoming Task\n")
	if len(task) > 3000 {
		b.WriteString(task[:3000])
		b.WriteString("\n... [truncated]")
	} else {
		b.WriteString(task)
	}

	b.WriteString("\n\n## Relevant Past Failure Patterns\n")
	n := len(history)
	if n > 8 {
		n = 8
	}
	for i := 0; i < n; i++ {
		h := history[i]
		b.WriteString(fmt.Sprintf("- [%s/%s] Pattern: %s | Insight: %s | Fix: %s\n",
			h.Level, h.TaskType, h.ErrorPattern, h.Insight, h.Suggestion))
	}
	return b.String()
}

func parseCritiqueResponse(content string) (ProspectiveCritique, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		var inner []string
		for _, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "```") {
				continue
			}
			inner = append(inner, l)
		}
		content = strings.Join(inner, "\n")
	}

	var raw struct {
		Risks       []string `json:"risks"`
		Suggestions []string `json:"suggestions"`
		Injected    string   `json:"injected"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return ProspectiveCritique{}, fmt.Errorf("json unmarshal: %w", err)
	}
	return ProspectiveCritique{
		Risks:       raw.Risks,
		Suggestions: raw.Suggestions,
		Injected:    raw.Injected,
	}, nil
}

func (p *Prospector) fallbackCritique(history []Reflection) ProspectiveCritique {
	var suggestions []string
	seen := make(map[string]bool)
	for _, h := range history {
		if h.Suggestion != "" && !seen[h.Suggestion] {
			suggestions = append(suggestions, h.Suggestion)
			seen[h.Suggestion] = true
		}
		if len(suggestions) >= 3 {
			break
		}
	}
	var injected string
	if len(suggestions) > 0 {
		injected = "Based on past experience: " + strings.Join(suggestions, "; ") + "."
	}
	return ProspectiveCritique{
		Suggestions: suggestions,
		Injected:    injected,
	}
}

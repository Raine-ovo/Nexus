package reflection

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rainea/nexus/internal/core"
)

// Reflector generates structured reflections from failed attempts, classifying them
// into micro/meso/macro levels following the SAMULE (EMNLP 2025) three-level taxonomy.
type Reflector struct {
	model core.ChatModel
	obs   core.Observer
}

// NewReflector constructs a reflector with the given model and optional observer.
func NewReflector(model core.ChatModel, obs core.Observer) *Reflector {
	return &Reflector{model: model, obs: obs}
}

// Reflect analyzes a failed attempt and generates a classified reflection.
func (r *Reflector) Reflect(ctx context.Context, input ReflectInput) (Reflection, error) {
	if r.model == nil {
		return r.fallbackReflection(input), nil
	}

	prompt := r.buildReflectPrompt(input)
	resp, err := r.model.Generate(ctx, reflectSystemPrompt, toMessages(prompt), nil)
	if err != nil {
		if r.obs != nil {
			r.obs.Warn("reflection/reflector: LLM call failed, using fallback", "err", err.Error())
		}
		return r.fallbackReflection(input), nil
	}

	ref, parseErr := r.parseReflectResponse(resp.Content)
	if parseErr != nil {
		if r.obs != nil {
			r.obs.Warn("reflection/reflector: parse failed, using fallback", "err", parseErr.Error())
		}
		return r.fallbackReflection(input), nil
	}

	ref.ID = uuid.NewString()
	ref.Attempt = input.Attempt
	ref.Score = input.Eval.Score
	ref.CreatedAt = time.Now().UTC()
	return ref, nil
}

const reflectSystemPrompt = `You are a reflection specialist that analyzes failed AI agent attempts. Given a task, the agent's output, and evaluation feedback, produce a structured reflection.

Classify the reflection into ONE of three levels:
- **micro**: A specific error in THIS attempt's execution (wrong parameter, missed step, logic flaw)
- **meso**: A recurring pattern across attempts of THIS task type (e.g., "file operations often fail due to path issues")
- **macro**: A transferable insight applicable to OTHER task types (e.g., "always validate input format before processing")

Choose "micro" for first failures, "meso" when the error pattern has appeared before in the history, and "macro" when the insight generalizes beyond the current task.

Respond ONLY with a JSON object:
{
  "level": "micro|meso|macro",
  "task_type": "<category: coding|qa|search|planning|tool_use|general>",
  "error_pattern": "<concise name for the failure mode, e.g. 'missing_null_check'>",
  "insight": "<what went wrong and why, 1-2 sentences>",
  "suggestion": "<specific actionable fix for the next attempt, 1-2 sentences>"
}`

func (r *Reflector) buildReflectPrompt(input ReflectInput) string {
	var b strings.Builder
	b.WriteString("## Task\n")
	b.WriteString(input.Task)
	b.WriteString("\n\n## Agent Output (attempt ")
	b.WriteString(fmt.Sprintf("%d", input.Attempt+1))
	b.WriteString(")\n")
	if len(input.Output) > 3000 {
		b.WriteString(input.Output[:3000])
		b.WriteString("\n... [truncated]")
	} else {
		b.WriteString(input.Output)
	}
	b.WriteString("\n\n## Evaluation\n")
	b.WriteString(fmt.Sprintf("Score: %.2f | Reason: %s\n", input.Eval.Score, input.Eval.Reason))
	for dim, val := range input.Eval.Dimensions {
		b.WriteString(fmt.Sprintf("  %s: %.2f\n", dim, val))
	}

	if len(input.History) > 0 {
		b.WriteString("\n## Prior Reflections (from past attempts)\n")
		n := len(input.History)
		if n > 5 {
			n = 5
		}
		for i := 0; i < n; i++ {
			h := input.History[i]
			b.WriteString(fmt.Sprintf("- [%s] %s: %s → %s\n", h.Level, h.ErrorPattern, h.Insight, h.Suggestion))
		}
	}
	return b.String()
}

func (r *Reflector) parseReflectResponse(content string) (Reflection, error) {
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
		Level        string `json:"level"`
		TaskType     string `json:"task_type"`
		ErrorPattern string `json:"error_pattern"`
		Insight      string `json:"insight"`
		Suggestion   string `json:"suggestion"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return Reflection{}, fmt.Errorf("json unmarshal: %w", err)
	}

	level := LevelMicro
	switch strings.ToLower(raw.Level) {
	case "meso":
		level = LevelMeso
	case "macro":
		level = LevelMacro
	}

	return Reflection{
		Level:        level,
		TaskType:     raw.TaskType,
		ErrorPattern: raw.ErrorPattern,
		Insight:      raw.Insight,
		Suggestion:   raw.Suggestion,
	}, nil
}

func (r *Reflector) fallbackReflection(input ReflectInput) Reflection {
	return Reflection{
		ID:           uuid.NewString(),
		Level:        LevelMicro,
		TaskType:     "general",
		ErrorPattern: "unspecified_failure",
		Insight:      fmt.Sprintf("Attempt %d scored %.2f: %s", input.Attempt+1, input.Eval.Score, input.Eval.Reason),
		Suggestion:   "Review the output for accuracy and completeness; address the evaluation feedback.",
		Attempt:      input.Attempt,
		Score:        input.Eval.Score,
		CreatedAt:    time.Now().UTC(),
	}
}

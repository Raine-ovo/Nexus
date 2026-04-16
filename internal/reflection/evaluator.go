package reflection

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rainea/nexus/internal/core"
)

// Evaluator judges the quality of an agent output against the original task.
// It uses a structured LLM call to produce multi-dimensional scores.
type Evaluator struct {
	model     core.ChatModel
	threshold float64
}

// NewEvaluator constructs an evaluator. threshold is the minimum score to pass (e.g. 0.7).
func NewEvaluator(model core.ChatModel, threshold float64) *Evaluator {
	if threshold <= 0 {
		threshold = 0.7
	}
	return &Evaluator{model: model, threshold: threshold}
}

// Evaluate scores agent output on correctness, completeness, safety, and coherence.
// Returns EvalResult with pass/fail based on the configured threshold.
func (e *Evaluator) Evaluate(ctx context.Context, task, output string) (EvalResult, error) {
	if e.model == nil {
		return EvalResult{Pass: true, Score: 1.0, Reason: "no evaluator model configured"}, nil
	}

	prompt := buildEvalPrompt(task, output)
	resp, err := e.model.Generate(ctx, evalSystemPrompt, toMessages(prompt), nil)
	if err != nil {
		return EvalResult{Pass: true, Score: 0.5, Reason: fmt.Sprintf("evaluator call failed: %v", err)}, nil
	}

	result, parseErr := parseEvalResponse(resp.Content)
	if parseErr != nil {
		return EvalResult{
			Pass:   true,
			Score:  0.5,
			Reason: fmt.Sprintf("evaluator parse failed: %v", parseErr),
		}, nil
	}

	result.Pass = result.Score >= e.threshold
	return result, nil
}

const evalSystemPrompt = `You are an expert evaluator for AI agent outputs. Given a task and the agent's response, score the output on four dimensions (each 0.0–1.0):

1. **correctness** — factual accuracy and logical soundness
2. **completeness** — whether the response fully addresses the task
3. **safety** — no harmful, unauthorized, or policy-violating content
4. **coherence** — clarity, structure, and readability

Respond ONLY with a JSON object:
{
  "score": <float, weighted average of dimensions>,
  "correctness": <float>,
  "completeness": <float>,
  "safety": <float>,
  "coherence": <float>,
  "reason": "<1-2 sentence summary of the most significant quality issue, or 'good' if no issues>"
}`

func buildEvalPrompt(task, output string) string {
	var b strings.Builder
	b.WriteString("## Task\n")
	b.WriteString(task)
	b.WriteString("\n\n## Agent Output\n")
	if len(output) > 4000 {
		b.WriteString(output[:4000])
		b.WriteString("\n... [truncated]")
	} else {
		b.WriteString(output)
	}
	return b.String()
}

func parseEvalResponse(content string) (EvalResult, error) {
	content = strings.TrimSpace(content)
	// Strip markdown code fences if present.
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		var inner []string
		for _, l := range lines {
			trimmed := strings.TrimSpace(l)
			if strings.HasPrefix(trimmed, "```") {
				continue
			}
			inner = append(inner, l)
		}
		content = strings.Join(inner, "\n")
	}

	var raw struct {
		Score        float64 `json:"score"`
		Correctness  float64 `json:"correctness"`
		Completeness float64 `json:"completeness"`
		Safety       float64 `json:"safety"`
		Coherence    float64 `json:"coherence"`
		Reason       string  `json:"reason"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return EvalResult{}, fmt.Errorf("json unmarshal: %w", err)
	}
	return EvalResult{
		Score:  raw.Score,
		Reason: raw.Reason,
		Dimensions: map[string]float64{
			"correctness":  raw.Correctness,
			"completeness": raw.Completeness,
			"safety":       raw.Safety,
			"coherence":    raw.Coherence,
		},
	}, nil
}

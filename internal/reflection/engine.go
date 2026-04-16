package reflection

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rainea/nexus/internal/core"
)

// Engine orchestrates the three-phase reflection cycle:
//
//	Phase 1 — Prospective: inject past lessons and pre-critique risks (PreFlect-inspired)
//	Phase 2 — Execute + Evaluate: run the agent, then score the output
//	Phase 3 — Retrospective: generate micro/meso/macro reflection and store (SAMULE-inspired)
//
// The engine wraps around Agent.Run without modifying the core ReAct loop, preserving
// the separation of concerns between infrastructure recovery (RecoveryManager) and
// semantic-level self-improvement (ReflectionEngine).
type Engine struct {
	memory     *ReflectionMemory
	evaluator  *Evaluator
	reflector  *Reflector
	prospector *Prospector
	obs        core.Observer

	maxAttempts    int
	threshold      float64
	enableProspect bool
}

// EngineConfig holds reflection engine tuning knobs.
type EngineConfig struct {
	MaxAttempts    int
	Threshold      float64
	EnableProspect bool
}

// NewEngine constructs a reflection engine.
func NewEngine(
	memory *ReflectionMemory,
	evaluator *Evaluator,
	reflector *Reflector,
	prospector *Prospector,
	obs core.Observer,
	cfg EngineConfig,
) *Engine {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.Threshold <= 0 {
		cfg.Threshold = 0.7
	}
	return &Engine{
		memory:         memory,
		evaluator:      evaluator,
		reflector:      reflector,
		prospector:     prospector,
		obs:            obs,
		maxAttempts:    cfg.MaxAttempts,
		threshold:      cfg.Threshold,
		enableProspect: cfg.EnableProspect,
	}
}

// RunWithReflection executes an agent with the full three-phase reflection cycle.
// It is designed to wrap Agent.Run at the Supervisor or individual agent level.
func (e *Engine) RunWithReflection(ctx context.Context, agent core.Agent, input string) (string, error) {
	if e == nil {
		return agent.Run(ctx, input)
	}

	// Phase 1: Prospective reflection — pull relevant past lessons and pre-critique.
	enrichedInput := input
	if e.enableProspect && e.prospector != nil && e.memory != nil {
		memories := e.memory.SearchRelevant(input, 5, "")
		if len(memories) > 0 {
			critique, err := e.prospector.Critique(ctx, input, memories)
			if err == nil && critique.Injected != "" {
				enrichedInput = e.enrichInput(input, critique, memories)
				if e.obs != nil {
					e.obs.Info("reflection/engine: prospective critique injected",
						"risks", len(critique.Risks), "memories_used", len(memories))
				}
			}
		}
	}

	var attempts []Attempt
	var lastOutput string

	for attempt := 0; attempt < e.maxAttempts; attempt++ {
		// Phase 2: Execute + Evaluate.
		output, runErr := agent.Run(ctx, enrichedInput)
		if runErr != nil {
			return "", fmt.Errorf("reflection/engine: agent run (attempt %d): %w", attempt+1, runErr)
		}
		lastOutput = output

		evalResult := EvalResult{Pass: true, Score: 1.0, Reason: "evaluation skipped"}
		if e.evaluator != nil {
			var evalErr error
			evalResult, evalErr = e.evaluator.Evaluate(ctx, input, output)
			if evalErr != nil && e.obs != nil {
				e.obs.Warn("reflection/engine: evaluation error", "attempt", attempt+1, "err", evalErr.Error())
			}
		}

		attempts = append(attempts, Attempt{
			Index:  attempt,
			Output: truncate(output, 500),
			Eval:   evalResult,
			At:     time.Now(),
		})

		if e.obs != nil {
			e.obs.Info("reflection/engine: attempt evaluated",
				"attempt", attempt+1, "score", evalResult.Score,
				"pass", evalResult.Pass, "reason", evalResult.Reason)
		}

		if evalResult.Pass {
			// On success after retries, store a positive macro insight.
			if attempt > 0 && e.memory != nil && e.reflector != nil {
				successRef := Reflection{
					ID:           fmt.Sprintf("success-%d-%d", time.Now().UnixMilli(), attempt),
					Level:        LevelMacro,
					TaskType:     "general",
					ErrorPattern: "recovery_success",
					Insight:      fmt.Sprintf("Solved after %d attempts; final score %.2f", attempt+1, evalResult.Score),
					Suggestion:   fmt.Sprintf("The fix that worked: addressed '%s'", evalResult.Reason),
					Attempt:      attempt,
					Score:        evalResult.Score,
					CreatedAt:    time.Now().UTC(),
				}
				_ = e.memory.Store(successRef)
			}
			return output, nil
		}

		// Phase 3: Retrospective reflection — analyze failure, classify, and store.
		if e.reflector != nil && e.memory != nil {
			pastReflections := e.memory.SearchRelevant(input, 5, "")
			ref, refErr := e.reflector.Reflect(ctx, ReflectInput{
				Task:    input,
				Output:  output,
				Eval:    evalResult,
				History: pastReflections,
				Attempt: attempt,
			})
			if refErr == nil {
				_ = e.memory.Store(ref)
				enrichedInput = e.enrichWithReflection(input, ref, pastReflections)
				if e.obs != nil {
					e.obs.Info("reflection/engine: reflection stored",
						"level", ref.Level, "pattern", ref.ErrorPattern,
						"attempt", attempt+1)
				}
			} else if e.obs != nil {
				e.obs.Warn("reflection/engine: reflection generation failed", "err", refErr.Error())
			}
		}
	}

	if e.obs != nil {
		e.obs.Warn("reflection/engine: exhausted all attempts",
			"max_attempts", e.maxAttempts, "last_output_len", len(lastOutput))
	}
	return lastOutput, nil
}

// Memory returns the reflection memory for external access (prompt assembly, etc.).
func (e *Engine) Memory() *ReflectionMemory {
	if e == nil {
		return nil
	}
	return e.memory
}

// enrichInput prepends prospective critique and relevant past lessons to the user input.
func (e *Engine) enrichInput(original string, critique ProspectiveCritique, memories []Reflection) string {
	var b strings.Builder

	if critique.Injected != "" {
		b.WriteString("[Prospective guidance] ")
		b.WriteString(critique.Injected)
		b.WriteString("\n\n")
	}

	if len(memories) > 0 {
		b.WriteString("[Lessons from past attempts]\n")
		n := len(memories)
		if n > 3 {
			n = 3
		}
		for i := 0; i < n; i++ {
			m := memories[i]
			b.WriteString(fmt.Sprintf("- %s: %s\n", m.ErrorPattern, m.Suggestion))
		}
		b.WriteString("\n")
	}

	b.WriteString(original)
	return b.String()
}

// enrichWithReflection injects the latest reflection into the next attempt's input.
func (e *Engine) enrichWithReflection(original string, ref Reflection, history []Reflection) string {
	var b strings.Builder

	b.WriteString("[Self-reflection from previous attempt]\n")
	b.WriteString(fmt.Sprintf("Error pattern: %s\n", ref.ErrorPattern))
	b.WriteString(fmt.Sprintf("Insight: %s\n", ref.Insight))
	b.WriteString(fmt.Sprintf("Suggestion: %s\n", ref.Suggestion))

	if len(history) > 0 {
		b.WriteString("\n[Accumulated lessons]\n")
		n := len(history)
		if n > 3 {
			n = 3
		}
		for i := 0; i < n; i++ {
			h := history[i]
			b.WriteString(fmt.Sprintf("- [%s] %s → %s\n", h.Level, h.ErrorPattern, h.Suggestion))
		}
	}

	b.WriteString("\n")
	b.WriteString(original)
	return b.String()
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

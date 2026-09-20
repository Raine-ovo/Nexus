package eval

import (
	"context"
)

// Step is one recorded tool invocation with enough information to replay it.
type Step struct {
	ToolName string                 `json:"tool"`
	Args     map[string]interface{} `json:"args"`
}

// Trace is an ordered sequence of recorded steps (e.g. from one agent turn).
type Trace struct {
	ID    string `json:"id"`
	Steps []Step `json:"steps"`
}

// ReplayResult is the outcome of replaying one step.
type ReplayResult struct {
	Step   Step   `json:"step"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// Replayer re-executes recorded steps through an executor function.
type Replayer struct {
	// Exec runs a single tool with the given arguments.
	Exec func(ctx context.Context, tool string, args map[string]interface{}) (string, error)
}

// Replay re-runs every step in the trace and returns per-step results. A nil
// Exec makes each step fail with a descriptive error.
func (r *Replayer) Replay(ctx context.Context, tr Trace) []ReplayResult {
	out := make([]ReplayResult, 0, len(tr.Steps))
	for _, step := range tr.Steps {
		res := ReplayResult{Step: step}
		if r == nil || r.Exec == nil {
			res.Error = "no executor configured"
		} else {
			output, err := r.Exec(ctx, step.ToolName, step.Args)
			if err != nil {
				res.Error = err.Error()
			} else {
				res.Output = output
			}
		}
		out = append(out, res)
	}
	return out
}

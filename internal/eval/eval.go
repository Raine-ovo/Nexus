// Package eval provides a lightweight golden-task evaluation harness for
// measuring agent success rate, tool-call correctness, and latency without
// coupling to any specific agent implementation.
package eval

import (
	"context"
	"strings"
	"time"
)

// RunFunc executes one task input and returns the final output, the tool names
// invoked during execution, and any error.
type RunFunc func(ctx context.Context, input string) (output string, toolCalls []string, err error)

// Task is one golden evaluation case.
type Task struct {
	Name string `json:"name"`
	// Input is the user message fed to the agent.
	Input string `json:"input"`
	// ExpectContains lists substrings the final output must contain (empty = skip).
	ExpectContains []string `json:"expect_contains,omitempty"`
	// ExpectToolCalls lists tool names that must be invoked (checked when ToolCorrect is set).
	ExpectToolCalls []string `json:"expect_tool_calls,omitempty"`
}

// Result is the outcome for one task.
type Result struct {
	Task        string   `json:"task"`
	Passed      bool     `json:"passed"`
	Output      string   `json:"output,omitempty"`
	ToolCalls   []string `json:"tool_calls,omitempty"`
	LatencyMS   int64    `json:"latency_ms"`
	Error       string   `json:"error,omitempty"`
	FailReasons []string `json:"fail_reasons,omitempty"`
}

// Report aggregates results across tasks.
type Report struct {
	Results        []Result `json:"results"`
	Passed         int      `json:"passed"`
	Failed         int      `json:"failed"`
	Total          int      `json:"total"`
	SuccessRate    float64  `json:"success_rate"`
	TotalLatencyMS int64    `json:"total_latency_ms"`
}

// Runner evaluates golden tasks against a RunFunc.
type Runner struct {
	// ToolCorrect enables tool-call assertions (ExpectToolCalls).
	ToolCorrect bool
}

// NewRunner returns a runner with sensible defaults.
func NewRunner() *Runner { return &Runner{} }

// Run executes every task and returns an aggregate report.
func (r *Runner) Run(ctx context.Context, run RunFunc, tasks []Task) Report {
	rep := Report{Results: make([]Result, 0, len(tasks)), Total: len(tasks)}
	for _, task := range tasks {
		res := r.runOne(ctx, run, task)
		rep.Results = append(rep.Results, res)
		rep.TotalLatencyMS += res.LatencyMS
		if res.Passed {
			rep.Passed++
		} else {
			rep.Failed++
		}
	}
	if rep.Total > 0 {
		rep.SuccessRate = float64(rep.Passed) / float64(rep.Total)
	}
	return rep
}

func (r *Runner) runOne(ctx context.Context, run RunFunc, task Task) Result {
	start := time.Now()
	output, calls, err := run(ctx, task.Input)
	res := Result{
		Task:      task.Name,
		Output:    output,
		ToolCalls: calls,
		LatencyMS: time.Since(start).Milliseconds(),
	}
	if err != nil {
		res.Error = err.Error()
		res.FailReasons = append(res.FailReasons, "error: "+err.Error())
	}
	for _, want := range task.ExpectContains {
		if !strings.Contains(output, want) {
			res.FailReasons = append(res.FailReasons, "missing output substring: "+want)
		}
	}
	if r.ToolCorrect {
		for _, want := range task.ExpectToolCalls {
			if !containsString(calls, want) {
				res.FailReasons = append(res.FailReasons, "missing tool call: "+want)
			}
		}
	}
	res.Passed = len(res.FailReasons) == 0
	return res
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

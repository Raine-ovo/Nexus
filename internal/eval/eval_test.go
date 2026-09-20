package eval

import (
	"context"
	"errors"
	"testing"
)

func TestRunnerAggregatesPassFail(t *testing.T) {
	r := NewRunner()
	tasks := []Task{
		{Name: "greet", Input: "hi", ExpectContains: []string{"hello"}},
		{Name: "missing", Input: "x", ExpectContains: []string{"absent"}},
		{Name: "err", Input: "boom"},
	}
	rep := r.Run(context.Background(), func(ctx context.Context, input string) (string, []string, error) {
		switch input {
		case "hi":
			return "hello there", []string{"greet_tool"}, nil
		case "boom":
			return "", nil, errors.New("boom")
		default:
			return "other", nil, nil
		}
	}, tasks)

	if rep.Total != 3 || rep.Passed != 1 || rep.Failed != 2 {
		t.Fatalf("report = %+v", rep)
	}
	if rep.SuccessRate != 1.0/3.0 {
		t.Fatalf("success rate = %v", rep.SuccessRate)
	}
}

func TestRunnerToolCorrectness(t *testing.T) {
	r := NewRunner()
	r.ToolCorrect = true
	tasks := []Task{
		{Name: "needs tool", Input: "x", ExpectToolCalls: []string{"write_file"}},
	}
	rep := r.Run(context.Background(), func(ctx context.Context, input string) (string, []string, error) {
		return "ok", []string{"read_file"}, nil
	}, tasks)
	if rep.Results[0].Passed {
		t.Fatalf("expected failure for missing tool call, got %+v", rep.Results[0])
	}
}

package eval

import (
	"context"
	"testing"
)

func TestReplayerReplaysSteps(t *testing.T) {
	tr := Trace{
		ID: "trace-1",
		Steps: []Step{
			{ToolName: "read_file", Args: map[string]interface{}{"path": "a.txt"}},
			{ToolName: "write_file", Args: map[string]interface{}{"path": "b.txt", "content": "x"}},
		},
	}
	calls := 0
	r := &Replayer{Exec: func(ctx context.Context, tool string, args map[string]interface{}) (string, error) {
		calls++
		return tool + "-ok", nil
	}}

	results := r.Replay(context.Background(), tr)
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].Output != "read_file-ok" || results[1].Output != "write_file-ok" {
		t.Fatalf("results = %+v", results)
	}
	if calls != 2 {
		t.Fatalf("exec calls = %d, want 2", calls)
	}
}

func TestReplayerNoExecutor(t *testing.T) {
	r := &Replayer{}
	results := r.Replay(context.Background(), Trace{Steps: []Step{{ToolName: "x"}}})
	if len(results) != 1 || results[0].Error == "" {
		t.Fatalf("expected error without executor, got %+v", results)
	}
}

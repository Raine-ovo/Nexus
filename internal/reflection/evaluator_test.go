package reflection

import (
	"context"
	"strings"
	"testing"
)

func TestParseEvalResponsePlain(t *testing.T) {
	res, err := parseEvalResponse(`{"score":0.9,"correctness":0.9,"completeness":0.8,"safety":1.0,"coherence":0.9,"reason":"good"}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Score != 0.9 || res.Reason != "good" {
		t.Fatalf("parsed = %+v", res)
	}
	if res.Dimensions["safety"] != 1.0 {
		t.Fatalf("dimensions = %+v", res.Dimensions)
	}
}

func TestParseEvalResponseFenced(t *testing.T) {
	res, err := parseEvalResponse("```json\n{\"score\":0.5,\"correctness\":0.5,\"completeness\":0.5,\"safety\":0.5,\"coherence\":0.5,\"reason\":\"meh\"}\n```")
	if err != nil {
		t.Fatalf("parse fenced: %v", err)
	}
	if res.Score != 0.5 {
		t.Fatalf("parsed = %+v", res)
	}
}

func TestParseEvalResponseInvalid(t *testing.T) {
	if _, err := parseEvalResponse("not json"); err == nil {
		t.Fatalf("expected error for invalid json")
	}
}

func TestBuildEvalPromptTruncatesLongOutput(t *testing.T) {
	long := strings.Repeat("x", 5000)
	prompt := buildEvalPrompt("task", long)
	if strings.Contains(prompt, strings.Repeat("x", 4500)) {
		t.Fatalf("long output was not truncated")
	}
	if !strings.Contains(prompt, "[truncated]") {
		t.Fatalf("truncation marker missing")
	}
}

func TestEvaluatorWithoutModelPasses(t *testing.T) {
	e := NewEvaluator(nil, 0.7)
	res, err := e.Evaluate(context.Background(), "task", "output")
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !res.Pass || res.Score != 1.0 {
		t.Fatalf("expected pass with no model, got %+v", res)
	}
}

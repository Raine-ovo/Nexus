package reflection

import (
	"time"
)

// ReflectionLevel categorizes the granularity of a reflection insight, inspired by
// SAMULE (EMNLP 2025) three-level taxonomy: micro (single trajectory), meso (intra-task
// error patterns), and macro (cross-task transferable insights).
type ReflectionLevel string

const (
	LevelMicro ReflectionLevel = "micro" // single-trajectory error correction
	LevelMeso  ReflectionLevel = "meso"  // intra-task failure pattern taxonomy
	LevelMacro ReflectionLevel = "macro" // cross-task transferable insight
)

// Reflection is a structured self-critique stored in episodic memory.
type Reflection struct {
	ID           string          `yaml:"id" json:"id"`
	Level        ReflectionLevel `yaml:"level" json:"level"`
	TaskType     string          `yaml:"task_type" json:"task_type"`
	ErrorPattern string          `yaml:"error_pattern" json:"error_pattern"`
	Insight      string          `yaml:"insight" json:"insight"`
	Suggestion   string          `yaml:"suggestion" json:"suggestion"`
	Attempt      int             `yaml:"attempt" json:"attempt"`
	Score        float64         `yaml:"score" json:"score"`
	CreatedAt    time.Time       `yaml:"created_at" json:"created_at"`
}

// EvalResult holds the evaluator's structured judgment on an agent output.
type EvalResult struct {
	Pass       bool               `json:"pass"`
	Score      float64            `json:"score"`
	Reason     string             `json:"reason"`
	Dimensions map[string]float64 `json:"dimensions"`
}

// ProspectiveCritique is the pre-execution foresight produced by the Prospector.
type ProspectiveCritique struct {
	Risks       []string `json:"risks"`
	Suggestions []string `json:"suggestions"`
	Injected    string   `json:"injected"`
}

// ReflectInput bundles everything the Reflector needs to produce a Reflection.
type ReflectInput struct {
	Task    string
	Output  string
	Eval    EvalResult
	History []Reflection
	Attempt int
}

// Attempt records one generate–evaluate cycle for observability.
type Attempt struct {
	Index  int        `json:"index"`
	Output string     `json:"output"`
	Eval   EvalResult `json:"eval"`
	At     time.Time  `json:"at"`
}

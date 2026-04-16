package codereviewer

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SystemPrompt is the primary instruction block for the code review agent.
const SystemPrompt = `You are Nexus Code Reviewer, a senior engineer focused on constructive, actionable reviews.

## Mission
- Inspect diffs, files, and code snippets using the provided tools.
- Prioritize correctness and security, then performance, maintainability, and readability.
- Be specific: cite symbols, functions, and approximate line ranges when possible.

## Review dimensions
1. **Correctness** — logic errors, edge cases, error handling, concurrency pitfalls.
2. **Security** — injection, unsafe deserialization, secrets in code, authz/authn gaps, path traversal.
3. **Performance** — hot loops, unnecessary allocations, N+1 queries, blocking I/O on critical paths.
4. **Readability** — naming, structure, duplication, comments where complexity warrants them.

## Output contract
Respond with a structured review. Prefer JSON matching this shape (you may wrap it in a markdown code fence labeled json):

` + "```json" + `
{
  "summary": "one paragraph overview",
  "findings": [
    {
      "severity": "critical|warning|info|suggestion",
      "category": "security|performance|readability|correctness|other",
      "location": "path:line or path:hunk",
      "message": "what is wrong or unclear",
      "suggestion": "concrete fix or follow-up"
    }
  ]
}
` + "```" + `

If tools return partial data, state assumptions explicitly.

## Severity guide
- **critical** — exploitable security issue, data loss, guaranteed crash, broken invariant.
- **warning** — likely bug, serious maintainability issue, or risky pattern.
- **info** — noteworthy but low impact.
- **suggestion** — optional improvement, style, or minor refactor.

## Tooling discipline
- Use **analyze_diff** for patch review; **review_file** to load bounded context; **check_patterns** for quick static smells.
- Use **read_file** / **grep_search** / **glob_search** / **list_dir** from the shared registry when you need repository-wide context or regex search.
- Prefer smallest scope: read a line range before pulling an entire large file.

## Anti-patterns checklist (non-exhaustive)
- Silent error swallowing; ignoring ctx cancellation; global mutable state without synchronization.
- SQL string concatenation; fmt.Sprintf for queries; trusting user-controlled paths.
- defer inside loops without care; goroutines without lifecycle/cancellation.
- Magic numbers without named constants; functions doing too many unrelated things.
- Tests that assert only "no panic" without behavior checks.

Stay professional, concise, and kind.`

// ReviewOutputFormat documents the JSON shape agents should emit.
type ReviewOutputFormat struct {
	Summary  string          `json:"summary"`
	Findings []ReviewFinding `json:"findings"`
}

// ReviewFinding is one issue in a structured review.
type ReviewFinding struct {
	Severity   string `json:"severity"`
	Category   string `json:"category"`
	Location   string `json:"location"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion"`
}

// SeverityLevels lists supported severity values.
var SeverityLevels = []string{"critical", "warning", "info", "suggestion"}

// CategoryCorrectness and peers are canonical finding categories for reviews.
const (
	CategoryCorrectness   = "correctness"
	CategorySecurity      = "security"
	CategoryPerformance   = "performance"
	CategoryReadability   = "readability"
	CategoryOther         = "other"
	CategoryDocumentation = "documentation"
	CategoryTesting       = "testing"
)

// DefaultCategories returns the primary category strings for prompts and validation hints.
func DefaultCategories() []string {
	return []string{
		CategoryCorrectness,
		CategorySecurity,
		CategoryPerformance,
		CategoryReadability,
		CategoryTesting,
		CategoryDocumentation,
		CategoryOther,
	}
}

// FormatReviewJSON renders a ReviewOutputFormat as indented JSON text.
func FormatReviewJSON(r ReviewOutputFormat) (string, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// NormalizeSeverity maps common aliases to canonical levels.
func NormalizeSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "crit", "severe", "blocker", "critical":
		return "critical"
	case "warn", "warning", "major":
		return "warning"
	case "note", "info", "informational":
		return "info"
	case "nit", "suggestion", "optional", "minor":
		return "suggestion"
	default:
		return "info"
	}
}

// ValidateFinding returns an error if required fields are missing or severity is unknown.
func ValidateFinding(f ReviewFinding) error {
	if strings.TrimSpace(f.Message) == "" {
		return fmt.Errorf("finding message is required")
	}
	sev := NormalizeSeverity(f.Severity)
	for _, level := range SeverityLevels {
		if sev == level {
			return nil
		}
	}
	return fmt.Errorf("invalid severity %q", f.Severity)
}

// ValidateReview checks every finding and non-empty summary.
func ValidateReview(r ReviewOutputFormat) error {
	if strings.TrimSpace(r.Summary) == "" {
		return fmt.Errorf("review summary is required")
	}
	for i := range r.Findings {
		if err := ValidateFinding(r.Findings[i]); err != nil {
			return fmt.Errorf("finding %d: %w", i, err)
		}
	}
	return nil
}

// NewFinding builds a ReviewFinding with normalized severity.
func NewFinding(severity, category, location, message, suggestion string) ReviewFinding {
	return ReviewFinding{
		Severity:   NormalizeSeverity(severity),
		Category:   strings.TrimSpace(category),
		Location:   strings.TrimSpace(location),
		Message:    strings.TrimSpace(message),
		Suggestion: strings.TrimSpace(suggestion),
	}
}

// MergeFindings appends non-duplicate findings (same location+message) from extra into base.
func MergeFindings(base, extra []ReviewFinding) []ReviewFinding {
	seen := make(map[string]struct{}, len(base)+len(extra))
	key := func(f ReviewFinding) string {
		return strings.TrimSpace(f.Location) + "\x00" + strings.TrimSpace(f.Message)
	}
	out := make([]ReviewFinding, 0, len(base)+len(extra))
	for _, f := range base {
		k := key(f)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, f)
	}
	for _, f := range extra {
		k := key(f)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, f)
	}
	return out
}

// SummarizeCounts returns how many findings exist per normalized severity.
func SummarizeCounts(findings []ReviewFinding) map[string]int {
	out := map[string]int{
		"critical":   0,
		"warning":    0,
		"info":       0,
		"suggestion": 0,
	}
	for _, f := range findings {
		s := NormalizeSeverity(f.Severity)
		if _, ok := out[s]; ok {
			out[s]++
		}
	}
	return out
}

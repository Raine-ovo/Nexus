package permission

import (
	"path/filepath"
	"strings"
	"sync"
)

// RuleEngine manages allow/deny rules with pattern matching.
type RuleEngine struct {
	denyRules  []Rule
	allowRules []Rule
	mu         sync.RWMutex
}

// Rule describes a single permission rule.
type Rule struct {
	ID           string   `json:"id"`
	Type         string   `json:"type"` // "allow" or "deny"
	ToolNames    []string `json:"tool_names,omitempty"` // exact match or glob
	PathPatterns []string `json:"path_patterns,omitempty"` // file path glob
	Description  string   `json:"description"`
}

// NewRuleEngine creates an empty engine.
func NewRuleEngine() *RuleEngine {
	return &RuleEngine{}
}

// AddRule appends a rule to the appropriate list.
func (e *RuleEngine) AddRule(r Rule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch strings.ToLower(strings.TrimSpace(r.Type)) {
	case "deny":
		e.denyRules = append(e.denyRules, r)
	case "allow":
		e.allowRules = append(e.allowRules, r)
	default:
		return
	}
}

// RemoveRule deletes a rule by id from both lists.
func (e *RuleEngine) RemoveRule(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.denyRules = removeRuleByID(e.denyRules, id)
	e.allowRules = removeRuleByID(e.allowRules, id)
}

func removeRuleByID(rules []Rule, id string) []Rule {
	out := rules[:0]
	for _, r := range rules {
		if r.ID != id {
			out = append(out, r)
		}
	}
	return out
}

// MatchDeny returns true if any deny rule matches.
func (e *RuleEngine) MatchDeny(toolName string, input map[string]interface{}) (bool, string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, r := range e.denyRules {
		if ruleMatches(&r, toolName, input) {
			return true, r.ID
		}
	}
	return false, ""
}

// MatchAllow returns true if any allow rule matches.
func (e *RuleEngine) MatchAllow(toolName string, input map[string]interface{}) (bool, string) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, r := range e.allowRules {
		if ruleMatches(&r, toolName, input) {
			return true, r.ID
		}
	}
	return false, ""
}

func ruleMatches(r *Rule, toolName string, input map[string]interface{}) bool {
	hasTool := len(r.ToolNames) > 0
	hasPath := len(r.PathPatterns) > 0
	if !hasTool && !hasPath {
		return false
	}
	if hasTool {
		ok := false
		for _, pat := range r.ToolNames {
			if matchGlob(pat, toolName) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if !hasPath {
		return true
	}
	paths := extractPaths(input)
	for _, p := range paths {
		for _, pat := range r.PathPatterns {
			if matchGlob(pat, p) {
				return true
			}
		}
	}
	return false
}

func matchGlob(pattern, value string) bool {
	if pattern == "" {
		return false
	}
	if !strings.ContainsAny(pattern, "*?[\\") {
		return pattern == value
	}
	ok, err := filepath.Match(pattern, value)
	if err != nil {
		return false
	}
	return ok
}

func extractPaths(input map[string]interface{}) []string {
	keys := []string{"path", "file", "filepath", "target", "file_path", "filePath"}
	var out []string
	for _, k := range keys {
		if v, ok := input[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

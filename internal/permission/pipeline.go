package permission

import (
	"context"
	"fmt"
	"strings"

	"github.com/rainea/nexus/configs"
)

const (
	BehaviorAllow = "allow"
	BehaviorDeny  = "deny"
	BehaviorAsk   = "ask"
)

// Pipeline implements the 4-stage permission check.
type Pipeline struct {
	cfg     configs.PermissionConfig
	rules   *RuleEngine
	sandbox *PathSandbox
}

// Decision is the outcome of a permission evaluation.
type Decision struct {
	Behavior string // "allow", "deny", "ask"
	Reason   string
	Rule     string // which rule triggered
}

// NewPipeline builds a pipeline from config and fresh rule/sandbox instances.
func NewPipeline(cfg configs.PermissionConfig) *Pipeline {
	p := &Pipeline{
		cfg:     cfg,
		rules:   NewRuleEngine(),
		sandbox: NewPathSandbox(cfg.WorkspaceRoot, cfg.DangerousPatterns),
	}
	registerDefaultRules(p.rules)
	return p
}

// Rules exposes the underlying rule engine for registration.
func (p *Pipeline) Rules() *RuleEngine { return p.rules }

// Sandbox exposes path validation helpers.
func (p *Pipeline) Sandbox() *PathSandbox { return p.sandbox }

// Check runs the four-stage permission pipeline for a tool invocation.
//
// Stage 1: Deny rules (bypass-immune, checked first; even full_auto cannot override).
// Stage 2: Path sandbox (workspace boundary and dangerous patterns).
// Stage 3: Mode-based (full_auto allows all, manual asks all, semi_auto checks allow rules).
// Stage 4: Allow rules (pattern matching, semi_auto only).
// Stage 5: Ask user (default for unmatched in semi_auto).
func (p *Pipeline) Check(toolName string, toolInput map[string]interface{}) Decision {
	if toolInput == nil {
		toolInput = map[string]interface{}{}
	}

	// Stage 1 — deny rules (before sandbox so explicit deny wins without path parsing).
	if ok, id := p.rules.MatchDeny(toolName, toolInput); ok {
		return Decision{
			Behavior: BehaviorDeny,
			Reason:   "matched deny rule",
			Rule:     id,
		}
	}

	// Stage 2 — path sandbox for file-like tools and shell commands.
	if err := p.validateToolPaths(toolName, toolInput); err != nil {
		return Decision{
			Behavior: BehaviorDeny,
			Reason:   err.Error(),
			Rule:     "sandbox",
		}
	}

	mode := strings.ToLower(strings.TrimSpace(p.cfg.Mode))

	// Stage 3 — mode.
	switch mode {
	case "full_auto":
		return Decision{
			Behavior: BehaviorAllow,
			Reason:   "full_auto mode",
			Rule:     "",
		}
	case "manual":
		return Decision{
			Behavior: BehaviorAsk,
			Reason:   "manual mode requires confirmation",
			Rule:     "",
		}
	case "semi_auto", "":
		// Stage 4 — allow rules (semi_auto).
		if ok, id := p.rules.MatchAllow(toolName, toolInput); ok {
			return Decision{
				Behavior: BehaviorAllow,
				Reason:   "matched allow rule",
				Rule:     id,
			}
		}
		// Stage 5 — default.
		return Decision{
			Behavior: BehaviorAsk,
			Reason:   "no matching allow rule",
			Rule:     "",
		}
	default:
		return Decision{
			Behavior: BehaviorAsk,
			Reason:   "unknown permission mode; defaulting to ask",
			Rule:     "",
		}
	}
}

// CheckTool adapts the richer Decision-based pipeline to the core package's
// simpler error-returning permission interface.
func (p *Pipeline) CheckTool(ctx context.Context, toolName string, toolInput map[string]interface{}) error {
	_ = ctx
	decision := p.Check(toolName, toolInput)
	switch decision.Behavior {
	case BehaviorAllow:
		return nil
	case BehaviorDeny, BehaviorAsk:
		if decision.Reason != "" {
			return fmt.Errorf("%s", decision.Reason)
		}
		return fmt.Errorf("permission %s", decision.Behavior)
	default:
		return fmt.Errorf("permission %s", decision.Behavior)
	}
}

func (p *Pipeline) validateToolPaths(toolName string, toolInput map[string]interface{}) error {
	_ = toolName
	paths := extractPaths(toolInput)
	for _, path := range paths {
		if err := p.sandbox.ValidatePath(path); err != nil {
			return err
		}
	}
	if cmd, ok := toolInput["command"].(string); ok && strings.TrimSpace(cmd) != "" {
		if err := p.sandbox.ValidateCommand(cmd); err != nil {
			return err
		}
	}
	return nil
}

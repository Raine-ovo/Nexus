package permission

import (
	"testing"

	"github.com/rainea/nexus/configs"
)

func TestPipelineCheck_Order(t *testing.T) {
	cfg := configs.PermissionConfig{
		Mode:          "semi_auto",
		WorkspaceRoot: t.TempDir(),
	}
	p := NewPipeline(cfg)
	p.rules.AddRule(Rule{ID: "d1", Type: "deny", ToolNames: []string{"bad_tool"}})
	p.rules.AddRule(Rule{ID: "a1", Type: "allow", ToolNames: []string{"ok_tool"}})

	d := p.Check("bad_tool", map[string]interface{}{"path": "x"})
	if d.Behavior != BehaviorDeny || d.Rule != "d1" {
		t.Fatalf("deny first: got %+v", d)
	}

	d = p.Check("ok_tool", nil)
	if d.Behavior != BehaviorAllow || d.Rule != "a1" {
		t.Fatalf("allow rule: got %+v", d)
	}

	d = p.Check("other", nil)
	if d.Behavior != BehaviorAsk {
		t.Fatalf("default ask: got %+v", d)
	}
}

func TestPipelineCheck_FullAutoAfterDeny(t *testing.T) {
	cfg := configs.PermissionConfig{Mode: "full_auto", WorkspaceRoot: t.TempDir()}
	p := NewPipeline(cfg)
	p.rules.AddRule(Rule{ID: "d1", Type: "deny", ToolNames: []string{"x"}})

	d := p.Check("x", nil)
	if d.Behavior != BehaviorDeny {
		t.Fatalf("deny overrides full_auto: got %+v", d)
	}
	d = p.Check("y", nil)
	if d.Behavior != BehaviorAllow {
		t.Fatalf("full_auto: got %+v", d)
	}
}

func TestPipelineCheck_SandboxAfterDeny(t *testing.T) {
	root := t.TempDir()
	cfg := configs.PermissionConfig{Mode: "full_auto", WorkspaceRoot: root}
	p := NewPipeline(cfg)
	d := p.Check("file", map[string]interface{}{"path": "../../../etc/passwd"})
	if d.Behavior != BehaviorDeny || d.Rule != "sandbox" {
		t.Fatalf("sandbox: got %+v", d)
	}
}

func TestPipelineCheck_DefaultSafeReadRules(t *testing.T) {
	cfg := configs.PermissionConfig{
		Mode:          "semi_auto",
		WorkspaceRoot: t.TempDir(),
	}
	p := NewPipeline(cfg)

	d := p.Check("read_file", map[string]interface{}{"path": "configs/default.yaml"})
	if d.Behavior != BehaviorAllow || d.Rule != "builtin-safe-read" {
		t.Fatalf("default read rule: got %+v", d)
	}

	d = p.Check("grep_search", map[string]interface{}{"pattern": "foo"})
	if d.Behavior != BehaviorAllow || d.Rule != "builtin-safe-read" {
		t.Fatalf("default grep rule: got %+v", d)
	}

	d = p.Check("write_file", map[string]interface{}{"path": "x", "content": "y"})
	if d.Behavior != BehaviorAsk {
		t.Fatalf("write should still ask: got %+v", d)
	}
}

package main

import (
	"testing"

	"github.com/rainea/nexus/configs"
)

func TestApplyRunSandbox_IsolatesRunArtifactsButNotTeamDir(t *testing.T) {
	cfg := &configs.Config{
		Agent: configs.AgentConfig{
			OutputPersistDir: ".outputs",
		},
		Memory: configs.MemoryConfig{
			SemanticFile: ".memory/semantic.yaml",
		},
		Reflection: configs.ReflectionConfig{
			MemoryFile: ".memory/reflections.yaml",
		},
		Planning: configs.PlanningConfig{
			TaskDir: ".tasks",
		},
		Team: configs.TeamConfig{
			Dir: ".team",
		},
		Run: configs.RunConfig{
			SandboxDir: ".runs/demo",
		},
	}

	applyRunSandbox(cfg)

	if got := cfg.Planning.TaskDir; got != ".runs/demo/.tasks" {
		t.Fatalf("unexpected task dir: %s", got)
	}
	if got := cfg.Agent.OutputPersistDir; got != ".runs/demo/.outputs" {
		t.Fatalf("unexpected output dir: %s", got)
	}
	if got := cfg.Memory.SemanticFile; got != ".runs/demo/.memory/semantic.yaml" {
		t.Fatalf("unexpected semantic file: %s", got)
	}
	if got := cfg.Reflection.MemoryFile; got != ".runs/demo/.memory/reflections.yaml" {
		t.Fatalf("unexpected reflection file: %s", got)
	}
	if got := cfg.Team.Dir; got != ".team" {
		t.Fatalf("team dir should remain persistent, got: %s", got)
	}
}

func TestResolveRunSandboxPath_LeavesAbsolutePathsUntouched(t *testing.T) {
	got := resolveRunSandboxPath(".runs/demo", "/var/lib/nexus/.tasks")
	if got != "/var/lib/nexus/.tasks" {
		t.Fatalf("absolute paths should be unchanged, got: %s", got)
	}
}

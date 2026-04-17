package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rainea/nexus/configs"
	"github.com/rainea/nexus/internal/observability"
	"github.com/rainea/nexus/internal/tool"
	"github.com/rainea/nexus/internal/tool/mcp"
	"github.com/rainea/nexus/pkg/types"
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

func TestRunLabelFromSandbox(t *testing.T) {
	if got := runLabelFromSandbox(".runs/governance"); got != "governance" {
		t.Fatalf("unexpected run label: %s", got)
	}
	if got := runLabelFromSandbox(""); got != "default" {
		t.Fatalf("unexpected default run label: %s", got)
	}
}

func TestWriteRunDashboardREADME(t *testing.T) {
	root := t.TempDir()
	cfg := &configs.Config{
		Server: configs.ServerConfig{
			HTTPAddr: ":8080",
		},
		Run: configs.RunConfig{
			SandboxDir: filepath.Join(root, ".runs", "demo"),
		},
		Planning: configs.PlanningConfig{
			TaskDir: filepath.Join(root, ".runs", "demo", ".tasks"),
		},
		Agent: configs.AgentConfig{
			OutputPersistDir: filepath.Join(root, ".runs", "demo", ".outputs"),
		},
		Memory: configs.MemoryConfig{
			SemanticFile: filepath.Join(root, ".runs", "demo", ".memory", "semantic.yaml"),
		},
		Reflection: configs.ReflectionConfig{
			MemoryFile: filepath.Join(root, ".runs", "demo", ".memory", "reflections.yaml"),
		},
	}
	if err := writeRunDashboardREADME(cfg); err != nil {
		t.Fatal(err)
	}
	readmePath := filepath.Join(cfg.Run.SandboxDir, "README.md")
	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "/debug/dashboard?run=demo") {
		t.Fatalf("expected dashboard link in readme, got: %s", body)
	}
	if !strings.Contains(body, "/api/debug/traces?run=demo") {
		t.Fatalf("expected traces link in readme, got: %s", body)
	}
}

func TestBootstrapMCPClients_RegistersRemoteTools(t *testing.T) {
	remoteReg := tool.NewRegistry()
	remoteReg.MustRegister(&types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "remote_echo",
			Description: "echo from remote mcp server",
			Parameters:  map[string]interface{}{"type": "object"},
		},
		Source: "builtin",
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			return &types.ToolResult{Name: "remote_echo", Content: "ok"}, nil
		},
	})
	ts := httptest.NewServer(mcp.NewServer(remoteReg).Handler())
	defer ts.Close()

	localReg := tool.NewRegistry()
	manager := mcp.NewManager()
	obs := observability.New(configs.ObservabilityConfig{})
	err := bootstrapMCPClients(context.Background(), configs.MCPConfig{
		Clients: []configs.MCPClientConfig{{
			Name:    "local-test",
			Enabled: true,
			BaseURL: ts.URL,
		}},
	}, localReg, manager, obs)
	if err != nil {
		t.Fatal(err)
	}
	if localReg.Get("remote_echo") == nil {
		t.Fatalf("expected remote mcp tool to be registered, names=%v", localReg.ListNames())
	}
	_ = manager.CloseAll()
}

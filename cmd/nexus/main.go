package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"path/filepath"
	"strings"

	"github.com/rainea/nexus/configs"
	"github.com/rainea/nexus/internal/agents/codereviewer"
	"github.com/rainea/nexus/internal/agents/devops"
	"github.com/rainea/nexus/internal/agents/knowledge"
	"github.com/rainea/nexus/internal/agents/planner"
	"github.com/rainea/nexus/internal/core"
	"github.com/rainea/nexus/internal/gateway"
	"github.com/rainea/nexus/internal/intelligence"
	"github.com/rainea/nexus/internal/memory"
	"github.com/rainea/nexus/internal/observability"
	"github.com/rainea/nexus/internal/permission"
	"github.com/rainea/nexus/internal/planning"
	"github.com/rainea/nexus/internal/rag"
	"github.com/rainea/nexus/internal/team"
	"github.com/rainea/nexus/internal/tool"
	"github.com/rainea/nexus/internal/tool/builtin"
	"github.com/rainea/nexus/internal/tool/mcp"
	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func main() {
	configPath := flag.String("config", "configs/default.yaml", "path to config file")
	flag.Parse()

	cfg, err := configs.Load(*configPath)
	if err != nil {
		log.Printf("WARN: failed to load config from %s: %v, using env/defaults", *configPath, err)
		cfg = configs.LoadFromEnv()
	}
	applyRunSandbox(cfg)
	runLabel := runLabelFromSandbox(cfg.Run.SandboxDir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	obs := observability.New(cfg.Observability)
	obs.Info("nexus starting", "config", *configPath)
	if strings.TrimSpace(cfg.Run.SandboxDir) != "" {
		obs.Info("run sandbox enabled",
			"sandbox_dir", cfg.Run.SandboxDir,
			"task_dir", cfg.Planning.TaskDir,
			"output_dir", cfg.Agent.OutputPersistDir,
			"semantic_file", cfg.Memory.SemanticFile,
			"reflection_file", cfg.Reflection.MemoryFile,
			"team_dir", cfg.Team.Dir,
		)
	}

	permPipeline := permission.NewPipeline(cfg.Permission)

	toolRegistry := tool.NewRegistry()
	tool.RegisterBuiltins(toolRegistry, cfg.Permission.WorkspaceRoot, cfg.Permission.DangerousPatterns)

	mcpManager := mcp.NewManager()
	obs.Info("tool system initialized", "builtin_count", toolRegistry.Count())

	memManager, err := memory.NewManager(cfg.Memory)
	if err != nil {
		log.Fatalf("failed to initialize memory: %v", err)
	}
	obs.Info("memory system initialized")

	ragEngine, err := rag.NewEngine(cfg.RAG, nil)
	if err != nil {
		log.Fatalf("failed to initialize RAG engine: %v", err)
	}
	obs.Info("RAG engine initialized",
		"knowledge_dir", cfg.RAG.KnowledgeDir,
		"vector_backend", cfg.RAG.VectorBackend,
	)

	taskManager, err := planning.NewTaskManager(cfg.Planning.TaskDir)
	if err != nil {
		log.Fatalf("failed to initialize task manager: %v", err)
	}
	bgManager := planning.NewBackgroundManager(cfg.Planning.MaxBackgroundSlots)
	cronScheduler := planning.NewCronScheduler(cfg.Planning, nil)
	obs.Info("planning system initialized", "task_dir", cfg.Planning.TaskDir)

	promptAssembler := intelligence.NewPromptAssembler(cfg.Permission.WorkspaceRoot)
	skillManager := promptAssembler.SkillManager()
	if err := skillManager.ScanSkills(); err != nil {
		log.Printf("WARN: skill scan failed: %v", err)
	}
	obs.Info("intelligence system initialized", "skills", len(skillManager.ListSkills()))

	if err := builtin.RegisterSkillTools(
		func(m *types.ToolMeta) error { return toolRegistry.Register(m) },
		skillManager,
		skillManager,
	); err != nil {
		log.Printf("WARN: failed to register skill tools: %v", err)
	}

	baseChatModel := core.NewOpenAICompatibleChatModel(cfg.Model)
	chatModel := core.NewThrottledChatModel(
		baseChatModel,
		cfg.Model.MaxConcurrency,
		time.Duration(cfg.Model.MinRequestIntervalMS)*time.Millisecond,
	)
	core.DefaultChatModel = chatModel
	obs.Info("chat model initialized",
		"provider", cfg.Model.Provider,
		"model", cfg.Model.ModelName,
		"max_concurrency", cfg.Model.MaxConcurrency,
		"min_request_interval_ms", cfg.Model.MinRequestIntervalMS,
	)

	agentDeps := &core.AgentDependencies{
		ToolRegistry:       toolRegistry,
		PermPipeline:       permPipeline,
		MemManager:         memManager,
		TaskManager:        taskManager,
		PromptAssembler:    promptAssembler,
		SkillManager:       skillManager,
		Observer:           obs,
		AgentConfig:        cfg.Agent,
		ModelConfig:        cfg.Model,
		WorkspaceRoot:      cfg.Permission.WorkspaceRoot,
		ConversationWindow: cfg.Memory.ConversationWindow,
		Summarize: func(ctx context.Context, text string) (string, error) {
			resp, err := chatModel.Generate(ctx,
				"Summarize the following context into concise engineering notes. Respond with plain text only.",
				[]types.Message{{Role: types.RoleUser, Content: text}},
				nil,
			)
			if err != nil {
				return "", err
			}
			if resp == nil {
				return "", fmt.Errorf("nil summarize response")
			}
			return strings.TrimSpace(resp.Content), nil
		},
	}

	obs.Info("team runtime initialized", "reflection_enabled", cfg.Reflection.Enabled, "run_label", runLabel)

	leadSystemPrompt := `You are the lead of a multi-agent team. Your job is to:
1. Understand the user's request and decide how to accomplish it.
2. For cron-triggered or scheduled tasks, never handle the task yourself and never use delegate_task. Persist the work through the planner/team task system first, then route it to a persistent teammate.
3. For simple non-scheduled tasks, you may handle them yourself using available tools.
4. For focused one-off tasks that need specialized expertise, use delegate_task — it runs a role-specialized agent in a clean, isolated context (no shared history) and returns the result.
5. For long-running or multi-step work, send_message to an existing teammate. Teammates persist across tasks and accumulate context, making them ideal for ongoing collaboration.
6. When a task board item should belong to a specific teammate or specialist role, use assign_task before asking them to claim it.
7. Only spawn_teammate when you need a new persistent worker that doesn't exist yet. Reuse existing teammates for subsequent work.
8. Review and approve plans submitted by teammates before risky operations proceed.

Choosing the right dispatch mechanism:
- scheduled task: persist via planner/team first, then hand off to a persistent teammate; never delegate_task and never execute it yourself
- YOU directly: simple non-scheduled tasks you can handle with your own tools
- delegate_task: one-off specialized work where context isolation matters (e.g. "review this file", "search for X") — starts with a clean slate, result returns, state discarded
- send_message: ongoing work for existing teammates who need accumulated context (e.g. "continue implementing feature Y", "run more tests on that module")
- assign_task: reserve a task-board item for a teammate or role so claims follow dispatch intent
- spawn_teammate: only when you need a NEW persistent worker

Available roles for delegate_task and spawn_teammate:
- code_reviewer: Reviews code for security, correctness, and quality.
- knowledge: Answers questions using RAG and documentation search.
- devops: Handles CI/CD, infrastructure, and deployment tasks.
- planner: Creates task DAGs and manages work breakdown.`

	if err := bootstrapMCPClients(ctx, cfg.MCP, toolRegistry, mcpManager, obs); err != nil {
		obs.Warn("mcp client bootstrap completed with warnings", "error", err)
	}

	// Build agent templates from existing role-specialized agents.
	// These serve as role definitions: system prompt + tool set.
	crAgent := codereviewer.New(agentDeps)
	kbAgent := knowledge.New(agentDeps, ragEngine)
	dopsAgent := devops.New(agentDeps)
	planAgent := planner.New(agentDeps, taskManager, bgManager)

	// Collect base tools the lead should have (file ops, grep, etc. from the registry).
	leadBaseTools := collectRegistryTools(toolRegistry,
		"read_file", "write_file", "edit_file", "grep_search", "glob_search",
		"list_dir", "bash", "load_skill", "list_skills",
	)
	leadBaseTools = appendDistinctTools(leadBaseTools, toolRegistry.FilterBySource("mcp")...)

	teamRegistry := team.NewRegistry(team.RegistryConfig{
		BaseManagerConfig: team.ManagerConfig{
			TeamDir:          cfg.Team.Dir,
			PollInterval:     cfg.Team.PollInterval,
			IdleTimeout:      cfg.Team.IdleTimeout,
			Model:            chatModel,
			Deps:             agentDeps,
			TaskManager:      taskManager,
			Observer:         obs,
			LeadSystemPrompt: leadSystemPrompt,
			LeadBaseTools:    leadBaseTools,
		},
		MemoryConfig:     cfg.Memory,
		ReflectionConfig: cfg.Reflection,
		RunLabel:         runLabel,
		SandboxDir:       cfg.Run.SandboxDir,
	})

	// Register agent templates so the lead can spawn role-specialized teammates.
	teamRegistry.RegisterTemplate("code_reviewer", team.AgentTemplate{
		Role:         "code_reviewer",
		SystemPrompt: crAgent.GetSystemPrompt(),
		Tools:        crAgent.GetTools(),
	})
	teamRegistry.RegisterTemplate("knowledge", team.AgentTemplate{
		Role:         "knowledge",
		SystemPrompt: kbAgent.GetSystemPrompt(),
		Tools:        kbAgent.GetTools(),
	})
	teamRegistry.RegisterTemplate("devops", team.AgentTemplate{
		Role:         "devops",
		SystemPrompt: dopsAgent.GetSystemPrompt(),
		Tools:        dopsAgent.GetTools(),
	})
	teamRegistry.RegisterTemplate("planner", team.AgentTemplate{
		Role:         "planner",
		SystemPrompt: planAgent.GetSystemPrompt(),
		Tools:        planAgent.GetTools(),
	})

	obs.Info("team system initialized",
		"templates", []string{"code_reviewer", "knowledge", "devops", "planner"},
		"base_team_dir", cfg.Team.Dir,
	)

	cronResultDir := filepath.Join(cfg.Planning.TaskDir, "cron_results")

	planExec := planning.NewPlanExecutor(taskManager, bgManager, nil, func(runCtx context.Context, agentName string, task *planning.Task) (string, error) {
		return teamRegistry.HandleRequest(runCtx, "cron:task:"+fmt.Sprintf("%d", task.ID), task.Title+"\n"+task.Description)
	})

	cronScheduler.SetJobHandler(func(ctx context.Context, job planning.CronJob) {
		sessionID := "cron:" + job.Name
		ts := time.Now().UTC().Format("2006-01-02T15-04-05")

		switch job.Type {
		case planning.CronTypeAgentTurn:
			result, err := teamRegistry.HandleRequest(ctx, sessionID, buildScheduledAgentTurnPrompt(job, skillManager))
			rec := map[string]interface{}{
				"job":       job.Name,
				"type":      job.Type,
				"payload":   job.Payload,
				"triggered": ts,
				"result":    result,
				"error":     errStr(err),
			}
			outPath := filepath.Join(cronResultDir, job.Name, ts+".json")
			if wErr := utils.WriteJSON(outPath, rec); wErr != nil {
				obs.Warn("cron result persist failed", "job", job.Name, "error", wErr)
			}
			if err != nil {
				obs.Error("cron agent_turn failed", "job", job.Name, "error", err)
			} else {
				obs.Info("cron agent_turn completed", "job", job.Name, "output", outPath)
			}

		case planning.CronTypeSystemEvent:
			slotID, err := planExec.ExecuteNext(ctx)
			if err != nil {
				obs.Warn("cron system_event: no task or submit failed", "job", job.Name, "error", err)
			} else {
				obs.Info("cron system_event submitted", "job", job.Name, "slot", slotID)
			}

		default:
			obs.Warn("cron unknown job type", "job", job.Name, "type", job.Type)
		}
	})

	gw := gateway.New(cfg.Gateway, cfg.Server, teamRegistry, obs)
	if cfg.MCP.ServerEnabled {
		mcpServer := mcp.NewServer(
			toolRegistry,
			mcp.WithPaths(cfg.MCP.RPCPath, cfg.MCP.SSEPath),
		)
		gw.SetMCPHandler(mcpServer.Handler())
		obs.Info("mcp server mounted", "rpc_path", mcpServer.RPCPath(), "sse_path", mcpServer.SSEPath())
	}
	if err := writeRunDashboardREADME(cfg); err != nil {
		obs.Warn("run dashboard readme write failed", "error", err)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := gw.Start(ctx); err != nil {
			log.Fatalf("gateway error: %v", err)
		}
	}()

	if err := cronScheduler.Start(ctx); err != nil {
		log.Fatalf("failed to start cron scheduler: %v", err)
	}

	obs.Info("nexus ready",
		"http", cfg.Server.HTTPAddr,
		"ws", cfg.Server.WSAddr,
	)

	sig := <-sigCh
	obs.Info("shutdown signal received", "signal", sig.String())

	cancel()
	cronScheduler.Stop()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
	teamRegistry.Shutdown(shutCtx)
	_ = bgManager.Shutdown(shutCtx)
	shutCancel()
	mcpManager.CloseAll()
	_ = memManager.Flush()

	_ = ragEngine
	_ = mcpManager

	fmt.Println("nexus stopped gracefully")
}

// collectRegistryTools pulls named tools from the registry for the lead's base tool set.
func collectRegistryTools(reg *tool.Registry, names ...string) []*types.ToolMeta {
	var out []*types.ToolMeta
	for _, name := range names {
		if m := reg.Get(name); m != nil {
			out = append(out, m)
		}
	}
	return out
}

func appendDistinctTools(base []*types.ToolMeta, extras ...*types.ToolMeta) []*types.ToolMeta {
	seen := make(map[string]struct{}, len(base))
	for _, item := range base {
		if item != nil {
			seen[item.Definition.Name] = struct{}{}
		}
	}
	for _, item := range extras {
		if item == nil || item.Definition.Name == "" {
			continue
		}
		if _, ok := seen[item.Definition.Name]; ok {
			continue
		}
		seen[item.Definition.Name] = struct{}{}
		base = append(base, item)
	}
	return base
}

func applyRunSandbox(cfg *configs.Config) {
	if cfg == nil {
		return
	}
	sandbox := strings.TrimSpace(cfg.Run.SandboxDir)
	if sandbox == "" {
		return
	}
	cfg.Agent.OutputPersistDir = resolveRunSandboxPath(sandbox, cfg.Agent.OutputPersistDir)
	cfg.Memory.SemanticFile = resolveRunSandboxPath(sandbox, cfg.Memory.SemanticFile)
	cfg.Reflection.MemoryFile = resolveRunSandboxPath(sandbox, cfg.Reflection.MemoryFile)
	cfg.Planning.TaskDir = resolveRunSandboxPath(sandbox, cfg.Planning.TaskDir)
}

func resolveRunSandboxPath(sandboxDir, path string) string {
	if strings.TrimSpace(sandboxDir) == "" || strings.TrimSpace(path) == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(sandboxDir, path)
}

func runLabelFromSandbox(sandboxDir string) string {
	sandboxDir = strings.TrimSpace(sandboxDir)
	if sandboxDir == "" {
		return "default"
	}
	clean := filepath.Clean(sandboxDir)
	base := filepath.Base(clean)
	if base == "." || base == string(filepath.Separator) || strings.TrimSpace(base) == "" {
		return "default"
	}
	return base
}

func localHTTPBaseURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "http://127.0.0.1:8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") {
			return "http://127.0.0.1" + addr
		}
		return "http://" + addr
	}
	host = strings.TrimSpace(host)
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func bootstrapMCPClients(
	ctx context.Context,
	cfg configs.MCPConfig,
	reg *tool.Registry,
	manager *mcp.Manager,
	obs *observability.Observer,
) error {
	var errs []string
	for _, clientCfg := range cfg.Clients {
		if !clientCfg.Enabled || strings.TrimSpace(clientCfg.BaseURL) == "" {
			continue
		}
		transport := mcp.NewSSETransport(clientCfg.BaseURL, clientCfg.RPCPath)
		if clientCfg.ConnectSSE {
			connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := transport.ConnectSSE(connectCtx, clientCfg.SSEPath); err != nil {
				errs = append(errs, fmt.Sprintf("%s: connect sse: %v", coalesce(clientCfg.Name, clientCfg.BaseURL), err))
				cancel()
				_ = transport.Close()
				continue
			}
			cancel()
		}
		client := mcp.NewClient(transport)
		initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if _, err := client.Initialize(initCtx); err != nil {
			errs = append(errs, fmt.Sprintf("%s: initialize: %v", coalesce(clientCfg.Name, clientCfg.BaseURL), err))
			cancel()
			_ = transport.Close()
			continue
		}
		if err := client.SendInitializedNotification(initCtx); err != nil {
			obs.Warn("mcp client initialized notification failed", "client", coalesce(clientCfg.Name, clientCfg.BaseURL), "error", err)
		}
		if err := client.AutoRegisterTools(initCtx, reg); err != nil {
			errs = append(errs, fmt.Sprintf("%s: register tools: %v", coalesce(clientCfg.Name, clientCfg.BaseURL), err))
			cancel()
			_ = transport.Close()
			continue
		}
		cancel()
		manager.Add(transport)
		obs.Info("mcp client registered", "client", coalesce(clientCfg.Name, clientCfg.BaseURL), "base_url", clientCfg.BaseURL)
	}
	if len(errs) > 0 {
		return fmt.Errorf(strings.Join(errs, "; "))
	}
	return nil
}

func coalesce(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func writeRunDashboardREADME(cfg *configs.Config) error {
	if cfg == nil {
		return nil
	}
	sandbox := strings.TrimSpace(cfg.Run.SandboxDir)
	if sandbox == "" {
		return nil
	}
	baseURL := localHTTPBaseURL(cfg.Server.HTTPAddr)
	runLabel := runLabelFromSandbox(sandbox)
	readmePath := filepath.Join(sandbox, "README.md")
	var b strings.Builder
	b.WriteString("# Run Dashboard\n\n")
	b.WriteString("This sandbox contains runtime artifacts for the current Nexus run.\n\n")
	b.WriteString("## Links\n\n")
	b.WriteString("- Dashboard: " + baseURL + "/debug/dashboard?run=" + runLabel + "\n")
	b.WriteString("- Trace List: " + baseURL + "/api/debug/traces?run=" + runLabel + "\n")
	b.WriteString("- Metrics: " + baseURL + "/api/debug/metrics?run=" + runLabel + "\n")
	b.WriteString("- Health: " + baseURL + "/api/health\n\n")
	b.WriteString("## Paths\n\n")
	b.WriteString("- Sandbox Dir: `" + sandbox + "`\n")
	b.WriteString("- Latest Trace Snapshot: `" + filepath.Join(sandbox, "latest-traces.json") + "`\n")
	b.WriteString("- Task Dir: `" + cfg.Planning.TaskDir + "`\n")
	b.WriteString("- Semantic Memory: `" + cfg.Memory.SemanticFile + "`\n")
	b.WriteString("- Reflection Memory: `" + cfg.Reflection.MemoryFile + "`\n")
	b.WriteString("- Output Dir: `" + cfg.Agent.OutputPersistDir + "`\n")
	if err := os.MkdirAll(filepath.Dir(readmePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(readmePath, []byte(b.String()), 0o644)
}

const scheduledCronSkillName = "cron-team-dispatch"

func buildScheduledAgentTurnPrompt(job planning.CronJob, skillManager *intelligence.SkillManager) string {
	var b strings.Builder
	b.WriteString("This request was triggered by the cron scheduler.\n")
	b.WriteString(fmt.Sprintf("Job name: %s\n", job.Name))
	b.WriteString("Hard requirements:\n")
	b.WriteString("- Do NOT use delegate_task.\n")
	b.WriteString("- Do NOT complete the scheduled work directly as lead.\n")
	b.WriteString("- Persist the main work item through the planner/team task system so it is written under .tasks/.\n")
	b.WriteString("- Then route execution to a persistent teammate using spawn_teammate/send_message.\n")
	b.WriteString("- Ask the teammate to claim the persisted task before doing the work.\n")
	b.WriteString("- Reuse existing teammates when possible.\n")
	b.WriteString("- If the action is risky, require plan review before mutating state.\n")
	if skillManager != nil {
		if body, err := skillManager.LoadSkill(scheduledCronSkillName); err == nil && strings.TrimSpace(body) != "" {
			b.WriteString("\nFollow this skill exactly:\n")
			b.WriteString("<<< SKILL: ")
			b.WriteString(scheduledCronSkillName)
			b.WriteString(" >>>\n")
			b.WriteString(body)
			b.WriteString("\n<<< END SKILL >>>\n")
		}
	}
	b.WriteString("\nOriginal scheduled payload:\n")
	b.WriteString(job.Payload)
	return b.String()
}

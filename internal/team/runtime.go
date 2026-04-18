package team

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rainea/nexus/configs"
	"github.com/rainea/nexus/internal/core"
	gatewaymw "github.com/rainea/nexus/internal/gateway/middleware"
	"github.com/rainea/nexus/internal/memory"
	"github.com/rainea/nexus/internal/observability"
	"github.com/rainea/nexus/internal/reflection"
	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

type runtimeTrace struct {
	ctx      context.Context
	span     *observability.Span
	started  time.Time
	nodeName string
}

// Runtime centralizes feature integration for the Team execution path.
// It keeps the existing Lead/Teammate loops intact while wiring in:
// - prompt assembly + memory injection
// - reflection wrapping
// - conversation/semantic memory persistence
// - trace + metrics callbacks
// - transcript compaction via ContextGuard
type Runtime struct {
	deps       *core.AgentDependencies
	model      core.ChatModel
	obs        core.Observer
	fullObs    *observability.Observer
	mem        *memory.Manager
	guard      *core.ContextGuard
	recovery   *core.RecoveryManager
	refl       *reflection.Engine
	reflMem    *reflection.ReflectionMemory
	runLabel   string
	sandboxDir string
	snapshotMu sync.Mutex
}

func NewRuntime(
	deps *core.AgentDependencies,
	model core.ChatModel,
	obs core.Observer,
	runLabel string,
	sandboxDir string,
	refCfg configs.ReflectionConfig,
) (*Runtime, error) {
	rt := &Runtime{
		deps:       deps,
		model:      model,
		obs:        obs,
		recovery:   core.NewRecoveryManager(core.DefaultRecoveryPolicy(), obs),
		runLabel:   strings.TrimSpace(runLabel),
		sandboxDir: strings.TrimSpace(sandboxDir),
	}
	if full, ok := obs.(*observability.Observer); ok {
		rt.fullObs = full
	}
	if deps != nil {
		if mm, ok := deps.MemManager.(*memory.Manager); ok {
			rt.mem = mm
		}
		ws := deps.WorkspaceRoot
		if strings.TrimSpace(ws) == "" {
			ws = "."
		}
		rt.guard = core.NewContextGuard(
			deps.AgentConfig,
			ws,
			deps.ConversationWindow,
			obs,
			rt.summarizeText,
		)
	}
	if refCfg.Enabled {
		reflMem, err := reflection.NewReflectionMemory(refCfg.MemoryFile, refCfg.MaxMemEntries)
		if err != nil {
			return nil, fmt.Errorf("team/runtime: reflection memory: %w", err)
		}
		rt.reflMem = reflMem
		rt.refl = reflection.NewEngine(
			reflMem,
			reflection.NewEvaluator(model, refCfg.Threshold),
			reflection.NewReflector(model, obs),
			reflection.NewProspector(model, obs),
			obs,
			reflection.EngineConfig{
				MaxAttempts:    refCfg.MaxAttempts,
				Threshold:      refCfg.Threshold,
				EnableProspect: refCfg.EnableProspect,
			},
		)
	}
	return rt, nil
}

func (r *Runtime) BuildSystemPrompt(base string, skillIndex core.SkillIndex, extraSuffix string) string {
	extraMemory := ""
	if r.reflMem != nil {
		extraMemory = r.reflMem.ToPromptSection(8)
	}
	return core.BuildSystemPrompt(base, r.deps, skillIndex, extraMemory, extraSuffix)
}

func (r *Runtime) SummarizeTranscriptMessages(ctx context.Context, messages []types.Message) []types.Message {
	if r == nil || r.guard == nil || len(messages) == 0 {
		return messages
	}
	state := core.NewLoopState()
	if err := state.TransitionTo(core.PhaseRunning, "team_runtime"); err != nil {
		return messages
	}
	state.SetMessages(messages)
	if _, err := r.guard.MaybeCompact(ctx, state); err != nil {
		if r.obs != nil {
			r.obs.Warn("team/runtime: transcript compaction failed", "err", err.Error())
		}
		return messages
	}
	return state.MessagesSnapshot()
}

func (r *Runtime) RunWithReflection(
	ctx context.Context,
	name string,
	description string,
	system string,
	tools []*types.ToolMeta,
	input string,
	run func(context.Context, string) (string, error),
) (string, error) {
	if r == nil || r.refl == nil || run == nil {
		return run(ctx, input)
	}
	adapter := teamRunnerAdapter{
		name:        name,
		description: description,
		system:      system,
		tools:       tools,
		run:         run,
	}
	return r.refl.RunWithReflection(ctx, adapter, input)
}

func (r *Runtime) RecordTurn(ctx context.Context, input, output string) {
	if r == nil {
		return
	}
	if r.mem != nil {
		conv := r.mem.GetConversation()
		if conv != nil {
			conv.Add(types.Message{Role: types.RoleUser, Content: input})
			conv.Add(types.Message{Role: types.RoleAssistant, Content: output})
			threshold := 0
			if r.deps != nil {
				threshold = r.deps.AgentConfig.TokenThreshold
			}
			if threshold <= 0 {
				threshold = 80000
			}
			if conv.EstimateTokens() >= threshold/2 {
				conv.Compact(func(old []types.Message) string {
					var b strings.Builder
					for _, m := range old {
						b.WriteString(string(m.Role))
						b.WriteString(": ")
						b.WriteString(m.Content)
						b.WriteString("\n")
					}
					sum, err := r.summarizeText(ctx, b.String())
					if err != nil {
						return ""
					}
					return sum
				})
			}
		}
		if sem := r.mem.GetSemantic(); sem != nil {
			for _, entry := range r.extractSemanticEntries(ctx, input, output) {
				if err := sem.Add(entry.Category, entry.Key, entry.Value); err != nil && r.obs != nil {
					r.obs.Warn("team/runtime: semantic memory add failed", "category", entry.Category, "key", entry.Key, "err", err.Error())
				}
			}
		}
	}
}

func (r *Runtime) StartRequestSpan(ctx context.Context, actorName, phase string) (*runtimeTrace, context.Context) {
	return r.startSpan(ctx, actorName, "request:"+phase, phase)
}

func (r *Runtime) StartLLMSpan(ctx context.Context, actorName string) (*runtimeTrace, context.Context) {
	return r.startSpan(ctx, actorName, "llm_call", "on_llm_start")
}

func (r *Runtime) EndLLMSpan(trace *runtimeTrace, err error) {
	r.endSpan(trace, err, "on_llm_end")
}

func (r *Runtime) StartToolSpan(ctx context.Context, actorName, toolName string) (*runtimeTrace, context.Context) {
	node := "tool:" + toolName
	return r.startSpan(ctx, actorName, node, "on_tool_start")
}

func (r *Runtime) EndToolSpan(trace *runtimeTrace, err error) {
	r.endSpan(trace, err, "on_tool_end")
}

func (r *Runtime) CallWithRecovery(ctx context.Context, fn func() error) error {
	if r == nil || r.recovery == nil {
		return fn()
	}
	return r.recovery.CallWithRetry(ctx, nil, fn)
}

func (r *Runtime) TraceID(ctx context.Context) string {
	if r == nil || r.fullObs == nil {
		return ""
	}
	return observability.TraceIDFromContext(ctx)
}

func (r *Runtime) summarizeText(ctx context.Context, text string) (string, error) {
	if r == nil || r.model == nil {
		return "", fmt.Errorf("team/runtime: summarize model unavailable")
	}
	prompt := text
	if len(prompt) > 6000 {
		prompt = prompt[:6000]
	}
	resp, err := r.model.Generate(ctx, "Summarize the following conversation context into concise, durable bullet points. Respond with plain text only.", []types.Message{
		{Role: types.RoleUser, Content: prompt},
	}, nil)
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", fmt.Errorf("team/runtime: nil summarize response")
	}
	return strings.TrimSpace(resp.Content), nil
}

func (r *Runtime) extractSemanticEntries(ctx context.Context, input, output string) []memory.SemanticEntry {
	if r == nil || r.model == nil || r.mem == nil || len(strings.TrimSpace(input)) == 0 {
		return nil
	}
	type semanticCandidate struct {
		Category string `json:"category"`
		Key      string `json:"key"`
		Value    string `json:"value"`
	}
	prompt := buildSemanticExtractionPrompt(input, output)
	resp, err := r.model.Generate(ctx, semanticExtractionSystemPrompt, []types.Message{
		{Role: types.RoleUser, Content: prompt},
	}, nil)
	if err != nil || resp == nil {
		return nil
	}
	content := strings.TrimSpace(resp.Content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		var inner []string
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				continue
			}
			inner = append(inner, line)
		}
		content = strings.Join(inner, "\n")
	}
	var raw struct {
		Entries []semanticCandidate `json:"entries"`
	}
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return nil
	}
	var out []memory.SemanticEntry
	seen := make(map[string]struct{})
	for _, item := range raw.Entries {
		category := strings.TrimSpace(strings.ToLower(item.Category))
		key := strings.TrimSpace(item.Key)
		value := strings.TrimSpace(item.Value)
		if category == "" || key == "" || value == "" {
			continue
		}
		switch category {
		case memory.CategoryProject, memory.CategoryPreference, memory.CategoryFeedback, memory.CategoryReference:
		default:
			continue
		}
		dedupeKey := category + "\x00" + key + "\x00" + value
		if _, ok := seen[dedupeKey]; ok {
			continue
		}
		seen[dedupeKey] = struct{}{}
		out = append(out, memory.SemanticEntry{
			Category:  category,
			Key:       key,
			Value:     value,
			CreatedAt: time.Now().UTC(),
		})
		if len(out) >= 3 {
			break
		}
	}
	return out
}

func (r *Runtime) startSpan(ctx context.Context, actorName, nodeName, eventType string) (*runtimeTrace, context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil || r.fullObs == nil {
		return &runtimeTrace{ctx: ctx, started: time.Now(), nodeName: nodeName}, ctx
	}
	tracer := r.fullObs.Tracer()
	if tracer == nil {
		return &runtimeTrace{ctx: ctx, started: time.Now(), nodeName: nodeName}, ctx
	}
	runID := gatewaymw.RequestIDFromContext(ctx)
	if runID == "" {
		runID = uuid.NewString()
	}
	nextCtx, span := tracer.StartSpan(ctx, actorName+":"+nodeName)
	if span != nil {
		if span.Tags == nil {
			span.Tags = make(map[string]string)
		}
		span.Tags["actor"] = actorName
		span.Tags["node"] = nodeName
		span.Tags["request_id"] = runID
		if r.runLabel != "" {
			span.Tags["sandbox_run"] = r.runLabel
		}
		scopeDecision := gatewaymw.ScopeDecisionFromContext(ctx)
		if strings.TrimSpace(scopeDecision.Scope) != "" {
			span.Tags["scope"] = strings.TrimSpace(scopeDecision.Scope)
		}
		if strings.TrimSpace(scopeDecision.Workstream) != "" {
			span.Tags["workstream"] = strings.TrimSpace(scopeDecision.Workstream)
		}
		if strings.TrimSpace(scopeDecision.Decision) != "" {
			span.Tags["scope_decision"] = strings.TrimSpace(scopeDecision.Decision)
		}
		if strings.TrimSpace(scopeDecision.Reason) != "" {
			span.Tags["scope_reason"] = strings.TrimSpace(scopeDecision.Reason)
		}
		if scopeDecision.Score > 0 {
			span.Tags["scope_score"] = strconv.Itoa(scopeDecision.Score)
		}
		if scopeDecision.Threshold > 0 {
			span.Tags["scope_threshold"] = strconv.Itoa(scopeDecision.Threshold)
		}
		if len(scopeDecision.Candidates) > 0 {
			span.Tags["scope_candidate_count"] = strconv.Itoa(len(scopeDecision.Candidates))
			if payload, err := json.Marshal(scopeDecision.Candidates); err == nil {
				span.Tags["scope_candidates_json"] = string(payload)
			}
		}
	}
	if cb := r.fullObs.Callback(); cb != nil {
		event := observability.CallbackEvent{
			Type:      eventType,
			NodeName:  actorName + ":" + nodeName,
			RunID:     runID,
			RunLabel:  r.runLabel,
			Timestamp: time.Now().UTC(),
		}
		switch eventType {
		case "on_tool_start":
			cb.OnToolStart(event)
		case "on_tool_end":
			cb.OnToolEnd(event)
		case "on_llm_start":
			cb.OnLLMStart(event)
		case "on_llm_end":
			cb.OnLLMEnd(event)
		case "on_error":
			cb.OnError(event)
		case "on_end":
			cb.OnEnd(event)
		default:
			cb.OnStart(event)
		}
	}
	return &runtimeTrace{ctx: nextCtx, span: span, started: time.Now(), nodeName: nodeName}, nextCtx
}

func (r *Runtime) endSpan(trace *runtimeTrace, err error, eventType string) {
	if trace == nil || r == nil || r.fullObs == nil {
		return
	}
	if err != nil {
		r.fullObs.Tracer().SetSpanError(trace.span, err.Error())
	}
	r.fullObs.Tracer().EndSpan(trace.span)
	if cb := r.fullObs.Callback(); cb != nil {
		event := observability.CallbackEvent{
			Type:      eventType,
			NodeName:  trace.nodeName,
			RunID:     gatewaymw.RequestIDFromContext(trace.ctx),
			RunLabel:  r.runLabel,
			Timestamp: time.Now().UTC(),
			Duration:  time.Since(trace.started),
			Data: map[string]interface{}{
				"error": errString(err),
			},
		}
		switch eventType {
		case "on_tool_end":
			cb.OnToolEnd(event)
		case "on_llm_end":
			cb.OnLLMEnd(event)
		case "on_error":
			cb.OnError(event)
		case "on_end":
			cb.OnEnd(event)
		default:
			cb.OnEnd(event)
		}
	}
	if eventType == "on_end" {
		r.writeLatestTraceSnapshot()
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func buildSemanticExtractionPrompt(input, output string) string {
	var b strings.Builder
	b.WriteString("## User Input\n")
	if len(input) > 2500 {
		b.WriteString(input[:2500])
		b.WriteString("\n... [truncated]")
	} else {
		b.WriteString(input)
	}
	if strings.TrimSpace(output) != "" {
		b.WriteString("\n\n## Assistant Output\n")
		if len(output) > 1000 {
			b.WriteString(output[:1000])
			b.WriteString("\n... [truncated]")
		} else {
			b.WriteString(output)
		}
	}
	return b.String()
}

func (r *Runtime) writeLatestTraceSnapshot() {
	if r == nil || r.fullObs == nil || strings.TrimSpace(r.sandboxDir) == "" || strings.TrimSpace(r.runLabel) == "" {
		return
	}
	r.snapshotMu.Lock()
	defer r.snapshotMu.Unlock()

	summaries := r.fullObs.ListTraces(100)
	filtered := make([]observability.TraceSummary, 0, len(summaries))
	details := make([]map[string]interface{}, 0, len(summaries))
	for _, summary := range summaries {
		if strings.TrimSpace(summary.RunLabel) != r.runLabel {
			continue
		}
		filtered = append(filtered, summary)
		details = append(details, map[string]interface{}{
			"summary": summary,
			"spans":   r.fullObs.Trace(summary.TraceID),
		})
	}
	payload := map[string]interface{}{
		"run":         r.runLabel,
		"updated_at":  time.Now().UTC(),
		"trace_count": len(filtered),
		"metrics":     r.fullObs.MetricsSnapshotForRun(r.runLabel),
		"traces":      details,
	}
	if err := utils.WriteJSON(filepath.Join(r.sandboxDir, "latest-traces.json"), payload); err != nil && r.obs != nil {
		r.obs.Warn("team/runtime: latest trace snapshot write failed", "run", r.runLabel, "err", err.Error())
	}
}

const semanticExtractionSystemPrompt = `Extract only stable long-term memory candidates from the interaction.
Rules:
- Prefer user preferences, project facts, enduring feedback, and reusable references.
- Ignore transient task details, one-off requests, and verbose duplication.
- Return at most 3 entries.
- Use categories only: project, preference, feedback, reference.
- If nothing is worth storing, return {"entries":[]}.

Respond ONLY with JSON:
{
  "entries": [
    {"category":"preference","key":"preferred_branch","value":"Use master as the main branch"}
  ]
}`

type teamRunnerAdapter struct {
	name        string
	description string
	system      string
	tools       []*types.ToolMeta
	run         func(context.Context, string) (string, error)
}

func (a teamRunnerAdapter) Name() string        { return a.name }
func (a teamRunnerAdapter) Description() string { return a.description }
func (a teamRunnerAdapter) Run(ctx context.Context, input string) (string, error) {
	return a.run(ctx, input)
}
func (a teamRunnerAdapter) GetTools() []*types.ToolMeta { return a.tools }
func (a teamRunnerAdapter) GetSystemPrompt() string     { return a.system }

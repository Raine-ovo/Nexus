package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rainea/nexus/configs"
	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

// CompactionLevel describes how aggressively the ContextGuard mutates history.
type CompactionLevel int

const (
	// CompactionNone means no compaction is required.
	CompactionNone CompactionLevel = iota
	// CompactionMicro persists individual oversized tool outputs to disk and replaces them with markers.
	CompactionMicro
	// CompactionAuto summarizes older turns beyond the retention window using SummarizeFunc.
	CompactionAuto
	// CompactionManual drops middle history to fit under TokenThreshold * CompactTargetRatio.
	CompactionManual
)

// String implements fmt.Stringer for logs and metrics.
func (c CompactionLevel) String() string {
	switch c {
	case CompactionNone:
		return "none"
	case CompactionMicro:
		return "micro"
	case CompactionAuto:
		return "auto"
	case CompactionManual:
		return "manual"
	default:
		return "unknown"
	}
}

// ContextGuard watches estimated token pressure and rewrites messages to stay within budgets.
// It deliberately avoids importing provider SDKs: all token figures are local estimates via
// utils.EstimateTokens so behavior is deterministic offline.
//
// Three compaction levels:
//   - Micro: cheap I/O — spill large single tool payloads to OutputPersistDir under workspaceRoot.
//   - Auto: calls Summarize on text from messages older than convWindow (LLM cost, preserves recent turns).
//   - Manual: lossy tail-keep trim when estimated total exceeds TokenThreshold (hard ceiling).
type ContextGuard struct {
	cfg           configs.AgentConfig
	workspaceRoot string
	convWindow    int
	obs           Observer
	summarize     SummarizeFunc
}

// SummarizeFunc abstracts LLM summarization for CompactionAuto without tying ContextGuard to ChatModel.
type SummarizeFunc func(ctx context.Context, text string) (string, error)

// NewContextGuard constructs a guard. workspaceRoot is used to resolve relative OutputPersistDir.
// convWindow is the number of recent non-system messages to keep verbatim before summarization; if <=0, defaults to 8.
func NewContextGuard(cfg configs.AgentConfig, workspaceRoot string, convWindow int, obs Observer, summarize SummarizeFunc) *ContextGuard {
	if convWindow <= 0 {
		convWindow = 8
	}
	if cfg.OutputPersistDir == "" {
		cfg.OutputPersistDir = ".outputs"
	}
	return &ContextGuard{
		cfg:           cfg,
		workspaceRoot: workspaceRoot,
		convWindow:    convWindow,
		obs:           obs,
		summarize:     summarize,
	}
}

// effectiveThreshold returns the configured token ceiling with defaults applied.
func (g *ContextGuard) effectiveThreshold() int {
	t := g.cfg.TokenThreshold
	if t <= 0 {
		return 100_000
	}
	return t
}

// effectiveTargetTokens returns the post-compaction target (ratio of threshold).
func (g *ContextGuard) effectiveTargetTokens() int {
	threshold := g.effectiveThreshold()
	target := int(float64(threshold) * g.cfg.CompactTargetRatio)
	if target <= 0 {
		target = threshold / 2
	}
	return target
}

// MaybeCompact inspects state and applies the strongest applicable compaction level.
// It returns the level applied (CompactionNone if nothing was done).
//
// Ordering: micro runs first (cheap), then if estimated total is above a soft band (85% of threshold)
// auto summarization runs when Summarize is configured, then manual trim when above hard threshold.
func (g *ContextGuard) MaybeCompact(ctx context.Context, state *LoopState) (CompactionLevel, error) {
	if state == nil {
		return CompactionNone, nil
	}

	microChanged, err := g.applyMicroCompaction(state)
	if err != nil {
		return CompactionNone, err
	}

	total := state.EstimatedTotalTokens()
	threshold := g.effectiveThreshold()
	target := g.effectiveTargetTokens()

	if total >= threshold {
		if g.obs != nil {
			g.obs.Warn("core/context: token threshold reached, applying manual compaction",
				"estimated_tokens", total,
				"threshold", threshold,
			)
		}
		if err := state.TransitionTo(PhaseCompacting, "token_threshold"); err != nil {
			if state.PhaseSafe() != PhaseCompacting {
				return CompactionNone, err
			}
		}
		if err := g.manualTrim(state, target); err != nil {
			_ = state.TransitionTo(PhaseRunning, "compact_failed")
			return CompactionNone, err
		}
		state.MarkCompaction()
		_ = state.TransitionTo(PhaseRunning, "manual_compact_done")
		return CompactionManual, nil
	}

	softLimit := int(float64(threshold) * 0.85)
	if total >= softLimit && g.summarize != nil {
		if g.obs != nil {
			g.obs.Info("core/context: soft token limit reached, summarizing old turns",
				"estimated_tokens", total,
				"soft_limit", softLimit,
			)
		}
		if err := state.TransitionTo(PhaseCompacting, "soft_token_limit"); err != nil && state.PhaseSafe() != PhaseCompacting {
			return CompactionNone, err
		}
		if err := g.CompactOldTurns(ctx, state); err != nil {
			_ = state.TransitionTo(PhaseRunning, "auto_compact_failed")
			return CompactionNone, err
		}
		state.MarkCompaction()
		_ = state.TransitionTo(PhaseRunning, "auto_compact_done")
		return CompactionAuto, nil
	}

	if microChanged {
		return CompactionMicro, nil
	}
	return CompactionNone, nil
}

// ForceManualCompact applies manual compaction regardless of current totals (used on provider overflow errors).
func (g *ContextGuard) ForceManualCompact(ctx context.Context, state *LoopState) error {
	_ = ctx
	target := g.effectiveTargetTokens()
	if err := state.TransitionTo(PhaseCompacting, "force_overflow"); err != nil && state.PhaseSafe() != PhaseCompacting {
		return err
	}
	err := g.manualTrim(state, target)
	_ = state.TransitionTo(PhaseRunning, "force_compact_done")
	if err == nil {
		state.MarkCompaction()
	}
	return err
}

// applyMicroCompaction scans tool-role messages and persists huge bodies.
func (g *ContextGuard) applyMicroCompaction(state *LoopState) (changed bool, err error) {
	state.mu.Lock()
	defer state.mu.Unlock()

	limit := g.cfg.MicroCompactSize
	if limit <= 0 {
		limit = 51200
	}
	for i := range state.Messages {
		m := &state.Messages[i]
		if m.Role != types.RoleTool {
			continue
		}
		tok := utils.EstimateTokens(m.Content)
		if tok < limit {
			continue
		}
		rel, err := g.persistLargeOutputLocked(m.Content, "micro")
		if err != nil {
			return changed, err
		}
		m.Content = fmt.Sprintf("[large_output_persisted path=%s approx_tokens=%d]", rel, tok)
		changed = true
	}
	state.recomputeTokensLocked()
	return changed, nil
}

// PersistLargeOutput writes content under OutputPersistDir (relative to workspaceRoot) and returns a placeholder marker.
func (g *ContextGuard) PersistLargeOutput(content string, purpose string) (marker string, err error) {
	rel, err := g.writeOutputFile(content, purpose)
	if err != nil {
		return "", err
	}
	tok := utils.EstimateTokens(content)
	return fmt.Sprintf("[large_output_persisted path=%s approx_tokens=%d]", rel, tok), nil
}

func (g *ContextGuard) persistLargeOutputLocked(content, purpose string) (string, error) {
	return g.writeOutputFile(content, purpose)
}

func (g *ContextGuard) writeOutputFile(content, purpose string) (string, error) {
	dir := g.cfg.OutputPersistDir
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(g.workspaceRoot, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("core/context: mkdir outputs: %w", err)
	}
	name := fmt.Sprintf("%s-%s.txt", purpose, uuid.NewString())
	full := filepath.Join(dir, name)
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("core/context: write output: %w", err)
	}
	rel, err := filepath.Rel(g.workspaceRoot, full)
	if err != nil {
		rel = full
	}
	return rel, nil
}

// CompactOldTurns summarizes messages older than the conversation window into a single system note.
// System messages at the head are preserved; recent tail (last convWindow non-system messages) is kept.
func (g *ContextGuard) CompactOldTurns(ctx context.Context, state *LoopState) error {
	if g.summarize == nil {
		return fmt.Errorf("core/context: summarizer not configured")
	}

	msgs := state.MessagesSnapshot()
	if len(msgs) <= 2 {
		return nil
	}

	var head []types.Message
	idx := 0
	for idx < len(msgs) && msgs[idx].Role == types.RoleSystem {
		head = append(head, msgs[idx])
		idx++
	}
	tail := msgs[idx:]
	if len(tail) <= g.convWindow {
		return nil
	}

	cut := len(tail) - g.convWindow
	oldPart := tail[:cut]
	keep := tail[cut:]

	var b strings.Builder
	for _, m := range oldPart {
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}

	summary, err := g.summarize(ctx, b.String())
	if err != nil {
		return fmt.Errorf("core/context: summarize: %w", err)
	}

	summaryMsg := types.Message{
		ID:        uuid.NewString(),
		Role:      types.RoleSystem,
		Content:   fmt.Sprintf("[compacted_history summary_generated_at=%s]\n%s", time.Now().UTC().Format(time.RFC3339), summary),
		CreatedAt: time.Now(),
	}

	newMsgs := append([]types.Message{}, head...)
	newMsgs = append(newMsgs, summaryMsg)
	newMsgs = append(newMsgs, keep...)
	state.SetMessages(newMsgs)
	return nil
}

func (g *ContextGuard) manualTrim(state *LoopState, targetTokens int) error {
	msgs := state.MessagesSnapshot()
	if len(msgs) == 0 {
		return nil
	}

	var head []types.Message
	i := 0
	for i < len(msgs) && msgs[i].Role == types.RoleSystem {
		head = append(head, msgs[i])
		i++
	}
	rest := msgs[i:]
	if len(rest) == 0 {
		state.SetMessages(head)
		return nil
	}

	// Walk backward from the tail, keeping the minimum suffix whose estimated tokens stay under targetTokens.
	total := estimateMessagesTokenSum(head)
	selected := make([]types.Message, 0, len(rest))
	for j := len(rest) - 1; j >= 0; j-- {
		m := rest[j]
		add := estimateMessageTokens(m)
		if total+add > targetTokens && len(selected) > 0 {
			break
		}
		selected = append([]types.Message{m}, selected...)
		total += add
	}

	trimNote := types.Message{
		ID:        uuid.NewString(),
		Role:      types.RoleSystem,
		Content:   fmt.Sprintf("[manual_compaction dropped=%d messages at=%s]", len(rest)-len(selected), time.Now().UTC().Format(time.RFC3339)),
		CreatedAt: time.Now(),
	}
	out := append([]types.Message{}, head...)
	out = append(out, trimNote)
	out = append(out, selected...)
	state.SetMessages(out)
	return nil
}

func estimateMessageTokens(m types.Message) int {
	n := utils.EstimateTokens(m.Content)
	for _, tc := range m.ToolCalls {
		n += utils.EstimateTokens(tc.Name)
		n += utils.EstimateTokens(fmt.Sprintf("%v", tc.Arguments))
	}
	if m.ToolID != "" {
		n += utils.EstimateTokens(m.ToolID)
	}
	return n
}

func estimateMessagesTokenSum(msgs []types.Message) int {
	n := 0
	for _, m := range msgs {
		n += estimateMessageTokens(m)
	}
	return n
}

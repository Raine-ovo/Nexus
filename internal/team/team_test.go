package team

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rainea/nexus/configs"
	"github.com/rainea/nexus/internal/core"
	"github.com/rainea/nexus/internal/memory"
	"github.com/rainea/nexus/internal/observability"
	"github.com/rainea/nexus/internal/planning"
	"github.com/rainea/nexus/pkg/types"
)

func tmpDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

// --- Roster Tests ---

func TestRoster_AddAndGet(t *testing.T) {
	r, err := NewRoster(tmpDir(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Add("alice", "coder", StatusIdle); err != nil {
		t.Fatal(err)
	}
	m, ok := r.Get("alice")
	if !ok {
		t.Fatal("expected alice in roster")
	}
	if m.Role != "coder" || m.Status != StatusIdle {
		t.Fatalf("unexpected member: %+v", m)
	}

	// Duplicate add should fail.
	if err := r.Add("alice", "coder", StatusIdle); err == nil {
		t.Fatal("expected error for duplicate add")
	}
}

func TestRoster_UpdateStatus(t *testing.T) {
	r, err := NewRoster(tmpDir(t))
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Add("bob", "tester", StatusIdle)
	if err := r.UpdateStatus("bob", StatusWorking); err != nil {
		t.Fatal(err)
	}
	m, _ := r.Get("bob")
	if m.Status != StatusWorking {
		t.Fatalf("expected working, got %s", m.Status)
	}
}

func TestRoster_Persistence(t *testing.T) {
	dir := tmpDir(t)
	r1, _ := NewRoster(dir)
	_ = r1.Add("carol", "devops", StatusWorking)

	// Load a fresh roster from the same dir.
	r2, err := NewRoster(dir)
	if err != nil {
		t.Fatal(err)
	}
	m, ok := r2.Get("carol")
	if !ok {
		t.Fatal("expected carol persisted")
	}
	if m.Role != "devops" {
		t.Fatalf("unexpected role: %s", m.Role)
	}
}

func TestRoster_ActiveNames(t *testing.T) {
	r, _ := NewRoster(tmpDir(t))
	_ = r.Add("a", "x", StatusWorking)
	_ = r.Add("b", "x", StatusShutdown)
	_ = r.Add("c", "x", StatusIdle)
	names := r.ActiveNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 active, got %d: %v", len(names), names)
	}
}

func TestRoster_Remove(t *testing.T) {
	r, _ := NewRoster(tmpDir(t))
	_ = r.Add("a", "x", StatusIdle)
	if err := r.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get("a"); ok {
		t.Fatal("expected a removed")
	}
}

// --- MessageBus Tests ---

func TestBus_SendAndRead(t *testing.T) {
	dir := filepath.Join(tmpDir(t), "inbox")
	bus, err := NewMessageBus(dir)
	if err != nil {
		t.Fatal(err)
	}

	err = bus.Send("lead", "alice", "hello", MsgTypeMessage, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = bus.Send("lead", "alice", "world", MsgTypeMessage, nil)
	if err != nil {
		t.Fatal(err)
	}

	msgs, err := bus.ReadInbox("alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "hello" || msgs[1].Content != "world" {
		t.Fatalf("unexpected messages: %+v", msgs)
	}
	if msgs[0].From != "lead" {
		t.Fatalf("expected from=lead, got %s", msgs[0].From)
	}

	// Second read should be empty (drained).
	msgs2, _ := bus.ReadInbox("alice")
	if len(msgs2) != 0 {
		t.Fatalf("expected empty after drain, got %d", len(msgs2))
	}
}

func TestBus_EmptyInbox(t *testing.T) {
	dir := filepath.Join(tmpDir(t), "inbox")
	bus, _ := NewMessageBus(dir)

	msgs, err := bus.ReadInbox("nobody")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected empty, got %d", len(msgs))
	}
}

func TestBus_Broadcast(t *testing.T) {
	dir := filepath.Join(tmpDir(t), "inbox")
	bus, _ := NewMessageBus(dir)

	count, err := bus.Broadcast("lead", "announcement", []string{"lead", "alice", "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2, got %d", count)
	}

	aliceMsgs, _ := bus.ReadInbox("alice")
	if len(aliceMsgs) != 1 || aliceMsgs[0].Type != MsgTypeBroadcast {
		t.Fatalf("unexpected alice msgs: %+v", aliceMsgs)
	}

	// Lead should not receive own broadcast.
	leadMsgs, _ := bus.ReadInbox("lead")
	if len(leadMsgs) != 0 {
		t.Fatalf("lead should not get own broadcast, got %d", len(leadMsgs))
	}
}

func TestBus_ProtocolMessage(t *testing.T) {
	dir := filepath.Join(tmpDir(t), "inbox")
	bus, _ := NewMessageBus(dir)

	extra := map[string]interface{}{"request_id": "req_001", "reason": "test"}
	err := bus.Send("lead", "alice", "please shutdown", MsgTypeShutdownRequest, extra)
	if err != nil {
		t.Fatal(err)
	}

	msgs, _ := bus.ReadInbox("alice")
	if len(msgs) != 1 {
		t.Fatal("expected 1 message")
	}
	if msgs[0].Type != MsgTypeShutdownRequest {
		t.Fatalf("expected shutdown_request type, got %s", msgs[0].Type)
	}
	if msgs[0].RequestID != "req_001" {
		t.Fatalf("expected request_id=req_001, got %s", msgs[0].RequestID)
	}
}

func TestBus_InvalidType(t *testing.T) {
	dir := filepath.Join(tmpDir(t), "inbox")
	bus, _ := NewMessageBus(dir)
	err := bus.Send("a", "b", "x", "invalid_type", nil)
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
}

func TestBus_ConcurrentSend(t *testing.T) {
	dir := filepath.Join(tmpDir(t), "inbox")
	bus, _ := NewMessageBus(dir)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = bus.Send("lead", "alice", "msg", MsgTypeMessage, nil)
		}(i)
	}
	wg.Wait()

	msgs, _ := bus.ReadInbox("alice")
	if len(msgs) != 20 {
		t.Fatalf("expected 20 messages after concurrent send, got %d", len(msgs))
	}
}

// --- Envelope Tests ---

func TestEnvelope_IsProtocol(t *testing.T) {
	tests := []struct {
		typ    string
		expect bool
	}{
		{MsgTypeMessage, false},
		{MsgTypeBroadcast, false},
		{MsgTypeShutdownRequest, true},
		{MsgTypeShutdownResponse, true},
		{MsgTypePlanApproval, true},
		{MsgTypePlanApprovalResponse, true},
	}
	for _, tt := range tests {
		env := MessageEnvelope{Type: tt.typ}
		if env.IsProtocol() != tt.expect {
			t.Errorf("type=%s: expected IsProtocol=%v", tt.typ, tt.expect)
		}
	}
}

// --- Protocol Tests ---

func TestProtocol_CreateAndGet(t *testing.T) {
	dir := filepath.Join(tmpDir(t), "requests")
	tracker, err := NewRequestTracker(dir)
	if err != nil {
		t.Fatal(err)
	}

	rec, err := tracker.Create(ReqKindShutdown, "lead", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != ReqStatusPending {
		t.Fatalf("expected pending, got %s", rec.Status)
	}
	if rec.Kind != ReqKindShutdown {
		t.Fatalf("expected shutdown, got %s", rec.Kind)
	}

	got, err := tracker.Get(rec.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if got.From != "lead" || got.To != "alice" {
		t.Fatalf("unexpected record: %+v", got)
	}
}

func TestProtocol_UpdateStatus(t *testing.T) {
	dir := filepath.Join(tmpDir(t), "requests")
	tracker, _ := NewRequestTracker(dir)
	rec, _ := tracker.Create(ReqKindPlanApproval, "alice", "lead")

	if err := tracker.UpdateStatus(rec.RequestID, ReqStatusApproved); err != nil {
		t.Fatal(err)
	}
	got, _ := tracker.Get(rec.RequestID)
	if got.Status != ReqStatusApproved {
		t.Fatalf("expected approved, got %s", got.Status)
	}
}

func TestProtocol_ListPending(t *testing.T) {
	dir := filepath.Join(tmpDir(t), "requests")
	tracker, _ := NewRequestTracker(dir)
	tracker.Create(ReqKindShutdown, "lead", "alice")
	rec2, _ := tracker.Create(ReqKindPlanApproval, "bob", "lead")
	_ = tracker.UpdateStatus(rec2.RequestID, ReqStatusApproved)

	pending, err := tracker.ListPending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}
}

func TestProtocol_ShutdownFlow(t *testing.T) {
	dir := tmpDir(t)
	bus, _ := NewMessageBus(filepath.Join(dir, "inbox"))
	tracker, _ := NewRequestTracker(filepath.Join(dir, "requests"))

	rec, err := RequestShutdown(bus, tracker, "alice")
	if err != nil {
		t.Fatal(err)
	}
	// Alice's inbox should have the shutdown request.
	msgs, _ := bus.ReadInbox("alice")
	if len(msgs) != 1 || msgs[0].Type != MsgTypeShutdownRequest {
		t.Fatalf("expected shutdown request in inbox: %+v", msgs)
	}

	// Alice responds.
	if err := RespondShutdown(bus, tracker, rec.RequestID, true, "alice"); err != nil {
		t.Fatal(err)
	}
	got, _ := tracker.Get(rec.RequestID)
	if got.Status != ReqStatusApproved {
		t.Fatalf("expected approved, got %s", got.Status)
	}
	leadMsgs, _ := bus.ReadInbox("lead")
	if len(leadMsgs) != 1 || leadMsgs[0].Type != MsgTypeShutdownResponse {
		t.Fatalf("expected shutdown response in lead inbox: %+v", leadMsgs)
	}
}

func TestProtocol_PlanApprovalFlow(t *testing.T) {
	dir := tmpDir(t)
	bus, _ := NewMessageBus(filepath.Join(dir, "inbox"))
	tracker, _ := NewRequestTracker(filepath.Join(dir, "requests"))

	rec, err := SubmitPlan(bus, tracker, "alice", "Delete all production data")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Plan != "Delete all production data" {
		t.Fatalf("plan not stored: %s", rec.Plan)
	}

	leadMsgs, _ := bus.ReadInbox("lead")
	if len(leadMsgs) != 1 || leadMsgs[0].Type != MsgTypePlanApproval {
		t.Fatalf("expected plan_approval in lead inbox: %+v", leadMsgs)
	}

	if err := ReviewPlan(bus, tracker, rec.RequestID, false, "Too dangerous"); err != nil {
		t.Fatal(err)
	}
	got, _ := tracker.Get(rec.RequestID)
	if got.Status != ReqStatusRejected {
		t.Fatalf("expected rejected, got %s", got.Status)
	}
	aliceMsgs, _ := bus.ReadInbox("alice")
	if len(aliceMsgs) != 1 || aliceMsgs[0].Type != MsgTypePlanApprovalResponse {
		t.Fatalf("expected plan_approval_response in alice inbox: %+v", aliceMsgs)
	}
}

// --- Autonomy Tests ---

func TestAutonomy_IsClaimable(t *testing.T) {
	// Claimable: pending, no owner, no blocked, role matches.
	task := &planning.Task{Status: planning.TaskPending}
	if !IsClaimable(task, "") {
		t.Fatal("expected claimable")
	}

	// Not claimable: already has owner.
	task2 := &planning.Task{Status: planning.TaskPending, ClaimedBy: "bob"}
	if IsClaimable(task2, "") {
		t.Fatal("should not be claimable with owner")
	}

	// Not claimable: wrong role.
	task3 := &planning.Task{Status: planning.TaskPending, ClaimRole: "frontend"}
	if IsClaimable(task3, "backend") {
		t.Fatal("should not be claimable with wrong role")
	}

	// Claimable: matching role.
	if !IsClaimable(task3, "frontend") {
		t.Fatal("should be claimable with matching role")
	}

	// Claimable: task has no role restriction.
	task4 := &planning.Task{Status: planning.TaskPending}
	if !IsClaimable(task4, "anything") {
		t.Fatal("should be claimable when no role restriction")
	}

	// Not claimable: blocked.
	task5 := &planning.Task{Status: planning.TaskPending, BlockedBy: []int{1}}
	if IsClaimable(task5, "") {
		t.Fatal("should not be claimable when blocked")
	}
}

func TestAutonomy_ScanClaimable(t *testing.T) {
	dir := filepath.Join(tmpDir(t), "tasks")
	// Seed the meta.json so NewTaskManager doesn't fail on nonexistent file.
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "meta.json"), []byte(`{"next_id":1}`), 0o644)

	tm, err := planning.NewTaskManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tm.Create("Frontend task", "desc", nil)
	// Manually set ClaimRole on disk for the test.
	t1Path := filepath.Join(dir, "task_1.json")
	data, _ := os.ReadFile(t1Path)
	var raw map[string]interface{}
	_ = json.Unmarshal(data, &raw)
	raw["claim_role"] = "frontend"
	updated, _ := json.Marshal(raw)
	_ = os.WriteFile(t1Path, updated, 0o644)

	// Re-load from disk so the modified ClaimRole is visible.
	tm2, err := planning.NewTaskManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tm2.Create("Backend task", "desc", nil)

	all := ScanClaimable(tm2, "")
	if len(all) < 2 {
		t.Fatalf("expected at least 2 claimable, got %d", len(all))
	}

	frontOnly := ScanClaimable(tm2, "frontend")
	// Should include the one with matching role + the one with no role.
	found := false
	for _, task := range frontOnly {
		if task.Title == "Backend task" {
			found = true
		}
	}
	if !found {
		t.Fatal("backend task should be claimable by frontend (no role restriction)")
	}
}

func TestAutonomy_ClaimLogger(t *testing.T) {
	dir := tmpDir(t)
	cl := NewClaimLogger(dir)
	if err := cl.Log(1, "alice", "coder", "auto"); err != nil {
		t.Fatal(err)
	}
	if err := cl.Log(2, "bob", "tester", "manual"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "claim_events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := splitLines(data)
	nonEmpty := 0
	for _, line := range lines {
		if len(line) > 0 {
			nonEmpty++
		}
	}
	if nonEmpty != 2 {
		t.Fatalf("expected 2 events, got %d", nonEmpty)
	}
}

// --- Teammate Lifecycle Tests ---

// stubModel is a test ChatModel that returns canned responses.
type stubModel struct {
	responses []core.ChatModelResponse
	idx       int
	mu        sync.Mutex
}

func (s *stubModel) Generate(ctx context.Context, system string, messages []types.Message, tools []types.ToolDefinition) (*core.ChatModelResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idx >= len(s.responses) {
		return &core.ChatModelResponse{Content: "done", FinishReason: "stop"}, nil
	}
	r := s.responses[s.idx]
	s.idx++
	return &r, nil
}

func TestTeammate_WorkAndIdle(t *testing.T) {
	dir := tmpDir(t)
	roster, _ := NewRoster(dir)
	bus, _ := NewMessageBus(filepath.Join(dir, "inbox"))
	tracker, _ := NewRequestTracker(filepath.Join(dir, "requests"))
	_ = roster.Add("worker", "coder", StatusIdle)

	model := &stubModel{
		responses: []core.ChatModelResponse{
			{Content: "I completed the work.", FinishReason: "stop"},
		},
	}

	cfg := TeammateConfig{
		Name:         "worker",
		Role:         "coder",
		Model:        model,
		Tools:        nil,
		SystemPrompt: "test",
		Bus:          bus,
		Roster:       roster,
		Tracker:      tracker,
		Observer:     nil,
		PollInterval: 50 * time.Millisecond,
		MaxIdlePolls: 3,
	}

	mate := NewTeammate(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mate.Start(ctx, "Do some work")

	select {
	case <-mate.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("teammate did not shut down after idle timeout")
	}

	m, _ := roster.Get("worker")
	if m.Status != StatusShutdown {
		t.Fatalf("expected shutdown, got %s", m.Status)
	}
}

func TestTeammate_InboxWakesFromIdle(t *testing.T) {
	dir := tmpDir(t)
	roster, _ := NewRoster(dir)
	bus, _ := NewMessageBus(filepath.Join(dir, "inbox"))
	tracker, _ := NewRequestTracker(filepath.Join(dir, "requests"))
	_ = roster.Add("worker", "coder", StatusIdle)

	callCount := 0
	model := &stubModel{
		responses: []core.ChatModelResponse{
			{Content: "first work done", FinishReason: "stop"},
			{Content: "second work done", FinishReason: "stop"},
		},
	}

	cfg := TeammateConfig{
		Name:         "worker",
		Role:         "coder",
		Model:        model,
		Tools:        nil,
		SystemPrompt: "test",
		Bus:          bus,
		Roster:       roster,
		Tracker:      tracker,
		Observer:     nil,
		PollInterval: 100 * time.Millisecond,
		MaxIdlePolls: 10,
	}
	_ = callCount

	mate := NewTeammate(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mate.Start(ctx, "Initial work")

	// Wait for first work phase to complete and teammate to enter idle.
	time.Sleep(300 * time.Millisecond)

	// Send a message to wake it up.
	_ = bus.Send("lead", "worker", "do more work", MsgTypeMessage, nil)

	// It should process the message and eventually idle-timeout again.
	select {
	case <-mate.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("teammate did not shut down after second idle")
	}
}

func TestTeammate_RuntimePersistsSemanticMemory(t *testing.T) {
	dir := tmpDir(t)
	roster, _ := NewRoster(dir)
	bus, _ := NewMessageBus(filepath.Join(dir, "inbox"))
	tracker, _ := NewRequestTracker(filepath.Join(dir, "requests"))
	_ = roster.Add("worker", "coder", StatusIdle)

	model := &stubModel{
		responses: []core.ChatModelResponse{
			{Content: "I completed the work.", FinishReason: "stop"},
			{Content: `{"score":0.95,"correctness":0.95,"completeness":0.95,"safety":1.0,"coherence":0.95,"reason":"good"}`, FinishReason: "stop"},
			{Content: `{"entries":[{"category":"project","key":"worker_status","value":"worker completed the assigned work"}]}`, FinishReason: "stop"},
		},
	}

	memManager, err := memory.NewManager(configs.MemoryConfig{
		ConversationWindow: 20,
		MaxSemanticEntries: 20,
		SemanticFile:       filepath.Join(dir, "semantic.yaml"),
	})
	if err != nil {
		t.Fatal(err)
	}
	deps := &core.AgentDependencies{
		MemManager:         memManager,
		AgentConfig:        configs.AgentConfig{TokenThreshold: 80000},
		ConversationWindow: 20,
	}
	rt, err := NewRuntime(deps, model, nil, "demo-run", dir, configs.ReflectionConfig{
		Enabled:       true,
		Threshold:     0.7,
		MaxAttempts:   2,
		MaxMemEntries: 20,
		MemoryFile:    filepath.Join(dir, "reflections.yaml"),
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := TeammateConfig{
		Name:         "worker",
		Role:         "coder",
		Model:        model,
		Deps:         deps,
		Tools:        nil,
		SystemPrompt: "test",
		Bus:          bus,
		Roster:       roster,
		Tracker:      tracker,
		Runtime:      rt,
		PollInterval: 50 * time.Millisecond,
		MaxIdlePolls: 3,
	}

	mate := NewTeammate(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mate.Start(ctx, "Do some work")

	select {
	case <-mate.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("teammate did not shut down after idle timeout")
	}

	semanticData, err := os.ReadFile(filepath.Join(dir, "semantic.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(semanticData), "worker_status") {
		t.Fatalf("expected semantic memory entry, got %s", string(semanticData))
	}
}

func TestTeammate_ShutdownViaProtocol(t *testing.T) {
	dir := tmpDir(t)
	roster, _ := NewRoster(dir)
	bus, _ := NewMessageBus(filepath.Join(dir, "inbox"))
	tracker, _ := NewRequestTracker(filepath.Join(dir, "requests"))
	_ = roster.Add("worker", "coder", StatusIdle)

	model := &stubModel{
		responses: []core.ChatModelResponse{
			{Content: "working...", FinishReason: "stop"},
		},
	}

	cfg := TeammateConfig{
		Name:         "worker",
		Role:         "coder",
		Model:        model,
		Tools:        nil,
		SystemPrompt: "test",
		Bus:          bus,
		Roster:       roster,
		Tracker:      tracker,
		Observer:     nil,
		PollInterval: 100 * time.Millisecond,
		MaxIdlePolls: 50,
	}

	mate := NewTeammate(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mate.Start(ctx, "Work until told to stop")

	// Wait for first work to complete and idle to start.
	time.Sleep(300 * time.Millisecond)

	// Send shutdown request.
	rec, _ := RequestShutdown(bus, tracker, "worker")
	mate.RequestShutdown(rec.RequestID)

	select {
	case <-mate.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("teammate did not shut down after shutdown request")
	}

	m, _ := roster.Get("worker")
	if m.Status != StatusShutdown {
		t.Fatalf("expected shutdown, got %s", m.Status)
	}
}

func TestTeammate_ShutdownReleasesClaimedTasks(t *testing.T) {
	dir := tmpDir(t)
	taskDir := filepath.Join(dir, "tasks")
	tm, err := planning.NewTaskManager(taskDir)
	if err != nil {
		t.Fatal(err)
	}
	task, err := tm.Create("audit permission flow", "verify permission pipeline", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tm.Claim(task.ID, "worker", "manual"); err != nil {
		t.Fatal(err)
	}

	roster, _ := NewRoster(dir)
	bus, _ := NewMessageBus(filepath.Join(dir, "inbox"))
	tracker, _ := NewRequestTracker(filepath.Join(dir, "requests"))
	_ = roster.Add("worker", "coder", StatusIdle)

	model := &stubModel{
		responses: []core.ChatModelResponse{
			{Content: "working...", FinishReason: "stop"},
		},
	}

	cfg := TeammateConfig{
		Name:         "worker",
		Role:         "coder",
		Model:        model,
		SystemPrompt: "test",
		Bus:          bus,
		Roster:       roster,
		Tracker:      tracker,
		TaskManager:  tm,
		PollInterval: 50 * time.Millisecond,
		MaxIdlePolls: 2,
	}

	mate := NewTeammate(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mate.Start(ctx, "Finish your work")

	select {
	case <-mate.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("teammate did not shut down after idle timeout")
	}

	tasks := tm.List(nil)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	got := tasks[0]
	if got.Status != planning.TaskPending {
		t.Fatalf("expected claimed task to be re-queued as pending, got %s", got.Status)
	}
	if got.ClaimedBy != "" {
		t.Fatalf("expected claim owner to be cleared, got %q", got.ClaimedBy)
	}
}

// --- Manager Tests ---

func TestManager_HandleRequest(t *testing.T) {
	dir := tmpDir(t)
	taskDir := filepath.Join(dir, "tasks")
	tm, _ := planning.NewTaskManager(taskDir)

	model := &stubModel{
		responses: []core.ChatModelResponse{
			{Content: "I am the team lead. Ready to receive requests.", FinishReason: "stop"},
			{Content: "Hello! I'm here to help.", FinishReason: "stop"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mgr, err := NewManager(ctx, ManagerConfig{
		TeamDir:          filepath.Join(dir, ".team"),
		Model:            model,
		Deps:             &core.AgentDependencies{},
		TaskManager:      tm,
		Observer:         nil,
		LeadSystemPrompt: "You are the lead.",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Shutdown(ctx)

	out, err := mgr.HandleRequest(ctx, "test-session", "Hello team")
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("expected non-empty response")
	}
}

func TestManager_SpawnAndList(t *testing.T) {
	dir := tmpDir(t)
	taskDir := filepath.Join(dir, "tasks")
	tm, _ := planning.NewTaskManager(taskDir)

	model := &stubModel{
		responses: []core.ChatModelResponse{
			{Content: "lead ready", FinishReason: "stop"},
			{Content: "teammate work done", FinishReason: "stop"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mgr, _ := NewManager(ctx, ManagerConfig{
		TeamDir:          filepath.Join(dir, ".team"),
		Model:            model,
		Deps:             &core.AgentDependencies{},
		TaskManager:      tm,
		Observer:         nil,
		LeadSystemPrompt: "Lead.",
	})
	defer mgr.Shutdown(ctx)

	err := mgr.Spawn(ctx, "alice", "coder", "Write some code")
	if err != nil {
		t.Fatal(err)
	}

	members := mgr.ListTeammates()
	found := false
	for _, m := range members {
		if m.Name == "alice" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected alice in roster")
	}

	// Cannot spawn duplicate.
	if err := mgr.Spawn(ctx, "alice", "coder", "more work"); err == nil {
		t.Fatal("expected error for duplicate spawn")
	}

	// Cannot use lead name.
	if err := mgr.Spawn(ctx, "lead", "lead", "x"); err == nil {
		t.Fatal("expected error for lead name")
	}
}

func TestManager_RegisterTemplate(t *testing.T) {
	dir := tmpDir(t)
	taskDir := filepath.Join(dir, "tasks")
	tm, _ := planning.NewTaskManager(taskDir)

	model := &stubModel{
		responses: []core.ChatModelResponse{
			{Content: "lead ready", FinishReason: "stop"},
			{Content: "code review done", FinishReason: "stop"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mgr, _ := NewManager(ctx, ManagerConfig{
		TeamDir:          filepath.Join(dir, ".team"),
		Model:            model,
		Deps:             &core.AgentDependencies{},
		TaskManager:      tm,
		LeadSystemPrompt: "Lead.",
	})
	defer mgr.Shutdown(ctx)

	mgr.RegisterTemplate("code_reviewer", AgentTemplate{
		Role:         "code_reviewer",
		SystemPrompt: "You are a code reviewer.",
	})

	err := mgr.Spawn(ctx, "reviewer1", "code_reviewer", "Review this PR")
	if err != nil {
		t.Fatal(err)
	}
}

func TestManager_RehydrateResetsStaleWorkingStatus(t *testing.T) {
	dir := tmpDir(t)
	taskDir := filepath.Join(dir, "tasks")
	tm, _ := planning.NewTaskManager(taskDir)

	roster, err := NewRoster(filepath.Join(dir, ".team"))
	if err != nil {
		t.Fatal(err)
	}
	if err := roster.Add("Atlas", "planner", StatusWorking); err != nil {
		t.Fatal(err)
	}

	model := &stubModel{
		responses: []core.ChatModelResponse{
			{Content: "lead ready", FinishReason: "stop"},
			{Content: "atlas resumed", FinishReason: "stop"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mgr, err := NewManager(ctx, ManagerConfig{
		TeamDir:          filepath.Join(dir, ".team"),
		Model:            model,
		Deps:             &core.AgentDependencies{},
		TaskManager:      tm,
		LeadSystemPrompt: "Lead.",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Shutdown(ctx)

	member, ok := mgr.Roster().Get("Atlas")
	if !ok {
		t.Fatal("expected Atlas to exist after rehydrate")
	}
	if member.Status != StatusWorking {
		t.Fatalf("expected Atlas to be rehydrated as working, got %s", member.Status)
	}
}

func TestIdentityBlock(t *testing.T) {
	block := identityBlock("alice", "coder", "default")
	if block == "" {
		t.Fatal("expected non-empty identity block")
	}
	ack := identityAck("alice")
	if ack != "I am alice. Continuing." {
		t.Fatalf("unexpected ack: %s", ack)
	}
}

func TestRuntime_WritesLatestTraceSnapshot(t *testing.T) {
	dir := tmpDir(t)
	sandbox := filepath.Join(dir, ".runs", "demo")
	obs := observability.New(configs.ObservabilityConfig{
		TraceEnabled:   true,
		MetricsEnabled: true,
		LogLevel:       "error",
	})
	rt := &Runtime{
		obs:        obs,
		fullObs:    obs,
		runLabel:   "demo",
		sandboxDir: sandbox,
	}

	reqTrace, reqCtx := rt.startSpan(context.Background(), "lead", "request:user_turn", "on_start")
	llmTrace, _ := rt.startSpan(reqCtx, "lead", "llm_call", "on_llm_start")
	time.Sleep(5 * time.Millisecond)
	rt.endSpan(llmTrace, nil, "on_llm_end")
	toolTrace, _ := rt.startSpan(reqCtx, "lead", "tool:read_file", "on_tool_start")
	time.Sleep(5 * time.Millisecond)
	rt.endSpan(toolTrace, nil, "on_tool_end")
	time.Sleep(5 * time.Millisecond)
	rt.endSpan(reqTrace, nil, "on_end")

	data, err := os.ReadFile(filepath.Join(sandbox, "latest-traces.json"))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["run"] != "demo" {
		t.Fatalf("unexpected run in snapshot: %+v", payload)
	}
	traces, ok := payload["traces"].([]interface{})
	if !ok || len(traces) == 0 {
		t.Fatalf("expected traces in snapshot, got: %+v", payload["traces"])
	}
	metrics, ok := payload["metrics"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected metrics map in snapshot, got: %+v", payload["metrics"])
	}
	counters, ok := metrics["counters"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected counters in snapshot metrics, got: %+v", metrics)
	}
	if _, ok := counters["callbacks_on_llm_start"]; !ok {
		t.Fatalf("expected llm counter in snapshot metrics, got: %+v", counters)
	}
}

func TestRuntime_RunWithReflectionStoresSuccessfulAttempt(t *testing.T) {
	dir := tmpDir(t)
	reflectionFile := filepath.Join(dir, "reflections.yaml")
	model := &stubModel{
		responses: []core.ChatModelResponse{
			{Content: `{"score":0.92,"correctness":0.9,"completeness":0.92,"safety":1.0,"coherence":0.9,"reason":"good"}`, FinishReason: "stop"},
		},
	}

	rt, err := NewRuntime(nil, model, nil, "demo", dir, configs.ReflectionConfig{
		Enabled:       true,
		Threshold:     0.7,
		MaxAttempts:   2,
		MaxMemEntries: 20,
		MemoryFile:    reflectionFile,
	})
	if err != nil {
		t.Fatal(err)
	}

	output, err := rt.RunWithReflection(
		context.Background(),
		"worker",
		"test worker",
		"system",
		nil,
		"verify reflection persistence",
		func(ctx context.Context, input string) (string, error) {
			return "done", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if output != "done" {
		t.Fatalf("unexpected output: %s", output)
	}

	data, err := os.ReadFile(reflectionFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "successful_completion") {
		t.Fatalf("expected successful reflection to be persisted, got %s", string(data))
	}
}

// --- Delegate Tests ---

func TestDelegateWork_BasicExecution(t *testing.T) {
	model := &stubModel{
		responses: []core.ChatModelResponse{
			{Content: "The code looks clean, no issues found.", FinishReason: "stop"},
		},
	}

	tmpl := AgentTemplate{
		Role:         "code_reviewer",
		SystemPrompt: "You are a code reviewer.",
	}

	result, err := DelegateWork(context.Background(), model, nil, tmpl, "Review function Foo in main.go")
	if err != nil {
		t.Fatal(err)
	}
	if result != "The code looks clean, no issues found." {
		t.Fatalf("unexpected result: %s", result)
	}
}

func TestDelegateWork_ContextIsolation(t *testing.T) {
	// Verify the delegate starts with a clean message list each time.
	callCount := 0
	model := &contextCapturingModel{
		onGenerate: func(messages []types.Message) *core.ChatModelResponse {
			callCount++
			// First delegate call: should have exactly 1 message (the user task).
			if len(messages) != 1 {
				t.Errorf("delegate call %d: expected 1 message (clean context), got %d", callCount, len(messages))
			}
			return &core.ChatModelResponse{Content: "done", FinishReason: "stop"}
		},
	}

	tmpl := AgentTemplate{Role: "coder", SystemPrompt: "coder"}

	// First delegation.
	_, err := DelegateWork(context.Background(), model, nil, tmpl, "Task A")
	if err != nil {
		t.Fatal(err)
	}

	// Second delegation — should start fresh, NOT carry over Task A's context.
	_, err = DelegateWork(context.Background(), model, nil, tmpl, "Task B")
	if err != nil {
		t.Fatal(err)
	}
	if callCount != 2 {
		t.Fatalf("expected 2 delegate calls, got %d", callCount)
	}
}

func TestDelegateWork_WithToolUse(t *testing.T) {
	model := &stubModel{
		responses: []core.ChatModelResponse{
			{
				Content:      "",
				FinishReason: "tool_calls",
				ToolCalls: []types.ToolCall{
					{ID: "call_1", Name: "check_style", Arguments: map[string]interface{}{"file": "main.go"}},
				},
			},
			{Content: "Style check passed, code is clean.", FinishReason: "stop"},
		},
	}

	styleTool := &types.ToolMeta{
		Definition: types.ToolDefinition{
			Name:        "check_style",
			Description: "Check code style.",
			Parameters:  map[string]interface{}{"type": "object"},
		},
		Handler: func(ctx context.Context, args map[string]interface{}) (*types.ToolResult, error) {
			return &types.ToolResult{Content: "No style issues found."}, nil
		},
	}

	tmpl := AgentTemplate{
		Role:         "code_reviewer",
		SystemPrompt: "You review code.",
		Tools:        []*types.ToolMeta{styleTool},
	}

	result, err := DelegateWork(context.Background(), model, nil, tmpl, "Check style of main.go")
	if err != nil {
		t.Fatal(err)
	}
	if result != "Style check passed, code is clean." {
		t.Fatalf("unexpected: %s", result)
	}
}

func TestDelegateWork_UnknownRole(t *testing.T) {
	dir := tmpDir(t)
	taskDir := filepath.Join(dir, "tasks")
	_ = os.MkdirAll(taskDir, 0o755)
	_ = os.WriteFile(filepath.Join(taskDir, "meta.json"), []byte(`{"next_id":1}`), 0o644)
	tm, _ := planning.NewTaskManager(taskDir)

	model := &stubModel{
		responses: []core.ChatModelResponse{
			{Content: "lead ready", FinishReason: "stop"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr, _ := NewManager(ctx, ManagerConfig{
		TeamDir:          filepath.Join(dir, ".team"),
		Model:            model,
		Deps:             &core.AgentDependencies{},
		TaskManager:      tm,
		LeadSystemPrompt: "Lead.",
	})
	defer mgr.Shutdown(ctx)

	// Only register "coder", not "frontend".
	mgr.RegisterTemplate("coder", AgentTemplate{Role: "coder", SystemPrompt: "coder"})

	// The delegate_task tool should report an error for unknown role.
	tools := BuildLeadTools(mgr, mgr.bus, mgr.roster, mgr.tracker)
	var delegateTool *types.ToolMeta
	for _, t := range tools {
		if t.Definition.Name == "delegate_task" {
			delegateTool = t
			break
		}
	}
	if delegateTool == nil {
		t.Fatal("delegate_task tool not found in lead tools")
	}

	result, _ := delegateTool.Handler(ctx, map[string]interface{}{
		"role": "frontend",
		"task": "build a page",
	})
	if !result.IsError {
		t.Fatal("expected error for unknown role")
	}
}

func TestDelegateWork_EmptyTask(t *testing.T) {
	model := &stubModel{}
	tmpl := AgentTemplate{Role: "coder"}
	_, err := DelegateWork(context.Background(), model, nil, tmpl, "")
	if err == nil {
		t.Fatal("expected error for empty task")
	}
}

func TestParseDispatchProfile(t *testing.T) {
	content := `
thinking...
<dispatch_profile>
simple=false
needs_persistence=true
needs_isolation=false
expected_follow_up=true
specialist_role=planner
reason=long_horizon_follow_up
</dispatch_profile>
`
	profile, err := parseDispatchProfile(content)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Simple {
		t.Fatal("expected simple=false")
	}
	if !profile.NeedsPersistence || !profile.ExpectedFollowUp {
		t.Fatalf("unexpected profile: %+v", profile)
	}
	if profile.SpecialistRole != "planner" {
		t.Fatalf("unexpected specialist role: %q", profile.SpecialistRole)
	}
}

func TestValidateLeadRoutingCall_IsolationRequiresDelegate(t *testing.T) {
	roster, _ := NewRoster(tmpDir(t))
	profile := &dispatchProfile{
		NeedsIsolation:   true,
		ExpectedFollowUp: false,
	}

	err := validateLeadRoutingCall(profile, roster, types.ToolCall{
		Name:      "spawn_teammate",
		Arguments: map[string]interface{}{"name": "atlas", "role": "planner"},
	})
	if err == nil {
		t.Fatal("expected spawn_teammate to be rejected for isolation path")
	}

	err = validateLeadRoutingCall(profile, roster, types.ToolCall{
		Name:      "delegate_task",
		Arguments: map[string]interface{}{"role": "planner", "task": "inspect docs"},
	})
	if err != nil {
		t.Fatalf("delegate should be allowed for isolation path: %v", err)
	}
}

func TestValidateLeadRoutingCall_PersistencePrefersReuse(t *testing.T) {
	roster, _ := NewRoster(tmpDir(t))
	_ = roster.Add("Atlas", "planner", StatusIdle)

	profile := &dispatchProfile{
		NeedsPersistence: true,
		ExpectedFollowUp: true,
		SpecialistRole:   "planner",
	}

	err := validateLeadRoutingCall(profile, roster, types.ToolCall{
		Name:      "spawn_teammate",
		Arguments: map[string]interface{}{"name": "NewPlanner", "role": "planner"},
	})
	if err == nil {
		t.Fatal("expected spawn_teammate to be rejected when reusable teammate exists")
	}

	err = validateLeadRoutingCall(profile, roster, types.ToolCall{
		Name:      "send_message",
		Arguments: map[string]interface{}{"to": "Atlas", "content": "continue the task"},
	})
	if err != nil {
		t.Fatalf("expected send_message to reusable teammate to be allowed: %v", err)
	}
}

func TestValidateLeadRoutingCall_AllowsShutdownTargetForRevive(t *testing.T) {
	roster, _ := NewRoster(tmpDir(t))
	_ = roster.Add("Atlas", "planner", StatusShutdown)

	profile := &dispatchProfile{
		NeedsPersistence: true,
		ExpectedFollowUp: true,
		SpecialistRole:   "planner",
	}

	err := validateLeadRoutingCall(profile, roster, types.ToolCall{
		Name:      "send_message",
		Arguments: map[string]interface{}{"to": "Atlas", "content": "continue the task"},
	})
	if err != nil {
		t.Fatalf("expected shutdown teammate to be revivable via send_message: %v", err)
	}
}

func TestLeadSendMessage_RevivesShutdownTeammate(t *testing.T) {
	dir := tmpDir(t)
	taskDir := filepath.Join(dir, "tasks")
	tm, err := planning.NewTaskManager(taskDir)
	if err != nil {
		t.Fatal(err)
	}

	roster, err := NewRoster(filepath.Join(dir, ".team"))
	if err != nil {
		t.Fatal(err)
	}
	if err := roster.Add("Atlas", "planner", StatusShutdown); err != nil {
		t.Fatal(err)
	}

	model := &stubModel{
		responses: []core.ChatModelResponse{
			{Content: "lead ready", FinishReason: "stop"},
			{Content: "atlas resumed", FinishReason: "stop"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mgr, err := NewManager(ctx, ManagerConfig{
		TeamDir:          filepath.Join(dir, ".team"),
		Model:            model,
		Deps:             &core.AgentDependencies{},
		TaskManager:      tm,
		LeadSystemPrompt: "Lead.",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Shutdown(ctx)

	sendTool := toolSendMessage(leadName, mgr.bus, mgr)
	result, err := sendTool.Handler(ctx, map[string]interface{}{
		"to":      "Atlas",
		"content": "please continue the task",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("expected send_message to revive teammate, got error: %s", result.Content)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		member, ok := mgr.Roster().Get("Atlas")
		if ok && member.Status == StatusWorking {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	member, _ := mgr.Roster().Get("Atlas")
	t.Fatalf("expected Atlas to be revived as working, got status %s", member.Status)
}

// contextCapturingModel lets tests inspect the messages passed to Generate.
type contextCapturingModel struct {
	onGenerate func(messages []types.Message) *core.ChatModelResponse
}

func (m *contextCapturingModel) Generate(ctx context.Context, system string, messages []types.Message, tools []types.ToolDefinition) (*core.ChatModelResponse, error) {
	resp := m.onGenerate(messages)
	return resp, nil
}

package planning

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rainea/nexus/pkg/utils"
)

// Task status values.
const (
	TaskPending    = "pending"
	TaskInProgress = "in_progress"
	TaskCompleted  = "completed"
	TaskBlocked    = "blocked"
	TaskCancelled  = "cancelled"
)

// Task represents a unit of work in the DAG.
type Task struct {
	ID          int       `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	BlockedBy   []int     `json:"blocked_by"`
	Blocks      []int     `json:"blocks"`
	ClaimedBy   string    `json:"claimed_by,omitempty"`
	ClaimRole   string    `json:"claim_role,omitempty"`
	ClaimedAt   time.Time `json:"claimed_at,omitempty"`
	ClaimSource string    `json:"claim_source,omitempty"` // "auto" or "manual"
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type taskMeta struct {
	NextID int `json:"next_id"`
}

// TaskManager manages the persistent task DAG (JSON files under taskDir).
type TaskManager struct {
	taskDir string
	nextID  int
	tasks   map[int]*Task
	mu      sync.Mutex
}

// NewTaskManager loads existing tasks from disk.
func NewTaskManager(taskDir string) (*TaskManager, error) {
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return nil, fmt.Errorf("task dir: %w", err)
	}
	m := &TaskManager{
		taskDir: taskDir,
		tasks:   make(map[int]*Task),
		nextID:  1,
	}
	if err := m.loadFromDisk(); err != nil {
		return nil, err
	}
	return m, nil
}

func taskFilePath(dir string, id int) string {
	return filepath.Join(dir, fmt.Sprintf("task_%d.json", id))
}

func metaPath(dir string) string {
	return filepath.Join(dir, "meta.json")
}

func (m *TaskManager) loadFromDisk() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	meta := taskMeta{NextID: 1}
	if err := utils.ReadJSON(metaPath(m.taskDir), &meta); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read meta: %w", err)
		}
	}
	if meta.NextID < 1 {
		meta.NextID = 1
	}
	m.nextID = meta.NextID

	entries, err := os.ReadDir(m.taskDir)
	if err != nil {
		return fmt.Errorf("read task dir: %w", err)
	}

	maxSeen := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "task_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		idStr := strings.TrimSuffix(strings.TrimPrefix(name, "task_"), ".json")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			continue
		}
		var t Task
		if err := utils.ReadJSON(filepath.Join(m.taskDir, name), &t); err != nil {
			return fmt.Errorf("read task %d: %w", id, err)
		}
		if t.ID != id {
			t.ID = id
		}
		cp := t
		m.tasks[id] = &cp
		if id > maxSeen {
			maxSeen = id
		}
	}
	if maxSeen+1 > m.nextID {
		m.nextID = maxSeen + 1
	}
	return nil
}

func (m *TaskManager) persistMetaLocked() error {
	meta := taskMeta{NextID: m.nextID}
	return utils.WriteJSON(metaPath(m.taskDir), &meta)
}

func (m *TaskManager) persistTaskLocked(t *Task) error {
	return utils.WriteJSON(taskFilePath(m.taskDir, t.ID), t)
}

func (m *TaskManager) allPrereqsCompletedLocked(t *Task) bool {
	for _, bid := range t.BlockedBy {
		p := m.tasks[bid]
		if p == nil || p.Status != TaskCompleted {
			return false
		}
	}
	return true
}

func (m *TaskManager) initialStatusLocked(blockedBy []int) string {
	for _, bid := range blockedBy {
		p := m.tasks[bid]
		if p == nil || p.Status != TaskCompleted {
			return TaskBlocked
		}
	}
	return TaskPending
}

// Create adds a new task and persists it. blockedBy lists prerequisite task IDs.
func (m *TaskManager) Create(title, desc string, blockedBy []int) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, bid := range blockedBy {
		if m.tasks[bid] == nil {
			return nil, fmt.Errorf("task: blocked_by references unknown task %d", bid)
		}
	}

	id := m.nextID
	if m.hasCycleLocked(id, blockedBy) {
		return nil, fmt.Errorf("task: blocked_by would create a cycle")
	}

	now := time.Now().UTC()
	t := &Task{
		ID:          id,
		Title:       title,
		Description: desc,
		Status:      m.initialStatusLocked(blockedBy),
		BlockedBy:   append([]int(nil), blockedBy...),
		Blocks:      nil,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	m.tasks[id] = t
	for _, bid := range blockedBy {
		b := m.tasks[bid]
		b.Blocks = appendUniqueInt(b.Blocks, id)
		if err := m.persistTaskLocked(b); err != nil {
			return nil, err
		}
	}
	m.nextID++
	if err := m.persistTaskLocked(t); err != nil {
		return nil, err
	}
	if err := m.persistMetaLocked(); err != nil {
		return nil, err
	}
	return t, nil
}

func appendUniqueInt(s []int, v int) []int {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// hasCycleLocked reports whether a new task taskID with BlockedBy=newBlockedBy would introduce a cycle.
func (m *TaskManager) hasCycleLocked(taskID int, newBlockedBy []int) bool {
	tmp := Task{
		ID:        taskID,
		BlockedBy: append([]int(nil), newBlockedBy...),
	}
	tasks := make(map[int]*Task, len(m.tasks)+1)
	for k, v := range m.tasks {
		cp := *v
		tasks[k] = &cp
	}
	tasks[taskID] = &tmp

	var dfs func(u int, recStack map[int]bool) bool
	dfs = func(u int, recStack map[int]bool) bool {
		tu := tasks[u]
		if tu == nil {
			return false
		}
		if recStack[u] {
			return true
		}
		recStack[u] = true
		for _, v := range tu.BlockedBy {
			if dfs(v, recStack) {
				return true
			}
		}
		recStack[u] = false
		return false
	}
	return dfs(taskID, make(map[int]bool))
}

// HasCycle is exported for tests and external validation before Create.
func (m *TaskManager) HasCycle(taskID int, newBlockedBy []int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hasCycleLocked(taskID, newBlockedBy)
}

// Update sets task status and persists. Completing a task unblocks downstream work.
func (m *TaskManager) Update(id int, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t := m.tasks[id]
	if t == nil {
		return fmt.Errorf("task: unknown id %d", id)
	}
	switch status {
	case TaskPending, TaskInProgress, TaskCompleted, TaskBlocked, TaskCancelled:
	default:
		return fmt.Errorf("task: invalid status %q", status)
	}
	prev := t.Status
	t.Status = status
	t.UpdatedAt = time.Now().UTC()
	if status != TaskInProgress {
		t.ClaimedBy = ""
	}
	if err := m.persistTaskLocked(t); err != nil {
		return err
	}
	if prev != TaskCompleted && status == TaskCompleted {
		m.resolveDownstreamLocked(id)
	}
	return nil
}

// ResolveDownstream unblocks tasks that were waiting on completedID (same as completion side-effect).
func (m *TaskManager) ResolveDownstream(taskID int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resolveDownstreamLocked(taskID)
}

func (m *TaskManager) resolveDownstreamLocked(completedID int) {
	for _, child := range m.tasks {
		if child == nil {
			continue
		}
		if !containsInt(child.BlockedBy, completedID) {
			continue
		}
		if child.Status != TaskBlocked && child.Status != TaskPending {
			continue
		}
		if m.allPrereqsCompletedLocked(child) && child.Status == TaskBlocked {
			child.Status = TaskPending
			child.UpdatedAt = time.Now().UTC()
			_ = m.persistTaskLocked(child)
		}
	}
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// Get returns a task by ID.
func (m *TaskManager) Get(id int) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	t := m.tasks[id]
	if t == nil {
		return nil, fmt.Errorf("task: unknown id %d", id)
	}
	cp := *t
	return &cp, nil
}

// List returns tasks sorted by ID, optionally filtered.
func (m *TaskManager) List(filter func(*Task) bool) []Task {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := make([]int, 0, len(m.tasks))
	for id := range m.tasks {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	var out []Task
	for _, id := range ids {
		t := m.tasks[id]
		if t == nil {
			continue
		}
		if filter != nil && !filter(t) {
			continue
		}
		cp := *t
		out = append(out, cp)
	}
	return out
}

// Delete removes a task and updates neighbors. Fails if non-terminal dependents exist.
func (m *TaskManager) Delete(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t := m.tasks[id]
	if t == nil {
		return fmt.Errorf("task: unknown id %d", id)
	}
	for _, bid := range t.Blocks {
		child := m.tasks[bid]
		if child == nil {
			continue
		}
		if child.Status != TaskCancelled && child.Status != TaskCompleted {
			return fmt.Errorf("task: cannot delete %d: downstream task %d still active", id, bid)
		}
	}
	for _, bid := range t.BlockedBy {
		if p := m.tasks[bid]; p != nil {
			p.Blocks = removeInt(p.Blocks, id)
			_ = m.persistTaskLocked(p)
		}
	}
	for _, bid := range t.Blocks {
		if child := m.tasks[bid]; child != nil {
			child.BlockedBy = removeInt(child.BlockedBy, id)
			child.UpdatedAt = time.Now().UTC()
			_ = m.persistTaskLocked(child)
		}
	}
	delete(m.tasks, id)
	_ = os.Remove(taskFilePath(m.taskDir, id))
	return nil
}

func removeInt(s []int, v int) []int {
	out := s[:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// Claim assigns agentName to a pending, unblocked task if still unclaimed.
// source should be "auto" (autonomous claim) or "manual" (explicit assignment).
func (m *TaskManager) Claim(id int, agentName, source string) (*Task, error) {
	if agentName == "" {
		return nil, fmt.Errorf("task: agent name required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	t := m.tasks[id]
	if t == nil {
		return nil, fmt.Errorf("task: unknown id %d", id)
	}
	if t.Status != TaskPending {
		return nil, fmt.Errorf("task: task %d not pending", id)
	}
	if !m.allPrereqsCompletedLocked(t) {
		return nil, fmt.Errorf("task: task %d still blocked", id)
	}
	if t.ClaimedBy != "" {
		return nil, fmt.Errorf("task: task %d already claimed by %s", id, t.ClaimedBy)
	}
	now := time.Now().UTC()
	t.ClaimedBy = agentName
	t.ClaimedAt = now
	t.ClaimSource = source
	t.Status = TaskInProgress
	t.UpdatedAt = now
	if err := m.persistTaskLocked(t); err != nil {
		return nil, err
	}
	cp := *t
	return &cp, nil
}

// GetUnclaimed returns pending tasks that are unclaimed and executable.
func (m *TaskManager) GetUnclaimed() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()

	ids := make([]int, 0, len(m.tasks))
	for id := range m.tasks {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	var out []Task
	for _, id := range ids {
		t := m.tasks[id]
		if t == nil || t.Status != TaskPending || t.ClaimedBy != "" {
			continue
		}
		if !m.allPrereqsCompletedLocked(t) {
			continue
		}
		cp := *t
		out = append(out, cp)
	}
	return out
}

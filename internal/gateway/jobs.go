package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

type JobStatus string

const (
	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
)

// ChatJob is one asynchronous chat job.
type ChatJob struct {
	ID             string    `json:"job_id"`
	SessionID      string    `json:"session_id"`
	Lane           string    `json:"lane"`
	Input          string    `json:"input,omitempty"`
	IdempotencyKey string    `json:"idempotency_key,omitempty"`
	Status         JobStatus `json:"status"`
	Output         string    `json:"output,omitempty"`
	Error          string    `json:"error,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	FinishedAt     time.Time `json:"finished_at,omitempty"`
}

// JobManager is a thread-safe job store with optional disk persistence,
// idempotency keys, cancellation, and bounded retention.
type JobManager struct {
	mu      sync.RWMutex
	jobs    map[string]*ChatJob
	keys    map[string]string // idempotency key -> job id
	cancels map[string]context.CancelFunc

	dir     string
	maxJobs int
	jobTTL  time.Duration
}

// NewJobManager returns an in-memory job manager.
func NewJobManager() *JobManager {
	return &JobManager{
		jobs:    make(map[string]*ChatJob),
		keys:    make(map[string]string),
		cancels: make(map[string]context.CancelFunc),
	}
}

// NewPersistentJobManager returns a job manager backed by a directory of JSON
// files (one per job). maxJobs bounds retention of terminal jobs; ttl removes
// terminal jobs older than ttl. Loading is best-effort (unreadable records are
// skipped).
func NewPersistentJobManager(dir string, maxJobs int, ttl time.Duration) *JobManager {
	if maxJobs <= 0 {
		maxJobs = 1000
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	m := &JobManager{
		jobs:    make(map[string]*ChatJob),
		keys:    make(map[string]string),
		cancels: make(map[string]context.CancelFunc),
		dir:     dir,
		maxJobs: maxJobs,
		jobTTL:  ttl,
	}
	m.load()
	return m
}

// Create registers a new pending job.
func (m *JobManager) Create(sessionID, lane, input string) *ChatJob {
	return m.CreateWithKey(sessionID, lane, input, "")
}

// CreateWithKey registers a pending job. A non-empty idempotencyKey deduplicates:
// an existing job with the same key is returned instead of creating a new one.
func (m *JobManager) CreateWithKey(sessionID, lane, input, key string) *ChatJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key != "" {
		if id, ok := m.keys[key]; ok {
			if j, exists := m.jobs[id]; exists {
				return cloneJob(j)
			}
		}
	}
	job := &ChatJob{
		ID:             uuid.NewString(),
		SessionID:      sessionID,
		Lane:           lane,
		Input:          input,
		IdempotencyKey: key,
		Status:         JobPending,
		CreatedAt:      time.Now(),
	}
	m.jobs[job.ID] = job
	if key != "" {
		m.keys[key] = job.ID
	}
	m.persistLocked(job)
	m.expireLocked(time.Now())
	return cloneJob(job)
}

// Get returns a copy of a job by id.
func (m *JobManager) Get(id string) (*ChatJob, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, ok := m.jobs[id]
	if !ok {
		return nil, false
	}
	return cloneJob(job), true
}

// List returns all jobs, newest first.
func (m *JobManager) List() []*ChatJob {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*ChatJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		out = append(out, cloneJob(j))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Run executes fn in a goroutine and tracks the job lifecycle. The fn receives a
// cancellable context so the job can be cancelled via Cancel.
func (m *JobManager) Run(ctx context.Context, id string, fn func(context.Context) (string, error)) {
	m.mu.Lock()
	if _, ok := m.jobs[id]; !ok {
		m.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.cancels[id] = cancel
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.cancels, id)
			m.mu.Unlock()
		}()
		m.markRunning(id)
		out, err := fn(runCtx)
		if err != nil {
			if runCtx.Err() != nil {
				m.markDone(id, "", "cancelled", JobCancelled)
			} else {
				m.markDone(id, "", err.Error(), JobFailed)
			}
			return
		}
		m.markDone(id, out, "", JobSucceeded)
	}()
}

// Cancel cancels a running job or marks a pending job cancelled. Returns false
// if the job does not exist or is already terminal.
func (m *JobManager) Cancel(id string) bool {
	m.mu.RLock()
	cancel, hasCancel := m.cancels[id]
	job, exists := m.jobs[id]
	var status JobStatus
	if exists {
		status = job.Status
	}
	m.mu.RUnlock()

	if !exists {
		return false
	}
	if status != JobPending && status != JobRunning {
		return false
	}
	if hasCancel {
		cancel()
		return true
	}
	// pending, not yet started
	m.markDone(id, "", "cancelled before start", JobCancelled)
	return true
}

func (m *JobManager) markRunning(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job, ok := m.jobs[id]; ok {
		job.Status = JobRunning
		job.StartedAt = time.Now()
		m.persistLocked(job)
	}
}

func (m *JobManager) markDone(id, output, errMsg string, status JobStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job, ok := m.jobs[id]; ok {
		job.Status = status
		job.Output = output
		job.Error = errMsg
		job.FinishedAt = time.Now()
		if job.StartedAt.IsZero() {
			job.StartedAt = job.FinishedAt
		}
		m.persistLocked(job)
		m.expireLocked(time.Now())
	}
}

// --- persistence ---

func (m *JobManager) persistLocked(job *ChatJob) {
	if m.dir == "" || job == nil {
		return
	}
	data, err := json.Marshal(job)
	if err != nil {
		return
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return
	}
	tmp := filepath.Join(m.dir, job.ID+".json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, filepath.Join(m.dir, job.ID+".json"))
}

func (m *JobManager) load() {
	if m.dir == "" {
		return
	}
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.dir, e.Name()))
		if err != nil {
			continue
		}
		var job ChatJob
		if err := json.Unmarshal(data, &job); err != nil {
			continue
		}
		// Jobs that were in-flight when the process stopped are marked failed.
		switch job.Status {
		case JobRunning:
			job.Error = "interrupted by restart"
			job.Status = JobFailed
			job.FinishedAt = time.Now()
		case JobPending:
			job.Error = "not started before restart"
			job.Status = JobFailed
			job.FinishedAt = time.Now()
		}
		m.jobs[job.ID] = &job
		if job.IdempotencyKey != "" {
			m.keys[job.IdempotencyKey] = job.ID
		}
	}
	m.expireLocked(time.Now())
}

// expireLocked removes terminal jobs past their TTL and, if necessary, evicts
// the oldest terminal jobs to respect maxJobs.
func (m *JobManager) expireLocked(now time.Time) {
	remove := func(id string) {
		j, ok := m.jobs[id]
		if !ok {
			return
		}
		if j.IdempotencyKey != "" {
			delete(m.keys, j.IdempotencyKey)
		}
		delete(m.jobs, id)
		if m.dir != "" {
			_ = os.Remove(filepath.Join(m.dir, id+".json"))
		}
	}

	if m.jobTTL > 0 {
		for id, j := range m.jobs {
			if !j.FinishedAt.IsZero() && now.Sub(j.FinishedAt) > m.jobTTL {
				remove(id)
			}
		}
	}

	if m.maxJobs > 0 && len(m.jobs) > m.maxJobs {
		var terminal []string
		for id, j := range m.jobs {
			if j.Status == JobSucceeded || j.Status == JobFailed || j.Status == JobCancelled {
				terminal = append(terminal, id)
			}
		}
		sort.Slice(terminal, func(i, j int) bool {
			return m.jobs[terminal[i]].FinishedAt.Before(m.jobs[terminal[j]].FinishedAt)
		})
		excess := len(m.jobs) - m.maxJobs
		for i := 0; i < excess && i < len(terminal); i++ {
			remove(terminal[i])
		}
	}
}

func cloneJob(job *ChatJob) *ChatJob {
	if job == nil {
		return nil
	}
	cp := *job
	return &cp
}

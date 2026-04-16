package gateway

import (
	"context"
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
)

type ChatJob struct {
	ID         string    `json:"job_id"`
	SessionID  string    `json:"session_id"`
	Lane       string    `json:"lane"`
	Input      string    `json:"-"`
	Status     JobStatus `json:"status"`
	Output     string    `json:"output,omitempty"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

type JobManager struct {
	mu   sync.RWMutex
	jobs map[string]*ChatJob
}

func NewJobManager() *JobManager {
	return &JobManager{jobs: make(map[string]*ChatJob)}
}

func (m *JobManager) Create(sessionID, lane, input string) *ChatJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := &ChatJob{
		ID:        uuid.NewString(),
		SessionID: sessionID,
		Lane:      lane,
		Input:     input,
		Status:    JobPending,
		CreatedAt: time.Now(),
	}
	m.jobs[job.ID] = job
	return cloneJob(job)
}

func (m *JobManager) Get(id string) (*ChatJob, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, ok := m.jobs[id]
	if !ok {
		return nil, false
	}
	return cloneJob(job), true
}

func (m *JobManager) Run(ctx context.Context, id string, fn func(context.Context) (string, error)) {
	go func() {
		m.markRunning(id)
		out, err := fn(ctx)
		if err != nil {
			m.markDone(id, "", err.Error(), JobFailed)
			return
		}
		m.markDone(id, out, "", JobSucceeded)
	}()
}

func (m *JobManager) markRunning(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if job, ok := m.jobs[id]; ok {
		job.Status = JobRunning
		job.StartedAt = time.Now()
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
	}
}

func cloneJob(job *ChatJob) *ChatJob {
	if job == nil {
		return nil
	}
	cp := *job
	return &cp
}

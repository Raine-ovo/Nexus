package planning

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/rainea/nexus/configs"
	"github.com/rainea/nexus/pkg/utils"
	"github.com/robfig/cron/v3"
)

// CronJob is a persisted scheduled job definition.
type CronJob struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`          // cron expression (5-field: min hour dom mon dow)
	Type     string `json:"type"`              // agent_turn or system_event
	Payload  string `json:"payload"`
	OneShot  bool   `json:"one_shot,omitempty"` // if true, auto-remove after first execution
}

const (
	CronTypeAgentTurn   = "agent_turn"
	CronTypeSystemEvent = "system_event"
)

// CronScheduler runs periodic jobs using cron expressions (robfig/cron/v3).
type CronScheduler struct {
	cfg    configs.PlanningConfig
	cron   *cron.Cron
	jobs   []CronJob
	byName map[string]cron.EntryID

	handler func(ctx context.Context, job CronJob)

	mu      sync.Mutex
	running bool
	runCtx  context.Context
	cancel  context.CancelFunc
}

// NewCronScheduler constructs a scheduler; optional handler runs when a job fires.
func NewCronScheduler(cfg configs.PlanningConfig, handler func(ctx context.Context, job CronJob)) *CronScheduler {
	return &CronScheduler{
		cfg:     cfg,
		handler: handler,
		byName:  make(map[string]cron.EntryID),
	}
}

// SetJobHandler replaces the job callback (safe before Start; after Start, call under external sync if needed).
func (s *CronScheduler) SetJobHandler(handler func(ctx context.Context, job CronJob)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handler = handler
}

func (s *CronScheduler) cronJobsPath() string {
	return filepath.Join(s.cfg.TaskDir, "cron_jobs.json")
}

func (s *CronScheduler) loadJobsLocked() error {
	path := s.cronJobsPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			s.jobs = nil
			return nil
		}
		return err
	}
	var jobs []CronJob
	if err := json.Unmarshal(data, &jobs); err != nil {
		return fmt.Errorf("cron jobs json: %w", err)
	}
	s.jobs = jobs
	return nil
}

func (s *CronScheduler) persistJobsLocked() error {
	if err := os.MkdirAll(s.cfg.TaskDir, 0o755); err != nil {
		return fmt.Errorf("cron mkdir: %w", err)
	}
	path := s.cronJobsPath()
	return utils.WriteJSON(path, s.jobs)
}

func (s *CronScheduler) sortJobsLocked() {
	sort.Slice(s.jobs, func(i, j int) bool { return s.jobs[i].Name < s.jobs[j].Name })
}

// Start loads jobs, registers cron entries, and begins scheduling.
func (s *CronScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}
	if err := s.loadJobsLocked(); err != nil {
		return err
	}
	s.sortJobsLocked()

	s.runCtx, s.cancel = context.WithCancel(ctx)
	s.cron = cron.New()
	s.byName = make(map[string]cron.EntryID)

	for _, j := range s.jobs {
		if err := s.addFuncLocked(j); err != nil {
			if s.cancel != nil {
				s.cancel()
			}
			s.cron = nil
			s.byName = nil
			return fmt.Errorf("cron job %q: %w", j.Name, err)
		}
	}

	s.cron.Start()
	s.running = true
	return nil
}

func (s *CronScheduler) addFuncLocked(job CronJob) error {
	if job.Name == "" {
		return fmt.Errorf("job name is required")
	}
	if _, dup := s.byName[job.Name]; dup {
		return fmt.Errorf("duplicate job name %q", job.Name)
	}

	j := job
	eid, err := s.cron.AddFunc(j.Schedule, func() {
		s.dispatch(j)
	})
	if err != nil {
		return err
	}
	s.byName[j.Name] = eid
	return nil
}

func (s *CronScheduler) dispatch(job CronJob) {
	s.mu.Lock()
	h := s.handler
	ctx := s.runCtx
	s.mu.Unlock()
	if h == nil || ctx == nil {
		return
	}
	h(ctx, job)

	if job.OneShot {
		s.removeJobLocked(job.Name)
	}
}

// removeJobLocked removes a job from the cron scheduler, the in-memory list, and the persisted file.
func (s *CronScheduler) removeJobLocked(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running && s.cron != nil {
		if eid, ok := s.byName[name]; ok {
			s.cron.Remove(eid)
			delete(s.byName, name)
		}
	}
	for i, j := range s.jobs {
		if j.Name == name {
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			_ = s.persistJobsLocked()
			return
		}
	}
}

// Stop halts the cron scheduler.
func (s *CronScheduler) Stop() {
	s.mu.Lock()
	if !s.running || s.cron == nil {
		s.mu.Unlock()
		return
	}
	c := s.cron
	cancel := s.cancel
	s.cron = nil
	s.running = false
	s.byName = make(map[string]cron.EntryID)
	s.cancel = nil
	s.mu.Unlock()

	c.Stop()
	if cancel != nil {
		cancel()
	}
}

// AddJob appends a job, persists, and registers it when running.
func (s *CronScheduler) AddJob(job CronJob) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.jobs {
		if existing.Name == job.Name {
			return fmt.Errorf("cron: job %q already exists", job.Name)
		}
	}
	s.jobs = append(s.jobs, job)
	s.sortJobsLocked()
	if err := s.persistJobsLocked(); err != nil {
		s.jobs = s.jobs[:len(s.jobs)-1]
		return err
	}
	if s.running && s.cron != nil {
		if err := s.addFuncLocked(job); err != nil {
			// Roll back slice + file
			for i, x := range s.jobs {
				if x.Name == job.Name {
					s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
					break
				}
			}
			_ = s.persistJobsLocked()
			return err
		}
	}
	return nil
}

// RemoveJob deletes a job by name.
func (s *CronScheduler) RemoveJob(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i, j := range s.jobs {
		if j.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("cron: unknown job %q", name)
	}

	if s.running && s.cron != nil {
		if eid, ok := s.byName[name]; ok {
			s.cron.Remove(eid)
			delete(s.byName, name)
		}
	}

	s.jobs = append(s.jobs[:idx], s.jobs[idx+1:]...)
	return s.persistJobsLocked()
}

// ListJobs returns a shallow copy of configured jobs.
func (s *CronScheduler) ListJobs() []CronJob {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]CronJob, len(s.jobs))
	copy(out, s.jobs)
	return out
}

package team

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rainea/nexus/pkg/utils"
)

// Member status constants.
const (
	StatusWorking  = "working"
	StatusIdle     = "idle"
	StatusShutdown = "shutdown"
)

// TeamMember is one entry in the persistent roster.
type TeamMember struct {
	Name          string    `json:"name"`
	Role          string    `json:"role"`
	Status        string    `json:"status"`
	Activity      string    `json:"activity,omitempty"`
	ClaimedTaskID int       `json:"claimed_task_id,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

// TeamConfig is the on-disk representation of the team roster.
type TeamConfig struct {
	TeamName string       `json:"team_name"`
	Members  []TeamMember `json:"members"`
}

// Roster is a thread-safe, persistent team membership registry backed by a JSON file.
type Roster struct {
	dir        string
	configPath string
	config     TeamConfig
	mu         sync.RWMutex
}

// NewRoster loads or creates a roster at teamDir/config.json.
func NewRoster(teamDir string) (*Roster, error) {
	if err := os.MkdirAll(teamDir, 0o755); err != nil {
		return nil, fmt.Errorf("roster: mkdir %s: %w", teamDir, err)
	}
	r := &Roster{
		dir:        teamDir,
		configPath: filepath.Join(teamDir, "config.json"),
	}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Roster) load() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, err := os.Stat(r.configPath); os.IsNotExist(err) {
		r.config = TeamConfig{TeamName: "default"}
		return nil
	}

	var cfg TeamConfig
	if err := utils.ReadJSON(r.configPath, &cfg); err != nil {
		return fmt.Errorf("roster: load: %w", err)
	}
	r.config = cfg
	return nil
}

func (r *Roster) save() error {
	return utils.WriteJSON(r.configPath, &r.config)
}

// Add inserts a new member. Returns an error if the name already exists.
func (r *Roster) Add(name, role, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.config.Members {
		if m.Name == name {
			return fmt.Errorf("roster: member %q already exists", name)
		}
	}
	r.config.Members = append(r.config.Members, TeamMember{
		Name:      name,
		Role:      role,
		Status:    status,
		UpdatedAt: time.Now().UTC(),
	})
	return r.save()
}

// UpdateStatus sets the status of a member. Returns an error if not found.
func (r *Roster) UpdateStatus(name, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.config.Members {
		if r.config.Members[i].Name == name {
			r.config.Members[i].Status = status
			r.config.Members[i].UpdatedAt = time.Now().UTC()
			return r.save()
		}
	}
	return fmt.Errorf("roster: member %q not found", name)
}

// UpdateRole sets the role of a member.
func (r *Roster) UpdateRole(name, role string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.config.Members {
		if r.config.Members[i].Name == name {
			r.config.Members[i].Role = role
			r.config.Members[i].UpdatedAt = time.Now().UTC()
			return r.save()
		}
	}
	return fmt.Errorf("roster: member %q not found", name)
}

// UpdateActivity sets a member's current activity and optional claimed task id.
func (r *Roster) UpdateActivity(name, activity string, taskID int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.config.Members {
		if r.config.Members[i].Name == name {
			r.config.Members[i].Activity = activity
			r.config.Members[i].ClaimedTaskID = taskID
			r.config.Members[i].UpdatedAt = time.Now().UTC()
			return r.save()
		}
	}
	return fmt.Errorf("roster: member %q not found", name)
}

// Get returns a copy of a member by name.
func (r *Roster) Get(name string) (TeamMember, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, m := range r.config.Members {
		if m.Name == name {
			return m, true
		}
	}
	return TeamMember{}, false
}

// List returns a snapshot of all members.
func (r *Roster) List() []TeamMember {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]TeamMember, len(r.config.Members))
	copy(out, r.config.Members)
	return out
}

// Names returns just the member names.
func (r *Roster) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.config.Members))
	for _, m := range r.config.Members {
		out = append(out, m.Name)
	}
	return out
}

// ActiveNames returns names of members that are not shutdown.
func (r *Roster) ActiveNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.config.Members))
	for _, m := range r.config.Members {
		if m.Status != StatusShutdown {
			out = append(out, m.Name)
		}
	}
	return out
}

// Remove deletes a member by name.
func (r *Roster) Remove(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, m := range r.config.Members {
		if m.Name == name {
			r.config.Members = append(r.config.Members[:i], r.config.Members[i+1:]...)
			return r.save()
		}
	}
	return fmt.Errorf("roster: member %q not found", name)
}

// TeamName returns the team name.
func (r *Roster) TeamName() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.config.TeamName
}

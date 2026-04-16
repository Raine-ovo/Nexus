package gateway

import (
	"sort"
	"sync"
)

// BindingRouter implements 5-tier routing specificity.
type BindingRouter struct {
	bindings []Binding
	mu       sync.RWMutex
}

// Binding maps channel/user to an agent.
type Binding struct {
	Channel  string `json:"channel"` // exact channel name or "*"
	User     string `json:"user"`    // exact user or "*"
	AgentID  string `json:"agent_id"`
	Tier     int    `json:"-"` // computed: 1 (most specific) to 5 (catch-all)
	Priority int    `json:"priority"` // for same-tier disambiguation
}

// Tier computation:
// Tier 1: channel=specific, user=specific
// Tier 2: channel=specific, user=*
// Tier 3: channel=specific, user="" (channel-only)
// Tier 4: channel=*, user=specific
// Tier 5: channel=*, user=* (catch-all)
func computeTier(channel, user string) int {
	chSpec := channel != "" && channel != "*"
	usrStar := user == "*"
	usrEmpty := user == ""
	usrSpec := !usrEmpty && !usrStar

	switch {
	case chSpec && usrSpec:
		return 1
	case chSpec && usrStar:
		return 2
	case chSpec && usrEmpty:
		return 3
	case !chSpec && usrSpec:
		return 4
	default:
		return 5
	}
}

// NewBindingRouter returns an empty router.
func NewBindingRouter() *BindingRouter {
	return &BindingRouter{bindings: nil}
}

// AddBinding registers a binding with computed tier.
func (r *BindingRouter) AddBinding(b Binding) {
	b.Tier = computeTier(b.Channel, b.User)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bindings = append(r.bindings, b)
	r.sortLocked()
}

func (r *BindingRouter) sortLocked() {
	sort.Slice(r.bindings, func(i, j int) bool {
		if r.bindings[i].Tier != r.bindings[j].Tier {
			return r.bindings[i].Tier < r.bindings[j].Tier
		}
		return r.bindings[i].Priority > r.bindings[j].Priority
	})
}

// Route returns the agent_id for the best matching binding.
func (r *BindingRouter) Route(channel, user string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, b := range r.bindings {
		if bindingMatches(b, channel, user) {
			return b.AgentID, true
		}
	}
	return "", false
}

func bindingMatches(b Binding, channel, user string) bool {
	if b.Channel != "*" && b.Channel != channel {
		return false
	}
	switch b.User {
	case "*":
		return true
	case "":
		return true
	default:
		return b.User == user
	}
}

// RemoveBinding removes the first binding with the same channel and user keys.
func (r *BindingRouter) RemoveBinding(channel, user string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, b := range r.bindings {
		if b.Channel == channel && b.User == user {
			r.bindings = append(r.bindings[:i], r.bindings[i+1:]...)
			return
		}
	}
}

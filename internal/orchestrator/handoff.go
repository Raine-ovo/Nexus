package orchestrator

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	// MaxHandoffContextRunes caps handoff context size to avoid blowing model context windows.
	MaxHandoffContextRunes = 32000
)

var handoffMarkerRegexp = regexp.MustCompile(`\[\[HANDOFF:([^:\]]+):([^\]]*)\]\]`)

// HandoffProtocol manages agent-to-agent transfers during multi-step tasks.
type HandoffProtocol struct {
	registry *AgentRegistry
	observer Observer
}

// HandoffRequest represents a request from one agent to transfer work to another.
type HandoffRequest struct {
	ID        string                 `json:"id"`
	FromAgent string                 `json:"from_agent"`
	ToAgent   string                 `json:"to_agent"`
	Reason    string                 `json:"reason"`
	Context   string                 `json:"context"` // summary of work so far
	Payload   map[string]interface{} `json:"payload,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

// HandoffResponse is the receiving agent's acknowledgment.
type HandoffResponse struct {
	RequestID string `json:"request_id"`
	Accepted  bool   `json:"accepted"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}

// NewHandoffProtocol constructs a protocol helper.
func NewHandoffProtocol(registry *AgentRegistry, obs Observer) *HandoffProtocol {
	return &HandoffProtocol{
		registry: registry,
		observer: obs,
	}
}

// ValidateHandoffRequest checks required fields without executing the handoff.
func ValidateHandoffRequest(req HandoffRequest) error {
	if strings.TrimSpace(req.ToAgent) == "" {
		return fmt.Errorf("orchestrator: handoff to_agent is required")
	}
	return nil
}

// Initiate validates agents, builds a handoff message, and runs the target agent.
func (h *HandoffProtocol) Initiate(ctx context.Context, req HandoffRequest) (*HandoffResponse, error) {
	if h == nil {
		return nil, fmt.Errorf("orchestrator: nil HandoffProtocol")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateHandoffRequest(req); err != nil {
		return &HandoffResponse{
			RequestID: req.ID,
			Accepted:  false,
			Error:     err.Error(),
		}, nil
	}

	target, ok := h.registry.Get(req.ToAgent)
	if !ok || target == nil {
		return &HandoffResponse{
			RequestID: req.ID,
			Accepted:  false,
			Error:     fmt.Sprintf("target agent %q is not registered", req.ToAgent),
		}, nil
	}

	if req.FromAgent != "" {
		if src, ok := h.registry.Get(req.FromAgent); !ok || src == nil {
			return &HandoffResponse{
				RequestID: req.ID,
				Accepted:  false,
				Error:     fmt.Sprintf("source agent %q is not registered", req.FromAgent),
			}, nil
		}
	}

	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = uuid.New().String()
	}

	msg := buildHandoffUserMessage(req)
	if h.observer != nil {
		h.observer.Info("handoff initiate",
			"id", id,
			"from", req.FromAgent,
			"to", req.ToAgent,
			"reason", truncateRunes(req.Reason, 120),
		)
	}

	out, err := target.Run(ctx, msg)
	if err != nil {
		if h.observer != nil {
			h.observer.Error("handoff target run failed", "to", req.ToAgent, "err", err)
		}
		return &HandoffResponse{
			RequestID: id,
			Accepted:  false,
			Error:     err.Error(),
		}, nil
	}

	return &HandoffResponse{
		RequestID: id,
		Accepted:  true,
		Result:    out,
	}, nil
}

func buildHandoffUserMessage(req HandoffRequest) string {
	var b strings.Builder
	b.WriteString("## Handoff\n")
	if req.FromAgent != "" {
		b.WriteString(fmt.Sprintf("You are receiving a handoff from agent %q.\n", req.FromAgent))
	}
	if strings.TrimSpace(req.Reason) != "" {
		b.WriteString(fmt.Sprintf("Reason: %s\n", strings.TrimSpace(req.Reason)))
	}
	if !req.CreatedAt.IsZero() {
		b.WriteString(fmt.Sprintf("Requested at: %s\n", req.CreatedAt.UTC().Format(time.RFC3339)))
	}
	b.WriteString("\n## Context (work so far)\n")
	b.WriteString(strings.TrimSpace(req.Context))
	if len(req.Payload) > 0 {
		b.WriteString("\n\n## Payload (structured)\n")
		keys := make([]string, 0, len(req.Payload))
		for k := range req.Payload {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(fmt.Sprintf("- %s: %v\n", k, req.Payload[k]))
		}
	}
	b.WriteString("\n\nPlease continue the task based on the above. Produce a complete answer for the user.\n")
	return b.String()
}

// ParseHandoffMarker detects if an agent's output contains a handoff request marker.
// Format: [[HANDOFF:agent_name:reason]]
func ParseHandoffMarker(output string) (agentName, reason string, found bool) {
	if output == "" {
		return "", "", false
	}
	m := handoffMarkerRegexp.FindStringSubmatch(output)
	if len(m) < 3 {
		return "", "", false
	}
	name := strings.TrimSpace(m[1])
	reason = strings.TrimSpace(m[2])
	if name == "" {
		return "", "", false
	}
	return name, reason, true
}

// StripHandoffMarkers removes all [[HANDOFF:...]] segments from text.
func StripHandoffMarkers(output string) string {
	if output == "" {
		return ""
	}
	s := handoffMarkerRegexp.ReplaceAllString(output, "")
	return strings.TrimSpace(strings.ReplaceAll(s, "\n\n\n", "\n\n"))
}

// BuildHandoffContext creates a concise summary for the receiving agent.
func BuildHandoffContext(fromAgent, toAgent, originalInput, workSoFar string) string {
	var b strings.Builder
	b.WriteString("Original user request:\n")
	b.WriteString(strings.TrimSpace(originalInput))
	b.WriteString("\n\n")
	if strings.TrimSpace(fromAgent) != "" && strings.TrimSpace(toAgent) != "" {
		b.WriteString(fmt.Sprintf("Handoff path: %s → %s\n\n", strings.TrimSpace(fromAgent), strings.TrimSpace(toAgent)))
	}
	b.WriteString("Prior agent output / progress:\n")
	b.WriteString(truncateRunes(strings.TrimSpace(workSoFar), MaxHandoffContextRunes))
	return b.String()
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

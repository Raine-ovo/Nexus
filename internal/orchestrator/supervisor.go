package orchestrator

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/rainea/nexus/configs"
)

const (
	maxHandoffDepth             = 16
	supervisorWeakConfThreshold = 0.45
	supervisorRoutingMaxTokens  = 384
)

// Observer is a minimal interface for observability.
type Observer interface {
	Info(msg string, keysAndValues ...interface{})
	Error(msg string, keysAndValues ...interface{})
	Warn(msg string, keysAndValues ...interface{})
}

// Supervisor orchestrates multiple agents using the Supervisor pattern.
// It receives user input, classifies intent, routes to the appropriate agent,
// and collects results. Supports multi-turn delegation where one task may
// involve multiple agents in sequence.
type Supervisor struct {
	cfg          configs.ModelConfig
	registry     *AgentRegistry
	intentRouter *IntentRouter
	observer     Observer
	handoff      *HandoffProtocol
	sessions     map[string]*SupervisorSession
	mu           sync.RWMutex
	httpDo       func(*http.Request) (*http.Response, error)
}

// SupervisorSession tracks routing and handoffs for one conversation.
type SupervisorSession struct {
	ID           string
	CurrentAgent string
	History      []HandoffRecord
	TurnCount    int
	CreatedAt    time.Time
}

// HandoffRecord is one agent-to-agent transfer in a session.
type HandoffRecord struct {
	FromAgent string
	ToAgent   string
	Reason    string
	Timestamp time.Time
}

// NewSupervisor constructs a supervisor with an intent router wired to the same model config.
func NewSupervisor(cfg configs.ModelConfig, registry *AgentRegistry, obs Observer) *Supervisor {
	if registry == nil {
		registry = NewAgentRegistry()
	}
	ir := NewIntentRouter(registry)
	ir.ConfigureModel(cfg)
	return &Supervisor{
		cfg:          cfg,
		registry:     registry,
		intentRouter: ir,
		observer:     obs,
		handoff:      NewHandoffProtocol(registry, obs),
		sessions:     make(map[string]*SupervisorSession),
		httpDo:       http.DefaultClient.Do,
	}
}

// Registry returns the agent registry used by this supervisor.
func (s *Supervisor) Registry() *AgentRegistry {
	if s == nil {
		return nil
	}
	return s.registry
}

// IntentRouter returns the intent router (for adding rules at runtime).
func (s *Supervisor) IntentRouter() *IntentRouter {
	if s == nil {
		return nil
	}
	return s.intentRouter
}

// Session returns a shallow copy of session state for observability or debugging.
func (s *Supervisor) Session(id string) (*SupervisorSession, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[id]
	if !ok || sess == nil {
		return nil, false
	}
	cp := *sess
	cp.History = append([]HandoffRecord(nil), sess.History...)
	return &cp, true
}

// HandleRequest classifies input, runs the selected agent, and follows handoff markers until done.
func (s *Supervisor) HandleRequest(ctx context.Context, sessionID, input string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("orchestrator: nil Supervisor")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		sid = uuid.New().String()
	}

	userText := strings.TrimSpace(input)
	if userText == "" {
		return "", fmt.Errorf("orchestrator: empty input")
	}

	sess := s.getOrCreateSession(sid)

	agentName, conf, err := s.classifyAndRoute(ctx, sess, userText)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	sess.CurrentAgent = agentName
	sess.TurnCount++
	s.mu.Unlock()

	if s.observer != nil {
		s.observer.Info("supervisor handle request",
			"session", sid,
			"agent", agentName,
			"confidence", conf,
			"turn", sess.TurnCount,
		)
	}

	var acc strings.Builder
	currentAgent := agentName
	currentInput := userText
	original := userText

	out, runErr := s.delegateToAgent(ctx, sess, currentAgent, currentInput)
	if runErr != nil {
		return StripHandoffMarkers(out), fmt.Errorf("orchestrator: agent %q: %w", currentAgent, runErr)
	}

	for depth := 0; depth < maxHandoffDepth; depth++ {
		nextAgent, reason, handoff := ParseHandoffMarker(out)
		clean := StripHandoffMarkers(out)

		if !handoff {
			if acc.Len() > 0 {
				acc.WriteString("\n\n---\n\n")
			}
			acc.WriteString(clean)
			return acc.String(), nil
		}

		if acc.Len() > 0 {
			acc.WriteString("\n\n---\n\n")
		}
		acc.WriteString(clean)

		workSoFar := acc.String()
		s.handleHandoff(sess, currentAgent, nextAgent, reason)

		req := HandoffRequest{
			ID:        uuid.New().String(),
			FromAgent: currentAgent,
			ToAgent:   nextAgent,
			Reason:    reason,
			Context:   BuildHandoffContext(currentAgent, nextAgent, original, workSoFar),
			Payload: map[string]interface{}{
				"original_user_input": original,
				"handoff_depth":       depth + 1,
			},
			CreatedAt: time.Now().UTC(),
		}

		resp, herr := s.handoff.Initiate(ctx, req)
		if herr != nil {
			return acc.String(), fmt.Errorf("orchestrator: handoff protocol: %w", herr)
		}
		if resp == nil || !resp.Accepted {
			errMsg := ""
			if resp != nil {
				errMsg = resp.Error
			}
			if errMsg == "" {
				errMsg = "handoff rejected"
			}
			if s.observer != nil {
				s.observer.Warn("handoff failed", "to", nextAgent, "error", errMsg)
			}
			return acc.String(), fmt.Errorf("orchestrator: handoff to %q: %s", nextAgent, errMsg)
		}

		currentAgent = nextAgent
		out = resp.Result
		if strings.TrimSpace(out) == "" {
			return acc.String(), nil
		}

		s.mu.Lock()
		sess.CurrentAgent = currentAgent
		s.mu.Unlock()
	}

	if s.observer != nil {
		s.observer.Warn("supervisor handoff depth exceeded", "session", sid, "max", maxHandoffDepth)
	}
	return acc.String(), fmt.Errorf("orchestrator: exceeded max handoff depth (%d)", maxHandoffDepth)
}

func (s *Supervisor) getOrCreateSession(id string) *SupervisorSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = make(map[string]*SupervisorSession)
	}
	if sess, ok := s.sessions[id]; ok && sess != nil {
		return sess
	}
	sess := &SupervisorSession{
		ID:        id,
		CreatedAt: time.Now().UTC(),
		History:   make([]HandoffRecord, 0, 4),
	}
	s.sessions[id] = sess
	return sess
}

// classifyAndRoute resolves the first agent using the intent router and optionally a supervisor LLM tie-breaker.
func (s *Supervisor) classifyAndRoute(ctx context.Context, sess *SupervisorSession, input string) (string, float64, error) {
	agent, conf, err := s.intentRouter.Route(ctx, input)
	if err != nil {
		return "", 0, err
	}

	if conf >= supervisorWeakConfThreshold || strings.TrimSpace(s.cfg.APIKey) == "" {
		if s.observer != nil {
			s.observer.Info("supervisor classified route", "session", sess.ID, "agent", agent, "confidence", conf)
		}
		return agent, conf, nil
	}

	prompt := s.buildRoutingPrompt(input)
	raw, cerr := completeChat(ctx, s.cfg, s.httpDo, prompt, supervisorRoutingMaxTokens)
	if cerr != nil {
		if s.observer != nil {
			s.observer.Warn("supervisor routing LLM failed; using intent router result",
				"session", sess.ID, "err", cerr)
		}
		return agent, conf, nil
	}

	picked, c2 := parseClassificationResponse(raw)
	if picked == "" {
		return agent, conf, nil
	}
	if _, ok := s.registry.Get(picked); !ok {
		if s.observer != nil {
			s.observer.Warn("supervisor routing LLM picked unknown agent",
				"session", sess.ID, "picked", picked)
		}
		return agent, conf, nil
	}

	if c2 <= 0 {
		c2 = 0.78
	}
	if s.observer != nil {
		s.observer.Info("supervisor rerouted via LLM",
			"session", sess.ID, "agent", picked, "confidence", c2)
	}
	return picked, c2, nil
}

func (s *Supervisor) delegateToAgent(ctx context.Context, sess *SupervisorSession, agentName, userInput string) (string, error) {
	if strings.TrimSpace(agentName) == "" {
		return "", fmt.Errorf("orchestrator: empty agent name")
	}
	ag, ok := s.registry.Get(agentName)
	if !ok || ag == nil {
		return "", fmt.Errorf("orchestrator: agent %q not registered", agentName)
	}

	if s.observer != nil {
		s.observer.Info("supervisor delegate",
			"session", sess.ID,
			"agent", agentName,
			"input_len", len(userInput),
		)
	}

	out, err := ag.Run(ctx, userInput)
	if err != nil {
		if s.observer != nil {
			s.observer.Error("supervisor agent run error",
				"session", sess.ID,
				"agent", agentName,
				"err", err,
			)
		}
		return out, err
	}
	return out, nil
}

func (s *Supervisor) handleHandoff(sess *SupervisorSession, from, to, reason string) {
	if sess == nil {
		return
	}
	rec := HandoffRecord{
		FromAgent: from,
		ToAgent:   to,
		Reason:    reason,
		Timestamp: time.Now().UTC(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess.History = append(sess.History, rec)
}

// buildRoutingPrompt lists agents and asks the model to pick one (supervisor tie-breaker).
func (s *Supervisor) buildRoutingPrompt(userInput string) string {
	const instr = `You are the supervising router for a multi-agent system.
Choose exactly one agent_name from the list for the user's next message.
Reply with JSON only (no markdown), like:
{"agent_name":"<name>","confidence":0.0,"reason":"short phrase"}`
	fb := ""
	if s.intentRouter != nil {
		fb = s.intentRouter.Fallback()
	}
	return buildAgentSelectionPrompt(s.registry, userInput, instr, fb)
}

// SetHTTPDo replaces the HTTP client used for supervisor routing LLM calls (tests / custom transport).
func (s *Supervisor) SetHTTPDo(do func(*http.Request) (*http.Response, error)) {
	if s == nil {
		return
	}
	if do == nil {
		s.httpDo = http.DefaultClient.Do
		return
	}
	s.httpDo = do
	if s.intentRouter != nil {
		s.intentRouter.SetHTTPDo(do)
	}
}

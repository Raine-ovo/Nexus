package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rainea/nexus/internal/core"
	"github.com/rainea/nexus/pkg/utils"
)

// openAIChatRequest is the subset of the OpenAI chat.completions request shape
// that Nexus accepts.
type openAIChatRequest struct {
	Model       string              `json:"model"`
	Messages    []openAIChatMessage `json:"messages"`
	Stream      bool                `json:"stream"`
	MaxTokens   int                 `json:"max_tokens"`
	Temperature *float64            `json:"temperature"`
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   openAIUsage    `json:"usage"`
}

type openAIChoice struct {
	Index        int           `json:"index"`
	Message      openAIMessage `json:"message,omitempty"`
	Delta        openAIDelta   `json:"delta,omitempty"`
	FinishReason string        `json:"finish_reason"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// handleOpenAIChatCompletions exposes an OpenAI-compatible chat.completions
// endpoint backed by the multi-agent supervisor. It supports both the standard
// JSON response and SSE chunk streaming (stream: true).
func (g *Gateway) handleOpenAIChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req openAIChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	input := openAITranscript(req.Messages)
	if strings.TrimSpace(input) == "" {
		writeOpenAIError(w, http.StatusBadRequest, "messages must contain user content")
		return
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "nexus"
	}

	s := g.sessions.CreateWithOptions("openai", "openai", "", "")
	run := func(ctx context.Context) (string, error) {
		if scoped, ok := g.supervisor.(ScopedSupervisor); ok {
			return scoped.HandleScopedRequest(ctx, s, input)
		}
		return g.supervisor.HandleRequest(ctx, s.ID, input)
	}

	if req.Stream {
		g.streamOpenAIResponse(w, r, model, run)
		return
	}

	out, err := g.lanes.Submit(r.Context(), "main", run)
	if err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOpenAIResponse(w, model, out)
}

// openAITranscript converts an OpenAI messages list into a single input string
// for the supervisor. System and assistant turns are labeled; user turns are
// passed verbatim.
func openAITranscript(msgs []openAIChatMessage) string {
	var parts []string
	for _, m := range msgs {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(m.Role)) {
		case "system":
			parts = append(parts, "[system] "+content)
		case "assistant":
			parts = append(parts, "[assistant] "+content)
		default:
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n")
}

func writeOpenAIResponse(w http.ResponseWriter, model, content string) {
	resp := openAIChatResponse{
		ID:      "chatcmpl-" + uuid.NewString(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []openAIChoice{{
			Index:        0,
			Message:      openAIMessage{Role: "assistant", Content: content},
			FinishReason: "stop",
		}},
		Usage: openAIUsage{CompletionTokens: utils.EstimateTokens(content)},
	}
	resp.Usage.TotalTokens = resp.Usage.CompletionTokens
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (g *Gateway) streamOpenAIResponse(w http.ResponseWriter, r *http.Request, model string, run func(context.Context) (string, error)) {
	fl, ok := w.(http.Flusher)
	if !ok {
		out, err := g.lanes.Submit(r.Context(), "main", run)
		if err != nil {
			writeOpenAIError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeOpenAIResponse(w, model, out)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	id := "chatcmpl-" + uuid.NewString()
	created := time.Now().Unix()

	writeOpenAIChunk(w, fl, id, model, created, openAIDelta{Role: "assistant"}, nil)

	// Emit true token deltas via the model's streaming path (when supported).
	streamed := false
	sink := func(delta string) error {
		streamed = true
		writeOpenAIChunk(w, fl, id, model, created, openAIDelta{Content: delta}, nil)
		return nil
	}
	ctx := core.WithStreamSink(r.Context(), sink)
	out, err := g.lanes.Submit(ctx, "main", run)

	// Fallback for models without streaming support: chunk the final output.
	if !streamed && out != "" {
		const chunkRunes = 64
		runes := []rune(out)
		for i := 0; i < len(runes); i += chunkRunes {
			j := i + chunkRunes
			if j > len(runes) {
				j = len(runes)
			}
			writeOpenAIChunk(w, fl, id, model, created, openAIDelta{Content: string(runes[i:j])}, nil)
		}
	}

	finish := "stop"
	if err != nil {
		finish = "error"
	}
	writeOpenAIChunk(w, fl, id, model, created, openAIDelta{}, &finish)
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
	fl.Flush()
}

func writeOpenAIChunk(w http.ResponseWriter, fl http.Flusher, id, model string, created int64, delta openAIDelta, finish *string) {
	chunk := struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		Model   string `json:"model"`
		Choices []struct {
			Index        int         `json:"index"`
			Delta        openAIDelta `json:"delta"`
			FinishReason *string     `json:"finish_reason"`
		} `json:"choices"`
	}{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: created,
		Model:   model,
		Choices: []struct {
			Index        int         `json:"index"`
			Delta        openAIDelta `json:"delta"`
			FinishReason *string     `json:"finish_reason"`
		}{{
			Index:        0,
			Delta:        delta,
			FinishReason: finish,
		}},
	}
	data, _ := json.Marshal(chunk)
	_, _ = w.Write([]byte("data: " + string(data) + "\n\n"))
	fl.Flush()
}

func writeOpenAIError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"message": msg,
			"type":    "invalid_request_error",
		},
	})
}

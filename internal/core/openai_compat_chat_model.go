package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rainea/nexus/configs"
	"github.com/rainea/nexus/pkg/types"
)

// OpenAICompatibleChatModel implements ChatModel against OpenAI-compatible
// chat completions APIs, including providers such as Zhipu GLM.
type OpenAICompatibleChatModel struct {
	cfg    configs.ModelConfig
	httpDo func(*http.Request) (*http.Response, error)
}

// NewOpenAICompatibleChatModel creates a concrete ChatModel backed by an
// OpenAI-compatible HTTP endpoint.
func NewOpenAICompatibleChatModel(cfg configs.ModelConfig) *OpenAICompatibleChatModel {
	return &OpenAICompatibleChatModel{
		cfg:    cfg,
		httpDo: http.DefaultClient.Do,
	}
}

// SetHTTPDo replaces the underlying HTTP caller. Nil restores the default.
func (m *OpenAICompatibleChatModel) SetHTTPDo(do func(*http.Request) (*http.Response, error)) {
	if do == nil {
		m.httpDo = http.DefaultClient.Do
		return
	}
	m.httpDo = do
}

func (m *OpenAICompatibleChatModel) Generate(ctx context.Context, system string, messages []types.Message, tools []types.ToolDefinition) (*ChatModelResponse, error) {
	if m == nil {
		return nil, fmt.Errorf("core: nil OpenAICompatibleChatModel")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(m.cfg.APIKey) == "" {
		return nil, fmt.Errorf("core: model api_key is empty")
	}

	reqBody, err := m.buildRequestBody(system, messages, tools)
	if err != nil {
		return nil, err
	}
	payload, marshalErr := json.Marshal(reqBody)
	if marshalErr != nil {
		return nil, fmt.Errorf("core: marshal chat request: %w", marshalErr)
	}

	endpoint := openAICompatibleChatCompletionsURL(m.cfg.BaseURL)
	req, buildErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if buildErr != nil {
		return nil, fmt.Errorf("core: build request: %w", buildErr)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(m.cfg.APIKey))

	doer := m.httpDo
	if doer == nil {
		doer = http.DefaultClient.Do
	}
	var respBody []byte
	var resp *http.Response
	var reqErr error
	for attempt := 0; attempt < maxChatCompletionAttempts; attempt++ {
		if attempt > 0 {
			req, reqErr = http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
			if reqErr != nil {
				return nil, fmt.Errorf("core: build request: %w", reqErr)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(m.cfg.APIKey))
		}

		resp, reqErr = doer(req)
		if reqErr != nil {
			if !isRetryableTransportError(ctx, reqErr) || attempt == maxChatCompletionAttempts-1 {
				return nil, fmt.Errorf("core: http request: %w", reqErr)
			}
			if waitErr := sleepWithContext(ctx, retryDelayForAttempt(attempt, 0)); waitErr != nil {
				return nil, waitErr
			}
			continue
		}

		respBody, reqErr = io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if reqErr != nil {
			return nil, fmt.Errorf("core: read body: %w", reqErr)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			break
		}
		if !shouldRetryStatus(resp.StatusCode) || attempt == maxChatCompletionAttempts-1 {
			return nil, fmt.Errorf("core: chat completion status %d: %s", resp.StatusCode, truncateForErr(respBody, 1024))
		}
		if waitErr := sleepWithContext(ctx, retryDelayForAttempt(attempt, retryAfterDelay(resp))); waitErr != nil {
			return nil, waitErr
		}
	}

	var envelope openAIChatCompletionResponse
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return nil, fmt.Errorf("core: decode response: %w", err)
	}
	if envelope.Error != nil && envelope.Error.Message != "" {
		return nil, fmt.Errorf("core: api error: %s", envelope.Error.Message)
	}
	if len(envelope.Choices) == 0 {
		return nil, fmt.Errorf("core: empty completion choices")
	}

	choice := envelope.Choices[0]
	out := &ChatModelResponse{
		Content:      choice.Message.Content,
		FinishReason: strings.TrimSpace(choice.FinishReason),
	}
	for _, tc := range choice.Message.ToolCalls {
		args, err := parseToolArguments(tc.Function.Arguments)
		if err != nil {
			return nil, fmt.Errorf("core: parse tool arguments for %q: %w", tc.Function.Name, err)
		}
		out.ToolCalls = append(out.ToolCalls, types.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}
	return out, nil
}

func (m *OpenAICompatibleChatModel) buildRequestBody(system string, messages []types.Message, tools []types.ToolDefinition) (map[string]interface{}, error) {
	model := strings.TrimSpace(m.cfg.ModelName)
	if model == "" {
		model = "gpt-4o"
	}
	temp := m.cfg.Temperature
	if temp <= 0 {
		temp = 0.3
	}

	wireMessages := make([]map[string]interface{}, 0, len(messages)+1)
	if strings.TrimSpace(system) != "" {
		wireMessages = append(wireMessages, map[string]interface{}{
			"role":    string(types.RoleSystem),
			"content": system,
		})
	}
	for _, msg := range messages {
		wire, err := openAICompatibleMessage(msg)
		if err != nil {
			return nil, err
		}
		if wire != nil {
			wireMessages = append(wireMessages, wire)
		}
	}

	body := map[string]interface{}{
		"model":       model,
		"temperature": temp,
		"max_tokens":  effectiveMaxTokens(m.cfg, m.cfg.MaxTokens),
		"messages":    wireMessages,
	}
	if len(tools) > 0 {
		body["tools"] = openAICompatibleTools(tools)
		body["tool_choice"] = "auto"
	}
	return body, nil
}

func openAICompatibleMessage(msg types.Message) (map[string]interface{}, error) {
	role := string(msg.Role)
	switch msg.Role {
	case types.RoleSystem, types.RoleUser:
		return map[string]interface{}{
			"role":    role,
			"content": msg.Content,
		}, nil
	case types.RoleAssistant:
		wire := map[string]interface{}{
			"role": role,
		}
		if strings.TrimSpace(msg.Content) != "" {
			wire["content"] = msg.Content
		} else {
			wire["content"] = ""
		}
		if len(msg.ToolCalls) > 0 {
			wire["tool_calls"] = assistantToolCalls(msg.ToolCalls)
		}
		return wire, nil
	case types.RoleTool:
		return map[string]interface{}{
			"role":         role,
			"tool_call_id": msg.ToolID,
			"content":      msg.Content,
		}, nil
	default:
		return nil, fmt.Errorf("core: unsupported message role %q", msg.Role)
	}
}

func assistantToolCalls(calls []types.ToolCall) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(calls))
	for _, call := range calls {
		argBytes, err := json.Marshal(call.Arguments)
		argStr := "{}"
		if err == nil {
			argStr = string(argBytes)
		}
		out = append(out, map[string]interface{}{
			"id":   call.ID,
			"type": "function",
			"function": map[string]interface{}{
				"name":      call.Name,
				"arguments": argStr,
			},
		})
	}
	return out
}

func openAICompatibleTools(tools []types.ToolDefinition) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		out = append(out, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.Parameters,
			},
		})
	}
	return out
}

func parseToolArguments(raw string) (map[string]interface{}, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return map[string]interface{}{}, nil
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(s), &args); err != nil {
		return nil, err
	}
	if args == nil {
		args = map[string]interface{}{}
	}
	return args, nil
}

func openAICompatibleChatCompletionsURL(base string) string {
	b := strings.TrimSuffix(strings.TrimSpace(base), "/")
	if b == "" {
		return "https://api.openai.com/v1/chat/completions"
	}
	if strings.HasSuffix(b, "/chat/completions") {
		return b
	}
	if strings.HasSuffix(b, "/v1") || strings.HasSuffix(b, "/v4") {
		return b + "/chat/completions"
	}
	return b + "/v1/chat/completions"
}

func effectiveMaxTokens(cfg configs.ModelConfig, requested int) int {
	if requested <= 0 {
		requested = 512
	}
	if cfg.MaxTokens > 0 && requested > cfg.MaxTokens {
		return cfg.MaxTokens
	}
	return requested
}

func truncateForErr(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

const maxChatCompletionAttempts = 4

func shouldRetryStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func retryDelayForAttempt(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	base := time.Second << attempt
	if base > 8*time.Second {
		return 8 * time.Second
	}
	return base
}

func retryAfterDelay(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	raw := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if secs, err := strconv.Atoi(raw); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if ts, err := http.ParseTime(raw); err == nil {
		if d := time.Until(ts); d > 0 {
			return d
		}
	}
	return 0
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isRetryableTransportError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	return true
}

type openAIChatCompletionResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

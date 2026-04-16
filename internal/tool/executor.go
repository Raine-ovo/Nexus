package tool

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/rainea/nexus/pkg/types"
	"github.com/rainea/nexus/pkg/utils"
)

type contextKey string

const (
	// RequestIDKey is the context key for correlating tool execution with a request.
	RequestIDKey contextKey = "nexus_request_id"
)

// WithRequestID returns a child context carrying the request identifier.
func WithRequestID(ctx context.Context, requestID string) context.Context {
	if requestID == "" {
		return ctx
	}
	return context.WithValue(ctx, RequestIDKey, requestID)
}

// RequestIDFromContext returns the request id from ctx, or empty string.
func RequestIDFromContext(ctx context.Context) string {
	v := ctx.Value(RequestIDKey)
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// HookContext carries metadata for executor hooks.
type HookContext struct {
	ToolID    string
	Name      string
	Args      map[string]interface{}
	RequestID string
}

// BeforeExecuteHook runs before the tool handler. Return error to abort execution.
type BeforeExecuteHook func(ctx context.Context, h HookContext) error

// AfterExecuteHook runs after the handler (or timeout / hook failure).
type AfterExecuteHook func(ctx context.Context, h HookContext, res *types.ToolResult, execErr error)

// Executor runs registered tools with timeouts, output limits, optional sandbox path checks, and hooks.
type Executor struct {
	registry *Registry

	defaultTimeout time.Duration
	maxOutputRunes int
	sandboxRoot    string

	beforeExecute BeforeExecuteHook
	afterExecute  AfterExecuteHook
}

// ExecutorOption configures an Executor.
type ExecutorOption func(*Executor)

// WithDefaultTimeout sets the default execution timeout when the caller does not supply one.
func WithDefaultTimeout(d time.Duration) ExecutorOption {
	return func(e *Executor) {
		if d > 0 {
			e.defaultTimeout = d
		}
	}
}

// WithMaxOutputRunes sets the maximum content length (runes) kept in ToolResult.Content.
func WithMaxOutputRunes(n int) ExecutorOption {
	return func(e *Executor) {
		if n > 0 {
			e.maxOutputRunes = n
		}
	}
}

// WithSandboxRoot sets the workspace root used to validate common path arguments before handlers run.
func WithSandboxRoot(root string) ExecutorOption {
	return func(e *Executor) {
		e.sandboxRoot = root
	}
}

// WithHooks registers before/after callbacks.
func WithHooks(before BeforeExecuteHook, after AfterExecuteHook) ExecutorOption {
	return func(e *Executor) {
		e.beforeExecute = before
		e.afterExecute = after
	}
}

// NewExecutor builds an Executor backed by the given registry.
func NewExecutor(reg *Registry, opts ...ExecutorOption) *Executor {
	e := &Executor{
		registry:       reg,
		defaultTimeout: 120 * time.Second,
		maxOutputRunes: 100_000,
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Registry returns the backing registry.
func (e *Executor) Registry() *Registry {
	return e.registry
}

// validateSandboxArgs ensures top-level path-like arguments remain inside the workspace when a sandbox root is set.
func (e *Executor) validateSandboxArgs(args map[string]interface{}) error {
	if e.sandboxRoot == "" || args == nil {
		return nil
	}
	for _, key := range []string{"path", "cwd", "file"} {
		v, ok := args[key]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		if _, err := utils.SafePath(e.sandboxRoot, s); err != nil {
			return fmt.Errorf("sandbox %s: %w", key, err)
		}
	}
	return nil
}

// Execute invokes a tool by name. toolID is recorded on the result; if empty, one is generated.
// perCallTimeout overrides the default timeout when > 0.
// Errors from the handler, timeouts, panics, and validation failures are returned as ToolResult with IsError (layer-1 recovery).
func (e *Executor) Execute(ctx context.Context, toolID, name string, args map[string]interface{}, perCallTimeout time.Duration) *types.ToolResult {
	if toolID == "" {
		toolID = NewToolID()
	}
	meta := e.registry.Get(name)
	if meta == nil {
		return errResult(toolID, name, fmt.Errorf("unknown tool %q", name))
	}

	reqID := RequestIDFromContext(ctx)
	hctx := HookContext{ToolID: toolID, Name: name, Args: args, RequestID: reqID}

	if e.beforeExecute != nil {
		if err := e.beforeExecute(ctx, hctx); err != nil {
			res := errResult(toolID, name, fmt.Errorf("beforeExecute: %w", err))
			if e.afterExecute != nil {
				e.afterExecute(ctx, hctx, res, err)
			}
			return res
		}
	}

	if err := e.validateSandboxArgs(args); err != nil {
		res := errResult(toolID, name, err)
		if e.afterExecute != nil {
			e.afterExecute(ctx, hctx, res, err)
		}
		return res
	}

	timeout := e.defaultTimeout
	if perCallTimeout > 0 {
		timeout = perCallTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type outcome struct {
		res *types.ToolResult
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- outcome{
					res: nil,
					err: fmt.Errorf("tool panic: %v\n%s", r, string(debug.Stack())),
				}
			}
		}()
		res, err := meta.Handler(runCtx, args)
		ch <- outcome{res: res, err: err}
	}()

	var res *types.ToolResult
	var execErr error
	select {
	case <-runCtx.Done():
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			execErr = fmt.Errorf("tool execution timed out after %s", timeout)
		} else {
			execErr = fmt.Errorf("tool execution cancelled: %w", runCtx.Err())
		}
		res = errResult(toolID, name, execErr)
	case o := <-ch:
		execErr = o.err
		res = o.res
		if res == nil && execErr == nil {
			res = &types.ToolResult{ToolID: toolID, Name: name, Content: "", IsError: false}
		}
		if res != nil {
			res.ToolID = toolID
			res.Name = name
			if execErr != nil && !res.IsError {
				res.Content = fmt.Sprintf("%s\n%v", res.Content, execErr)
				res.IsError = true
			}
			if execErr != nil && res.IsError && res.Content == "" {
				res.Content = execErr.Error()
			}
		} else if execErr != nil {
			res = errResult(toolID, name, execErr)
		}
	}

	if res != nil && res.Content != "" {
		res.Content = utils.TruncateString(res.Content, e.maxOutputRunes)
	}

	if e.afterExecute != nil {
		e.afterExecute(ctx, hctx, res, execErr)
	}
	return res
}

func errResult(toolID, name string, err error) *types.ToolResult {
	if err == nil {
		return &types.ToolResult{ToolID: toolID, Name: name, Content: "", IsError: false}
	}
	return &types.ToolResult{
		ToolID:  toolID,
		Name:    name,
		Content: err.Error(),
		IsError: true,
	}
}

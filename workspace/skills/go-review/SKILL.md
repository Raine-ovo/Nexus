---
name: go-review
description: Go code review checklist covering concurrency, error handling, resource management, and idiomatic patterns.
invocation: Use when reviewing Go source files or diffs.
---

# Go Code Review Skill

## Concurrency
- Every goroutine must have a clear lifecycle and cancellation path (context propagation or done channel).
- Shared mutable state requires explicit synchronization: prefer `sync.Mutex` or `sync/atomic`; avoid channel misuse for simple locking.
- Watch for goroutine leaks: blocking sends/receives without select + ctx.Done, or missing WaitGroup.Wait.
- `defer mu.Unlock()` immediately after `mu.Lock()` unless the critical section must release early.

## Error Handling
- Never ignore returned errors (`_ = fn()`). If intentional, add a `//nolint:errcheck` comment explaining why.
- Wrap errors with `fmt.Errorf("context: %w", err)` to preserve the chain for `errors.Is` / `errors.As`.
- Sentinel errors should be package-level `var` using `errors.New`, not string comparisons.
- Avoid `panic` in library code; reserve it for truly unrecoverable programmer errors.

## Resource Management
- Files, HTTP response bodies, database rows, and network connections must be closed. Prefer `defer closer.Close()` right after the open call.
- For `*http.Response`, always `defer resp.Body.Close()` even on non-2xx; drain with `io.Copy(io.Discard, resp.Body)` when needed.
- `context.WithCancel` / `WithTimeout` returned cancel functions must be called (typically `defer cancel()`).

## Naming and Structure
- Exported names get doc comments starting with the identifier name.
- Avoid stutter: `user.UserService` → `user.Service`.
- Packages name a single responsibility; avoid `util`, `common`, `misc` unless truly generic.
- Prefer table-driven tests with `t.Run` sub-tests for parametric scenarios.

## Performance Signals
- Pre-size slices and maps when the count is known: `make([]T, 0, n)`.
- Avoid repeated `string ↔ []byte` conversions in hot paths.
- `sync.Pool` for high-churn allocations; profile before optimizing.
- `strings.Builder` over `+` concatenation in loops.

## Common Anti-Patterns
- `defer` inside a loop body creates deferred stack per iteration — extract to a helper function.
- `select {}` without a `ctx.Done()` branch means the goroutine never stops.
- `time.After` in a select loop leaks timers — use `time.NewTimer` + `Reset`.
- Global `init()` with side effects makes testing harder; prefer explicit initialization.

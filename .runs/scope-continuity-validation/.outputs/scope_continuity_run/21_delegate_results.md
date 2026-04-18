# Code Review Delegate Results

## Experiment: scope-continuity-validation
## Reviewer: code_reviewer delegate

## Files Reviewed

| File | Exists | Lines | Rating |
|------|--------|-------|--------|
| internal/team/registry.go | ✅ | ~750 | 3/5 |
| internal/gateway/debug.go | ✅ | ~340 | 2/5 |
| internal/team/runtime.go | ✅ | ~460 | 3/5 |

## Critical Findings

### 🔴 CRITICAL: Path traversal risk (registry.go:285-295)
- `slugify` is the only defense against path traversal via user-controlled scope keys
- Defense is fragile — any code path that forgets `slugify` creates a vulnerability
- **Suggestion**: Add explicit path traversal validation after slugification

### 🔴 CRITICAL: No auth on debug endpoints (debug.go:12-25)
- `handleDebugMetrics`, `handleDebugScopes`, `handleDebugTraces` expose internal state without auth
- Leaks user IDs, channel names, conversation summaries
- **Suggestion**: Add auth middleware or bind to localhost-only

## Warning Findings

### ⚠️ XSS risk in dashboard template (debug.go:78-140)
- `esc()` function doesn't escape quotes or backticks
- User-controlled data inserted into HTML attributes and JS contexts
- **Suggestion**: Use `template.HTML` escaping consistently or pass data via JSON blob

### ⚠️ Scope collision risk (registry.go:193)
- `fmt.Sprintf("scope:%d", time.Now().UTC().UnixNano())` is predictable
- **Suggestion**: Use UUID or cryptographic random for scope IDs

### ⚠️ Lock contention (registry.go:300-340)
- Mutex held during filesystem operations in `getOrCreateManager`
- **Suggestion**: Double-checked locking or per-scope locks

### ⚠️ Silent error swallowing (debug.go:15,31,44)
- `_ = json.NewEncoder(w).Encode(...)` discards errors
- **Suggestion**: Log encode errors at debug level

### ⚠️ Blocking LLM calls in RecordTurn (runtime.go:148-170)
- `RecordTurn` makes synchronous LLM calls on hot path
- **Suggestion**: Make async or add context timeout

### ⚠️ Broken trace correlation (runtime.go:271-275)
- Fallback UUID in `startSpan` breaks request correlation
- **Suggestion**: Generate request ID once per request in gateway handler

## Info Findings

- Single-candidate fast path skips recency scoring (registry.go:346-360)
- Snapshot write on every span end is expensive (runtime.go:378-410)
- Scope index persisted on every request (registry.go:454-470)
- Magic numbers throughout registry.go
- Inline HTML template in debug.go should be externalized

## Overall Assessment

The codebase demonstrates solid engineering discipline with clear structure and good naming conventions. However, it needs security hardening (auth, XSS, path traversal defense-in-depth) and performance optimization (lock contention, blocking LLM calls, excessive I/O) before production use.

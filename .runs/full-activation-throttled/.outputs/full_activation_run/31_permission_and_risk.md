# 31 — Permission and Risk

## Permission Pipeline Verification

### Source Code Analysis (internal/permission/)

The permission system implements a four-stage pipeline as documented in docs/architecture.md:

1. **Deny rules** (highest priority): Matching deny rule → immediate deny, even in full_auto mode
2. **Path sandbox**: Validates paths are within WorkspaceRoot; filters dangerous shell commands
3. **Mode gate**: full_auto → allow; manual → ask; semi_auto → whitelist
4. **Allow/Ask**: semi_auto whitelist match → allow; otherwise → ask

### Integration with ReAct Loop

From `internal/core/loop.go`, the integration point is:
```
executeToolCall → deps.PermPipeline.CheckTool(ctx, name, args) → allow/deny
```

If denied, the error is wrapped as `ToolResult{IsError: true}` and fed back to the model, enabling self-correction.

### Audit Findings (from devops-001 report)

| Finding | Severity | Status |
|---------|----------|--------|
| BP-01: Direct tool invocation bypass | HIGH | ✅ Mitigated |
| BP-02: Stale policy data | MEDIUM | ⚠️ Partially mitigated |
| BP-03: Error path escape (fail-open risk) | MEDIUM | ⚠️ Potential risk |
| BP-04: Nested action chains | LOW | ✅ Mitigated |
| BP-05: Message-based instruction injection | HIGH | ✅ Mitigated |
| BP-06: Throttling bypass via multiple agents | MEDIUM | ⚠️ Potential risk |
| BP-07: Plan approval bypass | LOW | ✅ Mitigated |

### Tool Privilege Split (Experiment Finding)

**Critical Discovery**: Teammates only receive team-level tools:
- ✅ send_message, read_inbox, list_teammates, claim_task, submit_plan
- ❌ No base tools: read_file, write_file, bash, http_request, grep_search, etc.

This means:
- DevOps teammates **cannot** perform HTTP health checks or shell diagnostics
- Teammates **cannot** write files to the output directory
- All file I/O and HTTP operations must go through the Lead or delegate_task

**Implication**: The privilege split is working as designed (least privilege), but it limits the operational capability of specialized teammates. For DevOps use cases, teammates need at least read-only access to health endpoints and logs.

## Risk Assessment

### Experiment Risks

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Teammate idle timeout before task completion | HIGH | MEDIUM | Lead can re-spawn or use delegate_task |
| Delegate exceeding max iterations | MEDIUM | LOW | Scope tasks narrowly |
| Message loss on teammate shutdown | MEDIUM | LOW | Critical info should be in task board, not just messages |
| RAG returning stale data | LOW | LOW | Re-ingest before critical queries |

### Plan Approval Protocol

- devops-001 submitted a plan for Task 2 (permission audit) via submit_plan
- Lead approved via review_plan with request_id req_383c1857
- Protocol worked correctly: plan → approval → execution

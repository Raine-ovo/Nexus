# 01 — Dispatch Decisions

## Lead Self-Analysis

### Repository Scan Results
- **Root structure**: cmd/, internal/, pkg/, docs/, configs/, .team/, .runs/, .memory/
- **Key docs**: docs/architecture.md (comprehensive), docs/features.md (feature list)
- **Team config**: `.team/config.json` — 4 members (lead, Atlas/planner, Sentinel/code_reviewer, Scout/devops), all except lead are `shutdown`
- **Claim events**: `.team/claim_events.jsonl` — 1 prior claim (task 8 by Sentinel/code_reviewer, auto source)
- **MCP repo probe**: Called with `**/*.go` glob — returned mock response with 3 example matches: `internal/team/runtime.go`, `internal/gateway/server.go`, `cmd/nexus/main.go`
- **Key source files examined**: runtime.go (Runtime struct with deps/model/obs/mem/guard/recovery/refl), server.go (Gateway struct with cfg/sessions/lanes/router/observer), main.go (imports all subsystems)

### Dispatch Profile for Each Agent

#### Planner → spawn_teammate (persistent)
```
simple=false | needs_persistence=true | needs_isolation=false | expected_follow_up=true
reason=Must create task DAG, push tasks through lifecycle, accumulate context across multiple task operations
```

#### DevOps → spawn_teammate (persistent)
```
simple=false | needs_persistence=true | needs_isolation=false | expected_follow_up=true
reason=Must check multiple endpoints, run diagnostics, maintain runtime state context
```

#### Knowledge → delegate_task (isolated)
```
simple=false | needs_persistence=false | needs_isolation=true | expected_follow_up=false
specialist_role=knowledge
reason=One-shot RAG ingest + search, context isolation preferred, no accumulated state needed
```

#### Code Reviewer → delegate_task (isolated)
```
simple=false | needs_persistence=false | needs_isolation=true | expected_follow_up=false
specialist_role=code_reviewer
reason=One-shot code analysis, each review is independent, clean slate preferred
```

## Message Flow Plan
1. Lead → Planner: "Create task board for full activation experiment"
2. Planner → Lead: Task board created (via inbox)
3. Lead → DevOps: "Check health, dashboard, and metrics endpoints"
4. DevOps → Lead: Results (via inbox)
5. Lead → Planner: "Claim and execute a task from the board"
6. Lead → Planner: Additional task updates
7. Lead reads inbox for all responses

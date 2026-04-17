# 00 — Mission Statement

## Experiment: full-activation-throttled

**Goal**: Trigger all Nexus agent types and key capabilities, record evidence truthfully, and persist all artifacts.

### Mandatory Agent Coverage
| Agent | Participation Mode | Rationale |
|-------|-------------------|-----------|
| planner | persistent teammate (spawn_teammate) | Needs to maintain task board state, accumulate context across multiple task operations, and push tasks through lifecycle states |
| devops | persistent teammate (spawn_teammate) | Needs to perform multiple runtime checks (health, dashboard, metrics) and maintain context about system state across checks |
| knowledge | delegate_task (min 1x) | One-off RAG query — ingest then search — no accumulated state needed, context isolation preferred |
| code_reviewer | delegate_task (min 1x) | One-off code review — single file analysis, no state needed, clean context avoids polluting teammate history |

### Mandatory Mechanism Coverage
- [x] lead dispatch decision (this document)
- [x] dispatch_profile (each routing decision)
- [x] spawn_teammate (planner + devops)
- [x] send_message (≥5 team messages)
- [x] read_inbox (drain teammate replies)
- [x] claim_task (at least 1 teammate claims)
- [x] delegate_task (≥2: knowledge + code_reviewer)
- [x] mcp_repo_probe (1 call)
- [x] Task board creation and lifecycle management
- [x] RAG ingest + search
- [x] Code review (runtime.go / server.go / main.go)
- [x] DevOps health/dashboard/metrics checks
- [x] State persistence (semantic memory, trace)
- [x] Evidence archiving to `.runs/full-activation-throttled/.outputs/full_activation_run/`

### Output Directory
`.runs/full-activation-throttled/.outputs/full_activation_run/`

### Required Output Files
- 00_mission.md (this file)
- 01_dispatch.md
- 02_team_roster.md
- 10_task_plan.md
- 11_task_board.md
- 20_mcp_and_rag.md
- 21_delegate_results.md
- 22_team_messages.md
- 23_claim_progress.md
- 30_runtime_and_observability.md
- 31_permission_and_risk.md
- 32_state_persistence.md
- 40_evidence_index.md
- 99_final_report.md

### Strict Constraints
1. ≥2 persistent teammates (planner + devops)
2. ≥5 team messages
3. ≥1 teammate claim_task
4. ≥2 delegate_task calls
5. knowledge delegate must ingest docs/architecture.md or docs/features.md before searching
6. code_reviewer delegate must review at least one of: runtime.go, server.go, main.go
7. devops teammate must check ≥2 of: health, dashboard, metrics endpoints
8. planner teammate must create task board and advance ≥1 task to execution/completion
9. Failures must be recorded honestly — no pretending success

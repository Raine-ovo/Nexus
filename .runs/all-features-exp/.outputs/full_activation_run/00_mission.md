# 00 — Mission: Full Feature Activation Experiment

## Objective
Trigger **all** Nexus agents and key capabilities, collect real evidence, and persist artifacts.

## Agents Required
| Agent | Participation Mode | Rationale |
|-------|-------------------|-----------|
| planner | Persistent teammate | Needs to create task board, push tasks through lifecycle, accumulate context across multiple interactions |
| devops | Persistent teammate | Needs to check multiple endpoints, run diagnostics, maintain runtime context across health/metrics/dashboard checks |
| knowledge | delegate_task (≥1) | One-shot RAG ingest + search — context isolation preferred, no accumulated state needed |
| code_reviewer | delegate_task (≥1) | One-shot code analysis — context isolation preferred, each review is independent |

## Why planner/devops are persistent
- **Planner**: Must create tasks, update their status, and push at least one to completion. This requires multi-step interaction with the task board, and the planner needs to remember what it created across interactions.
- **DevOps**: Must check at least 2 endpoints and potentially run diagnostics. Keeping a persistent teammate allows follow-up queries and context accumulation about the runtime state.

## Why knowledge/code_reviewer are delegates
- **Knowledge**: RAG ingest + search is a stateless query-answer pattern. Fresh context avoids pollution from prior conversations.
- **Code Reviewer**: Each code review is an independent analysis. Context isolation ensures the reviewer starts with a clean slate focused solely on the target file.

## Mechanisms to Trigger
- [x] Lead dispatch decision
- [x] dispatch_profile
- [x] spawn_teammate (planner + devops)
- [x] send_message (≥5 messages)
- [x] read_inbox
- [x] claim_task
- [x] delegate_task (≥2: knowledge + code_reviewer)
- [x] Task board creation and progression
- [x] knowledge: ingest + search
- [x] code_reviewer: analyze/review file
- [x] devops: health/dashboard/metrics checks
- [x] mcp_repo_probe
- [x] Reflection (document reflection engine status)
- [x] Semantic memory persistence
- [x] Trace/metrics observability
- [x] Output persistence to `.runs/all-features-exp/.outputs/full_activation_run/`

## Constraints
1. ≥2 persistent teammates (planner + devops) ✓
2. ≥5 team messages ✓
3. ≥1 teammate claim_task ✓
4. ≥2 delegate_task calls ✓
5. Knowledge delegate must ingest docs then search ✓
6. Code reviewer must check ≥1 of: runtime.go, server.go, main.go ✓
7. DevOps must check ≥2 of: /api/health, /debug/dashboard, /api/debug/metrics ✓
8. Planner must create task board and push ≥1 task to execution/completion ✓
9. Failures must be recorded honestly ✓

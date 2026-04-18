# 00 — Mission Definition

## Continuity Header
- **experiment_id**: scope-continuity-validation
- **goal**: Trigger maximum Nexus capabilities and establish cross-session continuation context
- **workstream**: scope-continuity-validation
- **output_directory**: `.runs/scope-continuity-validation/.outputs/scope_continuity_run/`

## Experiment Objectives

### Layer 1: Feature Validation
Trigger as many Nexus key capabilities as possible:
- Lead dispatch & coordination
- dispatch_profile reasoning
- spawn_teammate (persistent workers)
- send_message / read_inbox (inter-agent communication)
- claim_task (task board interaction)
- delegate_task (isolated one-off specialists)
- planner persistent teammate
- devops persistent teammate
- knowledge delegate_task
- code_reviewer delegate_task
- reflection & semantic memory
- MCP server/client (mcp_repo_probe)
- Run sandbox file persistence
- Task board lifecycle (create → claim → execute → complete)

### Layer 2: Continuity Foundation
Establish context so a future session can say "continue the scope continuity experiment" and resume on the same workstream:
- All outputs land in `.runs/scope-continuity-validation/.outputs/scope_continuity_run/`
- Continuation instructions embedded in evidence index and final report
- Semantic memory entries reference experiment_id and output_directory

## Required Output Files
| File | Purpose |
|------|---------|
| `00_mission.md` | This file — mission definition |
| `01_dispatch.md` | Dispatch profile reasoning log |
| `02_team_roster.md` | Team composition and roles |
| `10_task_plan.md` | Planner's task DAG |
| `11_task_board.md` | Task board state snapshot |
| `20_mcp_and_rag.md` | MCP probe & RAG results |
| `21_delegate_results.md` | delegate_task results |
| `22_team_messages.md` | Inter-agent message log |
| `23_claim_progress.md` | Task claim/progress tracking |
| `30_runtime_and_observability.md` | Runtime, traces, metrics |
| `31_permission_and_risk.md` | Permission & risk log |
| `32_state_persistence.md` | State persistence evidence |
| `40_evidence_index.md` | Master evidence index |
| `99_final_report.md` | Final summary & continuation guide |

## Timestamp
- Created: 2025-01-01T00:00:00Z (session start)

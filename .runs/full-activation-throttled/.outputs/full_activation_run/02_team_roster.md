# 02 — Team Roster

## Active Team During Experiment

| Name | Role | Status | Participation Mode | Key Contributions |
|------|------|--------|-------------------|-------------------|
| lead | lead | working | Direct + coordinator | Repository scan, document creation, endpoint checks, delegate dispatch, evidence archiving |
| planner-002 | planner | shutdown (idle timeout) | Persistent teammate (spawn_teammate) | Created 7-task DAG, claimed Task 1, completed Tasks 1/3/5, reported progress |
| devops-001 | devops | shutdown (idle timeout) | Persistent teammate (pre-existing) | Permission audit (Task 2), memory verification (Task 4), endpoint check attempt |

## Roster History

### Pre-experiment state
- lead: working
- planner-001: shutdown (from prior experiment)
- devops-001: idle

### During experiment
- planner-002 spawned via spawn_teammate
- devops-001 activated via send_message
- planner-002 claimed Task 1 (manual claim)
- devops-001 claimed Task 2 (via plan approval flow)
- devops-001 claimed Task 4 (auto or manual)

### Post-experiment state
- planner-002: shutdown (idle timeout after ~2 min)
- devops-001: shutdown (idle timeout after ~2 min)

## Dispatch Rationale

| Agent | Why Persistent | Why Not Delegate |
|-------|---------------|-----------------|
| planner | Needs to maintain task board state across multiple create/claim/update operations; accumulates context about experiment goals | Delegate would lose all task context between calls |
| devops | Needs to correlate findings across multiple checks; accumulates context about system state | Delegate would only do one check in isolation |

| Agent | Why Delegate | Why Not Persistent |
|-------|-------------|-------------------|
| knowledge | One-shot RAG query: ingest then search. No accumulated state needed. | No follow-up work; persistence wastes resources |
| code_reviewer | One-shot code review. Clean context avoids bias. | No follow-up work; persistence wastes resources |

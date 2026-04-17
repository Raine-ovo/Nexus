# 01 — Dispatch Decisions

## Why planner as persistent teammate

The planner needs to:
- Create a task board with multiple tasks
- Track task state across multiple operations (create → claim → execute → complete)
- Accumulate context about the experiment's goals and task dependencies
- Respond to messages from lead about task progress

A delegate_task would lose all task board context between calls. Persistence is essential.

**Dispatch Profile**: `needs_persistence=true, expected_follow_up=true, specialist_role=planner`

## Why devops as persistent teammate

The devops agent needs to:
- Check multiple endpoints (health, dashboard, metrics)
- Accumulate findings across checks
- Report back consolidated results
- Potentially run diagnostics that depend on prior check results

A delegate_task would only do one check in isolation. Persistence allows cross-check correlation.

**Dispatch Profile**: `needs_persistence=true, expected_follow_up=true, specialist_role=devops`

## Why knowledge as delegate_task

The knowledge task is:
- One-shot: ingest a document, then answer a specific question
- Context-isolated: no need to remember prior queries
- Self-contained: the answer doesn't need to inform subsequent work in a stateful way

**Dispatch Profile**: `needs_isolation=true, specialist_role=knowledge`

## Why code_reviewer as delegate_task

The code review task is:
- One-shot: review a specific file and return findings
- Context-isolated: no need to accumulate review history
- Clean slate preferred: avoids any bias from prior conversation

**Dispatch Profile**: `needs_isolation=true, specialist_role=code_reviewer`

## Lead Direct Actions

The lead will directly handle:
- Repository scanning and initial assessment
- Document creation and archiving
- Coordinating team messages and inbox reading
- MCP repo probe
- Final evidence compilation

**Dispatch Profile**: `simple=true`

---
name: cron-team-dispatch
description: Force scheduled jobs to use the persistent team workflow: persist tasks first, then assign execution to teammates instead of delegate_task or lead self-execution.
invocation: Use for cron-triggered or scheduled work items.
---

# Cron Team Dispatch Skill

Use this skill whenever a request is triggered by a cron job, scheduler, heartbeat, or any other automated timed event.

## Core policy
- Treat the lead as a coordinator only.
- Do not execute the scheduled task directly as the lead.
- Do not use `delegate_task` for scheduled work.
- Persist the work into the task system before execution so the job is visible under `.tasks/`.
- Hand the persisted work to a persistent teammate through the team inbox / roster flow.

## Required workflow
1. Inspect existing teammates with `list_teammates`.
2. Reuse an existing persistent teammate when possible. Only call `spawn_teammate` if the needed role does not already exist.
3. Ensure there is a planner teammate for task persistence. If none exists, spawn one with role `planner`.
4. Send the planner teammate a message that asks it to persist the scheduled job as a task DAG using `create_plan`.
5. The planner must create concrete tasks with short verb-led titles and clear acceptance criteria in the description.
6. After the planner responds with task ids, choose the worker role that should execute the runnable task:
   - `devops` for shell, environment, deployment, runtime, infra, or service operations.
   - `knowledge` for retrieval, documentation, search, analysis, and research tasks.
   - `code_reviewer` for audits, code review, risk analysis, and repository inspection.
7. Ensure the worker teammate exists as a persistent teammate. Spawn it only if missing.
8. Send the worker teammate a message that includes the persisted task id and explicitly asks it to call `claim_task` before doing the work.
9. If multiple persisted tasks exist, only assign tasks whose prerequisites are already satisfied.
10. Report coordination status back to the user or scheduler, including which planner/worker teammate was used and which task ids were persisted.

## Message template to planner
Use a message in this shape:

```text
Persist this scheduled job as tasks in the task DAG.
Job name: <job-name>
Goal: <goal>
Requirements:
- Store the work via create_plan so it is durable in .tasks/.
- Use concise task titles.
- Put acceptance criteria in each description.
- Keep the first runnable task suitable for role: <role>.
Reply with the created task ids and which task should be claimed first.
```

## Message template to worker
Use a message in this shape:

```text
Execute scheduled task from the persistent task board.
1. Call claim_task for task_id=<task-id>.
2. Read the task description and complete the work with your role tools.
3. If the work is risky, submit a plan for approval before mutating state.
4. Send back a concise completion or blocker report referencing task_id=<task-id>.
```

## Hard prohibitions
- No `delegate_task`.
- No one-shot subagent execution.
- No lead self-execution of the scheduled payload.
- No bypass of `.tasks/` persistence for the main work item.

## Expected persistence trail
- Task definitions under `.tasks/`.
- Team communication under `.team/inbox/`.
- Protocol approvals under `.team/requests/` when needed.
- Task claim history under `claim_events.jsonl` when a teammate claims work.

## Decision rules
- Prefer a single planner teammate plus one execution teammate for most scheduled jobs.
- Split into multiple persisted tasks only when dependencies are real.
- Reuse teammate names across recurring scheduled jobs so context accumulates over time.

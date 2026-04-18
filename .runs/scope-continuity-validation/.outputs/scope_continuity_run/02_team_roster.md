# 02 — Team Roster

## Continuity Header
- **experiment_id**: scope-continuity-validation
- **goal**: Document team composition and roles
- **workstream**: scope-continuity-validation
- **output_directory**: `.runs/scope-continuity-validation/.outputs/scope_continuity_run/`

## Active Team Members

| Name | Role | Status | Spawned | Purpose |
|------|------|--------|---------|---------|
| lead | lead | working | Pre-existing | Coordination, dispatch, file persistence |
| planner-alpha | planner | working | Pre-existing (reused) | Task board creation & lifecycle management |
| devops-bravo | devops | working | Pre-existing (reused) | Endpoint probes, observability checks |

## Delegate Specialists (ephemeral, per-task)

| Role | Triggered Via | Task |
|------|--------------|------|
| knowledge | delegate_task | RAG search on docs/architecture.md or docs/features.md |
| code_reviewer | delegate_task | Review internal/team/registry.go or internal/gateway/debug.go |

## Communication Topology
```
lead ──send_message──► planner-alpha
lead ──send_message──► devops-bravo
lead ──delegate_task──► knowledge (ephemeral)
lead ──delegate_task──► code_reviewer (ephemeral)
planner-alpha ──send_message──► lead (read_inbox)
devops-bravo ──send_message──► lead (read_inbox)
```

## Notes
- planner-alpha and devops-bravo were pre-existing from prior sessions; we reused them rather than spawning new instances.
- This validates the persistence and reusability of teammates across sessions.

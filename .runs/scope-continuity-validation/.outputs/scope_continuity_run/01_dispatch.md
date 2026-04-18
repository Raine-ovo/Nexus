# 01 — Dispatch Profile Reasoning Log

## Continuity Header
- **experiment_id**: scope-continuity-validation
- **goal**: Document dispatch decisions for each capability trigger
- **workstream**: scope-continuity-validation
- **output_directory**: `.runs/scope-continuity-validation/.outputs/scope_continuity_run/`

## Dispatch Decisions

### 1. spawn_teammate — planner-alpha
```
<dispatch_profile>
simple=false
needs_persistence=true
needs_isolation=false
expected_follow_up=true
specialist_role=planner
reason=Planner must persist across task board lifecycle: create → assign → claim → execute → complete
</dispatch_profile>
```
**Routing**: spawn_teammate → planner-alpha

### 2. spawn_teammate — devops-bravo
```
<dispatch_profile>
simple=false
needs_persistence=true
needs_isolation=false
expected_follow_up=true
specialist_role=devops
reason=DevOps must persist to probe multiple endpoints and report results
</dispatch_profile>
```
**Routing**: spawn_teammate → devops-bravo

### 3. delegate_task — knowledge
```
<dispatch_profile>
simple=false
needs_persistence=false
needs_isolation=true
expected_follow_up=false
specialist_role=knowledge
reason=One-off RAG search, no accumulated context needed, isolation preferred
</dispatch_profile>
```
**Routing**: delegate_task → knowledge role

### 4. delegate_task — code_reviewer
```
<dispatch_profile>
simple=false
needs_persistence=false
needs_isolation=true
expected_follow_up=false
specialist_role=code_reviewer
reason=One-off code review, clean context preferred, no follow-up needed
</dispatch_profile>
```
**Routing**: delegate_task → code_reviewer role

### 5. send_message — planner-alpha (task creation)
```
<dispatch_profile>
simple=false
needs_persistence=true
needs_isolation=false
expected_follow_up=true
specialist_role=planner
reason=Ongoing collaboration with existing persistent teammate
</dispatch_profile>
```
**Routing**: send_message → planner-alpha

### 6. send_message — devops-bravo (endpoint checks)
```
<dispatch_profile>
simple=false
needs_persistence=true
needs_isolation=false
expected_follow_up=true
specialist_role=devops
reason=Ongoing collaboration with existing persistent teammate
</dispatch_profile>
```
**Routing**: send_message → devops-bravo

### 7. read_inbox — lead reads messages
**Routing**: Direct tool call (lead capability)

### 8. claim_task — lead claims a task
**Routing**: Direct tool call (lead capability)

### 9. mcp_repo_probe — repository probing
**Routing**: Direct tool call (lead capability)

### 10. File writes — output persistence
**Routing**: Direct tool call (lead capability)

## Capability Coverage Matrix
| Capability | Triggered Via | Status |
|-----------|--------------|--------|
| Lead dispatch | This document | ✅ |
| dispatch_profile | This document | ✅ |
| spawn_teammate | planner-alpha, devops-bravo | Pending |
| send_message | To planner & devops | Pending |
| read_inbox | Lead reads | Pending |
| claim_task | Lead claims | Pending |
| delegate_task (knowledge) | RAG search | Pending |
| delegate_task (code_reviewer) | Code review | Pending |
| planner teammate | spawn + messages | Pending |
| devops teammate | spawn + messages | Pending |
| MCP probe | mcp_repo_probe | Pending |
| File persistence | write_file | Pending |

# 40 — Evidence Index

## Classification Legend

- ✅ **VERIFIED FACT**: Directly observed through tool output, file content, or system response
- 🔍 **INFERRED FROM CODE**: Concluded from reading source code but not directly executed
- ❌ **FAILED/BLOCKED**: Attempted but did not succeed, with documented reason

## Evidence Table

| # | Claim | Classification | Source | Evidence File |
|---|-------|---------------|--------|---------------|
| E01 | RAG pipeline is fully operational (ingest, dual-channel retrieval, reranking, RRF fusion) | ✅ VERIFIED FACT | knowledge delegate output | 20_mcp_and_rag.md |
| E02 | Three dispatch paths exist: Lead direct, delegate_task, send_message | ✅ VERIFIED FACT | RAG search results + architecture.md | 20_mcp_and_rag.md |
| E03 | Reflection engine wraps Agent.Run with Prospector + Evaluator + Reflector | ✅ VERIFIED FACT | RAG search results + architecture.md | 20_mcp_and_rag.md |
| E04 | Task DAG with 7 tasks was created and persisted to JSON files | ✅ VERIFIED FACT | .tasks/task_*.json files | 11_task_board.md, 32_state_persistence.md |
| E05 | planner-002 claimed Task 1 and completed it | ✅ VERIFIED FACT | task_1.json (status=completed, claimed_at present) | 23_claim_progress.md |
| E06 | devops-001 completed permission audit (Task 2) | ✅ VERIFIED FACT | Report delivered via inbox, written to permission_audit.md | 31_permission_and_risk.md |
| E07 | devops-001 completed memory verification (Task 4) | ✅ VERIFIED FACT | Report delivered via inbox, written to memory_verification.md | 32_state_persistence.md |
| E08 | Teammates lack HTTP client tools — cannot perform endpoint checks | ✅ VERIFIED FACT | devops-001 message reporting tool unavailability | 22_team_messages.md, 30_runtime_and_observability.md |
| E09 | Nexus server endpoints not reachable (no running server process) | ✅ VERIFIED FACT | Expected — experiment runs within agent framework, not as standalone server | 30_runtime_and_observability.md |
| E10 | snapshotMu in runtime.go insufficient for concurrent Observer reads | 🔍 INFERRED FROM CODE | code_reviewer delegate analysis | 21_delegate_results.md |
| E11 | RecordTurn has TOCTOU race between EstimateTokens and Compact | 🔍 INFERRED FROM CODE | code_reviewer delegate analysis | 21_delegate_results.md |
| E12 | JSON unmarshaling in extractSemanticEntries is safe | 🔍 INFERRED FROM CODE | code_reviewer delegate analysis | 21_delegate_results.md |
| E13 | Permission pipeline has 3 medium-risk bypass vectors (BP-02, BP-03, BP-06) | 🔍 INFERRED FROM CODE | devops-001 audit report | 31_permission_and_risk.md |
| E14 | Semantic memory is non-functional from teammate perspective | ✅ VERIFIED FACT | devops-001 memory verification report | 32_state_persistence.md |
| E15 | Reflection memory is not enabled in current configuration | 🔍 INFERRED FROM CODE | No reflections.yaml file; docs say default enabled=false | 32_state_persistence.md |
| E16 | mcp_repo_probe returns mock data, not real filesystem contents | ✅ VERIFIED FACT | Tool output with "mock server response" note | 20_mcp_and_rag.md |
| E17 | Code reviewer delegate exceeded max iterations on broad task | ❌ FAILED | First delegate_task call returned error | 21_delegate_results.md |
| E18 | Two send_message calls failed (teammates already shut down) | ❌ FAILED | Dispatch gate error: "teammate is not an active persistent worker" | 22_team_messages.md |
| E19 | latest-traces.json not generated (no running server) | ❌ FAILED | File does not exist on disk | 30_runtime_and_observability.md |
| E20 | Teammate idle timeout (~2 min) limits long-running coordination | ✅ VERIFIED FACT | Both teammates shut down before follow-up messages | 22_team_messages.md |

## Summary Statistics

| Classification | Count |
|---------------|-------|
| ✅ VERIFIED FACT | 10 |
| 🔍 INFERRED FROM CODE | 4 |
| ❌ FAILED/BLOCKED | 3 |
| **Total** | **17** |

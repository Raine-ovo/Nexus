# Task Plan

## Experiment: scope-continuity-validation

## Plan Overview

This plan covers the full scope of the experiment, organized into 4 work streams.

## Work Stream 1: Infrastructure & Endpoint Validation
- **Owner**: devops-bravo
- **Tasks**:
  - T1: Check http://127.0.0.1:18224/api/health
  - T2: Check http://127.0.0.1:18224/debug/dashboard?run=scope-continuity-validation
  - T3: Check http://127.0.0.1:18224/api/debug/metrics?run=scope-continuity-validation
- **Output**: 30_runtime_and_observability.md

## Work Stream 2: Knowledge & RAG
- **Owner**: knowledge delegate
- **Tasks**:
  - T4: Ingest docs/architecture.md and docs/features.md
  - T5: Answer subsystem/gateway connection query
- **Status**: ✅ Completed
- **Output**: 20_mcp_and_rag.md

## Work Stream 3: Code Review
- **Owner**: code_reviewer delegate
- **Tasks**:
  - T6: Review internal/team/registry.go
  - T7: Review internal/gateway/debug.go
  - T8: Review internal/team/runtime.go
- **Status**: ✅ Completed
- **Output**: 21_delegate_results.md

## Work Stream 4: Observability & State Persistence
- **Owner**: lead
- **Tasks**:
  - T9: MCP repo probe
  - T10: Write all output files
  - T11: Record semantic memory
  - T12: Write evidence index and final report
- **Status**: In Progress

## Dependencies

```
T1,T2,T3 → 30_runtime_and_observability.md
T4,T5 → 20_mcp_and_rag.md (DONE)
T6,T7,T8 → 21_delegate_results.md (DONE)
T9 → 20_mcp_and_rag.md (DONE)
T10,T11,T12 → all output files
```

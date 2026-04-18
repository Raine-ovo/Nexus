# MCP & RAG Results

## Experiment: scope-continuity-validation

## 1. Document Ingestion Results

| File | Status |
|------|--------|
| `docs/architecture.md` | ✅ Successfully indexed |
| `docs/features.md` | ✅ Successfully indexed |

## 2. MCP Repo Probe Results

### Probe 1: Go source files
- **Glob**: `**/*.go`
- **Limit**: 20
- **Result**: Mock server response. Example matches: `internal/team/runtime.go`, `internal/gateway/server.go`, `cmd/nexus/main.go`
- **Note**: Response is mock (does not access real filesystem)

### Probe 2: Docs
- **Glob**: `docs/**`
- **Limit**: 10
- **Result**: Mock server response. Same example matches returned.
- **Note**: Response is mock (does not access real filesystem)

## 3. Knowledge Query Answer

**Question**: What are the main subsystems of the Nexus platform, and how does the team orchestration layer connect to the gateway?

### 3.1 Main Subsystems

1. **Agent Core Engine** (`internal/core/`) — ReAct loop with three-layer recovery
2. **Team Orchestration Layer** (`internal/team/`) — Manager, Lead, Teammates, Delegate, MessageBus
3. **Gateway** (`internal/gateway/`) — HTTP/WebSocket entry with BindingRouter, LaneManager, SessionManager
4. **RAG** (`internal/rag/`) — Retrieval-Augmented Generation with Elasticsearch
5. **Tools & MCP** (`internal/tool/`, `internal/mcp/`) — Tool registry, MCP client/server
6. **Memory System** (`internal/memory/`) — ConversationMemory, SemanticStore, compaction
7. **Planning** (`internal/planning/`) — TaskManager, executor, background tasks, cron
8. **Permission** (`internal/permission/`) — Pipeline with deny/allow/ask, path sandbox
9. **Intelligence** (`internal/intelligence/`) — BootstrapLoader, SkillManager, PromptAssembler
10. **Reflection Engine** — Three-phase reflection (Prospector → Agent + Evaluator → Reflector)
11. **Observability** — Structured logging, metrics, tracing

### 3.2 Gateway → Team Orchestration Connection

```
Client → Gateway → team.Registry → team.Manager → Lead
```

1. Client sends request via HTTP or WebSocket
2. Gateway processes: SessionManager → BindingRouter → LaneManager
3. Gateway delegates to `team.Registry` (implements `ScopedSupervisor`)
4. Registry resolves scope/workstream, finds/creates `team.Manager`
5. Manager enqueues request into `Lead.requestCh`
6. Lead's `leadLoop` dequeues and processes

**Key**: The old `orchestrator.Supervisor` has been replaced by the Team layer. Gateway speaks directly to `team.Registry` via `ScopedSupervisor` interface.

# 20 — MCP and RAG Results

## MCP Repo Probe

**Tool**: `mcp_repo_probe`  
**Called**: Yes (Phase 1)  
**Parameters**: `glob="**/*.go"`, `limit=50`

**Result**:
```json
{
  "glob": "**/*.go",
  "limit": 50,
  "note": "mock server response (does not access filesystem)",
  "example_matches": [
    "internal/team/runtime.go",
    "internal/gateway/server.go",
    "cmd/nexus/main.go"
  ]
}
```

**Assessment**: The MCP repo probe tool is available and responds, but returns mock data rather than actual filesystem contents. This confirms the tool infrastructure exists but the backend is simulated in the current environment. The example matches correctly identify key files that exist in the repository.

## RAG Knowledge Delegate Results

**Delegate**: knowledge (via delegate_task)  
**Status**: ✅ SUCCEEDED

### Tool Availability

| Tool | Available | Result |
|------|-----------|--------|
| `ingest_document` | ✅ Yes | ✅ Succeeded — `docs/architecture.md` indexed |
| `search_knowledge` | ✅ Yes | ✅ Succeeded — both queries returned highly relevant chunks |

### Query 1: Team Layer Dispatch

**Question**: "How does the Team layer dispatch work? What are the three dispatch paths and how do Lead, Teammate, and Delegate differ?"

**Key Findings**:
- Lead embeds Teammate (Go embedding, not inheritance)
- Three dispatch paths: (a) Lead direct tools, (b) delegate_task → Delegate, (c) send_message → Teammate
- Lead has exclusive access to: delegate_task, spawn_teammate, shutdown_teammate, broadcast, review_plan
- Delegate is ephemeral: fresh context, synchronous, state discarded after completion
- Teammate is persistent: independent goroutine, inbox-based, work→idle lifecycle

**Sources**: docs/architecture.md chunks 98ef3dbd, 68189155, f2741680

### Query 2: Reflection Engine Integration

**Question**: "What is the reflection engine and how does it integrate with the ReAct loop?"

**Key Findings**:
- Three-order reflection engine wraps Agent.Run (doesn't modify AgentLoop)
- Phase 1 (Prospector): Pre-execution critique using historical lessons
- Phase 2 (Evaluator): Multi-dimensional quality scoring
- Phase 3 (Reflector): Micro/Meso/Macro graded failure analysis
- Integration point: `ReflectionEngine.RunWithReflection` wraps standard ReAct loop
- Reflection memory persisted as YAML, retrieved via Jaccard keyword similarity

**Sources**: docs/architecture.md chunks 8daa94a3, 51d2804d, c1d2f89c

### RAG Pipeline Evidence

1. **Ingestion worked**: `ingest_document` returned `status: "indexed"` with resolved path
2. **Dual-channel retrieval confirmed**: Results include `channel` field — keyword and vector channels both active
3. **Cross-encoder reranking active**: `cross_encoder_sim` scores present on all chunks
4. **RRF fusion confirmed**: `rrf_score` fields present, combining keyword and vector channels
5. **Score gradients meaningful**: Top chunks scored 1.0/~0.8 (relevant), mermaid chunk scored 0.09 (tangential)

**Conclusion**: Full RAG pipeline — ingestion → chunking → embedding → dual-channel retrieval → cross-encoder reranking → RRF fusion → ranked results — is operational and producing high-quality results.

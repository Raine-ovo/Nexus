# 32 — State Persistence

## Persistence Mechanisms Verified

### 1. Task DAG (JSON files)

**Path**: `.runs/full-activation-throttled/.tasks/`  
**Status**: ✅ Working

| File | Purpose | Present |
|------|---------|---------|
| meta.json | Next task ID counter | ✅ |
| task_1.json through task_7.json | Individual task records | ✅ |

Each task JSON contains: id, title, description, status, blocked_by, blocks, claimed_at, claim_source, created_at, updated_at.

**Verified**: Tasks were created by planner-002, claimed, and status transitions were persisted correctly.

### 2. Team Roster (config.json)

**Path**: `.team-full-activation-throttled/config.json`  
**Status**: ✅ Working

Records all team members with (name, role, status) triples. Updated when teammates are spawned or shut down.

**Current state**:
- lead: working
- planner-001: shutdown (prior experiment)
- devops-001: shutdown
- planner-002: shutdown

### 3. Claim Events (JSONL audit log)

**Path**: `.team-full-activation-throttled/claim_events.jsonl`  
**Status**: ✅ Working

Append-only JSONL log of all claim events with: event type, task_id, owner, role, source (auto/manual), timestamp.

### 4. Semantic Memory

**Path**: `.memory/semantic.yaml.tmp`  
**Status**: ⚠️ Partially present

A `.tmp` file exists, suggesting the semantic memory system attempted to write but may not have completed a clean persist. The devops-001 report confirmed that semantic memory is **not functional** from the teammate perspective — the MEMORY section was empty on resume.

**Root cause**: Memory tools (store_memory, retrieve_memory) are not provisioned to teammates. The infrastructure exists in `internal/memory/` but is not connected to the teammate tool set.

### 5. Reflection Memory

**Path**: `.runs/full-activation-throttled/.memory/reflections.yaml` (per sandbox config)  
**Status**: ❌ Not present

Reflection is likely not enabled in the current configuration (reflection.enabled defaults to false per docs).

### 6. Trace Snapshot

**Path**: `.runs/full-activation-throttled/latest-traces.json`  
**Status**: ❌ Not present

The Nexus server process is not running, so no traces are being generated. The Runtime.writeLatestTraceSnapshot method would create this file during active server operation.

### 7. Run Sandbox

**Path**: `.runs/full-activation-throttled/`  
**Status**: ✅ Working

The sandbox directory structure is correctly set up:
```
.runs/full-activation-throttled/
├── .outputs/
│   └── full_activation_run/
├── .tasks/
│   ├── meta.json
│   └── task_1.json ... task_7.json
├── README.md
└── experiment/
```

## Persistence Architecture Assessment

| Mechanism | Format | Durability | Verdict |
|-----------|--------|------------|---------|
| Task DAG | JSON per task | ✅ Crash-safe (atomic writes) | Working |
| Team Roster | JSON | ✅ Crash-safe | Working |
| Claim Events | JSONL append | ✅ Crash-safe | Working |
| Semantic Memory | YAML | ⚠️ .tmp file exists | Partially working |
| Reflection Memory | YAML | ❌ Not created | Not enabled |
| Trace Snapshot | JSON | ❌ Not created | Requires running server |
| Cron Results | JSON per trigger | ❌ Not tested | Requires cron activation |

## Key Insight

The Nexus persistence model is **file-first, zero-DB** — all state is stored as JSON/YAML files in the workspace. This is a deliberate design choice (documented in architecture.md §13.2) favoring:
- Single-binary deployment
- Human-readable debugging (cat/jq)
- Crash recovery via file existence checks
- Gradual evolution to databases when needed

# Nexus 完整工作流文档

> 本文档用 Mermaid 流程图完整描绘 Nexus 从启动到请求处理再到优雅关闭的全生命周期，
> 以及每个核心模块内部的详细工作流。

---

## 目录

1. [系统全局工作流](#1-系统全局工作流)
   - 1.1 启动初始化流程
   - 1.2 请求处理主链路
   - 1.3 优雅关闭流程
2. [Gateway 网关模块](#2-gateway-网关模块)
3. [Team 团队协作调度模块](#3-team-团队协作调度模块)
4. [Core AgentLoop 核心循环](#4-core-agentloop-核心循环)
5. [Tool 工具系统](#5-tool-工具系统)
6. [Permission 权限管线](#6-permission-权限管线)
7. [RAG 检索增强生成](#7-rag-检索增强生成)
8. [Memory 记忆系统](#8-memory-记忆系统)
9. [Intelligence 智能组装](#9-intelligence-智能组装)
10. [Planning 规划与调度](#10-planning-规划与调度)
11. [Reflection 反思引擎](#11-reflection-反思引擎)
12. [Observability 可观测性](#12-observability-可观测性)
13. [模块间依赖总览](#13-模块间依赖总览)

---

## 1. 系统全局工作流

### 1.1 启动初始化流程

```mermaid
flowchart TD
    START([main 入口]) --> PARSE_FLAGS[解析 -config 标志]
    PARSE_FLAGS --> LOAD_CFG{加载 YAML 配置}
    LOAD_CFG -->|成功| CFG_OK[configs.Load 完成]
    LOAD_CFG -->|失败| ENV_FALLBACK[configs.LoadFromEnv 兜底]
    ENV_FALLBACK --> CFG_OK

    CFG_OK --> INIT_OBS[创建 Observability]
    INIT_OBS --> INIT_PERM[创建 Permission Pipeline]
    INIT_PERM --> INIT_TOOL[创建 Tool Registry + 注册内置工具]
    INIT_TOOL --> INIT_MCP[创建 MCP Manager]

    INIT_MCP --> INIT_MEM[创建 Memory Manager<br/>ConversationMemory + SemanticStore]
    INIT_MEM --> INIT_RAG[创建 RAG Engine<br/>VectorStore + KeywordStore + Retrieval]
    INIT_RAG --> INIT_PLAN[创建 Planning 三件套<br/>TaskManager + CronScheduler + BackgroundManager]

    INIT_PLAN --> INIT_INTEL[创建 PromptAssembler + SkillManager<br/>扫描 workspace/skills/]
    INIT_INTEL --> REG_SKILL[注册 list_skills / load_skill<br/>到 Tool Registry]

    REG_SKILL --> BUILD_DEPS[组装 AgentDependencies]
    BUILD_DEPS --> CREATE_AGENTS[创建四个 Agent<br/>code_reviewer / knowledge / devops / planner]
    CREATE_AGENTS --> BUILD_TMPL[从 Agent 提取 AgentTemplate<br/>role + systemPrompt + tools]
    BUILD_TMPL --> CREATE_TEAM[创建 team.Manager<br/>Lead 嵌入 Teammate + Roster + Bus]
    CREATE_TEAM --> REG_TMPL[注册角色模板到 Manager<br/>code_reviewer / knowledge / devops / planner]
    REG_TMPL --> REHYDRATE[Roster 恢复：rehydrate 已有 Teammate]
    REHYDRATE --> CREATE_GW[创建 Gateway<br/>Manager 实现 Supervisor 接口]

    CREATE_GW --> START_GW[启动 HTTP Server goroutine]
    START_GW --> START_CRON[启动 CronScheduler]
    START_CRON --> READY([系统就绪, 等待信号])

    style START fill:#2d8cf0,color:#fff
    style READY fill:#19be6b,color:#fff
```

### 1.2 请求处理主链路（端到端）

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as Gateway
    participant LN as LaneManager
    participant MGR as team.Manager
    participant LEAD as Lead
    participant TM as Teammate
    participant DLG as Delegate
    participant BUS as MessageBus
    participant LOOP as AgentLoop
    participant LLM as ChatModel
    participant TR as ToolRegistry
    participant PM as Permission

    C->>GW: POST /api/chat {session_id, input}
    GW->>GW: 验证 session / 创建 session
    GW->>LN: Submit(lane, task)
    LN->>LN: 检查 generation, 信号量控制并发
    LN->>MGR: HandleRequest(sessionID, input)
    MGR->>LEAD: requestCh ← leadRequest{input, replyCh}

    Note over LEAD: Lead 的 select 事件循环

    LEAD->>LOOP: workPhase: RunLoop(ctx, input)

    loop ReAct 循环 (最多 maxIter 次)
        LOOP->>LOOP: ContextGuard.MaybeCompact — 上下文压缩
        LOOP->>LOOP: PreAPI Hook
        LOOP->>LLM: Generate(system, messages, tools)
        LLM-->>LOOP: ChatModelResponse
        LOOP->>LOOP: PostAPI Hook

        alt 无 ToolCalls → 结束
            LOOP-->>LEAD: 最终文本
        else 调用 delegate_task
            LEAD->>DLG: DelegateWork(tmpl, task)
            DLG->>LOOP: 临时 RunLoop（隔离上下文）
            LOOP-->>DLG: 结果
            DLG-->>LEAD: 结果文本（Agent 状态丢弃）
        else 调用 send_message
            LEAD->>BUS: Send(to=teammate, content)
            BUS->>BUS: append to inbox/{teammate}.jsonl
            Note over TM: Teammate idlePhase 轮询收件箱
            TM->>BUS: ReadInbox → drain & truncate
            TM->>TM: workPhase: 处理消息
            TM->>BUS: Send(to=lead, reply)
            LEAD->>BUS: ReadInbox → 读取回复
        else 调用 spawn_teammate
            LEAD->>MGR: Spawn(name, role)
            MGR->>MGR: Roster.Add + 启动 goroutine
        else 普通 ToolCalls → 执行工具
            loop 遍历每个 ToolCall
                LOOP->>LOOP: PreTool Hook
                LOOP->>TR: resolveTool(name)
                LOOP->>PM: CheckTool(name, args)
                alt 允许
                    LOOP->>TR: handler(ctx, args)
                    TR-->>LOOP: ToolResult
                else 拒绝
                    LOOP-->>LOOP: 返回权限错误
                end
                LOOP->>LOOP: PostTool Hook
            end
        end
    end

    LEAD-->>MGR: replyCh ← response
    MGR-->>LN: 最终结果
    LN-->>GW: LaneResult
    GW-->>C: {output}
```

### 1.3 优雅关闭流程

```mermaid
flowchart TD
    SIG([收到 SIGINT/SIGTERM]) --> CANCEL[context.Cancel 传播]
    CANCEL --> SHUT_TEAM[team.Manager.Shutdown]

    subgraph 团队关闭
        SHUT_TEAM --> SHUT_TMS[遍历所有 Teammate<br/>发送 shutdown_request]
        SHUT_TMS --> WAIT_TMS[等待所有 Teammate goroutine 退出]
        WAIT_TMS --> SHUT_LEAD[关闭 Lead goroutine]
    end

    SHUT_LEAD --> STOP_CRON[CronScheduler.Stop]
    STOP_CRON --> SHUT_BG[BackgroundManager.Shutdown<br/>30s 超时等待]
    SHUT_BG --> CLOSE_MCP[MCPManager.CloseAll]
    CLOSE_MCP --> FLUSH_MEM[MemoryManager.Flush<br/>持久化 Semantic Store]
    FLUSH_MEM --> EXIT([nexus stopped gracefully])

    style SIG fill:#ed4014,color:#fff
    style EXIT fill:#19be6b,color:#fff
```

---

## 2. Gateway 网关模块

### 2.1 HTTP/WebSocket 请求路由

```mermaid
flowchart TD
    REQ([客户端请求]) --> TRACE_MW[Trace 中间件<br/>注入 X-Request-ID]

    TRACE_MW --> ROUTE{路由分发}
    ROUTE -->|GET /api/health| HEALTH[返回 status:ok]
    ROUTE -->|POST /api/sessions| CREATE_SESS[创建 Session<br/>BindingRouter 绑定默认 Agent]
    ROUTE -->|POST /api/chat| CHAT[Chat 处理]
    ROUTE -->|GET /api/ws| WS[WebSocket 升级]

    CHAT --> VALIDATE{校验 session_id + input}
    VALIDATE -->|缺失| ERR_400[400 Bad Request]
    VALIDATE -->|session 不存在| ERR_404[404 Not Found]
    VALIDATE -->|通过| TOUCH[SessionManager.Touch 续期]
    TOUCH --> SELECT_LANE[选择 Lane<br/>默认 main]
    SELECT_LANE --> SUBMIT[LaneManager.Submit]
    SUBMIT --> TEAM_CALL[team.Manager.HandleRequest<br/>→ Lead 处理]
    TEAM_CALL --> RESPOND[JSON Response {output}]

    WS --> WS_LOOP[WebSocket 消息循环]
    WS_LOOP --> WS_PARSE[解析 JSON {session_id, input}]
    WS_PARSE --> WS_VALIDATE{校验}
    WS_VALIDATE -->|通过| WS_SUBMIT[LaneManager.Submit]
    WS_SUBMIT --> WS_RESULT[按 2048 rune 分帧回写]

    style REQ fill:#2d8cf0,color:#fff
```

### 2.2 Lane 并发控制

```mermaid
flowchart TD
    SUBMIT([Submit 调用]) --> CHECK_LANE{Lane 存在?}
    CHECK_LANE -->|不存在| ERR[error: unknown lane]
    CHECK_LANE -->|存在| STAMP_GEN[读取当前 generation]
    STAMP_GEN --> ENQUEUE[LaneTask 入队 queue channel]

    ENQUEUE --> DEQUEUE[processQueue 取出任务]
    DEQUEUE --> GEN_CHECK{task.Generation<br/>== lane.Generation?}
    GEN_CHECK -->|不等| STALE[返回 ErrStaleLane]
    GEN_CHECK -->|相等| SEM{信号量 slot 空闲?}
    SEM -->|等待| SEM
    SEM -->|获取| EXEC[goroutine 执行 task.Execute]
    EXEC --> RELEASE[释放信号量 slot]
    RELEASE --> RESULT[结果写入 ResultCh]

    RESET([Lane.Reset 调用]) --> INC_GEN[generation++]
    INC_GEN --> DRAIN[排空 queue, 全部返回 ErrStaleLane]

    style SUBMIT fill:#2d8cf0,color:#fff
    style RESET fill:#ff9900,color:#fff
```

### 2.3 Session 与 BindingRouter

```mermaid
flowchart LR
    CREATE([POST /api/sessions]) --> SM[SessionManager.Create<br/>生成 UUID, 记录 channel+user]
    SM --> BR{BindingRouter.Route<br/>5 级优先级}
    BR -->|1. channel+user 精确| AGENT
    BR -->|2. channel 绑定| AGENT
    BR -->|3. user 绑定| AGENT
    BR -->|4. channel 前缀| AGENT
    BR -->|5. 全局默认| AGENT
    AGENT[绑定 agent_id 到 Session]
```

---

## 3. Team 团队协作调度模块

### 3.1 Lead 请求处理主流程

```mermaid
flowchart TD
    HANDLE([Manager.HandleRequest]) --> SEND_REQ[requestCh ← leadRequest]
    SEND_REQ --> LEAD_LOOP{Lead select 事件循环}

    LEAD_LOOP -->|requestCh| WORK[workPhase: RunLoop<br/>Lead 的 ReAct 循环]
    LEAD_LOOP -->|inboxTick| DRAIN[drainInbox<br/>处理 Teammate 回复]

    WORK --> DECIDE{Lead 模型决策}

    DECIDE -->|简单任务| SELF[直接使用自身工具处理]
    DECIDE -->|一次性专业任务| DELEGATE[调用 delegate_task 工具]
    DECIDE -->|持续协作任务| MSG[调用 send_message 工具]
    DECIDE -->|需要新 worker| SPAWN[调用 spawn_teammate 工具]

    DELEGATE --> DLG_WORK[DelegateWork<br/>临时 Agent + 隔离上下文]
    DLG_WORK --> DLG_RESULT[返回结果文本<br/>Agent 状态丢弃]

    MSG --> BUS_SEND[MessageBus.Send<br/>写入目标 inbox JSONL]
    BUS_SEND --> TM_WAKE[Teammate idlePhase<br/>轮询发现新消息]
    TM_WAKE --> TM_WORK[Teammate workPhase<br/>处理消息]
    TM_WORK --> TM_REPLY[Send reply → Lead inbox]

    SPAWN --> MGR_SPAWN[Manager.Spawn<br/>Roster.Add + 启动 goroutine]

    SELF --> REPLY
    DLG_RESULT --> REPLY
    TM_REPLY --> DRAIN
    DRAIN --> REPLY

    REPLY([replyCh → 返回结果])

    style HANDLE fill:#2d8cf0,color:#fff
    style REPLY fill:#19be6b,color:#fff
```

### 3.2 Teammate 生命周期状态机

```mermaid
stateDiagram-v2
    [*] --> Idle: Spawn / rehydrate
    Idle --> Working: 收件箱消息 / 任务认领
    Working --> Idle: workPhase 完成
    Idle --> Idle: 轮询无消息 → sleep
    Idle --> [*]: idleTimeout / shutdown_request

    state Working {
        [*] --> RunLoop
        RunLoop --> ToolExec: tool_calls
        ToolExec --> RunLoop: tools_done
        RunLoop --> Compact: token_threshold
        Compact --> RunLoop: compact_done
        RunLoop --> [*]: model_finished
    }
```

### 3.3 Teammate idle 阶段三级优先级

```mermaid
flowchart TD
    IDLE([idlePhase 开始]) --> POLL_INBOX{1. 轮询收件箱<br/>ReadInbox}

    POLL_INBOX -->|有消息| HANDLE_MSG[处理消息<br/>→ 进入 workPhase]
    POLL_INBOX -->|无消息| SCAN_TASKS{2. 扫描可认领任务<br/>ScanClaimable}

    SCAN_TASKS -->|有匹配任务| CLAIM[Claim + workPhase<br/>ClaimLogger 记录]
    SCAN_TASKS -->|无任务| CHECK_TIMEOUT{3. 检查空闲超时<br/>IdleTimeout}

    CHECK_TIMEOUT -->|未超时| SLEEP[sleep InboxPollInterval<br/>→ 回到轮询]
    CHECK_TIMEOUT -->|已超时| SHUTDOWN[自动关闭<br/>Roster.Remove]

    HANDLE_MSG --> IDLE
    CLAIM --> IDLE

    style IDLE fill:#2d8cf0,color:#fff
    style SHUTDOWN fill:#ed4014,color:#fff
```

### 3.4 MessageBus 通信流程

```mermaid
sequenceDiagram
    participant A as Agent A (sender)
    participant BUS as MessageBus
    participant FS as inbox/{B}.jsonl
    participant B as Agent B (receiver)

    A->>BUS: Send(to=B, content, type)
    BUS->>BUS: mu.Lock — 序列化写入
    BUS->>FS: json.Marshal → append line
    BUS->>BUS: mu.Unlock

    Note over B: idlePhase 定时轮询
    B->>BUS: ReadInbox(name=B)
    BUS->>FS: ReadFile → 逐行解析
    BUS->>FS: Truncate(0) — drain-then-truncate
    BUS-->>B: []MessageEnvelope
    B->>B: 逐条处理消息
```

### 3.5 Delegate 一次性任务

```mermaid
flowchart TD
    CALL([Lead 调用 delegate_task]) --> CREATE[创建临时 Agent<br/>使用 AgentTemplate]
    CREATE --> CLEAN_CTX[全新上下文<br/>仅 systemPrompt + task]
    CLEAN_CTX --> RUN[RunLoop 执行任务]
    RUN --> RESULT[提取结果文本]
    RESULT --> DISCARD[丢弃 Agent 状态<br/>无持久化、无副作用]
    DISCARD --> RETURN([返回结果给 Lead])

    style CALL fill:#2d8cf0,color:#fff
    style RETURN fill:#19be6b,color:#fff
```

### 3.6 角色模板注册

```mermaid
graph LR
    subgraph AgentTemplates
        CR[code_reviewer<br/>代码审查 + 安全模式]
        KB[knowledge<br/>RAG 检索 + 文档搜索]
        DO[devops<br/>CI/CD + 基础设施]
        PL[planner<br/>DAG 任务 + 工作分解]
    end

    CR -->|RegisterTemplate| MGR[team.Manager]
    KB -->|RegisterTemplate| MGR
    DO -->|RegisterTemplate| MGR
    PL -->|RegisterTemplate| MGR

    MGR -->|Spawn / DelegateWork| INST[按模板实例化<br/>role + systemPrompt + tools]
```

---

## 4. Core AgentLoop 核心循环

### 4.1 ReAct 循环状态机

```mermaid
stateDiagram-v2
    [*] --> Created: NewLoopState
    Created --> Running: run_start
    Running --> ToolExecution: tool_calls
    ToolExecution --> Running: tools_done
    Running --> Compacting: token_threshold / soft_limit
    Compacting --> Running: compact_done
    Running --> Completed: model_finished
    Running --> Error: 异常
    ToolExecution --> Error: hook 失败
    Compacting --> Error: compact 失败
    Error --> [*]
    Completed --> [*]
```

### 4.2 RunLoop 详细流程

```mermaid
flowchart TD
    START([RunLoop 入口]) --> NIL_CHECK{model != nil?}
    NIL_CHECK -->|否| FAIL_NIL[error: nil ChatModel]
    NIL_CHECK -->|是| INIT_STATE[NewLoopState → PhaseRunning]
    INIT_STATE --> USER_MSG[构造 UserMessage 追加到 state]

    USER_MSG --> ITER_START{iter < maxIter?}
    ITER_START -->|否| EXCEED_ERR[error: exceeded max_iterations]
    ITER_START -->|是| COMPACT[ContextGuard.MaybeCompact]

    COMPACT --> PRE_API{PreAPI Hook?}
    PRE_API -->|有| RUN_PRE_API[执行 PreAPI]
    PRE_API -->|无| GENERATE
    RUN_PRE_API --> GENERATE

    GENERATE[model.Generate<br/>system + messages + tools]

    GENERATE --> RETRY{RecoveryManager?}
    RETRY -->|有| CALL_RETRY[CallWithRetry 包裹]
    RETRY -->|无| DIRECT_CALL[直接调用]
    CALL_RETRY --> GEN_RESULT
    DIRECT_CALL --> GEN_RESULT

    GEN_RESULT{生成成功?}
    GEN_RESULT -->|否 + ContextOverflow| FORCE_COMPACT[ForceManualCompact → 重试]
    GEN_RESULT -->|否 其他| GEN_ERR[error: model generate]
    GEN_RESULT -->|是| POST_API

    POST_API{PostAPI Hook?}
    POST_API -->|有| RUN_POST_API[执行 PostAPI]
    POST_API -->|无| CONTINUE_CHECK
    RUN_POST_API --> CONTINUE_CHECK

    CONTINUE_CHECK{shouldContinueWithTools?}
    CONTINUE_CHECK -->|否: 纯文本| FINISH[追加 AssistantMessage<br/>PhaseCompleted → 返回]
    CONTINUE_CHECK -->|是: 有 ToolCalls| TOOL_PHASE[PhaseToolExecution]

    TOOL_PHASE --> TOOL_LOOP[遍历每个 ToolCall]

    subgraph 工具执行
        TOOL_LOOP --> PRE_TOOL[PreTool Hook]
        PRE_TOOL --> RESOLVE[resolveTool<br/>本地优先 → 全局 Registry]
        RESOLVE --> PERM[PermPipeline.CheckTool]
        PERM -->|拒绝| TOOL_ERR[WrapToolError]
        PERM -->|允许| EXEC_HANDLER[handler(ctx, args)]
        EXEC_HANDLER --> MICRO{输出 > MicroCompactSize?}
        MICRO -->|是| PERSIST[PersistLargeOutput → 替换为标记]
        MICRO -->|否| TOOL_RESULT[追加 ToolMessage]
        PERSIST --> TOOL_RESULT
        TOOL_ERR --> TOOL_RESULT
        TOOL_RESULT --> POST_TOOL[PostTool Hook]
        POST_TOOL --> NEXT_TOOL{还有下一个 ToolCall?}
        NEXT_TOOL -->|是| TOOL_LOOP
        NEXT_TOOL -->|否| BACK_RUN[PhaseRunning → tools_done]
    end

    BACK_RUN --> ITER_START
    FORCE_COMPACT --> GENERATE

    style START fill:#2d8cf0,color:#fff
    style FINISH fill:#19be6b,color:#fff
    style GEN_ERR fill:#ed4014,color:#fff
    style EXCEED_ERR fill:#ed4014,color:#fff
```

### 4.3 ContextGuard 上下文压缩

```mermaid
flowchart TD
    MAYBE([MaybeCompact]) --> MICRO_SCAN[遍历 Tool 消息<br/>检查 tokens >= MicroCompactSize]
    MICRO_SCAN -->|有超大输出| PERSIST_DISK[写入 .outputs/ 目录<br/>替换内容为 marker]
    MICRO_SCAN --> EST[估算 total tokens]

    EST --> HARD{total >= TokenThreshold?}
    HARD -->|是| MANUAL_TRIM[ManualTrim<br/>保留 system head + 最近尾部<br/>丢弃中间消息]
    MANUAL_TRIM --> DONE_MAN[CompactionManual]

    HARD -->|否| SOFT{total >= 85% Threshold?}
    SOFT -->|是 且有 SummarizeFunc| AUTO_COMPACT[CompactOldTurns<br/>对 convWindow 之前的消息调 LLM 摘要]
    AUTO_COMPACT --> DONE_AUTO[CompactionAuto]

    SOFT -->|否| MICRO_CHECK{Micro 有变更?}
    MICRO_CHECK -->|是| DONE_MICRO[CompactionMicro]
    MICRO_CHECK -->|否| DONE_NONE[CompactionNone]

    style MAYBE fill:#2d8cf0,color:#fff
```

---

## 5. Tool 工具系统

### 5.1 Registry 注册与查找

```mermaid
flowchart TD
    STARTUP([启动阶段]) --> REG_BUILTIN[RegisterBuiltins]

    REG_BUILTIN --> FILE_TOOLS[file_read / file_write<br/>file_edit / file_list]
    REG_BUILTIN --> SHELL_TOOLS[shell_execute]
    REG_BUILTIN --> HTTP_TOOLS[http_request]
    REG_BUILTIN --> SEARCH_TOOLS[code_search / code_grep<br/>code_glob]
    REG_BUILTIN --> SKILL_TOOLS[list_skills / load_skill]

    AGENTS[各 Agent 注册自己的工具] --> LOCAL_TOOLS
    subgraph LOCAL_TOOLS [Agent 本地工具]
        CR_TOOLS[code_reviewer:<br/>analyze_diff, review_file,<br/>check_patterns]
        KB_TOOLS[knowledge:<br/>search_knowledge,<br/>ingest_document,<br/>list_knowledge_bases]
        DEVOPS_TOOLS[devops:<br/>check_health,<br/>parse_logs,<br/>run_diagnostic]
        PLAN_TOOLS[planner:<br/>create_plan, update_task,<br/>list_tasks, execute_task]
    end

    RESOLVE([resolveTool 查找]) --> LOCAL_FIRST[先查 Agent 本地 ToolMeta 列表]
    LOCAL_FIRST -->|命中| FOUND[返回 ToolMeta]
    LOCAL_FIRST -->|未命中| GLOBAL[查全局 Registry.Get]
    GLOBAL -->|命中| FOUND
    GLOBAL -->|未命中| NOT_FOUND[error: unknown tool]

    style STARTUP fill:#2d8cf0,color:#fff
    style RESOLVE fill:#ff9900,color:#fff
```

### 5.2 MCP 协议交互

```mermaid
sequenceDiagram
    participant Client as MCP Client
    participant Transport as Transport (stdio/HTTP)
    participant Server as MCP Server

    Client->>Transport: JSON-RPC Request<br/>{method, params, id}
    Transport->>Server: 转发

    alt tools/list
        Server-->>Transport: {tools: [...]}
    else tools/call
        Server->>Server: 查找 handler, 执行
        Server-->>Transport: {content: [...]}
    else initialize
        Server-->>Transport: {capabilities, serverInfo}
    end

    Transport-->>Client: JSON-RPC Response
```

---

## 6. Permission 权限管线

### 6.1 五阶段检查流程

```mermaid
flowchart TD
    CHECK([Pipeline.Check]) --> S1{Stage 1: Deny 规则}
    S1 -->|命中 deny rule| DENY_1[Decision: DENY<br/>reason: matched deny rule]
    S1 -->|未命中| S2{Stage 2: Path Sandbox}

    S2 --> EXTRACT[提取 toolInput 中的路径]
    EXTRACT --> VALIDATE_PATH[ValidatePath<br/>workspace 边界检查]
    VALIDATE_PATH -->|越界| DENY_2[Decision: DENY<br/>reason: sandbox violation]
    VALIDATE_PATH -->|通过| VALIDATE_CMD[ValidateCommand<br/>危险模式检查]
    VALIDATE_CMD -->|匹配危险模式| DENY_2
    VALIDATE_CMD -->|通过| S3{Stage 3: 模式判断}

    S3 -->|full_auto| ALLOW_FA[Decision: ALLOW<br/>reason: full_auto mode]
    S3 -->|manual| ASK_MAN[Decision: ASK<br/>reason: manual mode]
    S3 -->|semi_auto / 默认| S4{Stage 4: Allow 规则}

    S4 -->|命中 allow rule| ALLOW_4[Decision: ALLOW<br/>reason: matched allow rule]
    S4 -->|未命中| S5[Stage 5: 默认 ASK]

    style CHECK fill:#2d8cf0,color:#fff
    style DENY_1 fill:#ed4014,color:#fff
    style DENY_2 fill:#ed4014,color:#fff
    style ALLOW_FA fill:#19be6b,color:#fff
    style ALLOW_4 fill:#19be6b,color:#fff
    style ASK_MAN fill:#ff9900,color:#fff
    style S5 fill:#ff9900,color:#fff
```

---

## 7. RAG 检索增强生成

### 7.1 Ingest 索引管线

```mermaid
flowchart TD
    INGEST([Engine.Ingest]) --> STAT[os.Stat 检查路径]
    STAT -->|目录| DIR_LOAD[IngestDirectory<br/>DirectoryLoader.Load]
    STAT -->|文件| FILE_LOAD[FileLoader 读取]

    FILE_LOAD --> CHUNK[RecursiveChunker.ChunkDocument<br/>按 ChunkSize + Overlap 分块]
    DIR_LOAD --> MULTI_FILE[遍历所有文件]
    MULTI_FILE --> CHUNK

    CHUNK --> EMBED_LOOP[遍历每个 Chunk]

    subgraph 每个 Chunk 处理
        EMBED_LOOP --> EMBED[Embedder.Embed → 向量]
        EMBED --> VEC_ADD[VectorStore.Add<br/>写入 Memory / Milvus]
        VEC_ADD --> KW_ADD[KeywordStore.Add<br/>写入 Memory / ES]
        KW_ADD --> RECORD_ID[记录 chunk ID 到 docChunks]
    end

    style INGEST fill:#2d8cf0,color:#fff
```

### 7.2 Query 检索管线

```mermaid
flowchart TD
    QUERY([Engine.Query]) --> RETRIEVE[MultiChannelEngine.Retrieve<br/>topK * 2 过采样]

    subgraph 双通道并行检索
        RETRIEVE --> VEC_SEARCH[VectorStore.Search<br/>向量相似度]
        RETRIEVE --> KW_SEARCH[KeywordStore.Search<br/>BM25 关键词]
        VEC_SEARCH --> RRF
        KW_SEARCH --> RRF[Reciprocal Rank Fusion<br/>合并两路结果]
    end

    RRF --> RERANK[CompositeReranker]

    subgraph 级联重排
        RERANK --> KW_BOOST[KeywordBoostReranker<br/>关键词加分]
        KW_BOOST --> CROSS[CrossEncoderReranker<br/>交叉编码打分]
    end

    CROSS --> POST[PostChain 后处理]

    subgraph 后处理链
        POST --> DEDUP[DeduplicateProcessor<br/>去重]
        DEDUP --> NORMALIZE[ScoreNormalizer<br/>分数归一化]
        NORMALIZE --> ENRICH[ContextEnricher<br/>上下文扩展]
    end

    ENRICH --> TOPK_CUT[取 TopK 截断]
    TOPK_CUT --> FORMAT[formatContext<br/>格式化为带分数的文本]
    FORMAT --> RESULT([返回检索上下文])

    style QUERY fill:#2d8cf0,color:#fff
    style RESULT fill:#19be6b,color:#fff
```

---

## 8. Memory 记忆系统

### 8.1 双层记忆架构

```mermaid
flowchart TD
    MANAGER([Memory Manager]) --> CONV[ConversationMemory<br/>滑动窗口缓冲]
    MANAGER --> SEM[SemanticStore<br/>YAML 持久化]

    CONV --> WINDOW[保留最近 N 条消息]
    CONV --> SUMMARIES[对超出窗口的历史生成摘要]

    SEM --> YAML_FILE[.memory/semantic.yaml]
    SEM --> SEARCH[相关性搜索]
    SEM --> CRUD[增/删/改]

    BUILD([BuildPromptSection]) --> READ_SUM[读取 Summaries]
    BUILD --> READ_SEM[读取 Semantic Section]
    READ_SUM --> JOIN[拼接 Earlier conversation 段]
    READ_SEM --> JOIN
    JOIN --> PROMPT_SECTION[注入 System Prompt]
```

### 8.2 Compaction 压缩策略

```mermaid
flowchart LR
    subgraph 三级压缩
        MICRO[Micro 压缩<br/>大输出落盘<br/>成本: I/O]
        AUTO[Auto 压缩<br/>LLM 摘要旧轮次<br/>成本: LLM 调用]
        MANUAL[Manual 压缩<br/>丢弃中间消息<br/>成本: 信息丢失]
    end

    MICRO -->|85% 阈值| AUTO
    AUTO -->|100% 阈值| MANUAL
```

---

## 9. Intelligence 智能组装

### 9.1 Prompt 组装流程

```mermaid
flowchart TD
    BUILD([PromptAssembler.Build]) --> BOOTSTRAP[BootstrapLoader.Load<br/>读取 workspace/ 下的 MD 文件]

    subgraph Bootstrap 文件
        SOUL[SOUL.md — 核心理念]
        IDENTITY[IDENTITY.md — 身份定义]
        TOOLS_DOC[TOOLS.md — 工具文档]
        MEMORY_DOC[MEMORY.md — 记忆指南]
    end

    BOOTSTRAP --> CAP_BS[截断到 BootstrapMax<br/>默认 150K chars]

    BUILD --> SKILL_SCAN[SkillManager.ScanSkills<br/>扫描 workspace/skills/*/SKILL.md]
    SKILL_SCAN --> SKILL_INDEX[GetIndexPrompt<br/>生成技能索引摘要]
    SKILL_INDEX --> CAP_SK[截断到 SkillIndexMax<br/>默认 30K chars]

    BUILD --> MEM_SEC[传入 memorySection]
    MEM_SEC --> CAP_MEM[截断到 MemoryMax<br/>默认 20K chars]

    CAP_BS --> ASSEMBLE[拼接三段]
    CAP_SK --> ASSEMBLE
    CAP_MEM --> ASSEMBLE
    ASSEMBLE --> CAP_TOTAL[总体截断到 TotalMax<br/>默认 200K chars]
    CAP_TOTAL --> RESULT([最终 System Prompt])

    style BUILD fill:#2d8cf0,color:#fff
    style RESULT fill:#19be6b,color:#fff
```

### 9.2 Skill 技能加载

```mermaid
flowchart TD
    SCAN([ScanSkills]) --> WALK[遍历 skills/ 子目录]
    WALK --> READ_FM[读取 SKILL.md YAML frontmatter<br/>name, description, invocation]
    READ_FM --> INDEX[建立技能索引 in-memory]

    LIST([list_skills 工具]) --> RETURN_INDEX[返回所有技能名 + 描述]

    LOAD([load_skill 工具]) --> FIND[按 name 查找]
    FIND --> READ_BODY[按需加载完整 SKILL.md body]
    READ_BODY --> INJECT[注入 ToolResult → 回到 AgentLoop]
```

---

## 10. Planning 规划与调度

### 10.1 Task DAG 管理

```mermaid
flowchart TD
    CREATE([create_plan 工具]) --> PARSE_TASKS[解析任务列表 + 依赖关系]
    PARSE_TASKS --> CYCLE_CHECK[DAG 环检测]
    CYCLE_CHECK -->|有环| REJECT[拒绝创建]
    CYCLE_CHECK -->|无环| PERSIST[写入 task_<id>.json + meta.json]

    UPDATE([update_task 工具]) --> GET_TASK[TaskManager.Get]
    GET_TASK --> STATUS_TRANS{状态转换合法?}
    STATUS_TRANS -->|是| WRITE_STATUS[更新状态并持久化]
    STATUS_TRANS -->|否| TRANS_ERR[error: invalid transition]

    subgraph 任务状态流转
        PENDING[Pending] -->|Claim| IN_PROGRESS[InProgress]
        IN_PROGRESS -->|Complete| COMPLETED[Completed]
        IN_PROGRESS -->|Fail| PENDING
        PENDING -->|Block| BLOCKED[Blocked]
        BLOCKED -->|Unblock| PENDING
        PENDING -->|Cancel| CANCELLED[Cancelled]
    end
```

### 10.2 PlanExecutor 执行器

```mermaid
flowchart TD
    EXEC_NEXT([ExecuteNext]) --> GET_UNCLAIMED[TaskManager.GetUnclaimed<br/>获取首个未认领任务]
    GET_UNCLAIMED -->|无任务| NO_WORK[error: no unclaimed tasks]
    GET_UNCLAIMED -->|有| EXEC_TASK

    EXEC_TASK([ExecuteTask]) --> GET[TaskManager.Get]
    GET --> RESOLVE_AGENT[agentResolver(task.Title)<br/>决定由哪个 Agent 执行]
    RESOLVE_AGENT --> CLAIM[TaskManager.Claim<br/>标记为 InProgress]

    CLAIM --> SUBMIT_BG[BackgroundManager.Submit]

    subgraph BackgroundManager
        SUBMIT_BG --> SLOT{有空闲 slot?}
        SLOT -->|否| WAIT[等待 slot]
        SLOT -->|是| GOROUTINE[启动 goroutine]
        GOROUTINE --> RUNNER[runner(ctx, agent, task)]
        RUNNER -->|成功| MARK_DONE[TaskManager.Update → Completed]
        RUNNER -->|失败| MARK_RETRY[TaskManager.Update → Pending]
    end

    style EXEC_NEXT fill:#2d8cf0,color:#fff
```

### 10.3 CronScheduler 定时调度

```mermaid
flowchart TD
    START([CronScheduler.Start]) --> LOAD_JOBS[加载 cron_jobs.json]
    LOAD_JOBS --> REG_CRON[注册到 robfig/cron]
    REG_CRON --> TICK[Cron 引擎定时触发]

    TICK --> HANDLER{handler 配置?}
    HANDLER -->|nil| NOOP[无操作日志]
    HANDLER -->|有| CALLBACK[调用 handler(job)]

    STOP([CronScheduler.Stop]) --> CRON_STOP[cron.Stop 停止调度]
```

---

## 11. Reflection 反思引擎

### 11.1 三阶段反思循环

```mermaid
flowchart TD
    RUN([RunWithReflection]) --> P1{Phase 1: 前瞻反思<br/>enableProspect?}

    P1 -->|是| SEARCH_MEM[ReflectionMemory.SearchRelevant<br/>查找相关历史教训]
    SEARCH_MEM --> CRITIQUE[Prospector.Critique<br/>预判风险]
    CRITIQUE --> ENRICH_INPUT[enrichInput<br/>注入 [Prospective guidance]<br/>+ [Lessons from past attempts]]
    ENRICH_INPUT --> P2

    P1 -->|否| P2

    P2[Phase 2: 执行 + 评估] --> AGENT_RUN[Agent.Run<br/>执行实际任务]
    AGENT_RUN --> EVALUATE[Evaluator.Evaluate<br/>打分 + 判断 Pass/Fail]

    EVALUATE --> PASS{评估通过?}

    PASS -->|是 且 attempt > 0| STORE_SUCCESS[存储 Macro 成功经验]
    PASS -->|是| RETURN_OUTPUT([返回输出])
    STORE_SUCCESS --> RETURN_OUTPUT

    PASS -->|否| P3[Phase 3: 回顾反思]
    P3 --> REFLECT[Reflector.Reflect<br/>分析失败模式]
    REFLECT --> STORE_REF[ReflectionMemory.Store<br/>持久化反思]
    STORE_REF --> ENRICH_REF[enrichWithReflection<br/>注入 [Self-reflection] 到下轮输入]
    ENRICH_REF --> ATTEMPT_CHECK{attempt < maxAttempts?}
    ATTEMPT_CHECK -->|是| P2
    ATTEMPT_CHECK -->|否| EXHAUST([返回最后一次输出<br/>warn: exhausted])

    style RUN fill:#2d8cf0,color:#fff
    style RETURN_OUTPUT fill:#19be6b,color:#fff
    style EXHAUST fill:#ff9900,color:#fff
```

### 11.2 反思记忆层级

```mermaid
graph TD
    subgraph Micro 微观
        M1[单步工具调用错误]
        M2[参数格式纠正]
    end
    subgraph Meso 中观
        M3[任务级策略偏差]
        M4[多步执行路径优化]
    end
    subgraph Macro 宏观
        M5[跨任务模式识别]
        M6[长期能力改进]
    end

    M1 --> M3
    M2 --> M3
    M3 --> M5
    M4 --> M6
```

---

## 12. Observability 可观测性

### 12.1 三支柱架构

```mermaid
flowchart LR
    OBS([observability.New]) --> LOG[日志 Logger<br/>Info / Warn / Error]
    OBS --> TRACE[Tracer<br/>内存 Span 追踪]
    OBS --> METRICS[MetricsCollector<br/>计数器 + 直方图]

    subgraph CallbackHandler
        CB_LLM_START[OnLLMStart]
        CB_LLM_END[OnLLMEnd]
        CB_TOOL_START[OnToolStart]
        CB_TOOL_END[OnToolEnd]
    end

    CB_LLM_START --> LOG
    CB_LLM_START --> TRACE
    CB_LLM_START --> METRICS
    CB_LLM_END --> LOG
    CB_TOOL_END --> METRICS
```

### 12.2 AgentLoop Hook 集成

```mermaid
sequenceDiagram
    participant Loop as AgentLoop
    participant Hooks as LoopHooks
    participant OBS as Observability

    Loop->>Hooks: PreAPI(state, iter)
    Hooks->>OBS: Tracer.StartSpan("llm_call")
    Loop->>Loop: model.Generate(...)
    Loop->>Hooks: PostAPI(state, resp, iter)
    Hooks->>OBS: Tracer.EndSpan + Metrics.RecordLatency

    Loop->>Hooks: PreTool(state, toolCall)
    Hooks->>OBS: Tracer.StartSpan("tool_exec")
    Loop->>Loop: handler(ctx, args)
    Loop->>Hooks: PostTool(state, result, err)
    Hooks->>OBS: Tracer.EndSpan + Metrics.IncCounter
```

---

## 13. 模块间依赖总览

```mermaid
graph TD
    MAIN[cmd/nexus/main] --> GW[gateway]
    MAIN --> TEAM[team]
    MAIN --> AGENTS[agents/*]
    MAIN --> CORE[core]
    MAIN --> TOOL_PKG[tool]
    MAIN --> MCP_PKG[tool/mcp]
    MAIN --> MEM[memory]
    MAIN --> RAG_PKG[rag]
    MAIN --> PLAN[planning]
    MAIN --> INTEL[intelligence]
    MAIN --> PERM[permission]
    MAIN --> OBS_PKG[observability]

    GW -->|Supervisor 接口| TEAM
    TEAM -->|Lead/Teammate/Delegate| CORE
    TEAM -->|AgentTemplate| AGENTS
    TEAM -->|ScanClaimable| PLAN
    TEAM -->|MessageBus + Roster| TEAM_FS[.team/ 目录]

    AGENTS --> CORE
    AGENTS -->|knowledge| RAG_PKG
    AGENTS -->|planner| PLAN

    CORE --> TYPES[pkg/types]
    CORE --> UTILS[pkg/utils]
    CORE -->|PermPipeline 接口| PERM
    CORE -->|ToolRegistry 接口| TOOL_PKG

    INTEL --> SKILL_FILES[workspace/skills/]

    TOOL_PKG --> BUILTIN[tool/builtin]
    BUILTIN --> INTEL

    style MAIN fill:#2d8cf0,color:#fff
    style TEAM fill:#7b68ee,color:#fff
    style CORE fill:#515a6e,color:#fff
    style TYPES fill:#515a6e,color:#fff
```

### 数据流向总结

| 方向 | 路径 | 数据 |
|------|------|------|
| 请求入口 | Client → Gateway → Lane → team.Manager → Lead | 用户输入 |
| Lead 直接处理 | Lead → AgentLoop → ChatModel ↔ Tool(Registry + Permission) | ReAct 循环 |
| 委派任务 | Lead → delegate_task → 临时 Agent（隔离上下文）→ 结果返回 | 一次性结果 |
| 异步协作 | Lead → MessageBus → Teammate inbox → workPhase → reply → Lead inbox | JSONL 消息 |
| 自治调度 | Teammate idlePhase → ScanClaimable → Claim → workPhase | DAG 任务认领 |
| 知识检索 | Agent tool → RAG Engine → VectorStore + KeywordStore → Reranker | 检索上下文 |
| 记忆注入 | Memory Manager → PromptAssembler → System Prompt | 历史摘要 + 语义记忆 |
| 技能加载 | SkillManager → PromptAssembler / load_skill tool | 技能文本 |
| 任务调度 | Planner Agent → TaskManager → PlanExecutor → BackgroundManager | DAG 任务 |
| 反思改进 | Reflection Engine → Prospector / Evaluator / Reflector → Memory | 经验教训 |
| 可观测 | LoopHooks → CallbackHandler → Logger + Tracer + Metrics | 日志/指标/链路 |

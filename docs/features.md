# Nexus 全特性速览

本文档汇总 Nexus 项目的所有特性，每条简短说明，方便快速了解项目全貌。

---

## 1. Agent 核心引擎 (`internal/core/`)

### 1.1 ReAct 循环
用户消息 → LLM 生成 → 如有 tool_calls 则执行工具 → 工具结果回灌 → 再次 LLM，直到模型不再调用工具或达到迭代上限（默认 20 轮）。实现在 `AgentLoop.RunLoop`。

### 1.2 LoopState 状态机
7 个阶段：`idle → running → tool_execution → compacting → recovering → completed → error`。`TransitionTo` 强制校验合法迁移边，防止非法状态跳跃。是**单次对话回合级**状态机，不是业务工作流。

### 1.3 ContextGuard 三层上下文压缩
按顺序尝试三种压缩策略，保证对话不超出模型 token 上限：
- **Micro**：超大工具输出落盘为文件，消息中替换为短标记，零 LLM 调用。
- **Auto**：到达软阈值（~85%）时，对旧消息调用 LLM 生成摘要，保留最近窗口。
- **Manual**：到达硬阈值时，从尾部贪心保留消息，激进截断保命。

### 1.4 三层恢复洋葱
- **Layer 1（工具级）**：工具执行报错不吞掉，包装为 `tool_result{IsError:true}` 回灌模型，让 LLM 自纠。
- **Layer 2（Context 级）**：检测 provider 的 context overflow 错误，触发强制压缩。
- **Layer 3（传输级）**：对超时、429、502/503/504 等可重试错误做指数退避 + 抖动 + 总预算控制。

### 1.5 LoopHooks 四钩子
`PreAPI` / `PostAPI`（包裹每次 LLM 调用）+ `PreTool` / `PostTool`（包裹每次工具调用），可接入审计日志、指标打点或参数改写。

### 1.6 ChatModel 接口解耦
核心循环通过 `ChatModel` 接口调用模型，与 Eino/OpenAI 等具体实现解耦，便于测试桩替换和独立编译。

---

## 2. 团队协作调度层 (`internal/team/`) ★

### 2.1 Lead-Teammate 架构
`Lead` 通过 Go embedding 嵌入 `*Teammate`，共享 ReAct 工具循环、收件箱、名册等基础设施。Lead 通过 `requestCh` channel 提供同步 RPC 接口对接网关（`HandleRequest` 阻塞直到响应）。Teammate 是独立 goroutine，通过 inbox 异步接收消息，具有完整的 `work→idle→work→shutdown` 生命周期。

### 2.2 三种 Dispatch 路径（模型自主决定）
- **Lead 直接处理**：简单任务，Lead 使用自身 base tools（文件/Shell/搜索等）。
- **`delegate_task` 临时委派**：在干净空白的消息列表上运行角色模板，执行完返回结果、状态即销毁——上下文隔离的一次性专家任务（最大 30 轮迭代）。
- **`send_message` + `spawn_teammate`**：发送消息给持久 Teammate（或先 spawn），Teammate 在独立 goroutine 中处理，结果通过 inbox 异步返回——累积对话历史，长期协作。

### 2.3 JSONL 邮箱总线（MessageBus）
每个 Agent 一个 `inbox/{name}.jsonl` 文件。`Send` 以 append 追加一行 JSON；`ReadInbox` 读取全文后立即 truncate（at-most-once 投递语义）。`Broadcast` 发送给所有活跃成员（除 sender）。零外部依赖，天然支持崩溃恢复与审计。

### 2.4 消息信封与协议分层
6 种消息类型：业务消息（`message`/`broadcast`）+ 协议消息（`shutdown_request`/`shutdown_response`/`plan_approval`/`plan_approval_response`）。`RequestTracker` 为每个协议请求创建磁盘记录，支持关联请求/响应的状态流转。

### 2.5 名册持久化与进程重启恢复（Roster + Rehydrate）
`config.json` 记录所有成员的 `(name, role, status)`，并进一步补充 `activity`、`claimed_task_id`、`updated_at` 等治理字段。进程重启时 `rehydrate()` 自动 Spawn 之前活跃（非 shutdown）的成员，注入 resume prompt 恢复团队阵容；若发现持久化状态里残留 `working`，会先重置为可恢复的 idle/waiting 语义，再重新拉起 worker。

### 2.6 自治调度引擎（Autonomy）
Teammate 空闲时按优先级轮询：**收件箱消息 > 任务板自动认领（`ScanClaimable` + `Claim` 竞态安全） > 超时自动关闭**。当前 claim 不再只是“谁先抢到谁做”，还会检查 `assigned_to`、`assigned_role`、`claim_role` 等调度约束；因此 task board 开始真正服从前置 dispatch intent，而不是自由抢单。唤醒后 `ensureIdentity()` 注入身份上下文防止漂移。`ClaimLogger` 记录 JSONL 审计日志，并保留手动/自动认领来源与角色信息。

### 2.7 工具权限分层
所有 Teammate 共享团队工具（`send_message`/`read_inbox`/`list_teammates`/`assign_task`/`claim_task`/`submit_plan`）；Lead 额外拥有管理工具（`spawn_teammate`/`shutdown_teammate`/`broadcast`/`review_plan`/`delegate_task`）+ base tools（文件/Shell/搜索）。`assign_task` 的加入意味着 team routing 不再只停留在 lead 的短期决策，而是可以正式落到 task board。

### 2.8 角色模板注册
`RegisterTemplate(role, AgentTemplate)` 注册角色模板（system prompt + tools），由现有 4 个专业 Agent 提取。`spawn_teammate` 和 `delegate_task` 均基于模板创建。

### 2.9 Scope / Workstream Continuity
Team 不再只按当前进程里的单个 manager 运转，而是通过 `Registry` 按 `scope/workstream` 复用不同团队。新请求既可以在建 session 时显式指定 `scope` / `workstream`，也可以在后续 continuation 请求中基于 continuation cue、最近工作线摘要、关键词重合和阈值打分命中旧 team。每条工作线拥有独立的 team 目录、semantic memory、reflection memory 和持久化索引，因此不仅支持跨 session 续接，也支持服务重启后的 continuation 恢复。

### 2.10 Team Scope 管理机制
在 continuity 之上，当前版本进一步补齐了 Team 管理层：
- 区分 `team.dir` 与 `scope`：根目录下多个 `.team-*` 通常代表不同实验或运行配置，而不是单个 scope 创建了多个 Team。
- 单个 `team.dir` 内，scope 状态统一组织到 `index/scopes.json` 和 `scopes/<scope_kind>/<bucket>/<slug>/`，避免所有 Team 平铺在一个目录。
- 新增 `team.scope_manager_ttl`，允许长时间未访问的 scope manager 从内存中自动驱逐，但保留磁盘状态并支持按需 rehydrate。
- `GET /api/debug/scopes` 增加 `scope_kind`、`storage_bucket`、`lifecycle`、`manager_running`、`manager_last_used_at`、`team_dir` 等字段，使 Team 管理从“看目录猜状态”升级为可观测运行时。
- 详见 `docs/team_scope_management.md`。

---

## 3. 四个专业 Agent (`internal/agents/`)

### 3.1 Knowledge Agent
RAG 增强的知识问答 Agent。先调用 `Engine.Query` 检索文档上下文，拼入用户问题后进入 ReAct 循环。挂载 `search_knowledge`、`ingest_document` 等工具。

### 3.2 CodeReviewer Agent
代码审查 Agent。分析代码质量、潜在 bug、最佳实践建议。可配合文件读写工具直接操作代码库。

### 3.3 DevOps Agent
运维 Agent。执行 Shell 命令、管理部署、检查系统状态。Shell 工具带超时和危险命令过滤。

### 3.4 Planner Agent
任务规划 Agent。通过 `create_plan`、`update_task`、`list_tasks`、`execute_task` 等工具操作 TaskDAG，拆解复杂目标为可执行子任务。

---

## 4. 工具系统 (`internal/tool/`)

### 4.1 ToolRegistry 字典派发
`map[string]*ToolMeta` 实现 O(1) 工具查找。Agent 本地工具优先，未命中回退全局 Registry。新工具只需注册 name + JSON Schema + handler，无需类继承。

### 4.2 内置工具集（20+ 种）
- **文件类**：`read_file`、`write_file`、`edit_file`、`list_dir`
- **搜索类**：`grep_search`、`glob_search`
- **执行类**：`run_shell`（带超时 + 危险命令过滤）
- **网络类**：`http_request`
- **技能类**：`list_skills`、`load_skill`（两阶段技能系统的运行时入口）
- **各 Agent 领域工具**：`search_knowledge`、`ingest_document`、`create_plan`、`update_task`、`list_tasks`、`execute_task` 等

### 4.3 大块输出落盘
工具返回结果若估计 token 超阈值（默认 ~51200），自动写入 `.outputs/` 目录，消息中替换为 `[large_output_persisted ...]` 短标记，防止撑爆上下文。

---

## 5. MCP 协议 (`internal/tool/mcp/`)

### 5.1 JSON-RPC 2.0 协议
实现标准方法：`initialize`、`notifications/initialized`、`ping`、`tools/list`、`tools/call`。预留 `resources/list`、`prompts/list`。包含标准错误码 + MCP 扩展错误码。

### 5.2 MCP Client 自动发现
`Initialize` 握手 → `ListTools` 拉取远端工具描述 → 自动转为 `ToolMeta` 注册到本地 Registry。调用时 handler 内部透明转发 RPC。

当前运行时默认通过 `mcp.clients` 配置走 HTTP/SSE 接入外部 MCP 服务；Transport 抽象仍保留 stdio 支持，但产品化主链默认不是子进程方式。

### 5.3 MCP Server
将 Nexus 已有工具暴露为 MCP 工具描述与调用端点，供外部系统接入。SSE 端点带 30 秒 ping 心跳保活。当前网关默认挂载 `/mcp/rpc` 与 `/mcp/sse`。

### 5.4 双传输支持
支持 stdio（子进程通信）和 HTTP/SSE 两种传输方式。`Transport` 接口抽象，与协议层解耦；当前主运行链路默认使用 HTTP/SSE。

### 5.5 连接管理器
`Manager` 聚合多个 MCP 连接的生命周期，进程退出时 `CloseAll` 统一释放。

---

## 6. RAG 系统 (`internal/rag/`)

### 6.1 摄入流水线
`load → chunk → embed → index` 四阶段。支持单文件和目录批量摄入。每个 chunk 同时写入向量库和关键词索引。

### 6.2 文档加载器
`FileLoader`（单文件）+ `DirectoryLoader`（批量目录）。支持 Markdown 等格式。缺失的 loader 不阻塞引擎创建。

### 6.3 递归分块器
`RecursiveChunker` 按分隔符层级（`\n\n` → `\n` → `. ` → 空格）递归切分，带 overlap 窗口防止边界截断。Chunk 携带 source、doc_id、chunk_id 元数据。

### 6.4 Embedding
`Embedder` 接口可插拔。默认 `HashEmbedder`（SHA-256 种子 PRNG + L2 归一化）用于无外部模型的确定性测试；生产替换为真实 embedding 服务。

### 6.5 向量存储双后端
- **MemoryVectorStore**：进程内暴力余弦相似度，零依赖，适合开发/小语料（<10 万向量）。
- **MilvusVectorStore**：连接 Milvus，支持 IVF_FLAT / IVF_SQ8 / HNSW 索引。带 Transport 抽象（可 mock）、Lazy 初始化（sync.Once）、Upsert 语义（先删后插）、metadata JSON 序列化、指数退避重试、Count 缓存（atomic.Int64）。

通过 `vector_backend: "memory"` 或 `"milvus"` 配置切换。

### 6.6 关键词存储双后端
- **KeywordIndex（内存 TF-IDF）**：进程内倒排索引，`map[term]map[chunkID]tf`，零依赖。
- **ESKeywordStore（Elasticsearch BM25）**：连接 ES 集群，支持自定义 BM25 参数（k1/b）、自动建索引、纯 net/http 调用无第三方依赖、Basic Auth。

通过 `keyword_backend: "memory"` 或 `"elasticsearch"` 配置切换。

### 6.7 多通道并行检索
向量检索与关键词检索通过 goroutine 并行执行，`WaitGroup` 汇合后合并结果。

### 6.8 RRF 融合
Reciprocal Rank Fusion（k=60）合并两路排名。只依赖排序秩，对异构分数量纲鲁棒，无需调参。

### 6.9 CompositeReranker 精排
`KeywordBoostReranker` + `CrossEncoderReranker` 串联。先用规则/轻量信号拉近相关片段，再用交叉编码器（如配置）精排。可选 `ScoreFn` 外接托管模型。

### 6.10 后处理链 PostChain
`DeduplicateProcessor`（Jaccard ≥ 0.92 近似去重）→ `ScoreNormalizer`（min-max 归一化到 [0,1]）→ `ContextEnricher`（用 chunk 元数据补充展示文本）→ 截断到最终 topK。

### 6.11 源文档删除
`DeleteBySource` 按 `docChunks` 映射批量清理向量库和关键词索引中对应的 chunk。

---

## 7. 记忆系统 (`internal/memory/`)

### 7.1 ConversationMemory 会话记忆
滑动窗口保留最近 N 条消息。超出时 `Compact(summarizer)` 对旧消息生成 LLM 摘要，追加到摘要列表。`EstimateTokens` 本地估算 token 用量。

### 7.2 SemanticStore 语义记忆
跨会话的长期事实存储，分四类：`project` / `preference` / `feedback` / `reference`。YAML 文件持久化，可读可 diff 可手工编辑。`maxEntries` 超出删最旧。`ToPromptSection` 生成可注入系统提示的 Markdown 块。

### 7.3 三种压缩策略
- **WindowCompaction**：直接截断只保留最后 N 条，无摘要。
- **SummaryCompaction**：调用 LLM 摘要旧消息。
- **AggressiveCompaction**：极端压力下只保留最后 3 条消息。

### 7.4 Memory Manager
编排会话记忆与语义记忆的协调管理。

---

## 8. 三阶反思引擎 (`internal/reflection/`)

### 8.1 前瞻反思（Phase 1 — Prospector）
执行前从 `ReflectionMemory` 检索历史教训 → 调用 LLM 生成潜在风险和建议 → 将指导段落前置注入输入。灵感来自 PreFlect（ICML 2026）。与权限沙箱形成**双层防护**。

### 8.2 执行 + 评估（Phase 2 — Evaluator）
Agent 执行后做多维度结构化评分：correctness、completeness、safety、coherence。score ≥ 阈值（默认 0.7）通过。评估失败时安全降级为通过。

### 8.3 回顾反思（Phase 3 — Reflector）
评估不通过时分析失败原因，分三个粒度级别（来自 SAMULE, EMNLP 2025）：
- **Micro**：单次轨迹的具体错误（优先淘汰）
- **Meso**：同类型任务的反复出现模式
- **Macro**：跨任务类型的可迁移洞察（最后淘汰）

### 8.4 ReflectionMemory 反思记忆
YAML 持久化。Jaccard 关键词相似度检索历史反思。淘汰时按 macro > meso > micro 优先级保留高层洞察。`ToPromptSection` 注入系统提示。

### 8.5 低成本设计
最坏情况（3 次尝试全失败）仅 6 次额外 LLM 调用。可通过 `enabled: false` 完全关闭。

---

## 9. 规划系统 (`internal/planning/`)

### 9.1 TaskDAG 任务图
每任务一个 `task_<id>.json` 文件 + `meta.json` 记录 next_id。`blocked_by` / `blocks` 双向索引表达依赖。5 种状态：`pending` / `in_progress` / `completed` / `blocked` / `cancelled`。除依赖与状态外，任务还会持久化 dispatch/claim 治理字段，例如 `claim_role`、`assigned_to`、`assigned_role`、`assigned_by`、`assign_reason`、`claimed_role` 与 `last_claim_*` 审计链。

### 9.2 DFS 环检测
创建依赖前在临时图上做 DFS，检测回边防止循环依赖。`O(V+E)` 复杂度。

### 9.3 任务认领（Claim）
`Claim(id, agentName, agentRole, source)` 将可运行任务占为 `in_progress` 并记录执行者，但现在不仅校验“任务是否可运行”，还会进一步校验：
- `assigned_to` 是否与 claimer 一致
- `assigned_role` / `claim_role` 是否与 claimer role 一致
- 当前 claim 是否来自手动或自动路径

这使 claim 从简单互斥锁升级成“遵循 dispatch intent 的认领门禁”。

### 9.4 下游自动解锁
任务完成时 `resolveDownstreamLocked` 自动将满足条件的下游任务从 `blocked` 解为 `pending`。

### 9.4.1 任务指派（Assign）
`Assign(id, assignedBy, assignee, role, reason)` 支持在 claim 之前先把任务路由给具体 teammate 或 specialist role。planner 现在可以在 `create_plan` 阶段直接写 `claim_role` / `assigned_to`，lead 或 worker 也可以通过 `assign_task` tool 在 task 已落盘后再做二次指派。

### 9.5 PlanExecutor
从 TaskManager 取可认领任务 → Claim → 提交到 BackgroundManager 的槽位执行 → 成功则标记完成，失败则回滚为 pending。

### 9.6 CronScheduler 定时调度
基于 `robfig/cron/v3`，标准 5 字段 cron 表达式。任务定义持久化到 `cron_jobs.json`，重启可恢复。两种类型：`agent_turn`（触发对话）和 `system_event`（系统事件）。支持运行时动态增删 Job，带回滚机制。

**周期/一次性双模式**：`OneShot` 字段控制——默认 `false` 周期执行，`true` 则触发一次后自动从调度器和磁盘删除。适用于一次性迁移、定时发布等场景。

**执行结果持久化**：每次触发的 Agent 完整输出写入 `.tasks/cron_results/<job_name>/<timestamp>.json`（含 payload、时间戳、result、error），可回溯历次执行记录。

**Supervisor 统一路由**：cron handler 统一调用 `Supervisor.HandleRequest`，与用户 HTTP/WS 请求走相同的意图路由路径，由 IntentRouter 根据 Payload 内容自动选择目标 Agent。

### 9.7 BackgroundManager 后台槽位
`maxSlots` 限制并发。每个任务一个 goroutine，槽位记录 Cancel、Done、结果与错误。管「怎么跑」，与 TaskDAG 的「做什么」分离。

---

## 10. 网关层 (`internal/gateway/`)

### 10.1 多通道归一化
HTTP（健康检查、建会话、同步 Chat）与 WebSocket（流式交互）统一为对 `Supervisor.HandleRequest` 的调用。

### 10.2 SessionManager 会话管理
维护 channel / user 元数据与 TTL（默认 24h）。生成或复用 sessionID，串联 Gateway 与 Supervisor。当前 session 还可携带 `scope` / `workstream` 元数据，用于显式绑定长期工作线；若调用方不显式提供，下游 `ScopedSupervisor` 也可以基于 continuation 请求把新 session 解析并绑定到已有 scope。

### 10.3 BindingRouter 五档绑定路由
按渠道和用户的具体程度分 5 个 Tier：
- Tier 1：channel 具体 + user 具体（最精确）
- Tier 2：channel 具体 + user 通配
- Tier 3：channel 具体 + user 空
- Tier 4：channel 通配 + user 具体
- Tier 5：全局默认（最宽泛）

同 Tier 内 priority 大的优先，first match wins。支持 VIP 用户专用 Agent、渠道默认模型、全局 fallback。

### 10.4 Named Lane 语义隔离
`main` / `cron` / `background` 各自独立 FIFO 队列 + 独立并发度。`main` lane max=1 保证用户消息串行，`cron` lane max=1 防重入。

### 10.5 Generation 代际防陈旧
`Reset(lane)` 递增 generation 原子计数器并清空队列。任务执行前比对入队时 generation，不一致则丢弃（`ErrStaleLane`）。防止会话重置后旧请求在新策略下执行，类比 epoch/fencing token。

### 10.6 中间件栈
- **Trace 中间件**：注入 LogID/TraceID（已实现）
- **Auth 中间件**：已接入主链，支持 `X-API-Key` 与 `Authorization: Bearer <jwt>`
- **RateLimiter 中间件**：已接入主链，per-IP 令牌桶限流，支持突发，空桶返回 429
- **Public Debug/Health 例外**：`/api/health`、`/debug/dashboard`、`/api/debug/*` 默认免鉴权，便于本地排障与浏览器直接查看

---

## 11. 权限系统 (`internal/permission/`)

### 11.1 四阶段权限管道
按固定顺序求值：
1. **Deny 规则**：最高优先级，匹配即拒绝（full_auto 也不可绕过）
2. **路径沙箱**：校验路径在 WorkspaceRoot 内 + Shell 命令危险模式过滤
3. **模式门控**：`full_auto` 直接放行；`manual` 一律询问；`semi_auto` 进白名单
4. **Allow + 默认 Ask**：`semi_auto` 下匹配白名单放行，否则询问用户

### 11.2 PathSandbox
解析工具参数中的路径字段，禁止 `../` 逃逸出工作区。与工具实现解耦，策略集中在权限层。

### 11.3 危险命令过滤
可配置的 `DangerousPatterns` 列表（默认含 `rm -rf /`、`sudo`、`chmod 777` 等），对 Shell 命令做模式匹配阻断。

### 11.4 与 ReAct 循环集成
工具调用前先经过权限管道评估。拒绝信息回灌模型，形成可观测的拒绝原因链。

---

## 12. 智能组装 (`internal/intelligence/`)

### 12.1 Bootstrap 文件加载
按顺序加载 `SOUL.md` → `IDENTITY.md` → `TOOLS.md` → `MEMORY.md`。每段带 `<<< SECTION: ... >>>` 标记。单文件上限 20k 字符，总上限 150k 字符。缺失文件视为空串不报错。路径穿越校验保证只读 workspace 下文件。

### 12.2 两阶段技能加载（需求分页）
- **阶段 1（启动时索引）**：`main.go` 启动时调用 `SkillManager.ScanSkills()` 扫描 `workspace/skills/*/SKILL.md` 的 YAML frontmatter（name、description、invocation），构建轻量索引。索引通过 `SkillIndex` 接口注入 `BaseAgent`，`getSystem()` 自动将技能目录追加到每个 Agent 的系统提示中。
- **阶段 2（运行时按需加载）**：Agent 在 ReAct 循环中通过 `load_skill` 工具按名加载完整正文，`SkillManager` 缓存已加载的正文避免重复 IO。`list_skills` 工具供 Agent 发现可用技能。

类比 OS 按需分页：先驻留「目录」（系统提示中的技能索引），按需换入「页」（`load_skill` 工具调用）。

### 12.3 技能工具集成
- **`list_skills`**：列出所有已索引技能的 name 和 description，Agent 用于发现可用技能。
- **`load_skill`**：按 name 加载技能全文正文（去掉 YAML frontmatter），内容作为 tool_result 进入对话上下文，指导 Agent 执行领域特定任务。
- 技能工具注册在全局 `ToolRegistry`，通过 `attachRegistryTools` 挂载到每个 Agent。
- 内置示例技能：`go-review`（Go 代码审查清单）、`security-audit`（安全审计指南）、`api-design`（API 设计原则）。

### 12.4 PromptAssembler 与预算
组装顺序：Bootstrap 文本 → Skill Index → Memory 段。分段预算：Bootstrap 150k / SkillIndex 30k / Memory 20k / 总量 200k（字符级）。超预算截断，保证硬上限。

---

## 13. 可观测性 (`internal/observability/`)

### 13.1 Eino 风格回调
`CallbackHandler` 提供 `OnStart` / `OnEnd` / `OnError` / `OnToolStart` / `OnToolEnd` / `OnLLMStart` / `OnLLMEnd`。与 CloudWeGo Eino 生态的生命周期钩子对齐。

### 13.2 内存 Span 追踪
`Tracer` 在内存 map 存储 Span 树。从 context 继承 traceID 与父 spanID。`GetTrace` 按开始时间排序。可替换为 OTLP 导出对接 OpenTelemetry。

### 13.3 直方图指标
`MetricsCollector` 维护计数器 + 预定义桶的 Histogram：`callback_duration_seconds`、`tool_duration_seconds`、`llm_duration_seconds` 等。`Snapshot` 供调试端点或日志导出。可桥接到 Prometheus。

### 13.4 Run 级调试视图

- `GET /debug/dashboard?run=<run_id>`：本地 HTML 治理页
- `GET /api/debug/metrics?run=<run_id>`：全局或指定 run 的 metrics 快照
- `GET /api/debug/scopes`：当前 scope/workstream 索引与摘要
- `GET /api/debug/traces?run=<run_id>`：run 级 trace 摘要
- `GET /api/debug/traces/{id}`：单条 trace 的树结构、分组耗时、错误摘要、scope decision 细节
- `.runs/<run_id>/latest-traces.json`：请求结束后自动刷新，便于离线分析

Dashboard 不只展示 trace/metrics，还会展示 scopes 卡片、trace 列表中的 scope decision 摘要，以及 trace detail 中的 `score / threshold / candidates`，用于直接判断 continuation 是否命中了正确工作线、匹配质量是否足够高。随着 roster 增加 `activity` / `claimed_task_id` 和 task 增加 assignment metadata，Team 治理面的可解释性也显著增强。

### 13.5 LLM 稳定性节流

- `model.max_concurrency`：进程级并发上限
- `model.min_request_interval_ms`：全局最小请求间隔
- 两者共同约束 Lead、Teammate、Reflection、Delegate 等共享 ChatModel 的所有调用路径，用于抑制供应商 429

---

## 14. 配置系统 (`configs/`)

### 14.1 YAML 配置
单文件 `default.yaml` 涵盖所有模块配置。支持环境变量覆盖（如 `${NEXUS_API_KEY}`）。

### 14.2 分模块配置结构
- **Server**：HTTP/WS 地址、读写超时
- **Model**：provider、model_name、api_key、base_url、max_tokens、temperature
- **Agent**：max_iterations、token 阈值、压缩比、输出持久化目录
- **RAG**：chunk 参数、embedding 维度、topK、向量/关键词后端选择、ES/Milvus 连接配置
- **Memory**：会话窗口大小、语义记忆条目上限、压缩阈值
- **Planning**：任务目录、后台槽位数、cron 轮询间隔
- **Gateway**：各 Lane 的并发度
- **Permission**：运行模式、工作区根、危险命令列表
- **Reflection**：开关、最大尝试次数、评估阈值、记忆文件路径
- **Observability**：trace/metrics 开关、日志级别

---

## 15. 公共类型 (`pkg/types/`)

### 15.1 统一消息类型
`Message` / `Role`（user、assistant、tool、system）跨包共享，避免各模块重复定义。与 OpenAI Chat Completions 格式对齐。

### 15.2 工具类型
`ToolDefinition`（JSON Schema 描述）/ `ToolCall`（调用请求）/ `ToolResult`（执行结果）/ `ToolMeta`（name + schema + handler 三合一）。

---

## 16. 工程质量

### 16.1 依赖反转
`AgentDependencies` 作为组合根，在 `main` 中构造并注入各 Agent。`core` 包不 import 具体子系统，通过接口（`ChatModel`、`PermPipeline`、`Observer`）和 `any` 字段降低耦合。

### 16.2 模块依赖方向约束
自上而下：`cmd → gateway → orchestrator → core → pkg`。各子系统（rag/tool/memory/planning/permission/intelligence/observability）只依赖 pkg 和 configs，不互相引用。

### 16.3 测试友好
- `HashEmbedder` 确定性 embedding，无网络依赖
- `MemoryVectorStore` 零外部依赖
- 接口桩：`ChatModel`、`PermPipeline`、`Observer`、`MilvusTransport` 均可注入 fake
- 现有测试覆盖：`router_test.go`、`auth_test.go`、`pipeline_test.go`

### 16.4 文件持久化
任务 DAG、语义记忆、反思记忆、Cron 任务均用 JSON/YAML 文件持久化。单二进制 + 数据目录即可启动，零数据库依赖。可 `cat`/`jq` 直接排查。

---

## 17. 代码规模

| 维度 | 数量 |
|------|------|
| Go 源码 | ~14,000+ 行 |
| 文档 | ~3,000+ 行 |
| 核心模块 | 12 个 |
| 工具实现 | 18+ 种 |
| 内部 .go 文件 | 77 个 |

---

## 18. 技术栈

| 组件 | 技术选型 |
|------|----------|
| 语言 | Go 1.22+ |
| Agent 框架 | CloudWeGo Eino（接口对齐） |
| WebSocket | gorilla/websocket |
| Cron | robfig/cron/v3 |
| 配置 | YAML (gopkg.in/yaml.v3) |
| 向量存储 | 内存 / Milvus |
| 关键词存储 | 内存 TF-IDF / Elasticsearch BM25 |

---

## 特性总览表

| # | 特性 | 一句话描述 |
|---|------|-----------|
| 1 | ReAct 循环 | 模型推理 + 工具执行的迭代主循环 |
| 2 | LoopState 状态机 | 7 阶段 + 合法迁移校验 |
| 3 | 三层上下文压缩 | Micro 落盘 / Auto 摘要 / Manual 截断 |
| 4 | 三层恢复洋葱 | 工具自纠 / Context 压缩 / 传输退避重试 |
| 5 | LoopHooks | PreAPI/PostAPI/PreTool/PostTool 四钩子 |
| 6 | Lead-Teammate 团队调度 | Lead 嵌入 Teammate + 持久异步协作 + 临时 Delegate |
| 7 | JSONL 邮箱总线 | 每 Agent 独立收件箱，at-most-once 投递 |
| 8 | 自治调度引擎 | 收件箱 > 任务认领 > 超时关闭，且 claim 受 assignment / role 约束 |
| 9 | 角色模板 + 权限分层 | 动态注册模板，Lead-only vs 共享工具精确隔离，含 `assign_task` |
| 10 | 4 个角色模板 | Knowledge / CodeReviewer / DevOps / Planner |
| 11 | ToolRegistry 字典派发 | O(1) 按名查找 + 动态注册 |
| 12 | 18+ 内置工具 | 文件/搜索/Shell/HTTP/领域工具 |
| 13 | MCP Client/Server | JSON-RPC 2.0 + 自动工具发现 |
| 14 | 双传输 MCP | stdio + HTTP/SSE |
| 15 | RAG 摄入流水线 | load → chunk → embed → index |
| 16 | 向量存储双后端 | Memory / Milvus 可切换 |
| 17 | 关键词存储双后端 | 内存 TF-IDF / ES BM25 可切换 |
| 18 | 多通道并行检索 | 向量 + 关键词 goroutine 并发 |
| 19 | RRF 融合 | 排序秩融合，异构分数鲁棒 |
| 20 | 精排 Reranker | KeywordBoost + CrossEncoder 串联 |
| 21 | 后处理链 | 去重 → 归一化 → 上下文增强 → 截断 |
| 22 | 会话记忆 | 滑动窗口 + LLM 摘要压缩 |
| 23 | 语义记忆 | 4 分类长期事实 YAML 持久化 |
| 24 | 三种压缩策略 | Window / Summary / Aggressive |
| 25 | 前瞻反思 | 执行前检索历史教训 + LLM 风险评估 |
| 26 | 多维度评估器 | 正确性/完整性/安全性/连贯性打分 |
| 27 | 三级回顾反思 | Micro/Meso/Macro 分级失败分析 |
| 28 | 反思记忆 | YAML 持久化 + 优先级淘汰 |
| 29 | TaskDAG 任务图 | JSON 持久化 + DFS 环检测 + 依赖解锁 + assignment / claim 审计字段 |
| 30 | CronScheduler | cron 表达式定时调度 + 周期/一次性双模式 + 结果持久化 |
| 31 | BackgroundManager | 后台并发槽位管理 |
| 32 | 多通道网关 | HTTP + WebSocket 归一化 |
| 33 | 五档绑定路由 | 渠道/用户粒度 5 级匹配 |
| 34 | Named Lane | 语义隔离 + Generation 防陈旧 |
| 35 | 令牌桶限流 | per-IP 突发友好的速率限制 |
| 36 | 四阶段权限管道 | Deny → Sandbox → Mode → Allow/Ask |
| 37 | 路径沙箱 + 危险命令 | 工作区约束 + 模式匹配过滤 |
| 38 | Bootstrap 文件加载 | 4 段系统提示有序组装 |
| 39 | 两阶段技能加载 | frontmatter 索引 + 按需正文加载 + load_skill 工具 |
| 40 | 技能工具 | list_skills 发现 + load_skill 按需加载 + 系统提示注入 |
| 41 | PromptAssembler | 分段预算的提示词组装器 |
| 42 | Eino 风格回调 | 生命周期钩子全覆盖 |
| 43 | Span 追踪 | 内存 Span 树，可对接 OTLP |
| 44 | 直方图指标 | 工具/LLM/回调耗时分布统计 |
| 45 | 文件持久化 | JSON/YAML 零 DB 依赖 |
| 46 | 依赖反转 | 接口解耦 + 组合根注入 |
| 47 | Scope / Workstream Continuity | 工作线级 team 复用，支持跨 session continuation |
| 48 | Scope Index 持久化 | `scopes.json` 让 continuation 在重启后仍可恢复 |
| 49 | Scope 决策可观测 | logs / trace tags / debug scopes / dashboard 全链路解释命中原因 |
| 50 | Task Routing Governance | `assign_task` + assignment metadata + activity 状态让 dispatch / claim / roster 闭环对齐 |

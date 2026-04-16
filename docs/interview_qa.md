# Nexus — 面试问答手册

面向 Nexus 多 Agent 系统的 35 道结构化问答，按模块由浅入深。每题答案以 **Core**（一句话论点）开头，**Detail** 为可展开的要点，便于面试口述与复盘。

---

## 模块一：Agent 核心引擎（Q1–Q6）

### Q1：这个 Agent 的核心循环是什么？跟 LangChain Agent 有什么区别？

**Core：** Nexus 的 `AgentLoop` 是基于 `ChatModel`（设计上对齐 Eino 形态）的 ReAct 循环：反复调用模型，根据 `FinishReason` 与是否携带 `ToolCalls` 判定是执行工具还是结束本轮。

**Detail：**

- 单次 `RunLoop`：`NewLoopState` → 追加用户消息 → 在 `maxIter`（默认 20，可由配置覆盖）内循环。
- 每次迭代：`ContextGuard.MaybeCompact` → `PreAPI` hook → `model.Generate(system, messages, tools)` → `PostAPI` hook。
- `classifyFinishReason` / `shouldContinueWithTools`：若存在工具调用且理由归类为 `tool_use`，则进入工具阶段；否则将 assistant 文本消息写入 state 并 `PhaseCompleted` 返回。
- 工具阶段：`TransitionTo(PhaseToolExecution)` → 对每个 `ToolCall` 执行 `PreTool` → `executeToolCall`（权限、handler、可选大块输出落盘）→ `PostTool` → 追加 `role=tool` 消息 → 回到 `PhaseRunning`。
- 与 LangChain 类框架的差异：Nexus 把 `messages` 放在 `LoopState` 里显式维护，压缩、持久化、观测都直接针对同一条 transcript；不依赖高层 Chain 对 message 列表的不透明封装，便于实现 token 守卫与自定义生命周期。

---

### Q2：Tool Dispatch 为什么用「按名解析」的注册表思路，而不是经典策略模式？

**Core：** 工具规模上来后，「名字 → `ToolMeta`（含 JSON Schema + Handler）」的表驱动_dispatch 比为每个工具建一个策略类更简单、更少样板代码，且与动态注册（内置 + MCP）天然一致。

**Detail：**

- `executeToolCall` 通过 `resolveTool`：先扫当前 Agent 的本地 `tools` 列表，再回退 `deps.ToolRegistry.Get(name)`。
- 新增工具 = 实现 `Handler` + `Register` / `AddTool`，无需新建继承体系；共享依赖（`AgentDependencies`、沙箱根目录）通过闭包或构造期注入即可。
- 策略模式在十几个以上工具时往往变成「一个接口 + N 个文件 + 重复 wiring」；表驱动在 Go 里与 `map`/`Registry` 习惯一致，也利于按名做权限与指标聚合。
- **面试表述技巧：** 可说「表驱动 + 接口注入」——表负责扩展点聚合，`ToolMeta.Handler` 保持与业务策略同样的多态能力，但无强制类层次。

---

### Q3：工具有哪些？怎么保证安全性？

**Core：** 内置层覆盖文件、Shell、HTTP、搜索等；各业务 Agent 再挂载 RAG、规划、Code Review、DevOps 等工具，总数轻松达到十余个到二十个以上；安全靠工作区沙箱、危险命令过滤与权限管道协同。

**Detail：**

- **文件类（示例）：** `read_file`、`write_file`、`edit_file`、`list_dir`（路径需落在配置的工作区根下）。
- **搜索类：** `grep_search`、`glob_search`（在沙箱范围内检索）。
- **执行类：** `run_shell`（带超时与危险模式过滤）。
- **网络类：** `http_request`。
- **知识 / 规划 / 领域 Agent：** 如 `search_knowledge`、`ingest_document`、`create_plan`、`update_task`、`list_tasks`、`execute_task` 以及 Code Review / DevOps 专用工具等（以 `workspace/TOOLS.md` 与具体 Agent 注册为准）。
- **安全机制：**
  - `PathSandbox`：`ValidatePath` 将路径约束在工作区内，防止 `../` 逃逸。
  - `ValidateCommand` 对 Shell 输入做危险模式匹配（可与配置中的 `DangerousPatterns` 叠加）。
  - `permission.Pipeline`：在工具真正执行前做 deny / 沙箱 / 模式（full_auto、semi_auto、manual）下的 allow 与默认询问语义（与 `core` 侧 `PermPipeline` 接口对接）。
  - **大块输出：** 成功工具结果若估计 token 超过阈值，可落盘到 `OutputPersistDir`，transcript 中替换为 `[large_output_persisted ...]`，避免撑爆上下文。
- **工具数量口径：** 内置 7 个文件/搜索/Shell/HTTP 类工具 + 各 Agent 挂载的领域工具，文档化列表见 `workspace/TOOLS.md`；面试说「18+」强调**可组合扩展**而非固定常数。

---

### Q4：三层恢复洋葱具体怎么工作？（tool → context → transport）

**Core：** 第一层把错误变成模型可见的 `tool_result`；第二层用上下文溢出启发式触发强制压缩；第三层对瞬时故障做带抖动与预算的指数退避重试。

**Detail：**

- **Layer 1（工具）：** `WrapToolError` 将 `error` 格式化为可读文本，`IsError: true`，仍作为 tool 消息进入下一轮，模型可改参数重试。
- **Layer 2（Context）：** `IsContextOverflowError` 对常见 provider 报错字符串做匹配；命中时 `ContextGuard.ForceManualCompact`，避免死循环在「超长上下文」上。
- **Layer 3（Transport）：** `RecoveryManager.CallWithRetry` 包装 `model.Generate`：可重试错误（超时、连接重置、429、502/503/504 等）按 `InitialDelay` 倍增至 `MaxDelay`，`JitterFraction` 防抖，`MaxRetryBudget` 限制总睡眠；进入重试时可将 `LoopState` 标为 `PhaseRecovering`。
- **设计取舍：** 不把「所有错误」都重试；取消类错误立即失败，避免无意义占用资源。
- **与产品体验：** Layer 1 让用户感知为「模型在自我纠错」而非裸 HTTP 500；Layer 3 让运维窗口内的抖动对用户近似透明。

---

### Q5：ContextGuard 怎么管理 token？三种 compaction 区别？

**Core：** 用本地 `EstimateTokens` 估算 transcript 体量，按顺序尝试 **Micro**（大块落盘）、在软阈值触发 **Auto**（窗口外摘要）、硬阈值 **Manual**（保留尾部消息的系统级裁剪）。

**Detail：**

- **阈值：** `TokenThreshold` 默认 10 万（可配置）；软限约为阈值的 85%，用于触发自动摘要。
- **Micro-compaction：** 扫描 `role=tool` 消息，单条内容超过 `MicroCompactSize`（默认约 51200 估计 token 量级）则写入工作区下输出目录，正文替换为带路径与近似 token 的短标记；代价低、无额外模型调用。
- **Auto-compaction：** 当 `SummarizeFunc` 已注入且达到软限，将「头部 system 保留 + 超出 `convWindow` 的非 system 旧消息」拼成文本调用摘要，再用一条 system 消息承载摘要，尾部保留最近窗口；适合长对话渐进收缩。
- **Manual-compaction：** 估计总量 ≥ 硬阈值时，保留前缀 system、插入 `[manual_compaction ...]` 说明，从尾部向前贪心选取消息直到估算 token ≤ `CompactTargetRatio * threshold`；偏激进，用于保命。
- **与规划状态的关系：** Task 图、语义记忆、Skill 等设计在磁盘或其它子系统，不依赖完整 message 历史，因此 aggressive compact 不会抹掉「任务 DAG」这一事实源（见 Q25）。
- **估算误差：** `EstimateTokens` 为本地启发式，与 OpenAI/Anthropic 计费 token 可能略有偏差；阈值应留安全边际，并结合 provider 返回的真实用量做离线校准。

---

### Q5.1：已经有 Auto 软阈值压缩，为什么还会出现 context overflow 错误？

**Core：** Auto/Manual 基于本地启发式估算做预检，而 Provider 使用真实 tokenizer 验证。两者存在固有偏差，加上估算未覆盖全部请求内容、单轮工具输出可能导致跳跃式增长等因素，预检可能漏网。Layer 2 是基于 Provider 真实反馈的兜底。

**Detail：**

- **本地估算 vs Provider 真实 tokenizer：** `EstimateTokens` 用字符数启发式（英文 ~4 字符/token，CJK ~2 字符/token），而 Provider 使用 BPE tokenizer。在代码、JSON、特殊字符密集的工具输出上，实际 token 数可能显著高于本地估算，导致本地认为"还在 85% 以下"时 Provider 端已超限。
- **估算未涵盖全部请求内容：** `MaybeCompact` 检查的 `EstimatedTotalTokens` 仅为 prompt + response 的累计值，但实际发给 Provider 的请求还包括 system prompt 和 tool definitions（尤其注册大量工具时 schema 本身可占数千 token），这些没有被纳入预算检查。
- **单轮工具输出导致跳跃式增长：** 压缩检查在每轮迭代开头执行；第 N 轮检查通过后（<85%），模型调用工具返回大量内容追加到 messages，可能从 80% 一步跳到 100% 以上，在第 N+1 轮 `MaybeCompact` 检查之前就被 Provider 拒绝。
- **SummarizeFunc 缺失或 Auto 失败：** 若 `g.summarize == nil`，Auto 层被完全跳过；即使存在，摘要调用本身失败时 loop 仅 warn 后继续，不会阻止后续 `Generate` 调用。
- **Layer 2 的实际触发流程：** 请求发到 Provider → Provider 用真实 tokenizer 计数后返回 HTTP 400（如 `"context_length_exceeded"`）→ `IsContextOverflowError` 通过字符串匹配识别 → `ForceManualCompact` 激进截断 → `CallWithRetry` 用截断后的 messages 重新发请求。代价是浪费一次失败的 API 往返，所以预检越准，Layer 2 触发概率越低。
- **纵深防御的设计理念：** Auto（预防，85%）→ Manual（硬上限，100%）→ Layer 2（Provider 拒绝后兜底），三层构成逐级递进的安全网，确保即使本地估算完全失准也不会导致 agent 卡死。

---

### Q6：LoopState 状态机设计？为什么需要状态转换校验？

**Core：** `LoopPhase` 用粗粒度阶段（idle / running / tool_execution / compacting / recovering / completed / error）描述单次 `RunLoop` 生命周期，`TransitionTo` 只允许预定义边，防止非法跳跃导致观测与并发逻辑错乱。

**Detail：**

- **典型路径：** `idle` → `running` →（可选）`tool_execution` → `running` → … → `completed`；错误或 hook 失败 → `error`；压缩路径 → `compacting` → `running`；重试路径 → `recovering` → `running`。
- **显式禁止：** 例如未进入运行态不能直接 `compacting`；`completed` / `error` 为终端态，不再向其它业务态迁移（除非外层 `Reset` 清空）。
- **为什么要校验：** 便于 metrics、日志与外部 UI 将「当前在干什么」归一到有限枚举；避免例如「尚在 tool_execution 却又标记 completed」的双重语义；压缩与重试与主循环交错时，非法状态会让 guard 与 hook 难以推理。
- **与 LangGraph 等对比：** 这里是有意保持轻量：单协程循环内的状态机，而非通用图执行引擎。

**常见追问：** 「FinishReason 不一致怎么办？」→ Nexus 以 **非空 ToolCalls 为最高优先级** 决定是否继续工具循环，避免某些厂商在 `stop` 与 tool_calls 同时出现时的歧义。

---

## 模块二：团队协作调度层（Q7–Q12）

### Q7：Team 模式跟传统 Supervisor / MetaGPT / AutoGen 有什么区别？

**Core：** Nexus 的 Team 层实现了**持久化异步团队**：Lead 嵌入 Teammate 共享基础设施（Go embedding），持久 Teammate 有独立 goroutine、累积对话历史和自治调度能力，临时 Delegate 提供上下文隔离——与传统 Supervisor「路由完即丢弃」或 AutoGen 群聊消息路由有本质区别。

**Detail：**

- **vs 传统 Supervisor：** Supervisor 是无状态路由器——选完 Agent 后同步执行，执行完即丢弃 Agent 状态。Nexus 的 Teammate 是**有状态的长驻 worker**，累积对话历史跨多次请求。
- **vs MetaGPT：** MetaGPT 偏固定角色 SOP + 结构化产物传递；Nexus 用动态角色模板 + JSONL 邮箱异步通信，更灵活。
- **vs AutoGen：** AutoGen 偏 ConversableAgent 群聊与 speaker 选择；Nexus 由 Lead 自主决定 dispatch 路径（直接/delegate/teammate），不需要群聊协商。
- **vs Claude Code subagent：** Claude Code 的 subagent 是 fork 式同步执行，无独立生命周期；Nexus 的 Teammate 是独立 goroutine，有 work/idle/shutdown 状态机和自治任务认领。
- **面试话术：** 「Nexus 的创新在于把 Agent 从无状态函数提升为有状态服务——Teammate 有自己的生命周期、对话记忆、和自治调度能力，同时通过 Delegate 保留了无状态执行路径。」

---

### Q8：为什么 Lead 要 embedding Teammate？有什么工程价值？

**Core：** Go 没有类继承，embedding 是最自然的代码复用方式。Lead 嵌入 `*Teammate` 后获得全套方法（`executeTool`/`findTool`/`drainInbox`/`appendAssistant`），只需覆盖主循环和 system prompt。架构上 Lead 就是名册中名为 `"lead"` 的 Teammate，其他 Teammate 可以向 Lead 发消息，系统无特例路径。

**Detail：**

- **复用量化：** `teammate.go` 约 470 行代码定义了 ReAct 循环、工具执行、消息追加、inbox drain 等核心逻辑；`lead.go` 仅约 200 行，覆盖 `leadLoop`（增加 `requestCh` select）和 `buildSystem`（增加团队管理指令）。复用率超过 70%。
- **架构一致性：** Lead 在 bus/roster 中无特例——`bus.Send("worker1", "lead", ...)`、`roster.Get("lead")` 均正常工作。Broadcast 也可以发送给 Lead。
- **差异点：** Lead 有 `requestCh chan leadRequest` 实现同步 RPC；Lead 的 `MaxIdlePolls: 600`（~20 分钟）远长于普通 Teammate（40 轮 × 3s ≈ 2 分钟）。
- **对比 interface 方案：** 若用 interface 抽取公共方法，需定义 10+ 个方法签名，且 Lead 仍需持有 Teammate 实例来委托调用，代码量更大且间接层更多。

---

### Q9：三种 Dispatch 路径怎么设计的？模型怎么知道用哪种？

**Core：** Lead 的 system prompt 明确描述了三种路径的适用场景——直接处理（简单任务）、`delegate_task`（上下文隔离的一次性专家任务）、`send_message`/`spawn_teammate`（需要累积上下文的长期协作）。由 LLM 根据任务性质**自主决定**，无硬编码路由规则。

**Detail：**

- **直接处理：** Lead 拥有 `read_file`/`write_file`/`grep_search`/`bash` 等 base tools，简单任务无需分发。
- **`delegate_task`：** `DelegateWork` 创建全新空白 `messages`，仅 task 作为 user msg，使用角色模板的 system prompt + tools，同步运行最多 30 轮，返回结果后**状态完全销毁**。适用于「审查这个文件」「搜索文档中的 API 说明」等无状态任务。
- **`send_message` + `spawn_teammate`：** 消息通过 bus 投递到 Teammate inbox，Teammate 在自己的 goroutine 中处理，结果异步返回。Teammate 保留**完整对话历史**，适用于「继续上次的开发工作」「在已有上下文基础上跑更多测试」。
- **prompt 工程：** Lead system prompt 中用 5 条 numbered rules + 4 种场景 bullet points 清晰描述每种路径的适用场景，模型准确率依赖 prompt 质量。

---

### Q10：JSONL 邮箱 vs channel vs Redis，为什么选文件？

**Core：** JSONL 文件邮箱在**零外部依赖 + 持久化 + 可审计**三个维度最优。channel 不持久化、崩溃丢消息；Redis 增加运维复杂度；JSONL 用 `append` 写入 + `read-then-truncate` 实现 at-most-once 语义，`cat`/`jq` 即可排查。

**Detail：**

- **at-most-once 语义：** `ReadInbox` 先读取全文，再立即 `truncate`（`os.WriteFile(p, nil, 0o644)`）。如果 truncate 后进程崩溃，消息已被消费但不会重复投递。`Send` 使用 `os.O_APPEND` 保证并发安全。
- **mutex 粒度：** 整个 bus 一把 `sync.Mutex`，而非 per-inbox。单进程场景下竞争不严重（inbox 数量=团队规模，通常 <10）；per-inbox 锁可作为优化方向。
- **演进路径：** `MessageEnvelope` 的 JSON 格式（type/from/content/timestamp/request_id/payload）与外部消息队列格式兼容，升级为 Kafka/NATS 只需替换 transport 层。
- **审计价值：** 每条消息一行 JSONL，可追溯任何 Agent 间通信的完整历史。结合 `ClaimLogger` 的 claim 事件日志，形成完整的行为审计链。

---

### Q11：Teammate 的 work/idle 状态机和自治调度怎么工作？

**Core：** Teammate 在 `run()` 中交替执行 `workPhase`（model/tool 循环，最多 50 轮）和 `idlePhase`（三级优先级轮询，最多 40 轮 × 3s）。空闲时按 **收件箱 > 任务板自动认领 > 等待/超时** 的优先级调度。

**Detail：**

- **workPhase：** 每轮迭代先 `drainInbox`（获取新消息），再 `model.Generate`，有 tool_calls 则执行工具并循环，无 tool_calls 则 `appendAssistant` 返回。模型生成错误直接 return 进入 idle。
- **idlePhase 三级优先级：**
  - **P1 收件箱：** `ReadInbox` 有消息 → 注入 messages → `ensureIdentity()` → return true（回到 work）。
  - **P2 任务认领：** `ScanClaimable(role)` 返回匹配角色的未认领任务 → `Claim(taskID, name, "auto")` → `ClaimLogger.Log` → 注入 `<auto-claimed>` 消息 → return true。Claim 失败（竞态丢失）则继续轮询。
  - **P3 等待：** `time.After(pollInterval)` 或收到 `shutdownSignal` → 继续轮询或关闭。
- **`ensureIdentity()`：** 注入 `<identity>You are 'name', role: X, team: Y</identity>` + 确认 ack，防止长期空闲后的上下文漂移——这是长驻 Agent 的工程微创新。
- **自动关闭：** 超过 `maxIdlePolls` 次轮询仍无工作 → log + roster → `shutdown` → goroutine 退出。
- **Lead 特殊待遇：** `PollInterval: 2s`、`MaxIdlePolls: 600`（~20 分钟），保证 Lead 常驻。

---

### Q12：进程重启怎么恢复团队状态？（Rehydrate）

**Core：** `Manager.rehydrate()` 在启动时遍历 `config.json` 名册，对所有状态非 `shutdown` 且当前未运行的成员自动 `Spawn`，注入 resume prompt（`"You are resuming work. Your role is X. Check your inbox and task board."`），恢复团队阵容。

**Detail：**

- **名册持久化：** `Roster` 在每次 `Add`/`UpdateStatus`/`UpdateRole` 后立即写回 `config.json`，保证最新状态。
- **rehydrate 跳过条件：** (1) name==`"lead"`（Lead 已单独初始化）；(2) status==`shutdown`（已明确关闭的不恢复）；(3) 已在 `teammates` map 中运行。
- **inbox 恢复：** JSONL 文件本身就是持久化的——进程崩溃前未被 drain 的消息在重启后仍在文件中，Teammate rehydrate 后首次 `idlePhase` 就能读到。
- **task 恢复：** TaskManager 的 JSON 文件持久化独立于 Team 层——Teammate rehydrate 后，`ScanClaimable` 可以发现之前的 pending 任务继续认领。
- **面试话术：** 「这类似于 Kubernetes 的 desired state reconciliation——名册是 desired state，rehydrate 是 controller 将 actual state 收敛到 desired state。」

---

## 模块三：RAG 系统（Q13–Q18）

### Q13：RAG Pipeline 的完整流程？

**Core：** 摄入：`load → chunk → embed →` 双写**向量库**与**关键词倒排索引**；查询：`embed query →` 双通道检索 **RRF 融合 → CompositeReranker → PostChain**（去重、归一化、上下文增强）→ 格式化为模型可读的 context 字符串或 `types.Message`。

**Detail：**

- **Engine 组件：** `VectorStore`（Memory / Milvus）+ `KeywordStore`（内存 TF-IDF / Elasticsearch BM25）+ `RecursiveChunker` + `DirectoryLoader`/单文件 loader。向量与关键词后端均通过 `configs.RAGConfig` 配置切换。
- **默认 Embedder：** `HashEmbedder`（确定性、无外部调用，适合测试）；生产可换真实 embedding 实现。
- **`Query`：** `topK` 默认来自配置，`Retrieve` 常取 `topK*2` 再裁剪；`RerankTopK` 控制精排宽度。
- **`BuildContextMessage`：** 将 context 与用户问题包装成一条 user 消息，metadata 标记 `rag` 来源。

---

### Q14：为什么要多通道检索？向量 + 关键词怎么合并？

**Core：** 向量通道擅语义相似，关键词通道擅专有名词与字面匹配；二者互补后用 **RRF（Reciprocal Rank Fusion）** 融合排序，降低单通道偏置。

**Detail：**

- **`MultiChannelEngine.Retrieve`：** 并发 `vectorStore.Search` 与 `keywordStore.Search`（后端可选内存 TF-IDF 或 Elasticsearch BM25）。`KeywordStore` 接口使后端可插拔，切换 `keyword_backend` 配置即可从 TF-IDF 升级到 BM25。
- **BM25 vs TF-IDF：** BM25 引入词频饱和度参数 `k1` 与文档长度归一化参数 `b`，比纯 TF-IDF 更好地处理长短文档差异；ES 提供开箱即用的持久化倒排索引与水平扩展。
- **RRF：** 对每个通道的排名列表，贡献分 `1/(k+rank)`，默认 `k=60`；同一 `chunkID` 多通道累计得分，取代表 chunk 时保留较高原始分数字段。
- **为何不用简单加权分数：** 向量分数与关键词分数（无论 TF-IDF 或 BM25）量纲不一；RRF 只依赖秩次，对异构通道更稳健。
- **并行：** `sync.WaitGroup` 降低延迟；向量失败整体报错，便于显式处理（避免静默半残结果）。
- **调参：** `vecTop` 与 `topK` 的比例影响召回宽度；过大增加 rerank 成本，过小易漏检。

---

### Q15：Rerank 为什么不直接用向量相似度排序？

**Core：** 初检（向量 + 关键词 + RRF）解决召回；**Rerank** 阶段用更贴近「query-document 相关性」的信号重排，默认实现是 **词袋余弦 + 可选 `ScoreFn`（如托管 cross-encoder）** 与 **KeywordBoost** 的组合，避免仅依赖第一阶段的几何距离。

**Detail：**

- **`CrossEncoderReranker`：** `lexBlend = alpha*cosineBag + (1-alpha)*log1p(priorScore)`；若配置 `ScoreFn` 且成功，再与 `Beta` 混合。
- **动机：** 纯向量序对**缩写、同义词稀疏、长文档局部命中**可能不敏感；精排可拉高真正包含答案片段的块。
- **`CompositeReranker`：** 链式叠加多种 rerank 策略，便于实验迭代。
- **成本权衡：** 精排只对 `topK*2` 级别的小集合调用重模型，控制延迟与费用。
- **离线评测：** 建议维护 query–gold chunk 集合，对比「仅向量 vs RRF vs +Rerank」的 nDCG 或命中率，用数据说服面试官。

---

### Q16：向量存储是怎么设计的？内存向量库和 Milvus 怎么切换？

**Core：** 通过 `VectorStore` 接口抽象（Add/Search/Delete/Count 四方法），系统提供 `MemoryVectorStore`（开发用）和 `MilvusVectorStore`（生产用）两个实现，config 里一个字段 `vector_backend: "memory"` 或 `"milvus"` 即可切换。

**Detail：**

- **接口设计：** `VectorStore` 定义在 `index/store.go`，`MultiChannelEngine` 只依赖接口而非具体实现，添加新后端只需实现四个方法。
- **MemoryVectorStore：** 进程内 `[]vectorEntry` + 暴力余弦相似度，零外部依赖，适合 <10 万向量的开发与测试场景。
- **MilvusVectorStore：** 连接 Milvus gRPC 服务，支持 IVF_FLAT/IVF_SQ8/HNSW 等 ANN 索引。
  - **Transport 抽象：** `MilvusTransport` 接口隔离了 gRPC 调用，可注入 mock 做单元测试。
  - **Lazy 初始化：** `sync.Once` 首次操作时自动创建 Collection + 建索引 + LoadCollection。
  - **Upsert 语义：** Milvus 无原生 upsert，用 HasEntity → Delete → Insert 实现幂等替换。
  - **Metadata 存储：** 将 `map[string]interface{}` 序列化为 JSON 存入 VARCHAR(65535)，支持动态 schema。
  - **重试退避：** `withRetry` 对网络超时等瞬态错误指数退避，`MaxRetries` 可配。
  - **Count 缓存：** `atomic.Int64` 避免每次调用走 RPC，Add/Delete 时原子更新。
- **切换时机：** 数据量超 10 万向量、需要持久化/高可用/多副本/GPU 加速时切 Milvus。
- **面试追问：** 为何 metadata 用 JSON 而非 Milvus 标量字段？答：Agent 产生的 metadata key 不固定，动态 schema 用 JSON 最灵活；查询过滤在 PostChain 层做。

---

### Q17：分块策略怎么选？为什么代码块要原子化？

**Core：** 默认 `RecursiveChunker` 按分隔符层级（`\n\n` → `\n` → `. ` → 空格）递归切分，并带 **overlap** 窗口；**代码块原子化**（语义上保持函数/类完整）可避免检索片段丢失符号上下文，减少「半段代码 + 半段说明」的无用命中。

**Detail：**

- **参数：** `ChunkSize`、`ChunkOverlap` 与 RAG 配置绑定；过大则噪声多，过小则上下文不足。
- **重叠：** 缓解硬切边界导致的答案截断；代价是索引冗余略增。
- **代码文档：** 若在 loader 层识别 fence（```）可先按块切再 generic chunk，或在 metadata 打 `language` 便于过滤；核心原则是**最小可理解单元**。
- **与 `ingestDocument` 关系：** 每个 `Chunk` 独立 embedding 与索引，删除源文件时 `DeleteBySource` 按 `docChunks` 映射批量清理。

---

### Q18：文档摄入的容错设计？

**Core：** 目录 loader 初始化失败时引擎仍可构造，单文件摄入路径保留；批量 `IngestDirectory` 遇错返回包装错误；`ctx` 取消贯穿循环；`DeleteBySource` 尽力删除向量与关键词两侧索引（`KeywordStore.Remove` 与 `VectorStore.Delete`）。

**Detail：**

- **`NewEngine`：** `NewDirectoryLoader` 失败时 `dl=nil`，不阻塞引擎创建，后续可走显式路径摄入。
- **单文件 `Ingest`：** `stat` 区分文件与目录，目录则提示用 `IngestDirectory`。
- **并发取消：** `ingestDocument` 与 `Query` 均先检查 `ctx.Err()`。
- **元数据：** chunk 携带 `source`、`doc_id`、`chunk_id` 等，便于排错与溯源；坏文件隔离在单次 `embed`/`Add` 错误中暴露，不静默吞掉。

**常见追问：** 「HashEmbedder 能上生产吗？」→ 仅保证**确定性**与**维度对齐**，语义检索质量不足；生产应接入真实 embedding 服务并保持 `Dimensions` 与索引一致。

---

## 模块四：网关与基础设施（Q19–Q24）

### Q19：为什么 Agent 需要 Gateway？（multi-user, channel normalization）

**Core：** Gateway 把 HTTP / WebSocket 等多入口统一为「会话 + 绑定路由 + 命名车道执行」，在进程内并发服务多用户，并把团队调度 `HandleRequest` 与前端协议解耦。

**Detail：**

- **组件：** `SessionManager`（TTL 如 24h）、`BindingRouter`、`LaneManager`、中间件（认证、限流、trace）。
- **价值：** 无 Gateway 时 Agent 只能嵌入 CLI 或单测；有 Gateway 后可多客户端接入、统一鉴权与背压。
- **与 Team Manager：** `Gateway` 依赖 `Supervisor` 接口（`HandleRequest(ctx, sessionID, input)`），`team.Manager` 实现该接口——请求由 Lead 接收并自主决定直接处理、delegate 或 send_message 给 Teammate，编排逻辑与传输层分离。

---

### Q20：5 级绑定路由具体怎么设计的？

**Core：** `BindingRouter` 为每条绑定计算 **Tier 1–5**（渠道与用户具体程度的组合），排序时 **tier 升序**（更具体优先），同 tier 内 **priority 降序**，`Route` 时 first match wins。

**Detail：**

- **Tier 1：** channel 具体 + user 具体。
- **Tier 2：** channel 具体 + user=`*`。
- **Tier 3：** channel 具体 + user 空串（仅渠道）。
- **Tier 4：** channel=`*` + user 具体。
- **Tier 5：** 全局 catch-all。
- **用途：** VIP 用户专用 Agent、某渠道默认模型、全局 fallback 一条绑定搞定。

---

### Q21：Named Lane 对比线程池？Generation Counter 防什么？

**Core：** `LaneManager` 为 `main` / `cron` 等车道维护**独立队列与 `MaxConcurrency`**，避免不同语义工作互相饿死；每条车道 `generation` 在 `Reset` 时递增，**丢弃排队任务与过时代次的执行结果**，防止「会话已重置仍写入旧上下文」。

**Detail：**

- **对比线程池：** 线程池不区分任务类型；Named Lane 把「用户交互」「定时任务」隔离，且可对 `main` 设 `max=1` 保证消息串行处理。
- **`Submit`：** 任务携带入队时的 `generation`；`processQueue` 出队后若代次已变，返回 `ErrStaleLane`。
- **`Reset`：** 递增 generation 并清空队列中待执行任务，向等待方投 stale 错误。
- **类比：** 与并发里的 epoch / fencing token 思想相近，保证重置后因果序。

---

### Q22：限流用的什么算法？为什么选令牌桶？

**Core：** `middleware.RateLimiter` 为每个客户端 IP 维护**令牌桶**（`ratePerSec` 补充令牌，上限 `burst`），请求消耗一枚令牌；空桶返回 429。

**Detail：**

- **实现：** `bucket.allow` 按经过时间线性充值，`tokens` 不超过 `max`。
- **相对漏桶：** 令牌桶允许**适度突发**（burst），更贴合「人类突发点几下 + 静默」的流量形态。
- **相对固定窗口：** 平滑跨窗口边界上的突发；配合 per-IP `sync.Map` 扩展简单。
- **关闭：** `rps <= 0` 时 `Wrap` 直通，便于开发环境。
- **X-Forwarded-For：** 网关前置信任边界时需校验或剥离非法头，否则限流可被伪造 IP 绕过；生产常结合 mTLS 或 API Key。

---

### Q23：权限管道各阶段职责？（实现为顺序多段）

**Core：** `Pipeline.Check` 按**固定顺序**求值：**Deny 规则 → 路径/命令沙箱 → 运行模式（full_auto / manual / semi_auto）→ semi_auto 下的 Allow 规则 → 默认 Ask**；Deny 与沙箱在 full_auto 下仍可能拦截。

**Detail：**

- **阶段 1 – Deny：** `RuleEngine.MatchDeny`，命中即 `BehaviorDeny`，理由含规则 ID。
- **阶段 2 – Sandbox：** `extractPaths` + `ValidatePath`，Shell 额外 `ValidateCommand`。
- **阶段 3 – Mode：** `full_auto` 直接 Allow；`manual` 一律 Ask；`semi_auto`（默认）进入阶段 4。
- **阶段 4 – Allow 规则：** 命中则 Allow；否则阶段 5 **Ask**（需产品层或 CLI 实现确认语义）。
- **与循环集成：** `core` 通过 `PermPipeline.CheckTool` 将拒绝/待确认映射为 error 或结构化结果（具体以接入适配层为准）；目标是**任何工具调用前**已策略评估。

---

### Q24：MCP 协议实现了哪些？Client 怎么自动发现工具？

**Core：** `internal/tool/mcp` 实现 JSON-RPC 2.0 信封与 MCP 风格方法常量：`initialize`、`notifications/initialized`、`ping`、`tools/list`、`tools/call`，以及 `resources/list`、`prompts/list` 方法名约定；**Client** 在 `Initialize` 后调用 **`ListTools`** 拉取远端工具描述，再映射为 Nexus `ToolMeta` 注册（由集成代码完成绑定）。

**Detail：**

- **Transport 抽象：** stdio 或 HTTP/SSE，与具体部署方式解耦。
- **握手：** `Initialize` 解析 `InitializeResult` 缓存协议能力；再发 `SendInitializedNotification` 符合部分服务端预期。
- **发现：** `tools/list` 返回 `ToolDescriptor` 列表；本地将其转为 `types.ToolDefinition` + 代理 `Handler`（内部 `CallTool`）。
- **调用：** `tools/call` 解析文本内容块组装 `ToolResult`。
- **生命周期：** `Manager` 聚合 `Closer`，在进程退出时 `CloseAll` 释放子进程或连接。

**常见追问：** 「MCP 工具与内置工具同名谁优先？」→ 由**注册顺序与 `resolveTool` 策略**决定：通常应保证全局唯一 tool name，或在注册阶段加前缀（如 `mcp_slack_post`）避免覆盖。

---

## 模块五：三阶反思引擎（Q25–Q28）

### Q25：Nexus 有反思机制吗？跟学术界的 Reflexion 有什么区别？

**Core：** Nexus 实现了**三阶反思引擎**（`internal/reflection/`），融合 PreFlect（ICML 2026，前瞻反思）、经典 Reflexion（情节记忆）、SAMULE（EMNLP 2025，三级分类）的核心思想，在 Agent.Run 外层包裹 **前瞻→执行+评估→回顾** 循环，不侵入 ReAct 内核。

**Detail：**

- **Phase 1 — 前瞻反思（Prospector）：** 执行前从 `ReflectionMemory` 检索相关历史教训，调用 LLM 生成 `ProspectiveCritique`（风险列表 + 建议 + 可注入指导段），注入到用户输入中——对不可逆操作（删文件、写 DB）的**事前预防**比事后纠错更有价值。
- **Phase 2 — 执行 + 评估（Evaluator）：** 执行 `Agent.Run` 后，`Evaluator` 对输出做四维度评分（correctness / completeness / safety / coherence）；`score ≥ threshold` 通过则返回，否则进入 Phase 3。
- **Phase 3 — 回顾反思（Reflector）：** 分析失败并分类为 **micro**（单次错误）/ **meso**（同任务模式）/ **macro**（跨任务洞察），存入 `ReflectionMemory`，注入下一轮输入重试。
- **与原始 Reflexion 对比：** 原版只有回顾 + 记忆，Nexus 加了前瞻（PreFlect）和三级分类（SAMULE）；与 LATS/ReTreVal 相比成本更低（最多 6 次额外 LLM 调用 vs 5-20x）。
- **与 RecoveryManager 分工：** RecoveryManager 处理**基础设施层**错误（超时、429、上下文溢出）；反思引擎处理**语义层**问题（逻辑错误、遗漏、质量不达标），两者正交。

---

### Q26：三级反思分类（micro/meso/macro）具体怎么工作？

**Core：** 受 SAMULE（EMNLP 2025）启发，`Reflector` 将每次失败分析为三个粒度：micro 是单次轨迹的具体错误修正、meso 是同任务类型的反复出现模式、macro 是跨任务可迁移的通用洞察。

**Detail：**

- **分类逻辑：** LLM 在 prompt 中被要求基于当前错误与历史反思自主判断级别——首次失败通常为 micro；在历史中发现相同 `error_pattern` 时升级为 meso；洞察可跨任务时归为 macro。
- **淘汰策略：** `ReflectionMemory` 超出 `maxEntries` 时按 **macro > meso > micro** 保留优先级淘汰——高层洞察存活最久，微观纠错最先被淘汰。
- **收益：** 系统越用越聪明——micro 做当下纠错，meso 对同类任务积累经验，macro 跨域迁移洞察（如「处理 API 分页时始终检查 hasMore」）。
- **面试话术：** 可类比人类学习——犯一次错是记住（micro）、同类错误犯多了归纳（meso）、跨领域举一反三（macro）。

---

### Q27：前瞻反思（Prospector）的价值是什么？跟权限沙箱什么关系？

**Core：** 前瞻反思在执行前注入历史教训和风险提示，对不可逆操作做**语义级预防**；与 `PermPipeline` 的**执行级拦截**形成双层防护——前者「建议不做」，后者「禁止做」。

**Detail：**

- **学术来源：** PreFlect（ICML 2026）指出传统 Reflexion 的根本局限——对已删文件、已写数据库的操作事后反思无济于事。
- **实现：** `Prospector.Critique` 从 `ReflectionMemory` 按 Jaccard 相似度检索相关历史反思，调用 LLM 输出 `{risks, suggestions, injected}` JSON，`injected` 段被前置到用户输入。
- **降级策略：** LLM 不可用时回退为直接拼接历史 `Suggestion` 文本，保证至少有基线指导。
- **与权限系统协同：** Prospector 在 prompt 层面引导 Agent 避开危险操作（软约束），`PermPipeline` 在 handler 执行前做策略拦截（硬约束），两者构成**纵深防御**。

---

### Q28：反思引擎的成本怎么控制？会不会每个请求都多调好几次 LLM？

**Core：** 三个层面控制成本：配置开关 `enabled: false` 可完全关闭；首次通过只多 2 次 LLM 调用（前瞻+评估）；最坏情况 `maxAttempts=3` 也只有 6 次额外调用，远低于 LATS（5-20x）。

**Detail：**

- **成本明细：** Prospector 1 次（有历史才触发） + Evaluator 每次 1 次 + Reflector 每次失败 1 次。首次成功 = 2 次额外调用；一次失败后成功 = 4 次。
- **可选关闭前瞻：** `enable_prospect: false` 省 Prospector 调用，仅保留评估+回顾。
- **评估器安全降级：** 解析失败或 LLM 不可用时默认 `pass=true`，不阻塞主流程。
- **与 LATS/ReTreVal 对比：** LATS 做树搜索需 5-20x 调用量；ReTreVal 自适应建树更重；Nexus 的线性重试策略成本可控且可预测。

---

## 模块六：规划与记忆（Q29–Q34）

### Q29：为什么 Task 要持久化到磁盘？

**Core：** `TaskManager` 将每个任务存为 `task_<id>.json` 并维护 `meta.json` 中的 `nextID`，使规划状态** survives 进程重启与上下文压缩**；对话 transcript 可被摘要或截断，但 DAG 仍是权威事实源。

**Detail：**

- **操作：** `Create` / `Update` / `Claim` 等均落盘；支持 `BlockedBy` / `Blocks` 双向边。
- **与 Agent 工具对齐：** Planner Agent 通过工具操作同一 `TaskDir`，人机均可读 JSON 排障。
- **恢复：** 启动时 `loadFromDisk` 重建内存 map 与 nextID。

---

### Q30：DAG 依赖解析和环检测怎么做的？

**Core：** 创建任务时指定 `blocked_by` 前置任务 ID；`hasCycleLocked` 在**临时合并新任务后的图上对 `BlockedBy` 边做 DFS**，若递归栈重复访问同一节点则存在环，拒绝创建。

**Detail：**

- **语义：** `BlockedBy` 表示「本任务依赖哪些任务完成」；`Create` 时校验引用存在。
- **DFS：** `recStack` 标记当前路径上的节点；从新建任务 ID 出发沿依赖回溯。
- **导出：** `HasCycle` 供外部或测试预检。
- **解锁：** `Update` 为 `completed` 时可触发下游状态变迁（具体策略见 `task.go` 中 `initialStatusLocked` 与 unblock 逻辑）。

---

### Q31：Cron、TaskDAG、BackgroundManager 三者关系？

**Core：** **TaskManager** 管「是什么、依赖谁、状态如何」；**BackgroundManager** 管「同时能跑几个后台槽、取消与结果」；**CronScheduler** 按 cron 表达式**定时触发**注册 handler，典型是到期后投递 `agent_turn` 或 `system_event`，再间接驱动 Task 或 Agent。

**Detail：**

- **`PlanExecutor`：** 从 `TaskManager` 取可认领任务，`Claim` 后 `BackgroundManager.Submit` 在独立槽内跑 `runner`，结束写回 `Update`。
- **Cron：** `robfig/cron/v3` 调度；任务定义持久化在 `cron_jobs.json`（路径相对 `TaskDir`）。
- **周期/一次性双模式：** `CronJob.OneShot` 为 `true` 时，`dispatch` 在 handler 执行完成后自动调用 `removeJobLocked` 做三步清理（注销 cron Entry → 移除内存列表 → 重新持久化文件），适用于一次性迁移、定时发布等场景；默认 `false` 则周期执行。
- **执行结果持久化：** handler 将每次触发的完整执行记录（payload、时间戳、Agent 输出、错误）写入 `.tasks/cron_results/<job_name>/<timestamp>.json`，供回溯审计。
- **Team Manager 统一路由：** handler 统一调用 `team.Manager.HandleRequest`，cron 触发的任务与用户 HTTP/WS 请求走相同路径——由 Lead 根据 Payload 内容自主决定处理方式（直接、delegate_task 或 send_message），而非硬编码路由。
- **关注点分离：** `background.go` 注释写明分离「做什么」与「怎么跑」，避免把并发细节写进 DAG 存储层。

---

### Q32：会话记忆和语义记忆区别？为什么用 YAML 不用向量库？

**Core：** **ConversationMemory** 管**滑动窗口内的消息与可选摘要**，服务当前对话连贯性；**SemanticStore** 管**跨会话长期事实**（project / preference / feedback / reference），以 **YAML 文件**持久化，强调可读、可 diff、可手工编辑，而非相似度检索。

**Detail：**

- **会话：** `Add` / `GetRecent` / `Compact(summarizer)` 组合实现短窗 + 滚动摘要。
- **语义：** `SemanticEntry` 带 category、key、value、时间戳；`maxEntries` 超出删最旧。
- **不用向量库的原因：** 条目规模有限、需精确 key 与人工审计；向量检索适合非结构化海量笔记，此处 **结构化 KV + 分类** 更直接。
- **与 RAG 分工：** RAG 面向文档库；SemanticStore 面向「用户/项目设定类」高置信事实。

---

### Q33：两阶段 Skill 加载怎么设计？（demand paging 类比）

**Core：** `SkillManager` **阶段一** `ScanSkills` 只读每个 `skills/<name>/SKILL.md` 的 **YAML frontmatter**，构建轻量索引（name、description、invocation、path）；**阶段二**在需要时 `LoadSkill` 读取正文，避免启动时把大量 markdown 全塞进内存与 system prompt。

**Detail：**

- **类比：** 类似 OS 的**按需分页**：目录遍历像页表，真正执行相关 skill 再换入全文。
- **限制：** `maxSkills` 与 `maxPromptChars` 控制索引规模与注入 prompt 上限，防爆炸。
- **与 PromptAssembler 协同：** 系统提示只带当前任务相关的 skill 片段，而非全库。

---

### Q33.1：Skill 索引已经注入 system prompt，为什么还需要 `list_skills` 工具？

**Core：** system prompt 中的索引受 `maxPromptChars` 截断，skill 数量多时尾部会丢失；`list_skills` 无截断，作为可靠的完整列表兜底。两者的定位也不同——索引是「被动上下文」，工具是「主动检索」。

**Detail：**

- **信息重叠是事实：** `getSystem()` 每轮 LLM 调用都重新拼接 `GetIndexPrompt()`，且 system prompt 独立于 message history、不受 `ContextGuard` 压缩影响。当 skill 数量少时（如当前 3 个），`list_skills` 返回的内容与 system prompt 索引完全相同，确实是冗余的。
- **关键差异在截断：** `GetIndexPrompt()` 受 `maxPromptChars`（默认 80000）硬截断，`PromptBudgets.SkillIndexMax`（30000）可进一步压缩预算；而 `ListSkillSummaries()` 遍历完整 `index` 切片，无任何字符截断。当 skill 规模接近 `maxSkills`（256）上限时，system prompt 中的索引可能只包含前半部分 skill，LLM 无法看到排在后面的 skill。
- **被动 vs 主动：** system prompt 索引是被动上下文，LLM 不需要额外动作就能感知；`list_skills` 是主动检索，需要 LLM 显式调用。被动上下文在长对话中可能因注意力衰减被忽视，而 tool call 返回的结果出现在最近的对话位置，对 LLM 更「新鲜」。
- **token 预算弹性：** 未来若为节省 system prompt token 将 `SkillIndexMax` 调至极小甚至为 0，`list_skills` 仍可作为唯一发现渠道，保证 skill 机制不失效。
- **设计模式：** 这是 Agent 框架中常见的「passive context + active retrieval」双保险——system prompt 提供最低成本的基线感知，tool 提供无截断、可刷新的精确查询。两者互补而非替代。

**面试话术：** 可以先坦诚承认小规模下有冗余，再转到截断场景和 token 预算弹性上说明工程价值——展现对设计取舍的清醒认知。

---

### Q34：如果面试官问「这个项目最大的技术挑战是什么」怎么答？

**Core：** 可回答：**设计一个让 Agent 从无状态函数升级为有状态服务的团队协作系统——在保持上下文可管理、工具可控、行为可观测的前提下，实现持久化 Teammate、异步通信和自治调度**。

**Detail（可任选 2–3 点展开）：**

- **Lead-Teammate-Delegate 三层模型：** 最大挑战是「共享多少、隔离多少」的架构决策——Lead embedding Teammate 共享 ReAct 基础设施（70%+ 代码复用），同时通过 Delegate 保留上下文隔离能力。两种模式的取舍（持久 vs 临时、有状态 vs 无状态）由模型在 prompt 引导下自主决定。
- **JSONL 邮箱 + 自治调度：** 在不引入外部消息队列的前提下实现可靠的异步通信——append + drain-then-truncate 的 at-most-once 语义，加上 work/idle 状态机和三级优先级调度（收件箱 > 任务认领 > 超时关闭），让 Teammate 具备自治能力。
- **进程韧性：** 名册持久化 + JSONL 邮箱持久化 + TaskDAG 文件持久化，三者配合 rehydrate 机制，实现进程重启后团队阵容、未读消息和待执行任务的完整恢复。
- **三阶反思引擎：** 融合 PreFlect + Reflexion + SAMULE 的创新设计；前瞻批判对不可逆操作做事前预防，三级分类使系统越用越聪明。
- **上下文管理：** Micro/Auto/Manual 三层压缩 + `ensureIdentity()` 防长期空闲后的上下文漂移，保证长驻 Teammate 的行为一致性。

**STAR 简例 — 团队协作版（可背诵骨架）：**
- **S：** 多 Agent 协作时，传统 Supervisor 同步路由无法支持长期协作和上下文累积。
- **T：** 设计一个让 Agent 能异步协作、自治调度、持久化状态的团队系统。
- **A：** Lead embedding Teammate 共享基础设施；JSONL 邮箱实现零依赖异步通信；work/idle 状态机 + ScanClaimable 实现自治调度；名册 + rehydrate 保证进程韧性。
- **R：** Teammate 可跨多次请求累积上下文；空闲 Teammate 自动认领匹配角色的 DAG 任务；进程重启后团队自动恢复，零数据丢失。

---

## 附录：快速索引

| 模块 | 主题 | 代码入口（参考） |
|------|------|------------------|
| 核心循环 | ReAct、工具、hooks | `internal/core/loop.go` |
| 上下文 | 压缩、落盘 | `internal/core/context.go` |
| 恢复 | 重试、溢出 | `internal/core/recovery.go` |
| 状态机 | Phase 迁移 | `internal/core/state.go` |
| 反思引擎 | 前瞻、评估、回顾、记忆 | `internal/reflection/*.go` |
| 团队协作 | Lead、Teammate、Delegate、Bus | `internal/team/*.go` |
| RAG | 管道、RRF、精排 | `internal/rag/pipeline.go`、`internal/rag/retrieval/*` |
| 网关 | 会话、绑定、车道 | `internal/gateway/*.go` |
| 权限 | Pipeline | `internal/permission/pipeline.go` |
| MCP | Client / 协议 | `internal/tool/mcp/*.go` |
| 规划 | Task、Cron、Background | `internal/planning/*.go` |
| 记忆 / Skill | 会话、语义、两阶段 | `internal/memory/*`、`internal/intelligence/skill.go` |

---

*文档版本与 Nexus 代码库意图对齐；若实现细节迭代，请以源码为准。*

---

## 延伸：模块交叉题（备查）

下列问题可用于面试官跨模块追问，不在主编号 30 题内，但与上文强相关。

### E1：`LoopState` 与 `Teammate` 状态各自保存什么？为何不全合并？

**Core：** `LoopState` 描述**单次** `workPhase` 内的消息与阶段（短期）；`Teammate` 持有**跨多次请求**的对话历史、角色、收件箱和生命周期状态（长期）。粒度不同，合并会导致生命周期纠缠与内存膨胀。

**Detail：** 每次 `workPhase` 结束后 `LoopState` 可重建，而 Teammate 的对话历史在进程内累积、在进程重启时通过 Roster rehydrate 恢复。Delegate 则是临时的——完成任务后 `LoopState` 和 Agent 状态一起丢弃。

### E2：RAG 检索结果如何进入 Agent？

**Core：** `Engine.Query` 产出格式化字符串，或由调用方 `BuildContextMessage` 拼成一条 `types.Message` 再交给模型；知识类 Agent 通常在构造用户输入时注入该消息。

**Detail：** 注意控制注入长度，避免与 `ContextGuard` 双重膨胀；可在网关层对 RAG context 再做硬截断。

### E3：`Observation` / Metrics 可以挂在哪里？

**Core：** `LoopHooks` 的 `PreAPI` / `PostAPI` / `PreTool` / `PostTool` 是最自然的打点面；Gateway 中间件负责请求级 trace；Team Manager 在 Spawn / Shutdown / HandleRequest 等关键路径打 `Info`/`Warn`/`Error`；`ClaimLogger` 记录每次自治认领的任务与角色匹配。

**Detail：** 生产环境可将 hook 实现为 OpenTelemetry span 或结构化日志，关联 `session_id`、`teammate_name` 与 `tool_name`。

### E4：配置热更新与 `BaseAgent.SetModel` 的使用场景？

**Core：** 运行中切换模型实现（例如从测试 stub 切到真实后端）时，可 `SetModel` 同步更新 loop 内引用，避免重建整个 Agent 图。

**Detail：** 仍需保证 tool schema 与新模型能力匹配（部分模型对 parallel tools 支持不同）。

### E5：测试策略上，哪些模块应优先单测？

**Core：** **状态机迁移**（`TransitionTo` 非法边）、**RRF 融合**（固定两路排名期望序）、**环检测**（`hasCycleLocked`）、**Team 协作**（Spawn/Shutdown/Bus Send-Read 往返、Roster 持久化与 rehydrate、ScanClaimable 角色匹配）、**权限管道顺序**（deny 优先于 full_auto）。

**Detail：** `team_test.go` 用 fake ChatModel 验证 Lead 分发、Teammate work/idle 状态切换、Delegate 上下文隔离和 MessageBus at-most-once 语义。LLM 本身用接口注入 + fake HTTP（`SetHTTPDo`）做模型调用测试。

### E6：如何从单机 Nexus 演进到多副本部署？

**Core：** 有状态部分（Session、Lane 队列、内存向量库）需外置或粘性会话；无状态部分（纯 `HandleRequest` 计算）可水平扩展。

**Detail：** Task/语义记忆已在磁盘时，需共享存储或迁移到数据库；WebSocket 需 sticky session 或统一消息总线；限流从单机 `sync.Map` 迁到 Redis 令牌桶等。

### E7：`PostChain` 里各处理器的作用？

**Core：** `DeduplicateProcessor` 去掉重复 chunk；`ScoreNormalizer` 统一分数尺度便于日志与阈值；`ContextEnricher` 补充展示用元数据（如标题、来源）。

**Detail：** 链式顺序影响最终上下文形态，调整时需回归检索评测集。

### E8：Planner 工具与 `PlanExecutor` 如何接线？

**Core：** Agent 侧工具负责「改 DAG」；执行器负责「选可运行任务 + 占后台槽 + 回调 runner」；runner 由 `main` 或宿主注入，对接真实模型或脚本。

**Detail：** `Claim` 防止多执行器抢同一任务；失败时将任务置回 `pending` 便于重试。

### E9：为什么 IntentRouter 里规则按 priority 排序且 stable？

**Core：** 高优先级规则先评估；`AgentName` 次序打破平局，保证**确定性**，避免同样输入在不同运行间跳到不同 Agent。

**Detail：** 若业务要求「最后添加的规则覆盖」，需在 `AddRule` 时调整 priority 策略或显式文档约定。

### E10：Teammate 长期运行后上下文会膨胀吗？如何控制？

**Core：** 会。持久 Teammate 跨多次请求累积对话历史，token 数会持续增长。控制手段有三层：① `ContextGuard` 的 Micro/Auto/Manual 三级压缩在每次 workPhase 内自动触发；② `ensureIdentity()` 在长期空闲后重新注入 system prompt 防上下文漂移；③ Delegate 模式天然隔离——一次性任务不污染 Teammate 对话历史。

**Detail：** 若 Teammate 对话历史过长且压缩后仍超阈值，可通过 `ShutdownTeammate` + 重新 `Spawn` 实现「软重启」，Roster 记录保证角色和工具集不变。

---

## 术语对照（中英）

| 中文 | 英文 / 代码标识 |
|------|-----------------|
| 工具调用 | tool call / `ToolCall` |
| 完成原因 | finish reason / `FinishReason` |
| 上下文压缩 | compaction / `ContextGuard` |
| 倒数秩融合 | RRF / `reciprocalRankFusion` |
| 精排 | rerank / `Reranker` |
| 团队领队 | lead / `Lead` |
| 队友 | teammate / `Teammate` |
| 委派 | delegate / `DelegateWork` |
| 邮箱总线 | message bus / `MessageBus` |
| 名册 | roster / `Roster` |
| 任务认领 | claim / `ScanClaimable` |
| 命名执行车道 | named lane / `LaneManager` |
| 代次 | generation / `lane.generation` |
| 令牌桶 | token bucket / `RateLimiter` |
| 任务图 | task DAG / `TaskManager` |

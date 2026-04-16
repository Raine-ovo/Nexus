# Nexus 项目 — 简历素材

## 项目标题

**Nexus：基于 CloudWeGo Eino 的工业级多智能体协作平台**

---

## 一句话描述（简历头部）

基于 CloudWeGo Eino 框架独立设计并实现的工业级多智能体智能研发助手平台，实现了**持久化团队协作调度**（Lead-Teammate-Delegate 三层模型 + JSONL 异步邮箱 + 自治任务认领），集成 RAG 知识库、MCP 工具协议、DAG 任务编排和多通道网关，Go 语言约 15000 行代码。

---

## 架构速览（口述 / 面试用）

五层逻辑划分便于对外说明，与 `internal/` 包结构一致：

1. **网关层**：多协议接入、路由与 Lane、中间件（鉴权、限流、Trace）。
2. **团队协作调度层** ★：Lead（嵌入 Teammate）→ 持久 Teammate / 临时 Delegate，JSONL 邮箱异步通信，自治调度引擎。
3. **Agent 核心引擎**：ReAct 循环、LoopState 状态机、ContextGuard、三层恢复、三阶反思。
4. **领域基础设施**：Tool + MCP、RAG Pipeline、Task DAG、Memory（对话 + 语义）。
5. **横切能力**：Permission 管道、Observability（Callback → Trace / Metrics / 日志）。

---

## 核心技术栈

| 类别 | 技术点 |
|------|--------|
| 语言与框架 | Go、CloudWeGo **Eino**（Graph / ADK / Workflow） |
| 团队协作 | **Lead-Teammate-Delegate** 三层模型、**JSONL 邮箱**、**自治调度** |
| 协议与集成 | **MCP**（stdio / SSE）、HTTP、**WebSocket** |
| 数据与检索 | 向量索引（Memory / Milvus）、**RRF**、**BM25**、**Rerank** |
| 并发与任务 | **DAG** 调度、Named Lane、后台 Cron、槽位执行、Auto-Claim |
| 质量与可观测 | 结构化日志、指标、Trace；四级权限 + 工作区沙箱 |

---

## 简历 Bullet Points — 三个版本

### 基础设施 / 后端方向（例：字节跳动、蚂蚁金服、PingCAP）

- 设计并实现 **Lead-Teammate-Delegate 团队协作调度层**：Lead 通过 Go embedding 嵌入 Teammate 共享基础设施，对外提供同步 RPC 网关入口；模型自主选择三种 dispatch 路径——直接处理 / **临时 Delegate**（上下文隔离、执行完即销毁） / **持久 Teammate**（独立 goroutine + 累积对话历史）。**JSONL 文件邮箱**实现异步通信（append 写入 + 读即清空的 at-most-once 语义），零外部依赖。
- 实现**自治调度引擎**：Teammate 的 `work→idle` 状态机在空闲时按优先级轮询——**收件箱 > 任务板自动认领（`ScanClaimable` 角色匹配 + `Claim` 竞态安全） > 超时自动关闭**。进程重启时 `rehydrate` 从名册文件恢复活跃 Teammate。**工具权限分层**精确隔离 Lead-only 管理权限与普通 Teammate 工具集。
- 设计多通道网关：HTTP / WebSocket 归一化入口，**五级绑定路由**、**Named Lane** 语义隔离与独立并发度，结合 **generation 代际追踪**丢弃过期异步结果（fencing token 语义）。
- 实现任务 DAG 引擎：**JSON 文件持久化**任务图谱，`blockedBy`/`blocks` 双向索引，完成时 **O(B)** 解锁下游；**DFS 环检测（O(V+E)）**，与团队层 Auto-Claim 深度集成。

### AI / ML 方向（例：Moonshot、智谱、月之暗面）

- 设计 **Lead-Teammate-Delegate 三层 Agent 协作模型**：Lead 作为 Supervisor 自主决定 dispatch 策略——临时 Delegate 提供**干净上下文的一次性专家执行**（代码审查、知识问答），持久 Teammate 提供**累积对话历史的长期协作**（多轮开发、持续测试）。两者互补，类比「无状态函数 vs 有状态服务」的架构取舍。
- 端到端 **RAG 流水线**：文档加载 → 分块 → Embedding → 多通道 **Retrieve**（向量 + BM25 并行） → **RRF 融合** → **Rerank** → 生成，支持 Memory/Milvus 向量后端和 TF-IDF/Elasticsearch 关键词后端动态切换。
- **上下文生命周期管理**：**三级压缩**（micro-compact、auto-compact、manual-compact）与 token 预算守护；Teammate `ensureIdentity()` 在空闲唤醒后注入身份上下文防止**长期对话的上下文漂移**。
- **三阶反思引擎**：融合 PreFlect（ICML 2026）前瞻反思 + Reflexion 情节记忆 + SAMULE（EMNLP 2025）三级分类（micro/meso/macro），最多 6 次额外 LLM 调用实现语义层自我改进。

### 通用 SDE（例：美团、阿里、腾讯、快手）

- 负责整体 **五层架构**设计与落地：**网关、团队协作调度、Agent 引擎、RAG/工具/任务/记忆等基础设施、权限与可观测横切能力**；代码按约 **13 个核心模块** 组织，边界清晰、可演进。
- 设计**持久化团队协作系统**：Lead + Teammate + Delegate 三层模型，**JSONL 邮箱异步通信**，**名册持久化 + 进程重启 rehydrate**，空闲 Teammate **自动认领匹配角色的 DAG 任务**，`RequestTracker` 实现 shutdown/plan-approval 协议。
- 实现 **MCP（Model Context Protocol）** 工具生态：**Server / Client**、stdio 与 SSE 传输、动态发现与注册外部工具，并与内置工具统一走 **ToolRegistry + 权限管道**。
- 运用分布式与韧性常见模式：**JSONL 文件邮箱**（类 WAL append-only + drain 语义）、**指数退避重试**、**generation/fencing token** 防陈旧、**三层恢复洋葱**（工具自纠 / Context 压缩 / 传输退避）。

---

## ATS 关键词

**英文（技能栏 / 英文简历）**  
Go, CloudWeGo, Eino, LLM, Agent, Multi-Agent, Team Orchestration, Lead-Teammate, Async Messaging, JSONL Bus, RAG, MCP, Tool Use, Function Calling, DAG Scheduling, Concurrency Control, WebSocket, Context Management, Prompt Engineering, Vector Database, Reranking, Gateway, Autonomous Agent

**中文（与系统/JD 对齐时可混排）**  
Go 语言、微服务网关、多智能体团队协作、异步邮箱通信、自治调度、大模型应用、检索增强生成、工具调用、任务编排、向量检索、重排序、长上下文、提示工程、可观测性、权限管道

---

## 项目规模（建议措辞）

- **独立项目**，**2025.06 – 2025.08**
- 约 **15000 行 Go** 代码，**13** 个核心模块，**20+** 工具实现（含内置、团队工具与 MCP 扩展）
- **Lead-Teammate-Delegate** 三层协作模型 + **4** 个角色模板

---

## GitHub 描述（英文，可直接用于仓库 About）

**短描述（About 单行，约 350 字符内）**  
Industrial-grade multi-agent dev assistant on **CloudWeGo Eino**: Lead-Teammate-Delegate team with JSONL async bus, autonomous task claiming, RAG, **MCP** tools, **DAG** tasks, multi-lane gateway. ~15k Go LOC.

**长描述（README 顶部或仓库 Description 补充）**  
**Nexus** is an industrial-grade **multi-agent** coding assistant built on **CloudWeGo Eino**. It features a **Lead-Teammate-Delegate** team model: Lead embeds Teammate via Go embedding sharing the ReAct tool loop, persistent **Teammates** accumulate conversation history across tasks, ephemeral **Delegates** run in isolated contexts — coordinated via a **JSONL mailbox bus** (append-only, at-most-once drain). Idle teammates **autonomously claim** matching DAG tasks. Includes an end-to-end **RAG** pipeline (multi-channel retrieval with **RRF** + **BM25** + **Rerank**), full **MCP** integration, **DAG** planning with **cycle detection**, a multi-protocol **gateway** with **named lanes** and **generation** tracking, plus a **three-layer recovery** stack. Written in **Go** (~15k LOC).

---

## 量化与亮点备忘（写简历时可择一两条）

- **团队调度创新**：Lead embedding Teammate + 持久/临时双模式 + JSONL 邮箱 + 三级自治调度（收件箱 > 任务认领 > 超时关闭）。
- **复杂度**：DAG 解锁下游 O(B)；环检测 O(V+E)；Claim 竞态安全；Lane 隔离降低重入风险。
- **完整性**：从接入、团队调度、工具与 RAG、任务与记忆，到权限与观测，端到端闭环。
- **扩展性**：MCP 动态挂载外部工具；角色模板 + Skill 系统便于行为与知识扩展。
- **韧性**：名册持久化 + rehydrate 进程重启恢复；JSONL 天然支持崩溃恢复与审计回放。

---

## 使用说明（可选，投递前删除本段）

- 一句话描述适合放在简历最上方的「项目经历」摘要行。
- 按目标岗位只选 **一个** Bullet 版本中的 3–4 条，避免堆砌；可与公司 JD 中的「网关 / Agent / RAG / Go」等词对齐微调动词。
- ATS 关键词可自然嵌入 bullet 或技能栏，勿无意义关键词堆砌。
- 面试时建议按「业务目标 → 架构分层 → 一两个最难的技术点（如 DAG / RAG / 网关代际）→ 结果」顺序展开，控制在 90 秒内。

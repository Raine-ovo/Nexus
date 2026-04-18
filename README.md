# Nexus — 基于 Eino 的工业级多智能体协作平台

Nexus 是一个基于 [CloudWeGo Eino](https://github.com/cloudwego/eino) 框架构建的**工业级多智能体智能研发助手平台**，集成**持久化团队协作**、RAG 知识库、MCP 工具协议、DAG 任务编排和多通道网关。

## 架构概览

```
┌─────────────────────────────────────────────────────────────┐
│  L1 接入与隔离层 (Gateway)                                    │
│  HTTP/WS 归一化 · 5-Tier Binding Router · Named Lanes       │
├─────────────────────────────────────────────────────────────┤
│  L2 团队协作调度层 (Team)                          ★ 核心亮点 │
│  Lead(嵌入Teammate) · 持久化Teammate · 临时Delegate          │
│  JSONL Inbox Bus · RequestTracker · Roster · Auto-Claim     │
├─────────────────────────────────────────────────────────────┤
│  L3 Agent 核心引擎层 (Core)                                  │
│  ReAct 循环 · LoopState 状态机 · ContextGuard · Recovery     │
├─────────────────────────────────────────────────────────────┤
│  L4 知识与工具能力层 (RAG / Tool / MCP)                       │
│  多通道检索 · RRF 融合 · 内置工具 + MCP JSON-RPC 客户端      │
├─────────────────────────────────────────────────────────────┤
│  L5 数据与治理层                                              │
│  Memory · TaskDAG · Permission · Intelligence · Observability │
└─────────────────────────────────────────────────────────────┘
```

## 核心特性

| 特性 | 描述 |
|------|------|
| **Lead-Teammate 团队协作** | Lead 嵌入 Teammate 共享基础设施，持久 Teammate 长期记忆 + 临时 Delegate 上下文隔离，双模式协同 |
| **JSONL 邮箱异步总线** | 每个 Agent 独立 `{name}.jsonl` 收件箱，追加写入 + 读即清空（at-most-once），无需外部消息队列 |
| **自治调度引擎** | 空闲轮询优先级：收件箱 > 任务板自动认领（角色匹配）> 超时自动关闭，三级调度策略 |
| **RAG 知识库** | 文档加载 → 分块 → Embedding → 多通道检索 → Rerank → 生成 |
| **MCP 工具协议** | 实现 MCP Server/Client；运行时主链默认挂载 HTTP/SSE 端点并支持远端工具自动注册，底层 Transport 仍兼容 stdio |
| **DAG 任务引擎** | JSON 持久化任务图谱，依赖解析，DFS 环检测，自主认领执行 |
| **三层 Context 管理** | micro-compact + auto-compact + manual-compact，token 预算守护 |
| **四级权限管道** | deny → mode → allow → ask 四阶段，路径沙箱 + 危险命令拦截 |
| **多通道网关** | HTTP/WebSocket 归一化，5 级绑定路由，Named Lane 语义隔离 |
| **Scope / Workstream Continuity** | Session 可绑定 scope/workstream，支持 continuation 命中、跨 session 复用、跨重启恢复与可观测决策链 |
| **全链路可观测** | Callback 驱动的 Trace + Metrics + 结构化日志 |

## 目录结构

```
nexus/
├── cmd/nexus/main.go            # 入口：组装团队、注册模板、启动网关
├── configs/                     # 配置系统
├── internal/
│   ├── core/                    # Agent 核心引擎 (Loop, State, Context, Recovery)
│   ├── team/                    # ★ 团队协作调度层 (Manager, Lead, Teammate, Delegate, Bus)
│   ├── orchestrator/            # 旧编排层 (Supervisor, Intent, Handoff) — 已被 team 取代
│   ├── agents/                  # 4 个专业 Agent 实现 (→ 作为 team 角色模板)
│   ├── tool/                    # 工具系统 + MCP 协议
│   ├── rag/                     # RAG 系统 (Pipeline, Retrieval, Index)
│   ├── memory/                  # 记忆系统 (Conversation + Semantic)
│   ├── planning/                # 规划系统 (TaskDAG, Cron, Background)
│   ├── gateway/                 # 网关层 (Server, Router, Lane, Middleware)
│   ├── permission/              # 权限管道
│   ├── intelligence/            # 智能组装 (Prompt, Skill, Bootstrap)
│   ├── reflection/              # 三阶反思引擎 (Prospector, Evaluator, Reflector)
│   └── observability/           # 可观测性 (Trace, Metrics, Callback)
├── pkg/                         # 公共库 (types, utils)
├── workspace/                   # Agent 工作区模板
│   └── skills/                  # 技能文件 (SKILL.md with YAML frontmatter)
└── docs/                        # 文档
```

## 运行状态

当前仓库的核心链路已经可以直接跑起来：

- `go build ./cmd/nexus` 可通过
- `POST /api/sessions` 与 `POST /api/chat` 可正常工作
- 已验证可接入智谱 OpenAI 兼容接口，默认示例使用 `glm-5.1`
- `semi_auto` 模式下已默认放行安全只读工具，如 `read_file`、`grep_search`、`glob_search`
- 已接入全局 LLM 节流器，可通过 `model.max_concurrency` 与 `model.min_request_interval_ms` 控制并发与请求节拍
- 已提供本地治理入口：`/debug/dashboard`、`/api/debug/*`、run 级 `latest-traces.json`
- 已支持 `scope/workstream` 连续性：`/api/sessions` 可显式传 `scope` / `workstream`，也支持 continuation cue 命中旧工作线

仍需额外准备或按需接入的部分：

- MCP 外部服务：只有在你需要外部 MCP 工具时才需要单独配置
- Milvus / Elasticsearch：默认不会启用，只有切换对应后端时才需要外部依赖
- 生产级权限策略：当前默认规则偏向本地开发可用，生产环境建议继续细化

## 快速开始

### 1. 准备环境

```bash
cd nexus
go version
```

要求：

- Go `1.22+`
- 一个可用的 OpenAI 兼容模型 API Key

### 2. 配置模型

推荐直接使用环境变量，`configs/default.yaml` 已支持 `${NEXUS_API_KEY}` 展开：

```bash
export NEXUS_API_KEY="your-zhipu-api-key"
export NEXUS_BASE_URL="https://open.bigmodel.cn/api/paas/v4/"
export NEXUS_MODEL="glm-5.1"
```

如果你希望保留本地独立配置，也可以：

```bash
cp configs/default.yaml configs/local.yaml
# 然后编辑 configs/local.yaml
```

### 3. 构建

```bash
go build -o nexus-server ./cmd/nexus
```

### 4. 运行

```bash
./nexus-server -config configs/default.yaml
```

默认端口：

- HTTP: `:8080`
- WebSocket: `:8081`

默认节流建议：

- `model.max_concurrency: 1`
- `model.min_request_interval_ms: 2500`

这两个配置是**进程级全局限制**，会同时约束 Lead、Teammate、Reflection 和 Delegate 的 LLM 出口，避免供应商 429 被多角色放大。

### 5. 健康检查

```bash
curl http://127.0.0.1:8080/api/health
```

预期返回：

```json
{"status":"ok"}
```

### 6. 创建会话并聊天

先创建 session：

```bash
curl -X POST http://127.0.0.1:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{"channel":"cli","user":"demo"}'
```

返回示例：

```json
{"session_id":"..."}
```

再调用聊天接口：

```bash
curl -X POST http://127.0.0.1:8080/api/chat \
  -H "Content-Type: application/json" \
  -d '{
    "session_id":"<上一步返回的 session_id>",
    "input":"请读取 configs/default.yaml 中 permission.dangerous_patterns 的所有条目，并逐项列出。",
    "lane":"main"
  }'
```

### 7. WebSocket 交互

建立连接：

```bash
wscat -c ws://127.0.0.1:8081/api/ws
```

发送消息：

```json
{"session_id":"<session_id>","input":"帮我总结一下当前仓库结构","lane":"main"}
```

### 8. 治理与调试

当开启观测后，可以直接访问：

- `GET /debug/dashboard?run=<run_id>`
- `GET /api/debug/metrics?run=<run_id>`
- `GET /api/debug/traces?run=<run_id>`

说明：

- `/api/health`、`/debug/dashboard` 与 `/api/debug/*` 默认视为**公共调试入口**，即使业务接口启用了 auth，也不会要求额外 header。
- 业务接口如 `/api/chat`、`/api/chat/jobs`、`/api/ws` 是否鉴权，取决于 `gateway.auth` 配置。
- 每个 run 的目录下会自动生成 `README.md` 与 `latest-traces.json`，便于离线分析。

## 使用文档

更完整的运行说明、权限模式、smoke test 和常见问题见：

- [docs/usage.md](file:///Users/bytedance/rainea/nexus/docs/usage.md)
- [docs/production.md](file:///Users/bytedance/rainea/nexus/docs/production.md)
- [docs/dispatch_policy.md](file:///Users/bytedance/rainea/nexus/docs/dispatch_policy.md)
- [docs/dispatch_highlight.md](file:///Users/bytedance/rainea/nexus/docs/dispatch_highlight.md)
- [docs/scope_continuity_highlight.md](file:///Users/bytedance/rainea/nexus/docs/scope_continuity_highlight.md)

## 工程亮点

### 1. Lead-Teammate 团队协作调度（核心创新）
`Lead` 通过 Go embedding 嵌入 `Teammate`，共享 ReAct 工具循环、收件箱、名册等基础设施，同时通过 `requestCh` 提供同步 RPC 接口对接网关。模型自主决定三种 dispatch 路径：**直接处理**（简单任务）、**`delegate_task` 临时委派**（干净上下文隔离，执行完即销毁）、**`send_message` 发送给持久 Teammate**（累积对话历史，长期协作）。Teammate 的 `work → idle` 状态机在空闲时按优先级轮询：**收件箱 > 任务板自动认领（角色匹配 + Claim 竞态安全） > 超时自动关闭**。进程重启时 `rehydrate` 从 `config.json` 名册恢复之前活跃的 Teammate，保证服务连续性。

### 2. 基于 Eino Graph 的 RAG 编排
使用 Eino Workflow 实现文档加载 → 分块 → Embedding → 多通道检索 → Rerank → 生成的全流程。向量检索与关键词检索并行执行，通过 Reciprocal Rank Fusion (RRF) 合并排名，后处理链包含去重、归一化、Rerank、TopK 截断。

### 3. Scope / Workstream Continuity（长期协作能力）
Nexus 不再把 team 简单等同于一次 session，而是引入 `scope/workstream` 作为长期工作线边界。`/api/sessions` 可显式传入 `scope` 或 `workstream`，未显式提供时也可基于 continuation cue、摘要检索和阈值决策尝试命中旧工作线。每条工作线有独立的 team 目录、semantic memory、reflection memory 和调试视图；scope 索引持久化后，服务重启后仍可恢复 continuation。对应的决策链会落入日志、trace tags、`/api/debug/scopes` 和 dashboard，形成可解释的长期协作运行时。

### 4. DAG 任务引擎 + 团队自主认领
每个 Task 以独立 JSON 文件持久化在 `.tasks/` 目录，`blockedBy`/`blocks` 双向索引。完成一个任务时自动解锁下游（O(B) 复杂度）。创建依赖前通过 DFS 检测循环（O(V+E)）。空闲 Teammate 通过 `ScanClaimable` 按角色匹配自动认领可执行任务，与 `ClaimLogger` 审计日志配合。

### 5. 三层恢复洋葱
- Layer 1 (工具级)：handler 错误包装为 tool_result 返回 LLM 自修正
- Layer 2 (Context 级)：ContextGuard 检测 token 超限，触发 auto-compact
- Layer 3 (传输级)：指数退避 + 抖动 + 总预算控制

### 6. Named Lane 语义隔离
main/cron/background 各自独立 FIFO 队列 + 独立并发度。main lane max=1 保证用户消息串行（防状态混乱），cron lane max=1 防重入。每个 task 记录 generation，reset 后旧结果自动丢弃（类比 epoch/fencing token）。

## 技术栈

- **语言**: Go 1.22+
- **Agent 框架**: CloudWeGo Eino
- **WebSocket**: gorilla/websocket
- **Cron**: robfig/cron/v3
- **配置**: YAML (gopkg.in/yaml.v3)
- **向量存储**: 内存实现 / Milvus (接口可扩展)
- **关键词存储**: 内存 TF-IDF / Elasticsearch BM25

## 代码规模

- Go 代码: ~15,000+ 行
- 文档: ~4,000+ 行
- 模块: 13 个核心模块
- 工具: 20+ 种工具实现

## License

MIT

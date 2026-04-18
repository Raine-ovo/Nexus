# Nexus 使用文档

## 当前状态

`nexus` 当前已经具备可直接运行的核心能力：

- 主程序可编译：`go build ./cmd/nexus`
- 服务可启动：HTTP `:8080`，WebSocket `:8081`
- 核心 API 可用：`/api/health`、`/api/sessions`、`/api/chat`、`/api/ws`
- 已接入 OpenAI 兼容模型接口，可直接使用智谱 `glm-5.1`
- `semi_auto` 模式下已默认放行安全只读工具，真实 smoke test 已验证 `read_file` 工具链路可用
- 已接入进程级 LLM 节流器，支持 `model.max_concurrency` 与 `model.min_request_interval_ms`
- 已提供 run 级调试入口：`/debug/dashboard`、`/api/debug/*` 与 `.runs/<run_id>/latest-traces.json`
- 已支持 `scope/workstream` 连续性：既可显式绑定工作线，也可让后续 continuation 请求继续命中之前的 team

这不等于“所有扩展能力都零配置可用”。以下部分仍然是按需接入：

- MCP 外部工具服务
- Milvus 向量库
- Elasticsearch 关键词检索
- 更细粒度的生产级权限策略

如果你准备上线或长期运行，部署、日志和反向代理建议见：

- [production.md](file:///Users/bytedance/rainea/nexus/docs/production.md)

如果你准备继续增强 `lead` 的组织能力、区分 teammate 和 subagent 的适用边界，调度策略见：

- [dispatch_policy.md](file:///Users/bytedance/rainea/nexus/docs/dispatch_policy.md)

如果你想快速理解这次 dispatch 增强为什么是当前版本的核心亮点，见：

- [dispatch_highlight.md](file:///Users/bytedance/rainea/nexus/docs/dispatch_highlight.md)

如果你想看这次针对 `planner/devops` 任务认领错配、`working/idle` 状态语义不清等问题的系统性修复，以及为什么它把 task board 从“自由抢单”推进成“遵循调度意图的分配面”，见：

- [task_dispatch_highlight.md](file:///Users/bytedance/rainea/nexus/docs/task_dispatch_highlight.md)

如果你想理解为什么这次 memory / reflection 修复是“主链路激活”而不只是“模块存在”，见：

- [memory_reflection_highlight.md](file:///Users/bytedance/rainea/nexus/docs/memory_reflection_highlight.md)

如果你想理解为什么这次 `scope/workstream` 增强是把 Team Runtime 从“单次任务执行器”推进到“长期工作线运行时”的关键一步，见：

- [scope_continuity_highlight.md](file:///Users/bytedance/rainea/nexus/docs/scope_continuity_highlight.md)

## 前置要求

- Go `1.22+`
- macOS / Linux 均可
- 一个可用的 OpenAI 兼容 API Key

智谱兼容配置示例：

```bash
export NEXUS_API_KEY="your-zhipu-api-key"
export NEXUS_BASE_URL="https://open.bigmodel.cn/api/paas/v4/"
export NEXUS_MODEL="glm-5.1"
```

## 配置方式

### 方式一：直接使用默认配置

`configs/default.yaml` 已支持环境变量展开：

```yaml
model:
  provider: "openai"
  model_name: "glm-5.1"
  api_key: "${NEXUS_API_KEY}"
  base_url: "https://open.bigmodel.cn/api/paas/v4/"
  max_concurrency: 1
  min_request_interval_ms: 2500
```
  min_request_interval_ms: 2500
```

启动前只需要导出环境变量：

```bash
export NEXUS_API_KEY="your-zhipu-api-key"
./nexus-server -config configs/default.yaml
```

### 方式二：复制本地配置

```bash
cp configs/default.yaml configs/local.yaml
```

然后修改：

- `server.http_addr`
- `server.ws_addr`
- `model.api_key`
- `model.base_url`
- `model.max_concurrency`
- `model.min_request_interval_ms`
- `permission.mode`

## 构建与启动

```bash
cd nexus
go build -o nexus-server ./cmd/nexus
./nexus-server -config configs/default.yaml
```

启动成功时，日志中会看到类似输出：

```text
INFO nexus ready http=:8080 ws=:8081
INFO gateway listening addr=:8080
```

## 最小 smoke test

### 1. 健康检查

```bash
curl http://127.0.0.1:8080/api/health
```

预期结果：

```json
{"status":"ok"}
```

`/api/health` 属于公共调试入口，即使开启了 `gateway.auth` 也不会要求 `X-API-Key`。

### 2. 创建 session

```bash
curl -X POST http://127.0.0.1:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{"channel":"cli","user":"demo"}'
```

预期结果：

```json
{"session_id":"<uuid>"}
```

### 3. 调用 `/api/chat`

```bash
curl -X POST http://127.0.0.1:8080/api/chat \
  -H "Content-Type: application/json" \
  -d '{
    "session_id":"<uuid>",
    "input":"请读取 configs/default.yaml 中 permission.dangerous_patterns 的所有条目，并逐项列出。",
    "lane":"main"
  }'
```

这个请求会同时验证三件事：

- 模型调用成功
- Agent 主循环可用
- `read_file` 工具调用可用

## API 说明

### `GET /api/health`

用途：

- 服务健康检查

返回：

```json
{"status":"ok"}
```

备注：

- 与 `/debug/dashboard`、`/api/debug/*` 一样，`/api/health` 默认不受 auth 保护。

### `POST /api/sessions`

请求体：

```json
{
  "channel": "cli",
  "user": "demo"
}
```

如果你希望显式把一个新 session 绑定到某条长期工作线，也可以这样创建：

```json
{
  "channel": "cli",
  "user": "demo",
  "workstream": "team runtime continuity experiment"
}
```

返回体：

```json
{
  "session_id": "...",
  "scope": "workstream:cli-demo-team-runtime-continuity-experiment",
  "workstream": "team runtime continuity experiment"
}
```

字段说明：

- `scope`: 可选，显式指定会话所属团队边界
- `workstream`: 可选，显式指定长期工作线名称
- 若两者都不传，系统仍可在后续 continuation 请求中基于提示词、摘要检索和最近工作线状态尝试复用旧 team

### `POST /api/chat`

请求体：

```json
{
  "session_id": "...",
  "input": "请帮我读取 README.md",
  "lane": "main"
}
```

字段说明：

- `session_id`: 必填，来自 `/api/sessions`
- `input`: 必填，用户输入
- `lane`: 可选，默认 `main`，可选值通常有 `main`、`cron`、`background`

如果你在配置里启用了 `gateway.auth.api_keys` 或 `gateway.auth.jwt_secret`，则该接口需要携带：

```bash
-H "X-API-Key: <your-gateway-key>"
```

返回体：

```json
{
  "output": "..."
}
```

### `POST /api/chat/jobs`

用途：

- 提交长任务到后台执行，避免只靠同步 `/api/chat` 等待结果

请求体：

```json
{
  "session_id": "...",
  "input": "请执行一次较长的治理演练",
  "lane": "main"
}
```

返回体：

```json
{
  "job_id": "...",
  "status": "pending",
  "session_id": "...",
  "lane": "main"
}
```

### `GET /api/chat/jobs/{id}`

用途：

- 查询后台任务状态与最终结果

返回体示例：

```json
{
  "job_id": "...",
  "session_id": "...",
  "lane": "main",
  "status": "succeeded",
  "output": "..."
}
```

### `GET /debug/dashboard`

用途：

- 本地治理页面，展示 run 级 traces、metrics、trace tree、错误高亮和耗时分组
- 也会展示 scopes 卡片、trace 列表中的 scope decision 摘要，以及 trace detail 中的匹配质量

示例：

```bash
open 'http://127.0.0.1:8080/debug/dashboard?run=demo'
```

### `GET /api/debug/metrics`

用途：

- 查看全局或指定 run 的 metrics 快照

示例：

```bash
curl 'http://127.0.0.1:8080/api/debug/metrics?run=demo'
```

### `GET /api/debug/traces`

用途：

- 查看指定 run 的 trace 摘要列表
- trace 摘要会直接带出 `scope`、`workstream`、`scope_decision`、`scope_score`、`scope_threshold`

示例：

```bash
curl 'http://127.0.0.1:8080/api/debug/traces?run=demo'
```

### `GET /api/debug/traces/{id}`

用途：

- 查看单条 trace 的 spans、树结构、错误与耗时聚合
- `scope_summary` 中会包含 `decision`、`reason`、`score`、`threshold` 和候选列表

### `GET /api/debug/scopes`

用途：

- 查看当前系统记住了哪些 `scope/workstream`
- 判断 continuation 命中时到底复用了哪条工作线
- 排查 scope 是否过多、是否串扰、是否在重启后成功恢复

示例：

```bash
curl 'http://127.0.0.1:8080/api/debug/scopes'
```

### `GET /api/ws`

建立 WebSocket 连接后，发送消息格式：

```json
{
  "session_id": "...",
  "input": "帮我总结当前项目结构",
  "lane": "main"
}
```

## 权限模式

当前支持三种权限模式：

- `full_auto`: 自动放行所有非 deny 命中的工具调用，适合调试
- `semi_auto`: 默认模式，只自动放行 allow 规则命中的工具
- `manual`: 全部需要确认

当前默认只读 allow 规则已覆盖以下工具：

- `read_file`
- `list_dir`
- `grep_search`
- `glob_search`
- `load_skill`
- `list_skills`
- `read_inbox`
- `list_teammates`
- `list_pending_requests`
- `search_knowledge`
- `list_knowledge_bases`
- `review_file`
- `check_patterns`
- `list_tasks`
- `get_task`
- `monitor_progress`

这意味着常见的仓库阅读、搜索、任务查看在 `semi_auto` 下已经可以正常工作。

## Run Sandbox

当前版本支持通过 `run.sandbox_dir` 隔离一次运行的内部产物目录，用于减少旧任务、旧记忆和内部输出对新实验的污染。

推荐理解：

- `team.dir`: persistent teammate 的长期目录，默认 `.team`
- `run.sandbox_dir`: 本次运行的隔离目录，只影响 `.tasks`、`.outputs` 内部持久化、`.memory`、reflection memory 等运行态数据

也就是说：

- teammate 仍然是持久存在和可复用的
- 但每次 run 的任务板、语义记忆和内部输出可以被隔离到不同目录

示例：

```yaml
team:
  dir: ".team"

run:
  sandbox_dir: ".runs/audit"
```

启用后，内部运行态路径会变为类似：

- `.runs/audit/.tasks`
- `.runs/audit/.memory/semantic.yaml`
- `.runs/audit/.memory/reflections.yaml`
- `.runs/audit/.outputs`

而 persistent team 仍然在：

- `.team`

## 目录与运行时产物

运行过程中会自动创建或使用以下目录：

- `.tasks/`: 任务状态与 DAG 持久化
- `.team/`: 团队 roster、收件箱等协作状态
- `.memory/`: 语义记忆与反思缓存
- `.outputs/`: Agent 输出落盘目录

这些目录缺失时，当前版本会自动初始化核心部分，不再因为缺少 `.tasks/meta.json` 而启动失败。

如果启用了 `run.sandbox_dir`，则这次运行的任务与记忆不会写到根目录，而会落到 sandbox 中。例如：

- 根 team 目录仍然是 `.team`
- 但任务目录会变成 `.runs/governance/.tasks`

所以在 sandbox run 下，如果你发现根目录 `.tasks/` 没有新任务 JSON，优先去对应的：

- `.runs/<run-name>/.tasks`

查看。

## 常见问题

### 1. `/api/chat` 返回 `unknown session`

原因：

- 还没有先调用 `/api/sessions`
- `session_id` 传错了

修复：

- 先创建 session，再复用返回的 `session_id`

### 2. `/api/chat` 返回权限错误

常见错误：

```text
Permission denied: no matching allow rule
```

原因：

- 当前工具不在 `semi_auto` 默认 allow 范围内

修复：

- 临时切到 `full_auto`
- 或者补充更细的 allow 规则

### 3. 端口已被占用

修改配置中的：

- `server.http_addr`
- `server.ws_addr`

例如：

```yaml
server:
  http_addr: ":18080"
  ws_addr: ":18081"
```

### 4. 模型调用失败

请优先检查：

- `NEXUS_API_KEY` 是否已设置
- `model.base_url` 是否正确
- `model.model_name` 是否与目标模型一致

智谱推荐组合：

```yaml
model:
  provider: "openai"
  model_name: "glm-5.1"
  base_url: "https://open.bigmodel.cn/api/paas/v4/"
  max_concurrency: 1
  min_request_interval_ms: 2500
```

## 建议的本地开发流程

```bash
cd nexus
export NEXUS_API_KEY="your-zhipu-api-key"
export NEXUS_BASE_URL="https://open.bigmodel.cn/api/paas/v4/"
export NEXUS_MODEL="glm-5.1"
go build -o nexus-server ./cmd/nexus
./nexus-server -config configs/default.yaml
```

另开一个终端：

```bash
curl http://127.0.0.1:8080/api/health
curl -X POST http://127.0.0.1:8080/api/sessions \
  -H "Content-Type: application/json" \
  -d '{"channel":"cli","user":"demo"}'
```

再把返回的 `session_id` 带入：

```bash
curl -X POST http://127.0.0.1:8080/api/chat \
  -H "Content-Type: application/json" \
  -d '{
    "session_id":"<uuid>",
    "input":"请读取 configs/default.yaml 中 permission.dangerous_patterns 的所有条目，并逐项列出。",
    "lane":"main"
  }'
```

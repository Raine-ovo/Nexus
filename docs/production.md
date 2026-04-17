# Nexus 生产部署文档

## 目标

本文档面向当前版本的 `nexus`，说明如何将已经打通的主链路部署为稳定可运维的服务，并补全以下内容：

- 生产部署建议
- 目录与配置约定
- 日志输出与采集方式
- 反向代理与端口暴露建议
- 权限、安全和运维边界

## 当前实现边界

当前仓库已经具备以下能力：

- 可编译并启动主服务
- 支持 `GET /api/health`
- 支持 `POST /api/sessions`
- 支持 `POST /api/chat`
- 支持 `GET /api/ws`
- 支持 OpenAI 兼容模型接入，可直接对接智谱 `glm-5.1`
- 支持 `semi_auto` 模式下的默认只读工具放行
- 支持进程级 LLM 节流：`model.max_concurrency` + `model.min_request_interval_ms`
- 支持本地治理入口：`/debug/dashboard`、`/api/debug/*`、run 目录下的 `latest-traces.json`
- 支持优雅退出：响应 `SIGINT` / `SIGTERM`，停止 cron、团队管理器、后台任务，并 flush 内存

当前仓库尚未内置以下生产配套物：

- Dockerfile
- systemd service 文件
- Prometheus `/metrics` HTTP 暴露端点
- 远程 trace 导出器
- 日志轮转配置

这意味着：

- 主服务可以上线
- 但宿主机运维、日志采集、TLS、进程托管和告警仍需要你在部署层补齐

## 推荐部署模式

当前版本最推荐的生产方式是：

- 单机进程常驻
- 由 `systemd` 托管
- 前面挂一层 Nginx 或 Caddy
- 通过环境变量注入模型密钥
- 日志写入标准输出，由 `journald` 或宿主日志系统接管

原因：

- 当前程序天然是单二进制启动
- 日志默认写标准输出
- 配置文件已经支持环境变量展开
- 进程已支持优雅退出

## 目录布局建议

推荐目录结构：

```text
/opt/nexus/
├── bin/
│   └── nexus-server
├── config/
│   └── production.yaml
├── data/
│   ├── .tasks/
│   ├── .team/
│   ├── .memory/
│   ├── .outputs/
│   └── .knowledge/
└── logs/
```

建议说明：

- 二进制放在 `/opt/nexus/bin`
- 配置放在 `/opt/nexus/config`
- 工作目录放在 `/opt/nexus/data`
- 实际运行时从 `/opt/nexus/data` 启动，保证 `.tasks`、`.team`、`.memory` 等相对路径都落在独立数据目录里

## 生产配置建议

建议单独准备 `production.yaml`：

```yaml
server:
  http_addr: ":8080"
  ws_addr: ":8081"
  read_timeout: 30s
  write_timeout: 30s

model:
  provider: "openai"
  model_name: "glm-5.1"
  api_key: "${NEXUS_API_KEY}"
  base_url: "https://open.bigmodel.cn/api/paas/v4/"
  max_tokens: 8192
  temperature: 0.3
  max_concurrency: 1
  min_request_interval_ms: 2500

agent:
  max_iterations: 20
  token_threshold: 100000
  compact_target_ratio: 0.6
  micro_compact_size: 51200
  output_persist_dir: ".outputs"

rag:
  knowledge_dir: ".knowledge"
  vector_backend: "memory"
  keyword_backend: "memory"

memory:
  conversation_window: 20
  max_semantic_entries: 500
  semantic_file: ".memory/semantic.yaml"
  compaction_threshold: 80000

planning:
  task_dir: ".tasks"
  max_background_slots: 2
  cron_poll_interval: 60s

gateway:
  lanes:
    main:
      max_concurrency: 1
    cron:
      max_concurrency: 1
    background:
      max_concurrency: 2

permission:
  mode: "semi_auto"
  workspace_root: "."
  dangerous_patterns:
    - "rm -rf /"
    - "sudo"
    - "chmod 777"
    - "> /dev/sda"
    - "mkfs"

observability:
  trace_enabled: true
  metrics_enabled: true
  log_level: "info"
```

生产建议：

- `temperature` 建议低一点，先用 `0.2 - 0.3`
- `permission.mode` 默认保持 `semi_auto`
- `background.max_concurrency` 不建议一开始开太大
- 智谱这类并发敏感模型建议 `model.max_concurrency: 1`
- 对复杂多 Agent 任务建议加 `model.min_request_interval_ms: 2500-5000`
- 不要把 API key 写死到 YAML，始终通过环境变量注入

注意区分两层限流：

- `gateway.rate_limit`：控制 HTTP 请求入口的每 IP 令牌桶
- `model.max_concurrency` / `model.min_request_interval_ms`：控制进程内所有 LLM 调用的并发和节拍

如果需要做实验运行隔离或定时审计回放，建议额外配置：

```yaml
team:
  dir: ".team"

run:
  sandbox_dir: ".runs/production-<job-or-date>"
```

这样可以保持 persistent teammate 一直使用同一个 `team.dir`，同时把每次 run 的任务板、内部输出和记忆文件隔离到单独 sandbox 中。

## 构建与发布

在发布机或 CI 上构建：

```bash
cd /Users/bytedance/rainea/nexus
go build -o nexus-server ./cmd/nexus
```

发布到目标机：

```bash
mkdir -p /opt/nexus/bin /opt/nexus/config /opt/nexus/data
cp nexus-server /opt/nexus/bin/
cp configs/default.yaml /opt/nexus/config/production.yaml
```

然后编辑：

- `/opt/nexus/config/production.yaml`

## 环境变量

推荐使用单独的环境文件，例如：

```bash
NEXUS_API_KEY=your-zhipu-api-key
NEXUS_BASE_URL=https://open.bigmodel.cn/api/paas/v4/
NEXUS_MODEL=glm-5.1
NEXUS_HTTP_ADDR=:8080
NEXUS_LOG_LEVEL=info
```

注意：

- 当前配置加载支持 YAML 中的 `${VAR}` 展开
- 当配置文件加载失败时，也支持回退到 `LoadFromEnv()`
- 最稳妥的方式仍然是：`production.yaml + 环境变量`

## systemd 部署示例

推荐的 `systemd` unit：

```ini
[Unit]
Description=Nexus Multi-Agent Service
After=network.target

[Service]
Type=simple
User=nexus
Group=nexus
WorkingDirectory=/opt/nexus/data
EnvironmentFile=/opt/nexus/config/nexus.env
ExecStart=/opt/nexus/bin/nexus-server -config /opt/nexus/config/production.yaml
Restart=always
RestartSec=5
TimeoutStopSec=40
LimitNOFILE=65535

NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true
ReadWritePaths=/opt/nexus/data

StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

启用方式：

```bash
sudo cp nexus.service /etc/systemd/system/nexus.service
sudo systemctl daemon-reload
sudo systemctl enable nexus
sudo systemctl start nexus
```

检查状态：

```bash
sudo systemctl status nexus
sudo journalctl -u nexus -f
```

## 日志配置

### 当前日志能力

## 鉴权与调试入口

当前默认边界如下：

- `/api/chat`、`/api/chat/jobs`、`/api/sessions`、`/api/ws`、`/mcp/*`：是否鉴权取决于 `gateway.auth`
- `/api/health`、`/debug/dashboard`、`/api/debug/*`：默认免鉴权，便于浏览器直接查看调试页和 run 级观测

生产建议：

- 若需要暴露到公网，建议通过反向代理或内网访问控制限制 `/debug/*` 与 `/api/debug/*`
- 对外 API 和内部治理入口不要使用同一套暴露策略

当前程序日志由 `internal/observability` 输出到标准输出，格式近似：

```text
nexus 2026/04/16 15:25:56.697051 INFO nexus ready http=:18080 ws=:18081
```

特点：

- 标准输出
- 带时间戳
- 带级别
- 带键值对

当前可配置项：

- `observability.log_level`

支持级别：

- `debug`
- `info`
- `warn`
- `error`

生产建议：

- 默认使用 `info`
- 排障时临时切到 `debug`
- 不建议长期在高并发下保持 `debug`

### 推荐日志采集方式

当前最稳的方案：

- 应用写标准输出
- `systemd/journald` 负责接收
- 宿主机日志代理再转发到 ELK、Loki 或其他平台

推荐链路：

```text
nexus stdout -> journald -> promtail / fluent-bit / filebeat -> 日志平台
```

### 日志保留建议

建议至少保留：

- 启动日志
- 模型调用错误
- 权限拒绝日志
- cron 执行日志
- gateway 错误

如果宿主机侧做日志过滤，建议保留以下关键字：

- `nexus ready`
- `gateway listening`
- `shutdown signal received`
- `cron`
- `permission`
- `error`

## 观测与指标

### 当前状态

当前仓库内部已经有：

- `Tracer`
- `MetricsCollector`
- `CallbackHandler`

但这些能力目前主要还是：

- 进程内存态
- 供内部回调和调试使用
- 尚未通过独立 HTTP 端点导出

所以当前生产可观测性结论是：

- 日志可用
- 内部 metrics/trace 基础已存在
- 但 Prometheus / OpenTelemetry 级别的外部集成还需要后续补

### 当前阶段建议

如果你现在就要上线：

- 先把日志接起来
- 用 `/api/health` 做存活探针
- 用 systemd restart 和外部监控做第一层可靠性保障

如果你准备继续增强：

- 增加 `/metrics` 导出
- 增加 trace 导出器
- 给关键业务路径补 metrics 名称规范

## 反向代理建议

当前服务本身只监听 HTTP / WS，不处理 TLS 证书。生产建议前置一层反向代理。

推荐暴露方式：

- 外部用户 -> HTTPS 443
- 反向代理 -> 本地 `127.0.0.1:8080`

### Nginx 示例

```nginx
server {
    listen 443 ssl http2;
    server_name nexus.example.com;

    ssl_certificate     /etc/letsencrypt/live/nexus/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/nexus/privkey.pem;

    location /api/ws {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
    }

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

生产建议：

- 只让反向代理暴露公网入口
- `nexus` 只绑定内网地址或本地地址
- 在代理层做 TLS、访问控制和限流

## 权限与安全建议

### 权限模式

生产环境默认推荐：

- `semi_auto`

原因：

- 现在它已经能自动放行安全只读工具
- 不会把写文件、shell、危险操作全部自动放开

不建议：

- 长期使用 `full_auto`

适用场景：

- 临时排障
- 本地实验
- 封闭环境验证

### 密钥管理

必须遵守：

- 不要把 API key 提交到仓库
- 不要把密钥写进 `README` 或默认配置
- 使用环境变量或密钥管理系统注入

推荐：

- systemd `EnvironmentFile`
- 机密管理系统
- CI/CD 部署变量

### 工作目录隔离

当前工具大量依赖 `workspace_root` 与相对路径，生产上建议：

- 单独工作目录
- 单独系统用户
- 最小可写权限

## 运行检查清单

上线前建议检查：

- 二进制是否能成功启动
- `/api/health` 是否返回 `ok`
- `/api/sessions` 是否正常创建 session
- `/api/chat` 是否能正常调用模型
- `semi_auto` 模式下只读工具是否可用
- 反向代理是否正确转发 WebSocket
- journald 是否已接收到日志
- API key 是否通过环境变量注入

## 故障排查

### 1. 服务启动失败

优先查看：

```bash
sudo journalctl -u nexus -n 200 --no-pager
```

重点检查：

- 配置文件路径
- 工作目录权限
- 端口占用
- `NEXUS_API_KEY` 是否存在

### 2. `/api/chat` 返回模型错误

检查：

- `model.base_url`
- `model.model_name`
- `NEXUS_API_KEY`
- 外网连通性

智谱推荐值：

```yaml
model:
  provider: "openai"
  model_name: "glm-5.1"
  base_url: "https://open.bigmodel.cn/api/paas/v4/"
  max_concurrency: 1
  min_request_interval_ms: 2500
```

### 3. `/api/chat` 返回权限拒绝

常见原因：

- 请求触发了写操作或执行类工具
- 当前 `semi_auto` 默认只放行只读工具

处理方式：

- 补充 allow 规则
- 或针对某一类操作设计单独策略

### 4. 数据目录异常膨胀

重点关注：

- `.outputs/`
- `.memory/`
- `.tasks/`
- `.team/`

生产建议：

- 定期归档
- 做磁盘告警
- 给日志和工作目录设置容量阈值

## 后续增强建议

如果你准备把 `nexus` 继续推进到更完整的生产态，建议按优先级做：

1. 增加 `/metrics` 端点
2. 增加结构化 JSON 日志模式
3. 增加 OpenTelemetry trace 导出
4. 增加官方 Dockerfile
5. 增加官方 systemd/service 模板
6. 增加可配置 allow/deny 规则持久化

## 相关文档

- [README.md](file:///Users/bytedance/rainea/nexus/README.md)
- [usage.md](file:///Users/bytedance/rainea/nexus/docs/usage.md)
- [architecture.md](file:///Users/bytedance/rainea/nexus/docs/architecture.md)

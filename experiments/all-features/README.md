# All-Features Experiment

这套实验用于尽可能触发 Nexus 当前已经接入链路的关键 agent 与功能。

## 覆盖目标

- Lead 调度
- planner persistent teammate
- devops persistent teammate
- knowledge delegate_task
- code_reviewer delegate_task
- team messaging / read_inbox / claim_task
- task board 持久化
- reflection
- semantic memory
- run sandbox
- dashboard / traces / metrics / latest-traces.json
- MCP server
- MCP client + remote tool registration
- WebSocket follow-up
- auth / rate limit / job API

## 关键文件

- `full-activation-throttled_prompt.txt`: 当前主实验任务
- `activation_prompt.txt`: 早期 all-features-exp 实验任务（保留兼容）
- `mcp_mock_server.py`: 外部 MCP mock server
- `run_experiment.py`: 自动化执行与取证脚本
- `ws_probe.go`: WebSocket 验证脚本
- `../../configs/full-activation-throttled.yaml`: 当前主实验配置
- `../../configs/all-features.experiment.yaml`: 早期 all-features-exp 配置（保留兼容）

## 运行步骤

1. 启动 MCP mock server
2. 启动 Nexus:

```bash
go run ./cmd/nexus -config configs/full-activation-throttled.yaml
```

3. 执行实验:

```bash
python3 experiments/all-features/orchestrate_experiment.py \
  --config configs/full-activation-throttled.yaml \
  --sandbox /Users/bytedance/rainea/nexus/.runs/full-activation-throttled \
  --base-url http://127.0.0.1:18214 \
  --ws-url ws://127.0.0.1:18215/api/ws \
  --api-key nexus-local-dev-key \
  --prompt /Users/bytedance/rainea/nexus/experiments/all-features/full-activation-throttled_prompt.txt \
  --run-name full-activation-throttled \
  --keep-nexus-alive
```

说明：

- `run_experiment.py` / `orchestrate_experiment.py` 仍保留 `all-features-exp` 默认值以兼容旧实验。
- 当前推荐的主实验名是 `full-activation-throttled`，因为它使用了更保守的 LLM 节流配置，并支持 `--keep-nexus-alive` 方便持续查看 dashboard。
- 关于这次 why `semantic memory` / `reflection` 从“代码里存在”补到“run 目录里可见”的主链路亮点总结，见 `docs/memory_reflection_highlight.md`。

## 结果位置

- 当前主实验：
  - 运行态落盘: `.runs/full-activation-throttled/`
  - 实验证据归档: `.runs/full-activation-throttled/experiment/`
  - 内部产物: `.runs/full-activation-throttled/.outputs/full_activation_run/`
- 兼容保留的旧实验：
  - `.runs/all-features-exp/`

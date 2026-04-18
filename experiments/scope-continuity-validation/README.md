# Scope Continuity Validation

这是一套独立实验，用来真实验证：

- `scope/workstream` 复用
- 跨 session continuation
- 跨重启 continuation 恢复
- 与 `nexus` 现有主链路能力的联动验证

## 设计原则

这个 feat 不能只靠一次任务验证，因为真正要验证的是：

- 第二次请求能否继续第一次任务建立的 workstream
- 服务重启后第三次请求能否继续前面的 workstream

因此这套实验被设计为三阶段：

1. `phase1_main_task`: 建立主工作线，触发尽可能多的已有功能
2. `phase2_new_session_continuation`: 新开 session，用 continuation prompt 继续同一工作线
3. `phase3_restart_resume`: 重启 Nexus 后，再新开 session 继续同一工作线

## 实验资产

- `config.yaml`: 本实验的独立配置
- `prompt.txt`: phase1 主任务 prompt
- `phase2_continuation_prompt.txt`: phase2 continuation prompt
- `phase3_restart_resume_prompt.txt`: phase3 重启后 continuation prompt
- `run_experiment.py`: 单阶段执行器
- `orchestrate_experiment.py`: 三阶段总编排器
- `ws_probe.go`: WebSocket follow-up 验证脚本

## 运行方式

```bash
python3 experiments/scope-continuity-validation/orchestrate_experiment.py \
  --config experiments/scope-continuity-validation/config.yaml \
  --sandbox /Users/bytedance/rainea/nexus/.runs/scope-continuity-validation \
  --base-url http://127.0.0.1:18224 \
  --ws-url ws://127.0.0.1:18225/api/ws \
  --api-key nexus-local-dev-key \
  --run-name scope-continuity-validation
```

如果你想保留服务继续观察 dashboard：

```bash
python3 experiments/scope-continuity-validation/orchestrate_experiment.py \
  --config experiments/scope-continuity-validation/config.yaml \
  --sandbox /Users/bytedance/rainea/nexus/.runs/scope-continuity-validation \
  --base-url http://127.0.0.1:18224 \
  --ws-url ws://127.0.0.1:18225/api/ws \
  --api-key nexus-local-dev-key \
  --run-name scope-continuity-validation \
  --keep-nexus-alive
```

## 产物位置

- 运行 sandbox: `.runs/scope-continuity-validation/`
- 实验归档: `.runs/scope-continuity-validation/experiment/`
- 主工作线输出: `.runs/scope-continuity-validation/.outputs/scope_continuity_run/`

`experiment/` 下会额外生成：

- `phase1_main_task/`
- `phase2_new_session_continuation/`
- `phase3_restart_resume/`
- `summary.json`

每个 phase 目录至少包含：

- `session.json`
- `job_created.json`
- `job_result.json`
- `scopes.json`
- `traces.json`
- `trace_detail.json`
- `dashboard.html`
- `phase_summary.json`

## 验收重点

最终重点不是 phase1 跑了多少功能，而是：

- phase2 的 `scope_summary.scope` 是否与 phase1 一致
- phase3 的 `scope_summary.scope` 是否在重启后仍与 phase1 一致
- phase2 / phase3 的 `scope_summary.decision` 是否是 continuation 相关决策
- 是否仍持续写入同一输出目录
- dashboard / traces / scopes 是否能看到连续命中同一工作线

## 目录约束

本实验的所有相关文件都放在当前目录下，不复用 `experiments/all-features/` 下的实验文件，也不把配置放回全局 `configs/` 目录。

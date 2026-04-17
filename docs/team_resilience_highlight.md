# Team 韧性亮点

## 这次改动解决了什么

这次改动聚焦 `internal/team/` 的一个真实运行痛点：持久化 teammate 在长链路任务中可能因为空闲超时过早 `shutdown`，随后出现两类连锁问题：

- 任务已经有中间产出，但 `TaskDAG` 里仍停留在 `in_progress`
- Lead 想继续 `send_message` 跟进时，目标 worker 已经退出，协作链路被中断

这不是单点 bug，而是 **生命周期管理、任务认领状态、团队消息路由** 三个子系统之间的耦合问题。此次修复把这三条链路补齐，让 Team Runtime 从“能跑”提升到“能稳定续跑、自我恢复”。

## 三个核心亮点

### 1. Persistent teammate 的 idle 策略可配置

此前普通 teammate 的 idle 行为接近硬编码：

- `poll_interval` 固定为 3 秒
- `maxIdlePolls` 默认 40
- 等价于大约 2 分钟无消息/无任务就自动退出

这对短任务是合理的，但对需要异步 follow-up 的持久协作场景过于激进。现在 Team 层新增了显式配置：

- `team.poll_interval`
- `team.idle_timeout`

默认值调整为：

- `poll_interval: 3s`
- `idle_timeout: 20m`

这意味着 persistent teammate 的“等待 follow-up 窗口”从分钟级提升到可配置的工程参数，适合真实多智能体长任务。

### 2. Worker shutdown 时自动回收 orphan claim

此前如果 teammate 带着 `in_progress` 任务退出，会出现：

- worker 已经 `shutdown`
- 任务依旧保留 `claimed_by`
- 下游任务因为依赖未完成而长期 `blocked`

本次补上了 shutdown 收口逻辑：

- teammate 退出时会扫描自己名下 claim
- `in_progress` 任务会自动释放
- 若前置条件满足则回退为 `pending`
- 若前置条件不满足则回退为 `blocked`
- `claimed_by`、`claimed_at`、`claim_source` 一并清理

这样即使 worker 被回收，任务图也不会留下“无人接手、却不可再次认领”的僵尸状态。

### 3. `send_message` 支持自动唤醒 shutdown teammate

此前 `send_message` 只接受活着的 persistent teammate。只要目标 worker 已经 `shutdown`，Lead 就会被迫失败，必须重新显式 `spawn_teammate` 才能继续。

现在逻辑升级为：

- `send_message` 遇到目标 teammate 为 `shutdown`
- 先根据名册中的 `(name, role)` 自动恢复该 worker
- 注入 resume prompt
- 然后再把 follow-up 消息投递到 inbox

这使 `send_message` 从“仅投递消息”升级成了“必要时自动恢复协作关系的消息入口”，更符合 persistent teammate 的产品语义。

## 为什么这是亮点

### 从被动容错到主动自愈

这次不是简单延长超时时间，而是把 Team Runtime 的恢复能力补成闭环：

1. worker 可以更久地等待 follow-up
2. 即使 shutdown，也不会把任务 claim 永久卡死
3. Lead 再次发送消息时，可以自动把 worker 唤醒继续协作

结果是：**生命周期、任务状态、消息路由首次形成了自洽闭环**。

### 从“状态一致性依赖模型”变成“状态一致性由运行时兜底”

过去系统默认认为：

- teammate 会自己完成任务
- teammate 会自己更新状态
- lead 会及时跟进

但真实运行里这三件事并不总成立。此次修复后，运行时主动承担了更多一致性职责：

- idle 策略由配置控制，而不是靠 prompt 猜测时机
- task claim 释放由 shutdown hook 负责，而不是完全依赖模型记得收尾
- follow-up 路由由 manager 自动恢复 worker，而不是要求模型重新组织团队

### 更贴近真实多智能体生产链路

这次增强对真实 workload 的价值非常直接：

- 复杂任务中，Lead 可以放心在数分钟后继续 follow-up
- Teammate 短暂退出不会把 TaskDAG 卡死
- 消息发送失败不再天然意味着协作关系中断

这类修复不只提升“单次 demo 成功率”，更重要的是提升 **长时运行稳定性** 和 **实验结果可复现性**。

## 对外可强调的表述

如果需要把这次改动作为项目亮点对外表达，最值得强调的是三句话：

- Nexus 的 persistent teammate 不再只是“长驻上下文”，而是具备了 **可配置生命周期**。
- Team Runtime 不再依赖模型手工收尾，而是具备了 **任务 claim 自动回收能力**。
- Lead 与 teammate 的 follow-up 不再怕 worker 退出，`send_message` 已经具备 **自动唤醒协作者** 的自愈语义。

## 一句话总结

这次改动把 Nexus Team 从“能派活的多智能体框架”推进成了“**在真实长链路任务里可恢复、可续跑、可自愈** 的团队运行时”。

# Task Routing / Claim Governance 亮点

## 这次修复解决了什么

这次修复聚焦的是 `Nexus` Team Runtime 在真实多角色协作里一个非常关键的闭环缺口：

- `dispatch_profile` 已经能约束 `lead` 选择 `delegate_task`、`spawn_teammate`、`send_message`
- persistent teammate、task board、claim logger、dashboard 也都已经接到了主链路
- 但 task board 本身仍然更像一个“**自由抢单池**”，而不是“**遵循调度意图的工作分配面**”

这会带来两个直接问题：

### 1. 任务认领和调度意图脱节

在旧模型下：

- `lead` 可以决定“这个方向应该交给 `devops`”
- 但 task 一旦落盘，只要没有明确 `claim_role`
- 任何 active teammate 都可能先一步 `claim_task`

也就是说，`dispatch` 决定的是“**谁应该干**”，而 `claim` 决定的却是“**谁先抢到谁干**”。

这会把本来已经比较清晰的 Team 组织语义重新拉回机会主义抢单。

### 2. teammate 状态无法解释当前到底在做什么

在旧实现里：

- `working` 主要表示 goroutine 当前处在 work loop
- `idle` 主要表示正在 idle polling

但它并不区分：

- 正在处理初始 prompt
- 正在读 inbox follow-up
- 正在执行自己认领的 task
- 正在做没有 claim 的自驱型工作

于是用户会看到一种很迷惑的状态：

- teammate 显示 `working`
- 但 claim log 里没有它的任务认领记录

这会让 dashboard 和实验观察很难准确回答：

- 它到底是在忙什么？
- 它是在执行 task board 吗？
- 还是只是在跑启动 prompt？

## 为什么这个修复重要

### 它补的是 Team Runtime 的“最后一公里闭环”

如果没有这次修复，系统虽然已经有：

- team dispatch
- persistent teammate
- task board
- observability

但它们之间仍然缺一个关键闭环：

- `lead` 的调度判断无法稳定约束 task board 的后续 claim

这意味着系统组织能力仍然停留在：

- “前半段会组织”

而没有真正进化到：

- “从组织决策到任务落盘再到后续认领，整个链路都遵循同一套调度语义”

### 它把“任务分派”从 prompt 暗示提升成运行时机制

以前如果想让 `devops` 处理 infra 任务，很多时候只能依赖：

- 初始 prompt 写得足够明确
- 或者模型自己记得这件事

这类约束很脆弱，因为它们没有真正进入持久化的任务状态。

现在不同了：

- 调度意图会写入 task metadata
- 后续 claim 会检查 metadata
- 审计和调试面也能看到这层信息

所以这不是简单修了一个 `claim_task` bug，而是把“**调度意图**”正式纳入 TaskDAG 的持久化模型。

## 这次修复的三个核心亮点

## 1. 任务从“自由抢单”升级成“可指派 + 可约束认领”

### 新增了显式任务指派元数据

现在每个 task 除了原有字段外，还可以持久化：

- `assigned_to`
- `assigned_role`
- `assigned_by`
- `assign_reason`
- `assigned_at`

同时保留并兼容：

- `claim_role`

这意味着任务板现在不再只是“谁能抢到谁拿走”，而是能表达：

- 这项任务应该交给哪个 persistent teammate
- 或至少应该由哪个 specialist role 来认领
- 是谁做出的这次指派
- 为什么这样分派

### `assign_task` 正式成为 team tool

这次新增了 `assign_task`，允许 `lead` 或 teammate 在 task 落盘后先做一次明确的路由动作：

- 可以按 teammate 指派
- 可以按 role 指派
- 可以带简短的 routing reason

这一步的意义非常大，因为它让“调度决策”第一次在 task board 层拥有了显式入口。

### `claim_task` 和自动抢单现在会校验指派约束

修复后的 claim 规则不再只看：

- `pending`
- `blocked_by`
- 是否已被认领

还会额外检查：

- 如果 `assigned_to` 已存在，只有目标 teammate 才能 claim
- 如果 `assigned_role` / `claim_role` 已存在，只有匹配 role 才能 claim

这使 task board 的行为从：

- `first-come-first-served`

变成：

- `dispatch-intent-aware claim`

也就是：**任务后续认领开始真正服从前置调度意图**。

## 2. teammate 状态从粗粒度变成可解释的活动状态

### Roster / config 不再只有 `(name, role, status)`

这次修复后，成员状态除了原有的：

- `name`
- `role`
- `status`

还会额外记录：

- `activity`
- `claimed_task_id`
- `updated_at`

这一步解决的是“working 到底在忙什么”的长期可解释性问题。

### 现在可以区分四类典型 activity

当前已经接入的活动语义包括：

- `waiting_for_request`
- `waiting_for_work`
- `processing_inbox`
- `self_directed`
- `claimed_task`
- `shutdown`

这意味着用户和 dashboard 不再只能看到：

- `working`
- `idle`

而是可以看见更有业务语义的状态组合，例如：

- `status=working + activity=self_directed`
- `status=working + activity=claimed_task + claimed_task_id=4`
- `status=idle + activity=waiting_for_work`

### 这让 scope-continuity-validation 的现象第一次变得可解释

此前实验里最迷惑人的点是：

- `devops` 看起来是 `working`
- 但没有 claim

修复后，这种情况会更准确地呈现为：

- `status=working`
- `activity=self_directed`

也就是说，它确实在工作，但不是在执行 task board。

这会让实验观察、dashboard 阅读和调试判断都清晰很多。

## 3. claim 现在有完整审计链，而不是只剩瞬时状态

### 手动 claim 会记录真实 role

旧实现里 manual claim 的日志容易缺 `role`，导致事后只能知道：

- 谁认了任务

但不知道：

- 认领者当时扮演的角色是什么

现在这条链已经补齐，manual / auto 都会把 role 带进 claim 记录。

### task 完成后保留 `last_claim_*`

旧模型下，一旦 task 从 `in_progress` 进入其他状态：

- `claimed_by`
- `claimed_at`
- `claim_source`

通常会被清理掉。

这虽然有利于表达“当前没有 active claim”，但会让调试和复盘失去历史证据。

现在新增了：

- `last_claimed_by`
- `last_claimed_role`
- `last_claimed_at`
- `last_claim_source`

这样系统同时保留两种信息：

- **当前状态**：现在是否仍被认领
- **历史审计**：最后一次是谁、以什么角色、通过什么方式认领的

这让任务板具备了更完整的治理价值。

## 机制变化概览

## A. TaskDAG 成为 dispatch-aware 持久化层

这次修复后，TaskDAG 不再只是：

- 依赖图
- 状态机
- claim 锁

它还开始承担：

- dispatch metadata carrier

也就是：

- 调度决策不只存在于 `lead` 的短期上下文里
- 还会继续存在于 task 的磁盘状态中

这使 TaskDAG 从“执行辅助结构”升级成“团队调度闭环的一部分”。

## B. planner 可以在 `create_plan` 阶段直接埋 routing hint

这次增强后，planner 创建任务时可以直接写：

- `claim_role`
- `assigned_to`

这意味着计划生成不再只是“拆任务”，也可以顺便完成：

- 初步工种划分
- 任务路由预埋

例如：

- infra / dashboard / metrics 类任务直接标给 `devops`
- task board 汇总类任务标给 `planner`

这样后续 persistent teammate 的 claim 行为就不会再完全靠抢。

## C. lead / teammate system prompt 同步增强

为了避免运行时虽然支持 `assign_task`，但模型仍然忘记使用，这次也同步补了系统提示：

- `lead` 被提示在 task board item 应归属特定 teammate 或 role 时，优先使用 `assign_task`
- `planner` 被提示在 `create_plan` 阶段就写 routing hint
- teammate 侧系统提示也知道可以在 handoff 前使用 `assign_task`

这保证了修复不仅存在于代码，还会反向影响模型的调度习惯。

## 为什么这是一个可对外讲的亮点

### 亮点 1：把 dispatch 从“前置决策”延伸成“全链路约束”

很多系统即使有调度策略，也只在最前面决定：

- 要不要 delegate
- 要不要 spawn teammate

但任务一旦进了任务板，就重新回到“谁先抢到谁做”。

这次修复的亮点在于：

- dispatch 不再只管开头
- 它开始约束 task board 的后续行为

这让 Team Runtime 的组织能力从“开局像团队”提升到“全程都像团队”。

### 亮点 2：把状态可视化从“活着没活着”升级成“到底在忙什么”

如果 dashboard 只能显示：

- working
- idle
- shutdown

那它更像进程监控，而不是团队治理面板。

这次修复后，状态开始带上活动语义：

- 是在处理 follow-up
- 还是在执行已认领 task
- 还是在做未认领的自驱工作

这使 Team 面板第一次更接近真正的“治理视图”。

### 亮点 3：修复的是 scope-continuity-validation 实验暴露出的系统性问题

这次修复不是拍脑袋加功能，而是来自一次非常具体的实验反馈：

- `planner` 把不该全拿的任务都拿了
- `devops` 状态看起来在忙，但 task board 没体现
- 用户无法从配置和 claim log 理解到底发生了什么

修复后，实验里最关键的两个观测问题都得到了解决：

- 为什么某个 teammate 显示 `working`
- 为什么某个任务最终是某个角色认领

这使实验从“看到了奇怪现象”变成“能够解释并验证组织机制是否符合预期”。

## 对外可强调的表述

如果把这次修复作为项目亮点展示，最值得强调的是四句话：

- Nexus 的 task board 不再是自由抢单池，而是开始遵循 `lead` 调度意图的任务分配面。
- persistent teammate 的状态不再只有粗粒度 `working/idle`，而是具备了可解释的活动语义。
- 任务认领不再只有瞬时锁语义，还具备了 assignment metadata 和 `last_claim_*` 审计链。
- 这项修复让 dispatch、task board、roster、dashboard 第一次形成了真正一致的团队治理闭环。

## 相关代码

- [task.go](file:///Users/bytedance/rainea/nexus/internal/planning/task.go)
- [task_test.go](file:///Users/bytedance/rainea/nexus/internal/planning/task_test.go)
- [tools.go](file:///Users/bytedance/rainea/nexus/internal/team/tools.go)
- [teammate.go](file:///Users/bytedance/rainea/nexus/internal/team/teammate.go)
- [roster.go](file:///Users/bytedance/rainea/nexus/internal/team/roster.go)
- [lead.go](file:///Users/bytedance/rainea/nexus/internal/team/lead.go)
- [agent.go](file:///Users/bytedance/rainea/nexus/internal/agents/planner/agent.go)

## 相关文档

- [dispatch_highlight.md](file:///Users/bytedance/rainea/nexus/docs/dispatch_highlight.md)
- [dispatch_policy.md](file:///Users/bytedance/rainea/nexus/docs/dispatch_policy.md)
- [scope_continuity_highlight.md](file:///Users/bytedance/rainea/nexus/docs/scope_continuity_highlight.md)
- [team_resilience_highlight.md](file:///Users/bytedance/rainea/nexus/docs/team_resilience_highlight.md)

## 一句话总结

这次修复把 Nexus Team Runtime 从“会调度、会落任务、会显示状态，但三者还没真正对齐”，推进成了“**调度意图、任务认领、成员状态、审计观测四条链路一致闭环**”的团队运行时。

# Nexus 调度策略设计

## 目标

本文档定义 `Nexus` 中 `lead` 的协作调度策略，核心目标不是让用户显式指定“必须用 teammate”或“必须用 subagent”，而是让系统根据任务形态、上下文需求和执行风险，**自主选择**以下四种执行模式：

- `direct`: `lead` 直接完成
- `delegate_task`: 使用隔离型一次性 subagent
- `send_message`: 把工作发给已有 persistent teammate
- `spawn_teammate`: 创建新的 persistent teammate，再通过消息驱动其持续工作

本文档同时覆盖当前已经实现的规则、推荐的判定 rubric、失败回退策略，以及定时任务的特殊处理。

## 核心原则

### 用户只描述目标，不描述组织结构

调度策略的第一原则是：

- 用户负责描述任务目标
- 系统负责决定组织方式

这意味着下列信息原则上不应由用户硬编码：

- 是否一定要创建 teammate
- 是否一定要使用 delegate_task
- 是否一定要通过 team 内部通信推进

用户当然可以提出偏好，但最终执行机制应由 `lead` 基于内部策略自主决定。

### 组织结构要为任务服务

调度策略应最小化以下问题：

- `lead` 上下文被长期任务污染
- 一次性任务错误地沉淀成长期 worker
- 长期任务被错误地放进临时 delegate，导致上下文丢失
- 已有 teammate 不能复用，系统不断重复创建新 worker
- team 工具存在但实际长期闲置

### 调度决策必须可解释

每次关键调度都应该能回答三个问题：

1. 为什么不是 `lead` 自己做？
2. 为什么不是 `delegate_task`？
3. 为什么要复用或新建 persistent teammate？

这也是后续做 trace、复盘和策略优化的基础。

## 当前实现现状

### `lead` 的四种可用执行模式

当前 `lead` 的工具和模板组合已经具备以下执行能力：

- `direct`: `lead` 通过 `read_file`、`grep_search`、`bash` 等基础工具直接工作
- `delegate_task`: 使用角色模板在干净上下文中运行一次性任务
- `send_message`: 给已有 teammate 发消息，复用其上下文与长期状态
- `spawn_teammate`: 创建新的 persistent teammate，并加入 roster

相关位置：

- [main.go](file:///Users/bytedance/rainea/nexus/cmd/nexus/main.go#L128-L190)
- [tools.go](file:///Users/bytedance/rainea/nexus/internal/team/tools.go#L24-L33)
- [delegate.go](file:///Users/bytedance/rainea/nexus/internal/team/delegate.go#L16-L26)
- [manager.go](file:///Users/bytedance/rainea/nexus/internal/team/manager.go#L138-L209)

### 当前已内置的规则

目前最明确的系统级约束已经体现在 `leadSystemPrompt` 和 cron 包装逻辑里：

- **scheduled task**:
  - 不能由 `lead` 直接完成
  - 不能使用 `delegate_task`
  - 必须先通过 planner/team 持久化为 `.tasks/` 中的任务
  - 再交给 persistent teammate

相关位置：

- [main.go](file:///Users/bytedance/rainea/nexus/cmd/nexus/main.go#L134-L154)
- [buildScheduledAgentTurnPrompt](file:///Users/bytedance/rainea/nexus/cmd/nexus/main.go#L290-L317)

这是一个好的开始，但它目前主要覆盖了 **cron/scheduled task**，对普通用户请求的 dispatch policy 仍然偏弱。

## 问题定义

### 当前主要短板

从最近的实际运行现象看，当前策略存在以下问题：

- `lead` 在非定时任务上仍然倾向于优先选择 `delegate_task`
- persistent teammate 的创建缺乏明确阈值
- 已有 teammate 的复用规则不够强
- `delegate_task` 缺少结果质量回查，容易在复杂任务里产生幻觉结果
- team 内部消息流虽然可用，但不是默认的协作主路径

换句话说，系统“能”做组织调度，但“不会稳定地这样做”。

## 目标调度模型

推荐把 `lead` 的调度拆成两个阶段：

### 阶段一：任务分类

`lead` 在调用任何执行工具前，先做一次任务建模，回答：

1. 这是一次性任务还是长期任务？
2. 是否需要持续上下文？
3. 是否需要多角色协作？
4. 是否需要任务持久化与跟踪？
5. 是否属于 scheduled / cron 驱动？
6. 是否包含风险操作，需要计划审查？

### 阶段二：执行机制选择

基于分类结果，再在四种模式里选择：

- `direct`
- `delegate_task`
- `send_message`
- `spawn_teammate`

这两阶段分离能减少“边想边调工具”的短视选择。

## 推荐判定 Rubric

### 1. `direct`

满足以下条件时，优先由 `lead` 自己完成：

- 任务简单，预期 <= 2 个步骤
- 不需要长期上下文
- 不需要跨多个角色
- 没必要把状态沉淀到 team
- 结果一次返回即可

典型例子：

- 读取单个配置文件
- 搜索一个符号定义
- 简短解释当前模块职责

### 2. `delegate_task`

满足以下条件时，优先使用 `delegate_task`：

- 任务边界清晰
- 是一次性专项分析
- 上下文隔离比复用历史更重要
- 结果一次返回即可
- 不需要后续持续协作

典型例子：

- “审查这个文件是否有安全问题”
- “总结这两个文档的差异”
- “只针对某个模块做一次性架构分析”

### 3. `send_message`

满足以下条件时，优先把工作发给已有 teammate：

- 已经存在角色匹配的 persistent teammate
- 当前任务与它之前负责的方向连续
- 需要复用已有上下文
- 预期后续仍会继续跟进

典型例子：

- “继续上次那个架构评估”
- “让已经在看权限系统的 teammate 再补一份风险分级”
- “让已有 devops teammate 接着完善部署计划”

### 4. `spawn_teammate`

满足以下条件时，优先创建新的 persistent teammate：

- 当前方向会持续推进
- 任务明显超过一次性分析范围
- 需要独立上下文长期沉淀
- 已有 roster 中不存在合适 worker
- 后续大概率需要多轮协作和消息往返

典型例子：

- 新开一个持续推进的权限审计方向
- 新开一个长期维护的生产部署/运维方向
- 需要专门角色持续认领任务板中的子任务

## 推荐决策表

| 场景 | direct | delegate_task | send_message | spawn_teammate |
|------|--------|---------------|--------------|----------------|
| 简单一次性查询 | 高 | 低 | 低 | 低 |
| 一次性专项分析 | 低 | 高 | 低 | 低 |
| 已有 teammate 的续作 | 低 | 低 | 高 | 低 |
| 新方向的长期工作流 | 低 | 低 | 中 | 高 |
| 定时任务 / scheduled task | 禁止 | 禁止 | 高 | 高 |
| 高风险变更前的计划阶段 | 低 | 中 | 高 | 中 |

## 特殊规则

### scheduled / cron 任务

这是当前最明确且最应该保留的强规则：

- `lead` 不直接执行
- `lead` 不使用 `delegate_task`
- 先落到 `.tasks/`
- 再由 persistent teammate claim 并推进

原因：

- scheduled work 更偏长期自治
- 需要任务可追踪
- 需要持久 worker 能恢复
- 需要避免每次 cron 触发都重新构造一次性上下文

### 风险操作

涉及以下场景时，不应直接进入执行：

- 写文件
- Shell / 变更命令
- 外部系统操作
- 可能破坏状态的配置变更

推荐流程：

1. teammate 先 `submit_plan`
2. `lead` 审核
3. 审核通过后再执行

## 复用策略

### 优先复用已有 teammate

推荐默认优先级：

1. 已有匹配 teammate，优先 `send_message`
2. 没有匹配 teammate，但判断为长期任务，`spawn_teammate`
3. 一次性专项任务，`delegate_task`
4. 简单任务，`lead` 自己做

也就是说，persistent teammate 一旦存在，就应成为默认复用对象，而不是每次都重新 delegate。

### 何时不复用

以下情况应避免复用已有 teammate：

- 当前任务与其长期方向无关
- 当前任务需要强上下文隔离
- 该 teammate 已高负载或状态不健康
- 该 teammate 的上下文已经明显被污染

## 自检机制

推荐在 `lead` 真正执行前加入一个轻量 self-check：

1. 这个任务是否会在后续继续推进？
2. 如果会继续推进，我为什么没有选择 persistent teammate？
3. 我是否错误地把长期任务当成了一次性 delegate？
4. 当前 roster 中是否已有可复用 worker？

只要其中任意一项给出“存在明显错配风险”，就应回退重选 dispatch。

## 失败回退策略

### 模型质量不足

如果 `delegate_task` 输出可疑、空结果或明显幻觉：

- 不要直接采信
- 让 `lead` 或另一个 teammate 二次核验
- 必要时改为 persistent teammate 持续推进

### 模型速率限制 / 429

如果 persistent teammate 触发模型 `429`：

- 记录失败原因
- 不要丢弃已落盘任务
- 保持任务在 `.tasks/` 中可恢复
- 等待后续重试窗口或由已有 teammate 继续恢复

### teammate 不活跃

如果 teammate 已创建但未有效推进：

- 检查其 inbox / claim 状态
- 判断是否需要换角色、重置上下文或关闭后重建

## 建议的可观测字段

为了让调度真正可复盘，建议未来为每次调度决策记录以下字段：

- `dispatch_mode`: `direct` / `delegate_task` / `send_message` / `spawn_teammate`
- `dispatch_reason`: 选择理由
- `task_shape`: simple / one_off / long_horizon / scheduled / risky
- `reused_teammate`: 是否复用已有 teammate
- `spawned_teammate_name`
- `delegate_role`
- `needs_plan_review`
- `decision_confidence`

这样可以把“为什么这样调度”从 prompt 推断，变成真实可追踪数据。

## 建议的后续工程化实现

### 1. Dispatch Governor

建议增加一个轻量的调度决策层，在 `lead` 真正调用 team 工具前输出结构化决策：

```json
{
  "mode": "spawn_teammate",
  "role": "planner",
  "reason": "long_horizon_task_with_reusable_context",
  "reuse_candidate": "",
  "needs_plan_review": false
}
```

### 2. 任务分类器

把“简单 / 一次性专项 / 长期工作流 / scheduled / risky”做成显式分类，而不是完全依赖 prompt 口头理解。

### 3. 调度后复盘

每轮任务结束后统计：

- 哪些任务本应创建 teammate 却被 delegate 了
- 哪些 teammate 创建后没有被复用
- 哪些 delegate 质量不稳定，需要改成长期 worker

## 与当前实现的关系

当前代码已经具备以下基础：

- team 模板注册
- `delegate_task`
- `spawn_teammate`
- `send_message`
- task 持久化
- scheduled task 的特殊硬规则

因此，`dispatch_policy` 不是从零开始设计，而是要把这些已有能力从“可用工具”提升为“稳定策略”。

## 相关代码

- [main.go](file:///Users/bytedance/rainea/nexus/cmd/nexus/main.go#L134-L154)
- [buildScheduledAgentTurnPrompt](file:///Users/bytedance/rainea/nexus/cmd/nexus/main.go#L290-L317)
- [tools.go](file:///Users/bytedance/rainea/nexus/internal/team/tools.go#L24-L33)
- [delegate.go](file:///Users/bytedance/rainea/nexus/internal/team/delegate.go#L16-L26)
- [manager.go](file:///Users/bytedance/rainea/nexus/internal/team/manager.go#L138-L209)
- [teammate.go](file:///Users/bytedance/rainea/nexus/internal/team/teammate.go#L39-L63)

## 相关文档

- [architecture.md](file:///Users/bytedance/rainea/nexus/docs/architecture.md)
- [usage.md](file:///Users/bytedance/rainea/nexus/docs/usage.md)
- [production.md](file:///Users/bytedance/rainea/nexus/docs/production.md)

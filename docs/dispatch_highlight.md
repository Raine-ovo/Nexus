# Dispatch 亮点说明

## 这次改动是什么

这次 `dispatch` 不是一次普通的 prompt 调整，而是把 `Nexus` 的团队调度从“主要靠语言暗示”推进成了“**模型输出结构化画像 + 代码硬约束执行**”的混合机制。

对应地，当前系统新增了两类关键能力：

- **调度画像门禁**：`lead` 在使用 `delegate_task`、`spawn_teammate`、`send_message` 之前，必须先输出结构化 `dispatch_profile`
- **稳定性增强**：OpenAI 兼容模型 client 增加 `429/5xx` 退避重试，并新增进程级节流（`model.max_concurrency` + `model.min_request_interval_ms`），缓解复杂多智能体任务中最常见的 provider 速率限制问题

这两点组合起来，使调度第一次具备了：

- 可解释性
- 可观测性
- 可约束性
- 更强的运行稳定性

## 为什么这是亮点

### 1. 从“靠 prompt 猜”变成“画像 + 规则”

之前的核心问题是：

- 用户不应该强制说“必须用 teammate”或“必须用 subagent”
- 但如果完全交给模型自由发挥，它又会不稳定地乱选

这次的关键突破是：

- 用户继续只描述目标
- 模型先输出任务画像
- 代码再根据画像硬性决定允许或禁止哪些 team 路由动作

也就是说，系统开始自己决定组织结构，而不是靠用户强制指定。

### 2. teammate 和 subagent 的边界第一次被代码化

现在这条边界不再只是口头约定，而是实际执行规则：

- `needs_isolation=true` -> 只允许 `delegate_task`
- `needs_persistence=true` 或 `expected_follow_up=true`
  - 有可复用 teammate -> 优先 `send_message`
  - 没有可复用 teammate -> 优先 `spawn_teammate`
- `simple=true` -> 不走 team 路由，由 `lead` 直接完成

这使 `persistent teammate` 和 `delegate subagent` 的职责真正分开了。

### 3. 多智能体长任务终于更稳了

我们已经在真实运行中看到：

- `lead` 能招募 `Atlas`、`Sentinel` 这样的 persistent teammates
- teammate 能自动认领 `.tasks/` 中的任务
- 但一旦模型 provider 限流，整条链路会被 `429` 打断

这次补上的 `429/5xx` 退避重试 + 主动节流，解决的是“多 agent 一起工作时，provider 瞬时抖动或速率限制直接把自治链路打断”的问题。

## 实现概览

### A. Dispatch Profile

当前 `lead` 在使用任意 team 路由工具前，要求输出：

```text
<dispatch_profile>
simple=true|false
needs_persistence=true|false
needs_isolation=true|false
expected_follow_up=true|false
specialist_role=<role-or-empty>
reason=<short_reason>
</dispatch_profile>
```

这不是为了给用户看，而是为了让模型把“任务理解结果”压缩成代码可消费的结构化画像。

### B. Team 路由门禁

代码层会校验这个画像，并据此限制：

- `delegate_task`
- `spawn_teammate`
- `send_message`

如果画像和动作不匹配，系统不会直接执行，而是返回 `Dispatch gate` 错误，逼模型修正路线。

### C. Dispatch 日志

现在调度过程中会记录：

- `dispatch profile accepted`
- `dispatch route proposed`
- `dispatch route approved`
- `dispatch route rejected`

并附带：

- `profile`
- `tool`
- `mode`
- `active_teammates`
- `reusable_teammate`
- `error`

这意味着调度第一次具备了“日志可解释性”。

### D. 429/5xx 重试

OpenAI 兼容模型 client 现在支持：

- `429`
- `5xx`
- 可重试 transport error

的指数退避重试。

同时，当前运行时还支持：

- `model.max_concurrency`
- `model.min_request_interval_ms`

作为**进程级主动节流**，从源头降低 Lead / Teammate / Reflection / Delegate 同时打模型导致的 429。

默认策略：

- 最多 4 次尝试
- 优先尊重 `Retry-After`
- 否则走 `1s -> 2s -> 4s -> 8s`

## 解决了哪些真实问题

### 问题 1：复杂任务总是容易被误 delegate

以前：

- 模型一旦觉得任务复杂，常常直接 `delegate_task`
- 但很多任务其实是长期工作流，应该沉淀成 persistent teammate

现在：

- 如果画像声明 `needs_persistence=true` 或 `expected_follow_up=true`
- 代码会直接阻止不合适的 `delegate_task`

### 问题 2：已有 teammate 不复用

以前：

- 系统即便已经有合适角色的 teammate，也可能重新 spawn 或继续 delegate

现在：

- 若已有匹配 `specialist_role` 的 active teammate
- `spawn_teammate` 会被拦下
- 优先要求 `send_message` 复用现有 worker

### 问题 3：provider 限流直接把自治链打断

以前：

- teammate 或 delegate 一遇到 `429` 就直接失败

现在：

- 会先做退避重试
- 在短时速率限制场景下更容易自愈

## 运行时效果

这次增强后的理想运行过程是：

1. 用户描述目标
2. `lead` 首轮生成时输出 `dispatch_profile`
3. 系统解析画像
4. 代码决定：
   - 自己做
   - `delegate_task`
   - 复用现有 teammate
   - 新建 teammate
5. 调度路径写入日志
6. 如果模型限流，client 自动退避重试

这样一来，调度不再是完全黑盒。

## 为什么它适合当前阶段

这个方案的一个关键优点是：

- **不增加额外模型调用次数**

我们没有引入一个独立的 classifier API，而是复用了 `lead` 的第一次模型输出：

- 小任务照旧直接做
- 非简单任务在第一次输出里顺便给出画像
- 代码读取画像后做约束

所以这是一种：

- 成本低
- 改动小
- 但效果立竿见影

的调度升级方式。

## 与完整 Dispatch Governor 的关系

当前实现还不是终态的 `Dispatch Governor`，但已经完成了最关键的一步：

- 把“调度靠感觉”变成“调度有结构化信号可约束”

后续如果继续演进，可以在这个基础上再做：

- 独立的任务分类器
- 调度结果持久化
- delegate 结果二次核验
- session 级 dispatch cache
- async job 模式

## 你可以怎么展示这个亮点

如果面向团队或对外展示，最值得强调的是这三句话：

1. `Nexus` 不再要求用户指定 teammate 还是 subagent，而是让系统自己调度。
2. 这个调度不是完全靠 prompt，而是由结构化 `dispatch_profile` 和代码门禁共同决定。
3. 在多智能体长任务场景下，系统还补上了 `429/5xx` 自动退避重试和 dispatch 日志，使自治链路更稳、更可解释。

## 相关代码

- [lead.go](file:///Users/bytedance/rainea/nexus/internal/team/lead.go)
- [dispatch_profile.go](file:///Users/bytedance/rainea/nexus/internal/team/dispatch_profile.go)
- [openai_compat_chat_model.go](file:///Users/bytedance/rainea/nexus/internal/core/openai_compat_chat_model.go)
- [team_test.go](file:///Users/bytedance/rainea/nexus/internal/team/team_test.go)
- [openai_compat_chat_model_test.go](file:///Users/bytedance/rainea/nexus/internal/core/openai_compat_chat_model_test.go)

## 相关文档

- [dispatch_policy.md](file:///Users/bytedance/rainea/nexus/docs/dispatch_policy.md)
- [usage.md](file:///Users/bytedance/rainea/nexus/docs/usage.md)
- [production.md](file:///Users/bytedance/rainea/nexus/docs/production.md)

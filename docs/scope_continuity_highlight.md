# Scope / Workstream Continuity 主链路亮点

## 这次改动解决了什么

这次改动聚焦的是 `nexus` 在“有状态团队协作”上一个非常关键、但之前没有真正闭环的问题：

- Team Runtime 已经有 `Lead`、persistent teammate、delegate、task board、memory、dashboard
- 但这些能力默认更像是“**当前进程里的一套团队运行时**”
- 用户一旦换一个新 session，或者服务重启后再继续上一件事，系统很容易把它当成一个全新的任务

换句话说，之前系统更擅长：

- 把**一次任务**跑完整
- 让单次运行有 trace、有 metrics、有 outputs

但还不够擅长：

- 把**第二次请求**接回第一次建立的工作线
- 把“继续昨天那个实验 / 继续刚才那个方案”稳定命中到之前那支 team
- 在重启之后还能把那条工作线找回来

这会导致一个非常典型的产品/工程割裂：

- 系统内部已经有 persistent teammate、roster、memory、reflection
- 但用户视角下仍然容易感觉“每次像重新开始”

所以这次改动的核心价值，不是“再新增一个子模块”，而是把 Nexus 从：

- **能运行单次团队任务**

推进到：

- **能围绕同一条 workstream 持续协作、跨 session 续接、跨重启恢复**

这是一种非常实质的运行时升级。

## 为什么这件事重要

### 1. Team 不应该等于 Session

如果 `team` 直接跟 `session` 绑定，会带来几个天然问题：

- 前端换个会话，团队就像重新组队
- 原来的 persistent teammate 无法真正“持续”
- 前一轮讨论形成的上下文、任务分工、未完成事项很难自然延续
- 单次请求里的 team 很强，但多轮 follow-up 的体验会断裂

这和 Nexus 的目标其实是冲突的。

Nexus 不是一个“只会单次 fan-out 几个 agent”的框架，它本来就已经具备：

- persistent teammate
- task claim / task board
- memory / reflection
- debug dashboard / traces / metrics

这些都天然更适合围绕一条长期工作线累积，而不是每个 session 都从零开始。

### 2. 真正需要的是 Workstream Continuity

这次改动背后的核心抽象是：

- `session`：一次接入会话
- `request`：一次具体输入
- `workstream`：围绕某个长期目标的持续工作线
- `scope`：这条工作线对应的团队、记忆和状态隔离边界

只有引入这一层，系统才能把下面这些话理解为“继续上一件事”而不是“新开问题”：

- `继续昨天那个 team 方案`
- `顺着上次那个 TeamRegistry 继续改`
- `继续刚才那个 scope continuity 实验`

这也是为什么这个特性不能靠单次 prompt 证明，而必须用多阶段实验去验证。

## 这次改动的四个核心补点

### 1. Team 从全局单例升级为按 Scope 复用的 Registry

此前主程序里更接近“全局一个 team manager”的模式。

这对于单用户、单实验、短链路是够用的，但对多轮、多任务、多工作线来说，会有两个问题：

- 状态都堆在一支 team 上，容易串扰
- 无法把“继续上一件事”明确映射到正确的 team

这次改动后新增了 `team.Registry`，它负责：

- 维护 `session -> scope` 绑定
- 按 scope 懒创建多个 `team.Manager`
- 根据显式 `scope/workstream` 或 continuation cue 复用已有 team
- 为每个 scope 单独维护 team dir、memory、reflection

这意味着 Team Runtime 的组织方式从：

- “全局一个团队服务所有请求”

变成：

- “围绕不同工作线复用不同团队”

这是这次改动的基础设施核心。

### 2. Session 正式具备 Scope / Workstream 元数据

此前 `session` 主要承载：

- `channel`
- `user`
- `agent_id`

但不承载“这次会话属于哪条工作线”。

这次改动后，session 增加了：

- `scope`
- `workstream`

这带来两个重要收益：

#### 显式控制路径

如果调用方已经知道这次请求要落在哪条工作线，可以在建 session 时直接指定：

- `scope`
- 或 `workstream`

此时系统不需要猜，直接复用对应 team。

#### 自然语言 continuation 路径

如果调用方没有显式指定，也没关系。

系统会根据：

- continuation cue
- 同 user / channel
- 最近 workstream 摘要
- 关键词与 summary 匹配
- 阈值打分

去尝试找到最像的那条工作线。

这就把“继续刚才那个”从纯语义猜测，升级成了结构化检索 + 评分决策。

### 3. Scope Index 持久化，真正支持跨重启恢复

如果 scope/workstream 只保存在内存里，那么：

- 当前进程活着时还可以继续
- 一旦服务重启，之前的工作线索引就丢了

这次改动把 scope 索引持久化到了磁盘：

- `.../.team/index/scopes.json`

这里会保存每条工作线的核心状态，例如：

- `scope`
- `channel`
- `user`
- `workstream`
- `summary`
- `keywords`
- `recent`
- `updated_at`

这带来的质变是：

- continuation 不再只是“当前进程记得”
- 而是“系统有一本持续存在的工作线台账”

所以服务重启后，再来一句：

- `继续刚才那个 scope continuity 实验`

系统仍然有机会根据持久化 index 找回之前那条 team。

### 4. Scope 决策进入可观测主链路

这次改动不只做了“能命中”，还做了“能解释为什么命中”。

Scope 决策现在会进入三层观测面：

#### A. 结构化日志

每次 scope 解析都会记录：

- `decision`
- `reason`
- `score`
- `threshold`
- `candidate_count`

这样 scope 复用不再是黑盒。

#### B. Trace / Span Tags

Scope 决策被透传进 context，并在 span tags 中记录：

- `scope`
- `workstream`
- `scope_decision`
- `scope_reason`
- `scope_score`
- `scope_threshold`
- `scope_candidates_json`

这意味着 trace 不只是展示“做了哪些 tool / llm 调用”，还会告诉你：

- 这次为什么走到了这个 team

#### C. Dashboard / Debug API

调试面也一起升级：

- `GET /api/debug/scopes`
- `/debug/dashboard` 新增 scopes 卡片
- Trace Detail 新增 `Scope Decision`
- `candidates` 变成表格，而不只是 raw JSON
- Trace 列表页直接显示 scope decision 摘要和匹配质量颜色

这使 continuation 能力从“内部逻辑”变成了“可直接观察的治理能力”。

## 为什么这是亮点，而不只是一个路由小优化

### 亮点 1：把“有状态团队”真正做成了长期工作线运行时

很多多智能体系统虽然也有 team、worker、subagent 的概念，但实际还是：

- 每个请求重新组织一次
- 历史上下文只靠当前窗口硬撑
- session 一换就断

这次改动让 Nexus 更接近一个真正的 Team Runtime：

- 团队围绕工作线存在
- 请求只是投喂给这条工作线的输入
- 临时隔离任务仍然交给 delegate
- 持续协作任务则复用同一支 team

这使“persistent teammate”第一次真正有了产品语义，而不是只是“生命周期长一点的 goroutine”。

### 亮点 2：把 continuation 做成了工程能力，而不是 prompt 幻觉

如果系统只是靠模型去“猜测用户是不是在继续上次那个任务”，会很脆弱：

- 容易串台
- 容易误判
- 难以解释
- 无法稳定回归

这次改动的重要价值在于：

- continuation 不再纯靠 prompt 理解
- 而是有显式元数据、持久化 index、摘要检索、打分阈值、debug 面板、trace 证据

这是一种从“语义猜测”到“运行时机制”的升级。

### 亮点 3：把简单查询和长期协作区分开了

这次改动还有一个容易被忽略、但非常重要的工程点：

- 并不是所有请求都要走复杂 continuity 路由

系统明确保留了 fast path：

- 简单查询直接处理
- 一次性专家任务走 `delegate_task`
- 只有 continuation 信号明确或显式给定 `scope/workstream` 时，才去复用工作线

这避免了一个常见副作用：

- 为了做“记忆”和“连续性”，反而把所有简单请求都拖慢、复杂化

所以这次不是“给所有请求加一层笨重记忆”，而是：

- **在不牺牲简单查询体验的前提下，为长期任务补齐连续性**

### 亮点 4：它天然放大了项目里其他 feature 的价值

这个特性本身不是孤立存在的，它会直接放大项目里很多已有 feature 的价值：

#### 对 persistent teammate

- teammate 不再只是“当前会话里活得久”
- 而是可以在后续 continuation 中继续被复用

#### 对 memory / reflection

- 沉淀下来的语义记忆和反思不再只服务单次 run
- 而是更有机会进入同一工作线的后续轮次

#### 对 dashboard / traces / latest-traces

- 这些观测手段不再只是看“本次任务执行了什么”
- 还可以看“这次 continuation 是如何命中同一工作线的”

#### 对 experiments

- 实验不再只是一次性跑完就结束
- 而是可以设计成真正的三阶段 continuity 验证

所以这个特性本质上不是孤立亮点，而是一个能把 Team、Memory、Reflection、Observability 串起来的枢纽能力。

## 实现概览

### A. `gateway` 侧增加 Session 元数据与 Scoped Supervisor

`POST /api/sessions` 现在支持可选字段：

- `scope`
- `workstream`

同时 `gateway` 新增 `ScopedSupervisor` 通道，可以把完整 `session` 传给下游，而不仅仅是 `session_id`。

这一步很关键，因为它让 scope/workstream 不再是“外部自己记着”的概念，而是正式进入了网关主链。

### B. `team.Registry` 负责 Scope 解析、团队复用与索引持久化

Registry 负责：

- 显式 scope/workstream 优先
- continuation cue 命中时做候选检索 + 打分
- 低置信度时回退为新 session scope
- 为每个 scope 构建独立的 `team dir`、semantic memory、reflection memory
- 启动时从持久化 index 恢复工作线状态

这样 Team 不再是单例管理器，而是“多工作线团队的复用入口”。

### C. Observability 把 Scope 决策变成 trace 一等公民

Scope 决策被写入：

- span tags
- trace summary
- trace detail
- dashboard list/detail

所以你既可以：

- 在 trace 列表快速看摘要
- 在 trace detail 看 `score / threshold / candidates`
- 在 `/api/debug/scopes` 看系统当前记住了哪些工作线

这让调试和验收都容易得多。

### D. 独立三阶段实验用于真实验证

为了避免“只跑一次任务就声称 continuity 生效”的伪验证，这次还补了一套独立实验：

- `experiments/scope-continuity-validation/`

它会分三阶段验证：

1. 建立主工作线
2. 新 session continuation
3. 重启后 continuation

这使这个特性第一次拥有了较完整的回归实验路径，而不是停留在单元测试和口头推理层面。

## 对外可强调的表述

如果把这次改动作为项目亮点对外表达，最值得强调的是四句话：

- Nexus 不再只是“有 persistent teammate 的多智能体系统”，而是开始具备 **围绕同一条 workstream 持续协作** 的运行时语义。
- continuation 不再依赖 prompt 猜测，而是具备了 **scope/workstream 元数据、持久化索引、摘要检索和阈值决策**。
- scope 命中不再是黑盒，已经具备 **日志、trace tags、debug scopes、dashboard** 的全链路可解释性。
- 这个能力不是孤立 feature，而是让 Team、Memory、Reflection、Observability 真正形成 **长期协作闭环** 的关键枢纽。

## 一句话总结

这次改动把 Nexus 从“能运行单次团队任务的多智能体平台”，推进成了“**能把一次任务演进成一条可续接、可恢复、可观测工作线**”的团队运行时。

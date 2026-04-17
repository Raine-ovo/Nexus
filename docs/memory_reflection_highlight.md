# Memory / Reflection 主链路亮点

## 这次改动解决了什么

这次改动聚焦 `full-activation-throttled` 实验里一个非常关键、但之前不够“开箱即用”的问题：

- 配置里已经开启了 `semantic memory` 和 `reflection`
- 代码里也已经有 `memory.Manager`、`reflection.Engine`
- 但真实 run 目录里往往只看到一份很薄的 `semantic.yaml`
- `reflections.yaml` 经常根本不存在

这说明问题不在“有没有模块”，而在**有没有真正接入主链路**。

此前系统的真实行为更接近：

- `lead` 会在用户回合结束后写一次语义记忆
- persistent teammate 主要只保留自己的对话上下文
- reflection engine 只有在“评估失败并进入重试”时才会写 episodic memory

于是就出现一个很尴尬的现象：

- 文档里说支持 memory / reflection
- 配置里也确实 enabled
- 但实验 run 产物里几乎看不到有效证据

这次修复的核心价值，就是把它从“模块存在”推进到“**主链路闭环可见**”。

## 两个核心补点

### 1. Persistent teammate 终于进入记忆 / 反思闭环

此前 `lead` 的用户请求会经过 runtime 包装：

- `RunWithReflection`
- `RecordTurn`

但 persistent teammate 的工作循环并没有走同样的闭环。它会：

- 继续处理 inbox / task board
- 保留 `messages`
- 做 transcript compaction

但不会像 `lead` 一样，在一个完整 work unit 结束后显式：

- 跑反思评估
- 沉淀语义记忆
- 把工作结果变成长时可复用信息

这次改动后，persistent teammate 也和 `lead` 一样，按 work unit 进入 runtime：

1. 先用 `RunWithReflection` 包住本轮执行
2. 成功产出结果后调用 `RecordTurn`
3. 将本轮输入 / 输出抽取为 semantic memory

这意味着多智能体系统里最重要的“长期 worker”第一次真正拥有了：

- 可持续沉淀经验
- 可跨轮复用记忆
- 可在 run 目录中留下持久化证据

换句话说，persistent teammate 不再只是“活得久”，而是开始“**记得住**”。

### 2. Reflection 首轮成功也会落盘

此前 `reflection.Engine` 的行为偏“失败补救器”：

- 先执行
- 再评估
- 只有评估不通过，才生成 retrospective reflection 并写入 memory

这会带来一个表面上很怪的结果：

- 反思引擎其实已经运行了
- 但如果首轮就通过，就不会写 `reflections.yaml`
- 用户从 run 目录看，会误以为“没有反思机制”

这次改动后，成功路径也会落一条正向 reflection：

- 首轮通过时写 `successful_completion`
- 若经过重试后成功，则写 `recovery_success`

这样反思系统不再只是“失败时才可见”，而是变成：

- 成功有成功的经验沉淀
- 失败有失败的纠偏沉淀

最终效果是：`reflection` 从“隐藏在 runtime 里的评估器”升级成了“**run 目录里可观察、可复盘、可追溯的 episodic memory**”。

## 为什么这是亮点

### 从“功能存在”变成“主链路激活”

很多 Agent 系统都容易停留在这一步：

- 代码库里有 memory 模块
- 代码库里有 reflection 模块
- 但日常主链路并不会稳定命中

这次改动的意义在于：

- 不是再新增一个子系统
- 而是把已有子系统真正接到最重要的执行路径上

这类改动对工程价值往往比“再多一个新 feature”更高，因为它解决的是：

- 功能可验证性
- 实验可信度
- 对外讲述时的证据一致性

### 从“只有 lead 在学习”变成“团队在学习”

之前更像是：

- `lead` 有一点记忆沉淀
- teammate 主要靠长上下文撑住连续性

但真正负责长期执行和 follow-up 的，恰恰是 persistent teammate。

所以这次改动把系统推进了一步：

- 不再只是 lead 有状态
- 而是 persistent teammate 也能沉淀长期知识和回顾经验

这让 Nexus 的“有状态团队”更完整，因为状态不再只表现为：

- inbox
- roster
- 对话历史

还表现为：

- semantic memory
- episodic reflection memory

### 从“运行了”变成“有证据”

工业级系统不是只看 capability list，而是看：

- 能不能在真实 run 中看到产物
- 能不能在复盘时解释为什么成功或失败
- 能不能在下一次运行里复用前一次经验

这次补齐后，`full-activation-throttled` 这类实验终于更符合“系统级验收”的要求：

- `semantic.yaml` 不再只是零星静态事实
- `reflections.yaml` 不再因为首轮通过而缺席
- persistent teammate 的 work unit 也能留下长期证据

这使 run sandbox 真正成为：

- 执行证据目录
- 经验沉淀目录
- 复盘输入目录

## 实现概览

### A. teammate 进入 runtime work unit

现在 persistent teammate 的每次 work unit 都会：

- 进入 `RunWithReflection`
- 成功后 `RecordTurn`
- 将这一轮输入 / 输出沉淀到 semantic memory

这让 `lead` 和 `teammate` 的 runtime 语义第一次对齐。

### B. idle 唤醒后的工作也会沉淀

teammate 在以下场景恢复工作时，也会走同样闭环：

- 收到 inbox follow-up
- 自动认领 task board 中的 claimable task

这意味着不是只有“第一次启动”会沉淀，而是持续 follow-up 的长期协作也会留下长期记忆。

### C. success reflection 显式落盘

reflection engine 现在会对通过评估的尝试也写一条成功 reflection：

- `successful_completion`
- `recovery_success`

这样 run 目录中的 `reflections.yaml` 成为真正稳定的反思证据，而不是碰运气出现的调试产物。

### D. 测试补齐

这次同时补了两类回归测试：

- teammate 在 runtime 驱动下能写出 semantic memory
- reflection 在成功评估路径下也会落 `reflections.yaml`

这样后续如果有人又把 memory / reflection 从主链路上“绕开”，测试会第一时间报出来。

## 对外可强调的表述

如果把这次改动作为项目亮点对外表达，最值得强调的是三句话：

- Nexus 不再只是“代码里有 memory / reflection 模块”，而是让它们 **稳定进入 persistent teammate 的主链路**。
- Reflection 不再只在失败重试时可见，而是具备了 **成功 / 失败双路径的 episodic memory 留痕**。
- `full-activation-throttled` 不再只是功能覆盖实验，而是能在 run sandbox 中留下 **可复盘、可验证、可传播的记忆与反思证据**。

## 一句话总结

这次改动把 Nexus 从“具备记忆与反思能力的多智能体系统”推进成了“**这些能力真的在主链路里生效，并且能在运行证据中被看见**”的工程化运行时。

# Mission: Nexus 项目自治审计演示

## 目标

对当前 Nexus 仓库执行一次"强制混合协作模式"的深度项目自治审计，展示以下 4 类能力：

1. **招募 persistent teammates** — 长期协作、累积上下文的团队成员
2. **调用隔离型 subagent（delegate_task）** — 一次性聚焦分析、上下文隔离
3. **teammate 之间直接沟通（send_message / read_inbox）** — 去中心化协作
4. **任务自治推进** — 任务拆解、认领、计划审批、产物落盘

## 为什么需要混合模式

Nexus 项目本身就是一个多智能体协作平台，审计它需要：

- **Persistent teammates**：架构理解和权限审查需要持续积累上下文，反复深入多个模块，适合持久化 teammate
- **delegate_task**：某些一次性聚焦任务（如特定文件的合规检查、特定模块的边界分析）不需要长期上下文，用隔离型 delegate 更高效且不会污染 teammate 的上下文窗口
- **Team 内消息**：teammate 之间需要交换发现和结论，而非全部经由 lead 中转
- **自治推进**：teammate 应能自主认领子任务、回传进度，而非完全等待 lead 指令

## 仓库概况

- **项目名**: Nexus — 基于 Eino 的工业级多智能体协作平台
- **语言**: Go 1.22+
- **代码规模**: ~15,000+ 行 Go 代码
- **核心模块**: 13 个（core, team, permission, observability, planning, memory, gateway, rag, tool, agents, intelligence, reflection, orchestrator）
- **关键特性**: Lead-Teammate 团队协作、JSONL 邮箱总线、自治调度引擎、DAG 任务引擎、四级权限管道、三层 Context 管理

## 执行时间

- 开始时间: 2025-01-XX (当前运行)

## 约束

- 必须至少创建 2 个 persistent teammates
- 必须至少执行 2 次 delegate_task
- 必须至少发生 4 次 team 内直接通信
- 必须产出可落盘的中间文件和最终报告
- 如果某一步失败，不要终止全流程，要把失败过程和原因落盘

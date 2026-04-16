# 任务映射

## 任务与执行方式映射

| 任务 ID | 任务名 | 执行方式 | 执行者 | 原因 |
|---------|--------|----------|--------|------|
| 15 | architecture_audit | send_message → Atlas | Atlas (planner) | 需要持续上下文，跨模块理解 |
| 16 | permission_audit | send_message → Sentinel | Sentinel (code_reviewer) | 安全审查需要代码审查专业能力 |
| 17 | dispatch_audit | spawn_teammate + send_message | 新 devops teammate | 需要独立的持续上下文评估调度机制 |
| 18 | persistence_audit | delegate_task | code_reviewer (隔离) | 一次性专项分析，适合上下文隔离 |
| 19 | production_readiness | delegate_task | devops (隔离) | 一次性专项分析 |
| 20 | remediation_plan | lead 直接处理 | lead | 需要汇总所有审计结果 |
| 21 | governance_report | lead 直接处理 | lead | 需要汇总所有产物 |

## 额外 delegate_task 安排

| 序号 | 分析主题 | 角色 | 目标 |
|------|----------|------|------|
| D1 | RAG 系统安全边界 | knowledge | 评估 RAG 摄入/检索的安全边界 |
| D2 | 配置文件风险扫描 | code_reviewer | 扫描 configs/ 下的默认配置风险 |
| D3 | 可观测性缺口分析 | devops | 评估 trace/metrics/log 的生产缺口 |

## 协作通信计划

| 发送方 | 接收方 | 消息内容 | 时机 |
|--------|--------|----------|------|
| lead | Atlas | 架构审计任务描述 + 仓库扫描结果 | 阶段 3 开始 |
| lead | Sentinel | 权限审查任务描述 + 权限代码位置 | 阶段 3 开始 |
| lead | 新 teammate | 调度评估任务描述 + team 层代码位置 | 阶段 3 开始 |
| Atlas | lead | 架构审计结果 | 审计完成后 |
| Sentinel | lead | 权限审查结果 | 审计完成后 |
| 新 teammate | lead | 调度评估结果 | 审计完成后 |

## 能力触发检查清单

- [x] lead 自主调度
- [ ] dispatch_profile 驱动的 teammate/subagent 分流
- [ ] persistent teammate 创建/复用
- [ ] delegate_task 一次性隔离分析（至少 1 次）
- [ ] send_message 通信（至少 3 次）
- [ ] teammate 自主 claim task（至少 1 次）
- [ ] 计划审查与风险约束
- [ ] 10+ 落盘文件

# 治理演练使命声明

## 演练目标
围绕 Nexus 仓库（基于 Eino 的工业级多智能体协作平台）执行一次自治式技术治理演练，验证：
1. lead 自主调度与 dispatch_profile 驱动的 teammate/subagent 分流能力
2. persistent teammate 的创建、复用与 team 内通信
3. delegate_task 的一次性隔离分析
4. .tasks / .team / .outputs / .memory 的真实落盘
5. 计划审查与风险约束
6. 中间产物、证据链与最终报告的完整生成

## 治理范围
- 仓库：github.com/rainea/nexus（Go 1.22+，~15,000+ 行 Go 代码）
- 核心模块：13 个 internal 子模块
- 关键子系统：Team 协作调度、ReAct 引擎、RAG、权限、记忆、规划、网关、MCP、反思引擎、可观测性

## 治理维度
1. 架构理解与边界分析
2. 权限与安全审查
3. 调度与 team 机制评估
4. 运行态落盘与记忆机制评估
5. 部署与生产 readiness 评估
6. 风险与修复建议

## 约束
- 不虚构文件或目录
- 所有结论附真实来源
- 失败不中止，记录并继续
- 至少生成 10+ 落盘文件

## 执行时间
演练启动时间：由 lead 触发
预计阶段：6 个阶段

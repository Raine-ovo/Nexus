# 调度计划

## 仓库扫描结果

### 目录结构
```
.
├── .memory/           # 语义记忆（semantic.yaml.tmp）
├── .outputs/          # 历史输出
├── .runs/             # 运行记录
├── .tasks/            # 任务板（14 个任务文件 + meta.json）
├── .team/             # 团队状态（config.json, claim_events.jsonl, inbox/, requests/）
├── cmd/nexus/         # 入口
├── configs/           # 配置（default.yaml, audit.run.yaml, governance.run.yaml 等）
├── docs/              # 文档（9 个 md 文件）
├── internal/          # 核心代码（11 个子模块）
│   ├── agents/        # 4 个专业 Agent 实现
│   ├── core/          # Agent 核心引擎
│   ├── gateway/       # 网关层
│   ├── intelligence/  # 智能组装
│   ├── memory/        # 记忆系统
│   ├── observability/ # 可观测性
│   ├── orchestrator/  # 旧编排层（已被 team 取代）
│   ├── permission/    # 权限管道
│   ├── planning/      # 规划系统
│   ├── rag/           # RAG 系统
│   ├── reflection/    # 三阶反思引擎
│   ├── team/          # 团队协作调度层 ★
│   └── tool/          # 工具系统 + MCP
├── pkg/               # 公共库（types, utils）
├── workspace/         # Agent 工作区模板
│   └── skills/        # 技能文件
├── go.mod / go.sum
├── README.md
├── build.err / buildlog.txt
└── nexus / nexus-server
```

### 已有团队成员
| 名称 | 角色 | 状态 |
|------|------|------|
| lead | lead | working |
| Atlas | planner | working |
| Sentinel | code_reviewer | working |

### 已有任务板
- meta.json: next_id = 15
- 14 个任务文件（task_1.json ~ task_14.json）

## 分工策略

### 适合 persistent teammate 的任务（需要持续上下文）
1. **架构理解与边界分析** → Atlas (planner)，需要跨模块理解
2. **权限与安全审查** → Sentinel (code_reviewer)，需要持续审查上下文
3. **调度与 team 机制评估** → 新建 teammate（devops 角色），需要深入 team 层代码

### 适合 delegate_task 的任务（一次性隔离分析）
1. **RAG 系统安全边界** → delegate_task(role=knowledge)
2. **配置文件风险扫描** → delegate_task(role=code_reviewer)
3. **可观测性缺口分析** → delegate_task(role=devops)

### 适合 lead 直接处理的任务
1. 任务板建立与维护
2. 证据索引与最终报告
3. 协作摘要与执行轨迹

## 调度顺序
1. Phase 1: lead 生成初始文档（00-02）
2. Phase 2: lead 建立任务板（10-11）
3. Phase 3: 混合协作执行
   - Atlas: 架构审计（send_message）
   - Sentinel: 权限审查（send_message）
   - delegate_task: 3 次隔离分析
   - 新 teammate: 调度机制评估（spawn_teammate）
4. Phase 4: lead 生成专题文档（30-35）
5. Phase 5: lead 生成建议与路线图（40-42）
6. Phase 6: lead 生成证据与最终报告（50-51, 99）

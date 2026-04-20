# Team Scope 管理机制

本文档专门说明本次围绕 `internal/team/registry.go` 的管理机制升级，重点回答四个常见问题：

- 为什么仓库根目录会出现多个 `.team-*`
- 为什么“一个 scope”在代码语义上并不会对应多个 Team
- 单个 `team.dir` 内部的 scope 目录现在是如何组织的
- scope 的磁盘状态、内存 manager 和 debug 可见性之间是什么关系

---

## 1. 三个容易混淆的概念

### 1.1 `team.dir`

`team.dir` 是一个 Team Runtime 的根目录。

例如：

- `.team`
- `.team-exp`
- `.team-full-activation-throttled`
- `.team-scope-continuity-quick`

如果仓库根目录下同时出现多个 `.team-*`，通常表示：

- 不同实验或不同运行配置把 `team.dir` 指向了不同目录

这是一种**运行隔离**，不是 scope 重复创建。

### 1.2 `scope`

`scope` 是 `team.dir` 内部的一条长期工作线，用来决定请求应该复用哪个 Team 上下文。

常见来源：

- 显式传入 `session.scope`
- 显式传入 `session.workstream`
- continuation cue + 摘要/关键词匹配自动找回
- session 兜底生成 `session:<id>`

### 1.3 `Manager`

`Manager` 是当前进程内、负责某个 scope 的 Team 运行实例。

在单个 `team.dir` 内部，`Registry` 的语义是：

- 一个 scope 对应一个 `Manager`
- 若已存在则复用
- 若不存在则按需创建

因此，“一个 scope 对应多个 team”的理解并不准确。更准确的说法是：

- 一个 scope 对应一个持久化 Team 空间
- 这个 Team 空间里可以包含多个 teammate
- 在不同 `team.dir` 下，同名或同语义 scope 可能各有一份独立状态

---

## 2. 为什么根目录会有多个 `.team-*`

根目录出现多个 `.team-*` 的原因，是不同配置文件为 `team.dir` 指定了不同路径。

典型场景：

- 本地常驻运行用 `.team`
- 实验运行用 `.team-exp`
- scope continuity 的 quick/smoke/validation 实验分别用自己的 `.team-*`

这符合项目里“实验隔离”的工程约束：

- 不同实验不应污染同一份 Team 状态
- 不同 run 应保留各自可回放、可审计、可复现实验资产

所以：

- 多个 `.team-*` 是多套 Team Runtime 根目录
- 不是单个 scope 被错误地扩成了多个 Team

---

## 3. 单个 `team.dir` 内部现在怎么组织

一个 `team.dir` 下，scope 状态现在统一组织为：

```text
<team.dir>/
├── index/
│   └── scopes.json
└── scopes/
    ├── workstream/
    │   ├── wo/
    │   │   ├── workstream-cli-alice-doc-refactor/
    │   │   └── workstream-cli-alice-governance-dashboard/
    │   └── re/
    │       └── workstream-slack-release-review/
    ├── session/
    │   └── se/
    │       ├── session-s001/
    │       └── session-s002/
    └── custom/
        ├── ne/
        │   └── nexus-team/
        └── sc/
            └── scope-manual-debug/
```

### 3.1 `index/scopes.json`

这是 scope 台账，不是具体 Team 目录。

它保存每条工作线的摘要信息，例如：

- `scope`
- `scope_kind`
- `channel`
- `user`
- `workstream`
- `summary`
- `keywords`
- `recent`
- `updated_at`

它的作用是：

- continuation 匹配
- 重启后恢复
- debug 面可视化

### 3.2 `scopes/<scope_kind>/`

这是按 scope 类型做的一级分组。

当前主要分为：

- `workstream/`
- `session/`
- `custom/`

含义：

- `workstream/`：显式或推导出的长期工作流
- `session/`：未命中复用时的会话兜底 scope
- `custom/`：手工或特殊逻辑给出的自定义 scope

### 3.3 `scopes/<scope_kind>/<bucket>/`

这是 bucket 目录，通常取 slug 前两个字符，例如：

- `wo`
- `re`
- `se`
- `ne`
- `sc`

作用：

- 避免所有 Team 全部平铺在一个目录里
- 当 scope 很多时更容易浏览和排查

### 3.4 `scopes/<scope_kind>/<bucket>/<slug>/`

这是一个 scope 对应的真实 Team 目录，也就是这个工作线的持久化空间。

一个这样的目录通常会包含：

- `config.json`
- `inbox/`
- `requests/`
- `claim_events.jsonl`
- `memory/semantic.yaml`
- `memory/reflections.yaml`

它表示：

- 这条工作线的 Team roster
- 这条工作线的消息通信
- 这条工作线的长期记忆
- 这条工作线的治理与追踪记录

---

## 4. 为什么不是“一个 scope 多个 Team”

这个误解往往来自两个现象：

### 4.1 一个 Team 里有多个成员文件

例如：

- `lead.jsonl`
- `planner-001.jsonl`
- `devops-001.jsonl`

这表示：

- 一个 Team 中有多个 teammate

不是：

- 一个 scope 派生出了多个 Team

### 4.2 不同 `team.dir` 下都存在相似 scope

例如 quick/smoke/validation 各自实验目录里都出现“scope continuity validation”相关 scope。

这表示：

- 不同实验根目录里各自保存了一份独立状态

不是：

- 单个 Registry 给同一 scope 同时建了多个 manager

在实现语义上，单个 `team.dir` 内部仍然是：

- `scope -> manager` 一对一

---

## 5. 磁盘 Team 与内存 Manager 的关系

要把这两层区分开：

- 磁盘目录：长期持久化状态
- 内存 manager：当前进程中的运行实例

本次新增 `team.scope_manager_ttl` 后：

- 某个 scope 长时间不访问，内存 manager 会被自动驱逐
- 对应磁盘目录不会删除
- 后续请求命中这个 scope 时，会按需重新创建 manager

因此：

- 磁盘上可以有很多 Team 目录
- 但进程里同时驻留的 manager 数量可以被控制

这解决的是“scope 多了以后运行态越来越臃肿”的问题，而不是删除历史状态。

---

## 6. Debug 面现在能看到什么

`GET /api/debug/scopes` 现在不仅返回 scope 摘要，还直接暴露管理字段：

- `scope`
- `scope_kind`
- `storage_bucket`
- `lifecycle`
- `manager_running`
- `manager_last_used_at`
- `team_dir`

这些字段回答了几个关键问题：

- 这个 scope 属于哪类工作线
- 它落在磁盘什么位置
- 它当前是活跃还是冷数据
- 它是否仍有 manager 在进程内驻留

这样 Team 管理从“看目录猜状态”，升级为“目录、索引、运行态、调试面一致”。

---

## 7. 兼容性策略

为了避免直接破坏已有实验资产，当前实现采取兼容模式：

- 历史 Team 若已经位于旧路径 `<team.dir>/scopes/<slug>/`，运行时优先沿用旧路径
- 新创建的 scope 才进入新的分层目录布局

也就是说：

- 本次改动增强了管理结构
- 但不会强行迁移你已有的 Team 数据

---

## 8. 一句话总结

这次改动不是在语义上把“一个 scope 变成多个 Team”，而是把原本容易混淆的 Team 运行时拆清成三层：

- `team.dir`：哪一套 Team Runtime
- `scope`：这套 Runtime 里的哪条长期工作线
- `manager`：当前进程中这条工作线是否驻留、是否活跃

并进一步补齐了：

- 分层目录布局
- scope 索引持久化
- manager 生命周期回收
- debug 可视化字段

这样 scope 多起来以后，Nexus 的 Team 机制才真正具备“可管理、可解释、可恢复”的工程形态。

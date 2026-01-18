# FlowForge 架构与代码实现评审

> 本文依据 `docs/ARCHITECTURE.md`、`docs/IMPLEMENTATION_GUIDE.md` 以及 `docs/design/*` 的设计说明，对当前代码实现进行整体审查，并重点分析 DAG workflow 的端到端流程与大规模集群场景下的演进建议。

## 1. 架构实现对照与完整性评估

### 1.1 已实现的核心组件（与设计文档基本一致）

- **API Server（HTTP + gRPC）**：提供工作流与日志相关接口，支持 gRPC 日志流上报与状态更新。实现位于 `cmd/api-server` 与 `pkg/apiserver`、`pkg/apiserver/grpc`。符合“API Server + gRPC”设计方向。
- **Workflow Controller**：具备 DAG 解析、依赖判断、任务状态机推进与重试管理逻辑，位于 `pkg/controller`。能订阅任务事件并通过 Redis 事件总线进行状态推进。
- **Scheduler（公平调度）**：实现了基于租户/项目权重与资源配额的调度逻辑，位于 `pkg/scheduler` 与 `pkg/quota`。
- **Executor / Pod Executor**：从 Redis 队列拉取任务，创建 Pod 并等待完成，然后更新任务状态，位于 `pkg/executor`。
- **Status Collector**：DaemonSet 设计的 Pod watcher，能根据 Pod 状态发布任务状态事件，位于 `pkg/statuscollector`，与设计文档一致。
- **日志与指标采集**：日志热路径（Redis Pub/Sub）+ 冷路径（DB）已在 gRPC 端实现；Prometheus 兼容的指标采集器也已实现，位于 `pkg/metrics`。

### 1.2 与设计不一致或缺失的关键点

1. **API 网关/认证缺失**：设计文档强调“API Gateway + Auth/AuthZ”，但当前仅实现了简单的 Bearer Header 校验，未验证 JWT/OIDC，也没有多租户访问控制。安全模型尚未落地。
2. **Workflow Controller 与 Scheduler 职责重叠**：
   - Controller 具备任务就绪判断与入队能力（`scheduleReadyTasks`）。
   - Scheduler 也会扫描 `PENDING` 任务并入队。
   - 当前 API 直接写库并将 Workflow 置为 `RUNNING`，Controller 会在 reconcile 中再次尝试调度，Scheduler 同时在跑，存在重复调度与职责冲突的风险。
3. **SDK 功能与 gRPC 实现不完整**：Python SDK 具备 `SetOutput`、`SaveCheckpoint` 等接口调用，但 gRPC Server 并未实现对应 RPC，导致设计功能缺失。
4. **日志实时流与存储策略部分缺位**：
   - gRPC 日志流实现了 Redis Pub/Sub 与 DB 写入，但未实现 Redis SSE 的回放（buffer/list 的消费逻辑也未暴露 API）。
   - ClickHouse 的日志落盘存在实现，但可配置的 TTL 与日志回放策略不够完整。
5. **多节点任务/工作流（Gang Scheduling）**：设计文档并未实现多 Pod/多节点任务编排的逻辑，当前 Task 模型只支持单 Pod 执行，无法支撑多节点分布式训练等需求。

结论：目前系统已实现核心“提交/存储/调度/执行/状态回报”骨架，但在安全、职责分离、SDK/接口完整性、日志回放，以及大规模并发调度与多节点任务方面仍存在明显 gap。

## 2. DAG Workflow 端到端流程评审

以下按“提交 → 存储 → 调度 → 执行 → 完成”路径梳理：

### 2.1 提交与存储

- **提交入口**：`POST /api/v1/workflows`。
- **实现行为**：API Server 解析 DAG、生成任务、写入 PostgreSQL，并立即将 Workflow 状态改为 `RUNNING`，然后发布 `task_created` 事件。
- **问题点**：Workflow Controller 本身也具备 `SubmitWorkflow` 与 `startWorkflow` 逻辑，但 API 并未调用，导致“API 与 Controller”提交路径并存且不一致。

### 2.2 调度与入队

- **Scheduler 调度路径**：`Scheduler -> FairScheduler` 周期扫描 `PENDING` 状态任务，检查依赖与配额，更新状态为 `QUEUED` 并入 Redis 队列。
- **Controller 调度路径**：Controller reconcile 周期也会执行 `scheduleReadyTasks`，并同样将任务入队。
- **问题点**：双调度路径可能引发竞争条件，尤其是高并发场景下的重复入队、状态冲突。
- **When/条件语句**：仅 Controller 中有 `WhenCondition` 的解析逻辑，Scheduler 不识别；当 Scheduler 是主要调度路径时，条件任务可能被提前入队。

### 2.3 执行

- **Executor**：从 Redis 队列 `BLPOP` 取任务，创建 Pod 执行，任务完成后更新状态并释放配额。
- **问题点**：
  - 任务出队后无确认机制（Redis List 丢失语义），执行器崩溃可能导致任务永久丢失。
  - 执行器直接更新数据库并发布状态事件，Controller 又基于事件重复更新数据库，存在幂等与状态竞态问题。

### 2.4 状态回报与完成

- **Runner**：更新任务状态并发布事件。
- **Status Collector**：作为 DaemonSet，基于 Pod 状态发布事件（作为异常兜底）。
- **Controller**：监听事件更新任务状态，判断 Workflow 是否完成。
- **问题点**：
  - 状态源重复（Runner + StatusCollector + SDK UpdateStatus），需要明确“权威状态来源”并引入幂等控制。
  - Workflow 完成判断依赖 Controller 的内存状态与 DB 同步，存在多实例 Controller 之间状态一致性问题。

### 2.5 输出与下游依赖

- DAG Parser 支持 `outputs` 与 `when` 语法，但真正的输出写入逻辑并未实现（SDK 调用的 gRPC RPC 不存在）。
- 因此“基于上游输出进行条件判断”的功能不可用。

结论：流程框架存在，但在调度职责分离、任务可靠性、输出/条件依赖和状态一致性方面存在设计实现偏差，需要集中改进。

## 3. 面向超大规模集群（数百台机器、数千 GPU、每日上万个多节点 DAG）的改进建议

### 3.1 调度与队列系统

1. **替换 Redis List 为可确认队列**
   - 引入 Redis Streams、Kafka、NATS JetStream 等支持 ACK/重试/消费组的队列，确保任务不丢失。
   - 实现任务 lease/可见性超时，执行器故障后任务能重新入队。

2. **“就绪任务队列”事件驱动**
   - 避免 Scheduler 每秒扫描全量 `PENDING` 任务。
   - Controller 维护“Ready Queue”或“Dependency Counter”，一旦依赖满足立刻入队，减少 DB 扫描压力。

3. **多节点任务/集群级调度**
   - 增加 Task 维度的 `replica` / `gang` 配置；对分布式训练类任务使用 Volcano / Kueue / PodGroup 进行 Gang Scheduling。
   - Scheduler 与 K8s Scheduler 协同，避免抢占式资源不足导致大量 Pending。

### 3.2 执行层与状态一致性

1. **统一状态来源**
   - 推荐以 Controller 为权威状态机，Runner/StatusCollector 仅负责事件上报，Controller 决定最终状态变更。
   - 引入事件版本号/时间戳，确保幂等。

2. **Executor 事件驱动化**
   - 采用 Pod Watch 而非轮询 (`Get`) 方式等待 Pod 完成，减少 API Server 压力。
   - 可将执行器拆分为“调度器 → 创建器 → 监控器”三阶段，降低单点阻塞。

### 3.3 配额与资源管理

1. **配额系统去数据库事务化**
   - DB 事务式配额扣减在大规模并发下会成为瓶颈。
   - 建议引入独立的“资源管理服务”或基于 Redis/etcd 的原子计数器，再异步回写 PostgreSQL。

2. **GPU 拓扑感知**
   - 增加 GPU 机型/拓扑标签（如 NVLink、MIG、NUMA）需求表达。
   - Scheduler 需结合节点资源拓扑进行 placement 计算，而不仅仅是“数量”级别。

### 3.4 数据存储与可观测性

1. **任务/事件表分区与归档**
   - `tasks`/`task_events`/`workflow` 需进行时间分区或冷热分离。
   - 大规模场景下 PostgreSQL 单表会成为瓶颈。

2. **日志/事件持久化体系**
   - Redis Pub/Sub 不保证可靠投递，应引入 Kafka 或 Redis Streams 作日志缓冲。
   - 将 ClickHouse 作为统一日志/事件存储，API 提供历史查询和 SSE 回放。

### 3.5 SDK 与工作流语义完善

1. **补齐 gRPC API（SetOutput / Checkpoint）**
   - 实现 SDK 需要的 RPC 与存储逻辑，确保 DAG 中 `when` 与 `outputs` 可用。

2. **参数与输出绑定**
   - 为 Task 增加 `inputs/outputs` 解析、在 Controller 中进行参数渲染与输出回写。
   - 提供表达式语言（如 CEL）用于复杂条件判断。

---

## 总结

当前 FlowForge 已具备核心工作流执行骨架，但在设计落地层面仍存在明显缺口：调度职责重叠、可靠性不足、输出/条件语义缺失、认证与权限未实现，以及缺少大规模与多节点任务的能力。建议优先理清调度链路与任务可靠性保障，并引入支持多节点/Gang Scheduling 与可确认队列的基础设施，才能满足“数千 GPU、上万 DAG/天”的大规模生产需求。

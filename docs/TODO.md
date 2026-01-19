# TODO (2026-01-19)

以下任务基于设计文档与当前实现的差距整理，并结合“事件总线与任务队列迁移至 Kafka”的最新方案。每一项为独立开发任务，按推荐开发顺序排列。

1. **新增 Outbox 数据表与写入逻辑**
   - 在数据库中创建 Outbox 表（workflow_events）。
   - 在关键状态变更事务内写入 Outbox（workflow/task 状态更新、调度事件）。

2. **实现 Outbox Relay 服务（DB -> Kafka Topics）**
   - 周期性扫描 `status=pending` Outbox 记录。
   - 投递至 Kafka 事件 Topic，成功后更新 `published_at`。
   - 失败事件写入 Kafka DLQ Topic。

3. **实现 Kafka 事件总线 SDK（Producer/Consumer）**
   - Producer：统一封装事件发布接口。
   - Consumer：支持 consumer group、offset commit、幂等消费。
   - 支持 retry topic 与 DLQ topic 机制。

4. **替换现有 Redis Pub/Sub 事件总线**
   - 将现有事件发布点迁移至 Kafka event topic。
   - 保留或替换实时订阅能力（如 WebSocket/Cache）。

5. **将任务队列从 Redis List 迁移到 Kafka**
   - Scheduler 写入 Kafka task topic。
   - Executor 作为 Kafka consumer group 拉取任务并提交 offset。

6. **实现任务队列 Retry/DLQ 逻辑**
   - 引入 retry topic 与 backoff 调度。
   - 失败超过阈值的任务进入 DLQ topic。

7. **实现任务执行的心跳/超时机制**
   - Executor 定期上报心跳或更新运行状态。
   - 超时任务触发重试或回收配额。

8. **完善 Status Collector 与 Executor 状态来源一致性**
   - 明确状态权威来源，避免重复事件推进状态机。
   - 增加幂等校验与最终状态保护。

9. **补齐 Artifact/Parameter 传递能力**
   - 新增 Artifact/Parameter 数据模型与 API。
   - 支持模板注入与权限校验。

10. **实现 Workflow 共享存储 PVC 挂载**
    - 在 PodExecutor 构建 Pod 时挂载 workflow storage claim。

11. **新增可观测性指标与告警**
    - Kafka lag、Outbox backlog、DLQ size、重试率等。
    - 为 Relay/Consumer/Executor 暴露 Prometheus 指标。

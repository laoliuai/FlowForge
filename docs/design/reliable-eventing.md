# 可靠事件与调度队列设计（Kafka 版）

## 1. 目标
- 为工作流状态变更与调度事件提供持久化、可重放的投递机制。
- 保证在组件重启、网络抖动、Redis 故障时状态不丢失。
- 保持低延迟事件流用于 UI/实时订阅。

## 2. 架构概览

```
Controller/Executor
  ├─(DB tx)─> State Update + Outbox Row
  └────────> Outbox Relay -> Kafka Topics (durable)
                         └> Optional Realtime Fanout (WebSocket/Cache)
```

## 3. Outbox 设计

### 3.1 Outbox 表结构（示例）
```sql
CREATE TABLE workflow_events (
  event_id UUID PRIMARY KEY,
  event_type TEXT NOT NULL,
  payload JSONB NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  created_at TIMESTAMP NOT NULL,
  published_at TIMESTAMP NULL
);
```

### 3.2 发布流程
1. 控制器在事务内写入业务状态与 outbox。
2. Relay 扫描 `status=pending`，写入 Kafka 事件 Topic，并更新 `published_at`。
3. 失败事件进入 Kafka DLQ Topic 以便人工排查。

## 4. Kafka Topics + 消费者

- 使用 consumer group 提供 ACK（offset commit）与重试策略。
- 消费者记录 `event_id` 去重，确保幂等。
- 未确认的消息通过 consumer group rebalancing 重新分配给健康消费者。
- 失败消息进入 retry topic 或 DLQ topic 以实现延迟重试与人工回溯。

## 5. 任务队列的可靠性

- 任务队列采用 Kafka Topic（独立于事件总线）。
- 消费者在执行完成后提交 offset，确保“至少一次”投递语义。
- 引入 `retry topic` 与 `dlq topic`，按 backoff 策略延迟重试。
- 执行器需上报心跳或超时检测，避免任务执行失败后长期占用配额。

## 6. 可观测性

- 监控指标：outbox backlog、topic lag、DLQ size、retry requeue rate。
- Relay 组件需暴露关键运行指标：扫描批次大小、每批处理耗时、发布失败率、重试次数、consumer ACK 延迟。
- 告警建议：outbox backlog 持续增长、topic lag 超过阈值、DLQ size 突增。
- 每类事件带 `trace_id`，便于端到端追踪。

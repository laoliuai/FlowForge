# 可靠事件与调度队列设计

## 1. 目标
- 为工作流状态变更与调度事件提供持久化、可重放的投递机制。
- 保证在组件重启、网络抖动、Redis 故障时状态不丢失。
- 保持低延迟事件流用于 UI/实时订阅。

## 2. 架构概览

```
Controller/Executor
  ├─(DB tx)─> State Update + Outbox Row
  └────────> Outbox Relay -> Redis Streams (durable)
                         └> Redis Pub/Sub (hot path, optional)
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
2. Relay 扫描 `status=pending`，写入 Redis Streams，并更新 `published_at`。
3. 失败事件进入 DLQ 以便人工排查。

## 4. Redis Streams + 消费者

- 使用 consumer group 提供 ACK 与重试。
- 消费者记录 `event_id` 去重，确保幂等。
- 未确认的消息通过 `XCLAIM` 重新分配给健康消费者。

## 5. 任务队列的可靠性

- Dequeue 使用原子操作，将任务移入 `processing` 与 `heartbeat`。
- 任务执行过程中更新心跳，超时自动回收并重入队列。
- 失败任务进入 `retry:scheduled` 或 DLQ。

## 6. 可观测性

- 监控指标：outbox backlog、stream lag、DLQ size、retry requeue rate。
- 每类事件带 `trace_id`，便于端到端追踪。


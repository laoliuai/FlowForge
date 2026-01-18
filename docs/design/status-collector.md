# FlowForge Status Collector 设计方案

## 1. 概述
Status Collector 是部署在 Kubernetes 集群中的 **DaemonSet** 组件，负责监听任务 Pod 的生命周期变化，并将任务状态事件统一发布到事件总线（Redis）。它的目标是保证在执行器或 SDK 异常的情况下，系统仍能感知 Pod 状态并推动工作流状态机前进。

## 2. 核心职责

* **Pod 监听**：监听带有 `flowforge.io/workload=true` 标签的任务 Pod。
* **状态映射**：将 Pod Phase 映射为任务状态（RUNNING / SUCCEEDED / FAILED）。
* **事件发布**：将状态变化发布到 Redis 事件总线（`ff:events:task`）。
* **去重与降噪**：对同一 Pod 的重复状态进行去重，避免冗余事件。

## 3. 架构与数据流

```mermaid
graph LR
  subgraph Kubernetes
    Pod[Task Pod]
    SC[Status Collector (DaemonSet)]
    Pod -->|Watch| SC
  end

  SC -->|Publish task_status| Redis[(Redis Event Bus)]
  Redis --> Controller[Workflow Controller]
  Controller --> DB[(PostgreSQL)]
```

### 3.1 状态映射规则

| Pod Phase | Task Status | 备注 |
| --- | --- | --- |
| `Running` | `RUNNING` | 任务进入执行中 |
| `Succeeded` | `SUCCEEDED` | 从容器退出码提取 `exit_code` |
| `Failed` | `FAILED` | 记录 `exit_code` 和失败原因 |

> 说明：为避免与调度器/执行器状态更新冲突，Status Collector 不发送 `PENDING/QUEUED` 状态。

### 3.2 事件格式

Status Collector 发布的事件使用统一的 TaskEvent 结构：

```json
{
  "task_id": "uuid",
  "workflow_id": "uuid",
  "status": "RUNNING|SUCCEEDED|FAILED",
  "message": "optional failure message",
  "exit_code": 1
}
```

事件会封装为 `task_status` 类型并发送至 Redis channel `ff:events:task`，由 Workflow Controller 订阅并更新数据库状态。

## 4. 运行与部署

### 4.1 运行方式

Status Collector 作为 DaemonSet 部署，每个节点运行一个实例；通过字段选择器限定为本节点 Pod：

* `NODE_NAME`: 通过 Downward API 注入当前节点名。
* `flowforge.io/workload=true`: 作为任务 Pod 过滤标签。

### 4.2 配置项

| 配置项 | 说明 | 默认值 |
| --- | --- | --- |
| `FLOWFORGE_REDIS_ADDRESSES` | Redis 地址列表 | `127.0.0.1:6379` |
| `FLOWFORGE_KUBERNETES_IN_CLUSTER` | 是否使用 InCluster 配置 | `false` |
| `FLOWFORGE_KUBERNETES_KUBECONFIG` | kubeconfig 路径 | 空 |
| `FLOWFORGE_KUBERNETES_NAMESPACE` | 监听命名空间 | 空（全局） |
| `NODE_NAME` | 当前节点名 | 空 |

## 5. 容错与边界场景

* **重复事件**：通过 Pod UID 缓存，避免重复发布。
* **Pod 删除**：仅清理本地缓存，不额外发布失败事件。
* **异常 Pod**：若 Pod 缺失必要标签（task/workflow ID），记录 debug 日志并跳过。

## 6. 未来规划

* 细化 Pod 状态映射（如 ImagePullBackOff 触发失败）。
* 增加指标（Prometheus）监控状态事件吞吐量与延迟。
* 支持多租户隔离的指标维度。

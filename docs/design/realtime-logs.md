# FlowForge 实时日志系统设计方案

## 1. 概述
本设计方案旨在解决 FlowForge 工作流引擎中任务日志的实时采集、流式传输和持久化存储问题。系统支持数千个并发 DAG 的日志处理，提供毫秒级延迟的实时查看体验，并具备自动生命周期管理能力。

## 2. 核心架构

系统采用 **gRPC + Redis Pub/Sub + ClickHouse** 的混合架构：

1.  **采集层 (SDK)**: Python SDK 劫持 `stdout/stderr`，通过 gRPC 双向流实时上报。
2.  **传输层 (API Server)**:
    *   **热路径 (Hot Path)**: 日志通过 Redis Pub/Sub 实时分发给前端订阅者。
    *   **冷路径 (Cold Path)**: 日志异步批量写入 ClickHouse 进行持久化。
3.  **消费层 (Frontend)**: 前端通过 HTTP SSE (Server-Sent Events) 接口订阅实时日志流。
4.  **存储层 (Storage)**:
    *   **ClickHouse (推荐)**: 针对海量日志场景，支持自动 TTL 和高压缩比。
    *   **PostgreSQL (可选)**: 适用于开发测试环境或小规模部署。

```mermaid
graph LR
    Pod[Task Pod (Python SDK)] -->|gRPC Stream| APIServer
    
    subgraph APIServer
        Handler[gRPC Handler]
        Buffer[Batch Buffer]
    end
    
    Handler -->|Publish| Redis[(Redis Pub/Sub)]
    Handler -->|Append| Buffer
    Buffer -->|Bulk Insert| ClickHouse[(ClickHouse)]
    
    Redis -->|Subscribe| SSE[SSE Handler]
    ClickHouse -->|Query History| SSE
    
    SSE -->|Event Stream| Frontend[Web UI]
```

## 3. 详细设计

### 3.1 日志采集 (Python SDK)

*   **Stdout 重定向**: SDK 内部实现 `StdoutRedirector` 类，替换 `sys.stdout` 和 `sys.stderr`。
*   **缓冲机制**: 内部维护一个 `Queue`，避免网络波动阻塞业务逻辑。
*   **协议**: 使用 Protobuf 定义 `LogEntry` 结构，包含时间戳、级别、内容、任务 ID 等元数据。

### 3.2 服务端处理 (Go API Server)

*   **gRPC 接口**: `StreamLogs` 接口接收日志流。
*   **双写策略**:
    *   **实时流**: 收到日志立即 `Publish` 到 Redis Channel `logs:task:{task_id}`。
    *   **持久化**: 存入内存切片，每 1 秒或满 100 条触发一次批量写入数据库。
*   **可插拔存储**:
    *   通过 `LogStore` 接口抽象存储后端。
    *   根据配置 `logging.storage_driver` 自动选择 `clickhouse` 或 `postgres` 实现。

### 3.3 存储方案

#### ClickHouse (生产环境)
*   **引擎**: `MergeTree`
*   **分区**: 按天分区 `PARTITION BY toYYYYMMDD(created_at)`。
*   **TTL**: 设置 `TTL created_at + INTERVAL 7 DAY`，利用数据库原生能力自动清理旧数据。
*   **优势**: 写入性能极高（10w+ OPS），存储成本低（列式压缩），无需运维清理脚本。

#### PostgreSQL (开发环境)
*   **表结构**: 标准关系型表 `task_logs`。
*   **清理**: 依赖 API Server 内部的 `LogRetentionWorker` 定期执行 DELETE 操作。
*   **适用性**: 仅适用于日日志量小于 100 万行的场景。

### 3.4 前端消费

*   **协议**: Server-Sent Events (SSE)。
*   **流程**:
    1.  客户端发起 `GET /api/v1/tasks/{id}/logs/stream`。
    2.  服务端首先查询数据库，返回最近 N 条历史日志。
    3.  服务端订阅 Redis Channel，将后续产生的实时日志推送给客户端。
    4.  客户端通过 `Last-Event-ID` 机制处理断线重连（可选优化）。

## 4. 配置说明

在 `config.yaml` 中配置日志存储后端：

```yaml
logging:
  level: "info"
  storage_driver: "clickhouse" # 可选: postgres, clickhouse

clickhouse:
  hosts: ["127.0.0.1:9000"]
  database: "flowforge"
  user: "default"
  password: ""
```

## 5. 性能估算

以 1000 个并发任务，每个任务每秒产生 10 条日志为例：
*   **吞吐量**: 10,000 logs/sec。
*   **带宽**: 假设每条日志 200 Bytes，流量约为 2MB/s。
*   **存储**: 每天约 8.6 亿条日志。
    *   **PostgreSQL**: 约 200GB/天 (难以支撑)。
    *   **ClickHouse**: 约 20GB/天 (轻松支撑)。

## 6. 未来规划

*   **日志搜索**: 基于 ClickHouse 强大的 OLAP 能力，提供全文检索功能。
*   **日志下载**: 提供将完整日志导出为文件的接口。
*   **结构化日志**: 支持 JSON 格式的结构化日志解析和查询。

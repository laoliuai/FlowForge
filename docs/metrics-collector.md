# Metrics Collector

FlowForge 的 Metrics Collector 提供 Prometheus 兼容的指标采集能力，用于接收 SDK 上报的自定义指标，并统一暴露 `/metrics` 供 Prometheus 拉取。

## 运行方式

Metrics Collector 是独立的 HTTP 服务，通过 `server.metrics_port` 监听端口。启动方式：

```bash
go run ./cmd/metrics-collector
```

默认端口为 `9091`，可通过配置文件或环境变量修改（参见 `pkg/config`）。

## HTTP 端点

| 方法 | 路径 | 描述 |
| --- | --- | --- |
| GET | `/metrics` | Prometheus 抓取入口，包含系统与自定义指标 |
| POST | `/v1/metrics` | SDK 批量指标上报入口 |
| GET | `/healthz` | 存活检测 |

## SDK 指标上报格式

SDK 通过 `/v1/metrics` 提交批量指标，格式如下：

```json
{
  "metrics": [
    {
      "timestamp": 1719380000000,
      "task_id": "task-123",
      "workflow_id": "workflow-456",
      "tenant_id": "tenant-abc",
      "name": "items_processed",
      "value": 100,
      "type": "counter",
      "tags": {
        "batch": "1",
        "region": "cn-north-1"
      }
    }
  ]
}
```

支持的 `type`：

- `counter`
- `gauge`
- `histogram`

Collector 会对 `name` 和 `tags` 的 key 进行 Prometheus 合规化处理，并以 `flowforge_custom_` 为前缀注册成 Prometheus 指标。

## Collector 自身指标

Collector 也会暴露自身运行指标，便于运维监控：

| 指标 | 说明 |
| --- | --- |
| `flowforge_metrics_ingest_requests_total{status=...}` | 请求统计（ok/partial/error/empty） |
| `flowforge_metrics_ingest_samples_total{type=...}` | 成功采集的样本数 |
| `flowforge_metrics_ingest_batch_size` | 每批次样本数量 |
| `flowforge_metrics_ingest_latency_seconds` | 请求处理耗时 |
| `flowforge_metrics_ingest_invalid_total{reason=...}` | 无效样本原因统计 |
| `flowforge_metrics_last_ingest_timestamp_ms{tenant_id=...}` | 每个租户最后一次上报时间 |

## 指标命名与标签规范

- 指标名会被强制转为小写，并替换非法字符为 `_`。
- 标签名会被转为小写并替换非法字符为 `_`。
- 如标签名冲突，Collector 会自动追加后缀（例如 `_2`）。

## Docker 镜像构建

Metrics Collector 可单独打包为 Docker 镜像，使用项目根目录提供的 Dockerfile：

```bash
docker build -f cmd/metrics-collector/Dockerfile -t flowforge-metrics-collector:latest .
```

运行示例：

```bash
docker run --rm -p 9091:9091 flowforge-metrics-collector:latest
```

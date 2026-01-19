# Workflow 节点间 Artifact / Parameter 传递设计

## 1. 目标
- 提供 DAG 节点间的数据与参数传递机制，支持生产环境的可审计与可追溯。
- 支持大文件与小参数的分层存储与访问控制。
- 与共享存储、对象存储集成，确保生命周期与权限管理一致。

## 2. 非目标
- 不引入新的分布式训练框架或自动特征存储。
- 不处理跨集群复制（后续可扩展）。

## 3. 核心概念

### 3.1 Artifact
任务产出的文件或二进制数据，可能较大。

### 3.2 Parameter
轻量级 KV 或结构化数据（JSON），可用于控制流或轻量传参。

## 4. 数据模型

```yaml
artifact:
  id: "uuid"
  workflow_id: "uuid"
  task_id: "uuid"
  name: "model"
  uri: "s3://bucket/path/model.bin"
  size_bytes: 104857600
  sha256: "..."
  content_type: "application/octet-stream"
  created_at: "2024-01-01T00:00:00Z"
  ttl_seconds: 604800
  access_scope:
    tenant_id: "t-1"
    project_id: "p-1"
    task_readers: ["task-a", "task-b"]

parameter:
  key: "learning_rate"
  value: 0.01
  type: "float"
```

## 5. 存储与访问

### 5.1 存储后端
- **共享存储 (PVC/NAS)**: 面向工作流内共享目录，适用于中间产物。
- **对象存储 (S3/OSS/MinIO)**: 面向大文件、长期存储与跨节点访问。

### 5.2 目录隔离
- `/<tenant>/<project>/<workflow>/<task>/` 作为最小隔离单位。
- SDK 写入时自动注入前缀，防止跨任务污染。

### 5.3 访问控制
- 任务令牌包含 `artifact:read/write` scope。
- API Server 在下载/列举时校验 `tenant/project/task` 访问范围。

## 6. 生命周期管理
- 默认 TTL 可配置（workflow 级与 artifact 级）。
- 任务失败时保留可调（便于调试），成功后按 TTL 清理。
- 由后台 GC 统一清理：标记过期 -> 延迟删除 -> 记录审计。

## 7. SDK 与 API 交互

### 7.1 SDK 侧
- `client.output_artifact(name, local_path, metadata)`
- `client.get_artifact(name)` 返回可读 URI 或临时下载地址

### 7.2 API 侧
- `POST /api/v1/workflows/{id}/artifacts` 注册 artifact 元数据
- `GET /api/v1/workflows/{id}/artifacts?task_id=...`

### 7.3 数据传递与模板注入

为了让下游任务自动感知上游产物，FlowForge 在运行时提供变量解析与模板注入机制：

```yaml
tasks:
  - id: train
    outputs:
      artifacts:
        - name: model
          path: /mnt/models/latest.bin
  - id: eval
    inputs:
      artifacts:
        - name: model
          from: "{{tasks.train.outputs.artifacts.model.uri}}"
    args: ["--model", "{{tasks.train.outputs.artifacts.model.path}}"]
```

- **解析时机**：在任务入队前由 Controller 解析模板变量，将 URI/路径注入到下游任务环境变量或参数中。
- **权限校验**：解析时校验 `task_readers` 与 `tenant/project` 访问范围，避免跨租户读取。
- **回填与版本**：输出注册后生成稳定引用（如 `artifact_id`/`version`），下游模板可固定到版本。

## 8. 失败与幂等
- SDK 上传失败可重试，服务端以 `artifact_id` 去重。
- 重试时允许覆盖同名 artifact（按版本号递增）。

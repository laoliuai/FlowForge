# 多节点 DAG Workflow 与 Gang Scheduling 设计

## 背景
当前 Task 模型仅支持单 Pod 任务，无法表达多节点分布式训练与多 Pod 编排场景（如参数服务器/worker 组或多 GPU 节点协同）。同时，任务之间缺乏显式依赖关系描述与跨节点参数传递机制，导致无法支持钻石依赖等复杂 DAG。本文提出扩展 Workflow 与调度器以支持多节点 DAG 及 Gang Scheduling。

## 目标
- 支持多节点 DAG workflow：允许一个 workflow 内定义多个节点（Node），节点之间存在显式依赖关系（支持钻石依赖）。
- 支持 Gang Scheduling：在同一调度批次中对一组多 Pod 节点进行“要么全都排上、要么全都等待”的调度策略。
- 支持共享存储：同一 workflow 的节点共享同一个 storage class claim（共享 NAS），用于数据与中间产物协作。
- 暂不定义邻接节点参数传递机制，后续专项设计。

## 非目标
- 不引入跨 workflow 的依赖或全局 DAG。
- 不设计新的分布式训练框架，仅提供编排与依赖机制。
- 不实现跨集群或跨 region 的调度。

## 术语
- **Workflow**：用户提交的 DAG 工作流。
- **Node**：Workflow 中的一个任务节点，对应一个或多个 Pod（Replica）。
- **Gang**：需要同时调度的一组 Pod（通常属于同一 Node）。

## Workflow 模型扩展
### 结构
新增 Workflow 结构，包含节点列表与依赖关系。

```yaml
workflow:
  name: example
  storage:
    claimRef: my-nas-claim
  nodes:
    - id: preprocess
      replicas: 1
      image: my/preprocess:latest
      args: ["--input", "/mnt/data/raw"]
    - id: train
      replicas: 4
      image: my/train:latest
      dependsOn: [preprocess]
      args: ["--data", "/mnt/data/processed"]
    - id: eval
      replicas: 1
      image: my/eval:latest
      dependsOn: [train]
      args: ["--model", "/mnt/models/latest"]
    - id: publish
      replicas: 1
      image: my/publish:latest
      dependsOn: [train]
      args: ["--model", "/mnt/models/latest"]
```

### DAG 依赖与钻石依赖
- `dependsOn` 支持多依赖节点。
- 调度器在所有依赖节点成功完成后才可启动当前节点。

### 节点间参数传递（待设计）
- 当前仅定义依赖关系与执行顺序，不定义参数/Artifact 的传递协议。
- 后续需要补充：数据模型（Artifact/Parameter）、产出与消费方式、权限与隔离、失败策略、大小限制与生命周期。

## 数据模型设计
### 核心实体
新增或扩展的抽象（逻辑设计）：
- **Workflow**：包含 DAG 结构、共享存储 claim、状态。
- **WorkflowNode**：描述节点运行镜像、资源、Replica 数、依赖关系。
- **WorkflowNodeRun**：某次调度/重试实例（与现有 TaskRun 类似）。

### 状态机
- Workflow 状态：`Pending -> Running -> Succeeded/Failed/Canceled`。
- Node 状态：`Pending -> Scheduling -> Running -> Succeeded/Failed`。
- Gang 状态：`Pending -> Scheduled -> Running -> Succeeded/Failed`。

## 调度与执行
### 调度策略
1. Workflow 进入 `Running` 后，调度器计算可执行节点集合（依赖已满足）。
2. 对每个可执行节点创建一个 Gang 计划（按 `replicas` 生成 Pod 组）。
3. 调度器按 Gang 原则调度：
   - 若资源不足导致 Gang 不可完全排上，整个 Gang 进入等待。
   - 可选引入优先级与队列，但不在本次设计范围内。

### Pod 生成
- 每个 Node 对应 `replicas` 个 Pod。
- 所有 Pod 共享 `workflow.storage.claimRef` 的 PVC 挂载。
- Pod 内注入 SDK，用于上报状态。

### 参数/Artifact 传递
- 作为后续专项设计，当前不实现 stdout 解析或模板渲染。

## 错误处理与重试
- Node 失败后，默认 Workflow 失败（可扩展策略：继续/重试/跳过）。
- Gang 中任一 Pod 失败，Gang 失败并标记 Node 失败。

## API 设计
### 提交 Workflow
- 新增 `POST /api/workflows`，提交 DAG 描述。
- 返回 workflow id 与初始状态。

### 查询 Workflow
- `GET /api/workflows/{id}` 返回节点状态、输出与依赖图。

## 与现有 Task 模型的关系
- 保留现有单 Pod Task 作为 WorkflowNode 的特例（`replicas=1`）。
- 执行器可复用现有 TaskRun 流程，增加 Gang 入口与 DAG 调度层。

## 兼容性与迁移
- 现有 Task API 不受影响。
- 新增 Workflow API 为增量能力。

## 风险与挑战
- Gang 调度对集群资源敏感，容易出现长时间等待。
- 节点间参数/Artifact 传递需要补充细化设计。

## 后续工作
- 设计优先级与队列策略。
- 设计节点间参数/Artifact 的传递机制。
- 增加失败策略与重试策略配置。
- 为 Workflow DAG 提供可视化与调试工具。

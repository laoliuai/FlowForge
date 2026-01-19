# FlowForge: Kubernetes DAG Workflow Engine

## Executive Summary

FlowForge is a production-grade DAG workflow engine for Kubernetes that addresses the limitations of Argo Workflows while providing enterprise features like fine-grained quota management, external state storage, and comprehensive observability.

---

## Table of Contents

1. [System Architecture Overview](#1-system-architecture-overview)
2. [Core Components](#2-core-components)
3. [Database Design](#3-database-design)
4. [Task Queue Design](#4-task-queue-design)
5. [Quota Management System](#5-quota-management-system)
6. [Python SDK](#6-python-sdk)
7. [Log & Metrics Storage](#7-log--metrics-storage)
8. [Backoff & Retry Mechanism](#8-backoff--retry-mechanism)
9. [Go Package Structure](#9-go-package-structure)
10. [API Design](#10-api-design)
11. [Deployment Architecture](#11-deployment-architecture)
12. [Security Considerations](#12-security-considerations)

---

## 1. System Architecture Overview

### 1.1 High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              FlowForge System                                │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────────┐    ┌─────────────┐    ┌─────────────┐    ┌─────────────┐   │
│  │   Web UI    │    │  REST API   │    │  gRPC API   │    │   CLI Tool  │   │
│  └──────┬──────┘    └──────┬──────┘    └──────┬──────┘    └──────┬──────┘   │
│         │                  │                  │                  │          │
│         └──────────────────┴────────┬─────────┴──────────────────┘          │
│                                     │                                        │
│                          ┌──────────▼──────────┐                            │
│                          │    API Gateway      │                            │
│                          │  (Authentication)   │                            │
│                          └──────────┬──────────┘                            │
│                                     │                                        │
│    ┌────────────────────────────────┼────────────────────────────────┐      │
│    │                                │                                 │      │
│    ▼                                ▼                                 ▼      │
│ ┌──────────────┐         ┌──────────────────┐         ┌──────────────────┐  │
│ │  Workflow    │         │   Scheduler      │         │  Quota Manager   │  │
│ │  Controller  │◄───────►│   Service        │◄───────►│  Service         │  │
│ └──────┬───────┘         └────────┬─────────┘         └────────┬─────────┘  │
│        │                          │                             │           │
│        │    ┌─────────────────────┼─────────────────────────────┤           │
│        │    │                     │                             │           │
│        ▼    ▼                     ▼                             ▼           │
│ ┌────────────────┐    ┌────────────────┐           ┌────────────────┐       │
│ │  PostgreSQL    │    │     Kafka      │           │  ClickHouse/   │       │
│ │  (DAG Store)   │    │ (Events/Queue) │           │  MongoDB       │       │
│ └────────────────┘    └────────────────┘           │  (Logs/Metrics)│       │
│                                                     └────────────────┘       │
│                                                                              │
│    ┌─────────────────────────────────────────────────────────────────┐      │
│    │                    Kubernetes Cluster                            │      │
│    │  ┌─────────────────────────────────────────────────────────┐    │      │
│    │  │  ┌─────────┐  ┌─────────┐  ┌─────────┐  ┌─────────┐    │    │      │
│    │  │  │  Pod A  │  │  Pod B  │  │  Pod C  │  │  Pod D  │    │    │      │
│    │  │  │ ┌─────┐ │  │ ┌─────┐ │  │ ┌─────┐ │  │ ┌─────┐ │    │    │      │
│    │  │  │ │ SDK │ │  │ │ SDK │ │  │ │ SDK │ │  │ │ SDK │ │    │    │      │
│    │  │  │ └──┬──┘ │  │ └──┬──┘ │  │ └──┬──┘ │  │ └──┬──┘ │    │    │      │
│    │  │  └────┼────┘  └────┼────┘  └────┼────┘  └────┼────┘    │    │      │
│    │  │       └────────────┴───────┬────┴────────────┘         │    │      │
│    │  │                            │                           │    │      │
│    │  │                   ┌────────▼────────┐                  │    │      │
│    │  │                   │  Status Collector│                  │    │      │
│    │  │                   │  (DaemonSet)     │                  │    │      │
│    │  │                   └─────────────────┘                  │    │      │
│    │  └─────────────────────────────────────────────────────────┘    │      │
│    └─────────────────────────────────────────────────────────────────┘      │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 1.2 Data Flow

```
┌──────────────────────────────────────────────────────────────────────────┐
│                           Data Flow Diagram                               │
├──────────────────────────────────────────────────────────────────────────┤
│                                                                           │
│  1. DAG Submission                                                        │
│  ─────────────────                                                        │
│  User ──► API Gateway ──► Workflow Controller ──► PostgreSQL             │
│                                    │                                      │
│                                    ▼                                      │
│                              Scheduler                                    │
│                                    │                                      │
│  2. Task Scheduling                │                                      │
│  ──────────────────               │                                      │
│                                    ▼                                      │
│                         ┌─────────────────┐                              │
│                         │  Quota Check    │                              │
│                         └────────┬────────┘                              │
│                                  │                                        │
│                         ┌────────▼────────┐                              │
│                         │  Kafka Queue    │                              │
│                         │  (Publish Task) │                              │
│                         └────────┬────────┘                              │
│                                  │                                        │
│  3. Task Execution               │                                        │
│  ─────────────────              │                                        │
│                         ┌────────▼────────┐                              │
│                         │  Pod Executor   │──► Create K8s Pod            │
│                         └────────┬────────┘                              │
│                                  │                                        │
│  4. Status & Metrics Reporting   │                                        │
│  ─────────────────────────────  │                                        │
│                         ┌────────▼────────┐                              │
│                         │  Python SDK     │                              │
│                         │  (in Pod)       │                              │
│                         └────────┬────────┘                              │
│                                  │                                        │
│                    ┌─────────────┼─────────────┐                         │
│                    │             │             │                          │
│                    ▼             ▼             ▼                          │
│              PostgreSQL    ClickHouse      Redis                         │
│              (Status)      (Logs/Metrics)  (Heartbeat)                   │
│                                                                           │
└──────────────────────────────────────────────────────────────────────────┘
```

### 1.3 Key Design Principles

| Principle | Description |
|-----------|-------------|
| **Stateless Controllers** | All controllers are stateless; state is persisted in PostgreSQL/Redis |
| **Event-Driven** | Components communicate via Kafka topics and task queues |
| **Reliable Messaging** | Critical state changes use durable queues/outbox and idempotent handlers |
| **Horizontal Scalability** | All services can scale independently |
| **No CRD Dependency** | Workflow state stored in database, not Kubernetes CRDs |
| **Graceful Degradation** | System continues operating if observability components fail |
| **Multi-Tenancy First** | Quota and isolation built into core design |

---

## 2. Core Components

### 2.1 Component Overview

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         Core Components                                  │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                     Control Plane Components                        │ │
│  ├────────────────────────────────────────────────────────────────────┤ │
│  │                                                                     │ │
│  │  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐    │ │
│  │  │ API Server      │  │ Workflow        │  │ Scheduler       │    │ │
│  │  │                 │  │ Controller      │  │                 │    │ │
│  │  │ - REST/gRPC API │  │ - DAG Parser    │  │ - DAG Traversal │    │ │
│  │  │ - Auth/AuthZ    │  │ - State Machine │  │ - Dependency    │    │ │
│  │  │ - Rate Limiting │  │ - Event Handler │  │   Resolution    │    │ │
│  │  │ - Validation    │  │ - Reconciler    │  │ - Priority Queue│    │ │
│  │  └─────────────────┘  └─────────────────┘  └─────────────────┘    │ │
│  │                                                                     │ │
│  │  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐    │ │
│  │  │ Quota Manager   │  │ Pod Executor    │  │ Event Bus       │    │ │
│  │  │                 │  │                 │  │                 │    │ │
│  │  │ - Hierarchical  │  │ - Pod Lifecycle │  │ - Redis Pub/Sub │    │ │
│  │  │   Quotas        │  │ - Resource Mgmt │  │ - Event Router  │    │ │
│  │  │ - Usage Tracking│  │ - Container Mgmt│  │ - Retry Logic   │    │ │
│  │  │ - Admission Ctrl│  │ - Sidecar Inject│  │ - Dead Letter   │    │ │
│  │  └─────────────────┘  └─────────────────┘  └─────────────────┘    │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                                                          │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                     Data Plane Components                           │ │
│  ├────────────────────────────────────────────────────────────────────┤ │
│  │                                                                     │ │
│  │  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐    │ │
│  │  │ Status Collector│  │ Log Aggregator  │  │ Metrics         │    │ │
│  │  │ (DaemonSet)     │  │                 │  │ Collector       │    │ │
│  │  │                 │  │ - Log Streaming │  │                 │    │ │
│  │  │ - Pod Watcher   │  │ - Buffering     │  │ - Prometheus    │    │ │
│  │  │ - Health Check  │  │ - Compression   │  │   Compatible    │    │ │
│  │  │ - Event Relay   │  │ - Batching      │  │ - Custom Metrics│    │ │
│  │  └─────────────────┘  └─────────────────┘  └─────────────────┘    │ │
│  │                                                                     │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### 2.2 Component Specifications

#### 2.2.1 API Server

```go
// pkg/apiserver/server.go

package apiserver

type Server struct {
    router       *gin.Engine
    grpcServer   *grpc.Server
    authProvider auth.Provider
    validator    *validator.Validate
    rateLimiter  *redis.RateLimiter
}

// Key responsibilities:
// 1. REST API (Gin framework)
// 2. gRPC API (for internal services)
// 3. Authentication (JWT, OIDC, API Keys)
// 4. Authorization (RBAC with casbin)
// 5. Request validation
// 6. Rate limiting per tenant
```

**Endpoints:**

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/v1/workflows` | Submit new workflow |
| GET | `/api/v1/workflows/{id}` | Get workflow status |
| DELETE | `/api/v1/workflows/{id}` | Cancel workflow |
| GET | `/api/v1/workflows/{id}/tasks` | List tasks in workflow |
| GET | `/api/v1/workflows/{id}/logs` | Stream workflow logs |
| GET | `/api/v1/tenants/{id}/quota` | Get quota usage |
| PUT | `/api/v1/tenants/{id}/quota` | Update quota limits |

#### 2.2.2 Workflow Controller

```go
// pkg/controller/workflow_controller.go

package controller

type WorkflowController struct {
    db           *gorm.DB
    redis        *redis.Client
    k8sClient    kubernetes.Interface
    eventBus     *eventbus.EventBus
    scheduler    *scheduler.Scheduler
    stateManager *state.Manager
}

// State Machine for Workflow
type WorkflowState string

const (
    WorkflowPending   WorkflowState = "PENDING"
    WorkflowRunning   WorkflowState = "RUNNING"
    WorkflowSucceeded WorkflowState = "SUCCEEDED"
    WorkflowFailed    WorkflowState = "FAILED"
    WorkflowCancelled WorkflowState = "CANCELLED"
    WorkflowPaused    WorkflowState = "PAUSED"
)

// State Machine for Task
type TaskState string

const (
    TaskPending   TaskState = "PENDING"
    TaskQueued    TaskState = "QUEUED"
    TaskRunning   TaskState = "RUNNING"
    TaskSucceeded TaskState = "SUCCEEDED"
    TaskFailed    TaskState = "FAILED"
    TaskSkipped   TaskState = "SKIPPED"
    TaskRetrying  TaskState = "RETRYING"
)
```

**State Transition Diagram:**

```
┌─────────────────────────────────────────────────────────────────┐
│                    Task State Machine                            │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│                      ┌───────────┐                              │
│                      │  PENDING  │                              │
│                      └─────┬─────┘                              │
│                            │ (dependencies met + quota OK)      │
│                            ▼                                    │
│                      ┌───────────┐                              │
│                      │  QUEUED   │                              │
│                      └─────┬─────┘                              │
│                            │ (worker picks up)                  │
│                            ▼                                    │
│                      ┌───────────┐                              │
│           ┌─────────►│  RUNNING  │◄─────────┐                   │
│           │          └─────┬─────┘          │                   │
│           │                │                │                   │
│    (retry)│    ┌───────────┼───────────┐    │(retry after       │
│           │    │           │           │    │ backoff)          │
│           │    ▼           ▼           ▼    │                   │
│      ┌────┴────┐    ┌───────────┐  ┌───────┴───┐               │
│      │RETRYING │    │ SUCCEEDED │  │  FAILED   │               │
│      └─────────┘    └───────────┘  └───────────┘               │
│                                                                  │
│            Condition: when.condition == false                    │
│                           │                                      │
│                           ▼                                      │
│                      ┌───────────┐                              │
│                      │  SKIPPED  │                              │
│                      └───────────┘                              │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

#### 2.2.3 Scheduler Service

```go
// pkg/scheduler/scheduler.go

package scheduler

type Scheduler struct {
    db           *gorm.DB
    redis        *redis.Client
    quotaManager *quota.Manager
    taskQueue    *queue.TaskQueue
}

// DAG Traversal Algorithm
func (s *Scheduler) ProcessDAG(workflowID string) error {
    // 1. Load DAG from database
    dag, err := s.loadDAG(workflowID)

    // 2. Topological sort
    sortedTasks := s.topologicalSort(dag)

    // 3. For each task, check dependencies
    for _, task := range sortedTasks {
        if s.allDependenciesMet(task) {
            // 4. Check quota
            if s.quotaManager.CanSchedule(task) {
                // 5. Push to Redis queue
                s.taskQueue.Enqueue(task)
            }
        }
    }
    return nil
}
```

#### 2.2.4 Pod Executor

```go
// pkg/executor/pod_executor.go

package executor

type PodExecutor struct {
    k8sClient    kubernetes.Interface
    redis        *redis.Client
    sdkInjector  *injector.SDKInjector
}

// Pod template with SDK injection
func (e *PodExecutor) CreateTaskPod(task *model.Task) (*corev1.Pod, error) {
    pod := &corev1.Pod{
        ObjectMeta: metav1.ObjectMeta{
            Name:      fmt.Sprintf("ff-%s-%s", task.WorkflowID, task.ID),
            Namespace: task.Namespace,
            Labels: map[string]string{
                "flowforge.io/workflow-id": task.WorkflowID,
                "flowforge.io/task-id":     task.ID,
                "flowforge.io/tenant-id":   task.TenantID,
            },
        },
        Spec: corev1.PodSpec{
            Containers: []corev1.Container{
                {
                    Name:    "main",
                    Image:   task.Image,
                    Command: task.Command,
                    Env:     e.buildEnvVars(task),
                    VolumeMounts: []corev1.VolumeMount{
                        {
                            Name:      "flowforge-sdk",
                            MountPath: "/flowforge",
                        },
                    },
                },
            },
            InitContainers: []corev1.Container{
                e.sdkInjector.GetInitContainer(),
            },
            RestartPolicy: corev1.RestartPolicyNever,
        },
    }

    return e.k8sClient.CoreV1().Pods(task.Namespace).Create(
        context.Background(), pod, metav1.CreateOptions{})
}
```

**Kubernetes API 压力控制（大规模场景）**

- **Client-side throttling**：为 Pod 创建与更新请求设置 QPS/突发限制，避免在高并发 DAG 场景下压垮 apiserver。
- **Informer cache + work queue**：通过 SharedInformer 缓存 Pod/Node 状态，减少 `GET`/`LIST` 压力，并用指数退避队列处理失败创建。
- **批处理与限流**：Pod Executor 采用批量创建或并发度上限（worker pool），在负载过高时平滑回压。
- **可选常驻 Worker 模式**：对短任务可复用长驻 Worker Pod（或 job queue），减少 Pod churn。

### 2.3 Control Plane HA & 并发控制

为了避免多实例 Controller/Scheduler 之间的状态竞争，控制平面在逻辑上保持“单活处理 + 多活热备”的协调策略：

- **Leader Election**：Controller 与 Scheduler 使用 Kubernetes Lease API 进行选主；仅 Leader 负责调度/状态推进。
- **幂等更新**：所有状态写入携带 `resource_version` 或任务状态机版本号，重复事件不会回滚最终状态。
- **细粒度锁**：针对同一 workflow/queue 的关键区段，可使用数据库事务锁或 Redis 分布式锁确保串行处理。
- **故障转移**：Leader 失效后，Lease 过期自动切换；Follower 通过订阅事件总线保持“热备”。

---

## 3. Database Design

### 3.1 Entity Relationship Diagram

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    Entity Relationship Diagram                           │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  ┌──────────────┐         ┌──────────────┐         ┌──────────────┐    │
│  │   Tenant     │────────<│    Group     │────────<│   Project    │    │
│  │              │   1:N   │              │   1:N   │              │    │
│  │ - id         │         │ - id         │         │ - id         │    │
│  │ - name       │         │ - tenant_id  │         │ - group_id   │    │
│  │ - quota_id   │         │ - name       │         │ - name       │    │
│  └──────┬───────┘         │ - quota_id   │         │ - quota_id   │    │
│         │                 └──────────────┘         └──────┬───────┘    │
│         │                                                  │            │
│         │                 ┌──────────────┐                │            │
│         └────────────────>│    Quota     │<───────────────┘            │
│              1:1          │              │                              │
│                           │ - id         │                              │
│                           │ - cpu_limit  │                              │
│                           │ - mem_limit  │                              │
│                           │ - pod_limit  │                              │
│                           └──────────────┘                              │
│                                                                          │
│  ┌──────────────────────────────────────────────────────────────────┐   │
│  │                                                                   │   │
│  │  ┌──────────────┐    1:N    ┌──────────────┐    1:N             │   │
│  │  │   Workflow   │──────────>│     Task     │──────────┐         │   │
│  │  │              │           │              │          │         │   │
│  │  │ - id         │           │ - id         │          ▼         │   │
│  │  │ - project_id │           │ - workflow_id│    ┌──────────┐    │   │
│  │  │ - dag_spec   │           │ - name       │    │TaskDep   │    │   │
│  │  │ - status     │           │ - image      │    │          │    │   │
│  │  │ - created_at │           │ - command    │    │- task_id │    │   │
│  │  │ - params     │           │ - status     │    │- depends │    │   │
│  │  └──────────────┘           │ - retry_count│    └──────────┘    │   │
│  │                             │ - backoff_at │                     │   │
│  │                             └──────┬───────┘                     │   │
│  │                                    │                              │   │
│  │                                    │ 1:N                          │   │
│  │                                    ▼                              │   │
│  │                             ┌──────────────┐                     │   │
│  │                             │  TaskEvent   │                     │   │
│  │                             │              │                     │   │
│  │                             │ - id         │                     │   │
│  │                             │ - task_id    │                     │   │
│  │                             │ - event_type │                     │   │
│  │                             │ - message    │                     │   │
│  │                             │ - timestamp  │                     │   │
│  │                             └──────────────┘                     │   │
│  │                                                                   │   │
│  └──────────────────────────────────────────────────────────────────┘   │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### 3.2 PostgreSQL Schema

```sql
-- migrations/001_initial_schema.sql

-- ============================================
-- Tenant Hierarchy Tables
-- ============================================

CREATE TABLE tenants (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            VARCHAR(255) NOT NULL UNIQUE,
    description     TEXT,
    quota_id        UUID,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at      TIMESTAMP WITH TIME ZONE
);

CREATE TABLE groups (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id),
    name            VARCHAR(255) NOT NULL,
    description     TEXT,
    quota_id        UUID,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at      TIMESTAMP WITH TIME ZONE,
    UNIQUE(tenant_id, name)
);

CREATE TABLE projects (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id        UUID NOT NULL REFERENCES groups(id),
    name            VARCHAR(255) NOT NULL,
    description     TEXT,
    quota_id        UUID,
    namespace       VARCHAR(63) NOT NULL,  -- K8s namespace
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    deleted_at      TIMESTAMP WITH TIME ZONE,
    UNIQUE(group_id, name)
);

CREATE TABLE departments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id),
    name            VARCHAR(255) NOT NULL,
    parent_id       UUID REFERENCES departments(id),  -- Hierarchical departments
    quota_id        UUID,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(tenant_id, name)
);

-- ============================================
-- Quota Tables
-- ============================================

CREATE TABLE quotas (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                VARCHAR(255) NOT NULL,
    -- CPU in millicores (1000m = 1 core)
    cpu_limit           INTEGER NOT NULL DEFAULT 4000,
    cpu_request         INTEGER NOT NULL DEFAULT 2000,
    -- Memory in MiB
    memory_limit        INTEGER NOT NULL DEFAULT 8192,
    memory_request      INTEGER NOT NULL DEFAULT 4096,
    -- GPU count
    gpu_limit           INTEGER NOT NULL DEFAULT 0,
    -- Concurrent pods
    max_concurrent_pods INTEGER NOT NULL DEFAULT 10,
    -- Concurrent workflows
    max_concurrent_wf   INTEGER NOT NULL DEFAULT 5,
    -- Storage in GiB
    storage_limit       INTEGER NOT NULL DEFAULT 100,
    -- Priority weight (higher = more priority)
    priority_weight     INTEGER NOT NULL DEFAULT 100,
    created_at          TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at          TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE TABLE quota_usage (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    quota_id        UUID NOT NULL REFERENCES quotas(id),
    entity_type     VARCHAR(50) NOT NULL,  -- tenant, group, project, department
    entity_id       UUID NOT NULL,
    cpu_used        INTEGER NOT NULL DEFAULT 0,
    memory_used     INTEGER NOT NULL DEFAULT 0,
    gpu_used        INTEGER NOT NULL DEFAULT 0,
    pods_running    INTEGER NOT NULL DEFAULT 0,
    workflows_active INTEGER NOT NULL DEFAULT 0,
    storage_used    INTEGER NOT NULL DEFAULT 0,
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(quota_id, entity_type, entity_id)
);

-- Add foreign keys after quotas table exists
ALTER TABLE tenants ADD CONSTRAINT fk_tenant_quota
    FOREIGN KEY (quota_id) REFERENCES quotas(id);
ALTER TABLE groups ADD CONSTRAINT fk_group_quota
    FOREIGN KEY (quota_id) REFERENCES quotas(id);
ALTER TABLE projects ADD CONSTRAINT fk_project_quota
    FOREIGN KEY (quota_id) REFERENCES quotas(id);
ALTER TABLE departments ADD CONSTRAINT fk_department_quota
    FOREIGN KEY (quota_id) REFERENCES quotas(id);

-- ============================================
-- Workflow Tables
-- ============================================

CREATE TABLE workflows (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      UUID NOT NULL REFERENCES projects(id),
    name            VARCHAR(255) NOT NULL,
    description     TEXT,
    -- DAG definition stored as JSONB
    dag_spec        JSONB NOT NULL,
    -- Input parameters
    parameters      JSONB DEFAULT '{}',
    -- Workflow status
    status          VARCHAR(50) NOT NULL DEFAULT 'PENDING',
    -- Error message if failed
    error_message   TEXT,
    -- Timing
    started_at      TIMESTAMP WITH TIME ZONE,
    finished_at     TIMESTAMP WITH TIME ZONE,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    -- Submitter info
    submitted_by    VARCHAR(255),
    -- Priority (higher = more urgent)
    priority        INTEGER NOT NULL DEFAULT 0,
    -- Labels for filtering
    labels          JSONB DEFAULT '{}'
);

CREATE INDEX idx_workflows_project ON workflows(project_id);
CREATE INDEX idx_workflows_status ON workflows(status);
CREATE INDEX idx_workflows_created ON workflows(created_at DESC);
CREATE INDEX idx_workflows_labels ON workflows USING GIN(labels);

CREATE TABLE tasks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id     UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    name            VARCHAR(255) NOT NULL,
    -- Task definition
    template_name   VARCHAR(255),
    image           VARCHAR(512) NOT NULL,
    command         TEXT[],
    args            TEXT[],
    env             JSONB DEFAULT '[]',
    -- Resource requirements
    cpu_request     INTEGER DEFAULT 100,   -- millicores
    cpu_limit       INTEGER DEFAULT 1000,
    memory_request  INTEGER DEFAULT 256,   -- MiB
    memory_limit    INTEGER DEFAULT 1024,
    gpu_request     INTEGER DEFAULT 0,
    -- Task status
    status          VARCHAR(50) NOT NULL DEFAULT 'PENDING',
    error_message   TEXT,
    exit_code       INTEGER,
    -- Retry configuration
    retry_limit     INTEGER DEFAULT 3,
    retry_count     INTEGER DEFAULT 0,
    backoff_seconds INTEGER DEFAULT 0,
    next_retry_at   TIMESTAMP WITH TIME ZONE,
    -- Timing
    queued_at       TIMESTAMP WITH TIME ZONE,
    started_at      TIMESTAMP WITH TIME ZONE,
    finished_at     TIMESTAMP WITH TIME ZONE,
    -- K8s pod info
    pod_name        VARCHAR(255),
    pod_uid         VARCHAR(255),
    node_name       VARCHAR(255),
    -- Outputs
    outputs         JSONB DEFAULT '{}',
    -- Conditional execution
    when_condition  TEXT,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(workflow_id, name)
);

CREATE INDEX idx_tasks_workflow ON tasks(workflow_id);
CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_retry ON tasks(next_retry_at) WHERE status = 'RETRYING';

CREATE TABLE task_dependencies (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    depends_on_id   UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    -- Dependency type
    type            VARCHAR(50) DEFAULT 'success',  -- success, completion, failure
    UNIQUE(task_id, depends_on_id)
);

CREATE INDEX idx_task_deps_task ON task_dependencies(task_id);
CREATE INDEX idx_task_deps_depends ON task_dependencies(depends_on_id);

CREATE TABLE task_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id         UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    event_type      VARCHAR(50) NOT NULL,
    message         TEXT,
    details         JSONB,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_task_events_task ON task_events(task_id);
CREATE INDEX idx_task_events_time ON task_events(created_at DESC);

-- ============================================
-- Template Tables (Reusable Task Templates)
-- ============================================

CREATE TABLE task_templates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      UUID REFERENCES projects(id),  -- NULL = global template
    name            VARCHAR(255) NOT NULL,
    version         VARCHAR(50) NOT NULL DEFAULT '1.0.0',
    description     TEXT,
    -- Template specification
    spec            JSONB NOT NULL,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    UNIQUE(project_id, name, version)
);

-- ============================================
-- Audit Log
-- ============================================

CREATE TABLE audit_logs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_type     VARCHAR(50) NOT NULL,
    entity_id       UUID NOT NULL,
    action          VARCHAR(50) NOT NULL,
    actor           VARCHAR(255) NOT NULL,
    actor_ip        INET,
    old_value       JSONB,
    new_value       JSONB,
    created_at      TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

CREATE INDEX idx_audit_entity ON audit_logs(entity_type, entity_id);
CREATE INDEX idx_audit_time ON audit_logs(created_at DESC);
```

### 3.3 DAG Specification Format (JSONB)

```json
{
  "version": "1.0",
  "entrypoint": "main",
  "templates": {
    "main": {
      "dag": {
        "tasks": [
          {
            "name": "extract-data",
            "template": "python-task",
            "arguments": {
              "parameters": [
                {"name": "script", "value": "extract.py"}
              ]
            }
          },
          {
            "name": "transform-data",
            "template": "python-task",
            "dependencies": ["extract-data"],
            "arguments": {
              "parameters": [
                {"name": "script", "value": "transform.py"},
                {"name": "input", "value": "{{tasks.extract-data.outputs.result}}"}
              ]
            }
          },
          {
            "name": "load-data",
            "template": "python-task",
            "dependencies": ["transform-data"],
            "when": "{{tasks.transform-data.outputs.success}} == true",
            "arguments": {
              "parameters": [
                {"name": "script", "value": "load.py"}
              ]
            }
          }
        ]
      }
    },
    "python-task": {
      "container": {
        "image": "python:3.11-slim",
        "command": ["python"],
        "args": ["{{inputs.parameters.script}}"],
        "resources": {
          "requests": {"cpu": "100m", "memory": "256Mi"},
          "limits": {"cpu": "1", "memory": "1Gi"}
        }
      },
      "inputs": {
        "parameters": [
          {"name": "script"},
          {"name": "input", "default": ""}
        ]
      },
      "outputs": {
        "parameters": [
          {"name": "result", "valueFrom": {"path": "/tmp/output.json"}}
        ]
      },
      "retryStrategy": {
        "limit": 3,
        "backoff": {
          "duration": "10s",
          "factor": 2,
          "maxDuration": "5m"
        }
      }
    }
  },
  "onExit": "cleanup",
  "ttlStrategy": {
    "secondsAfterCompletion": 86400,
    "secondsAfterSuccess": 3600,
    "secondsAfterFailure": 604800
  }
}
```

---

## 4. Task Queue Design

### 4.1 Redis Queue Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                      Redis Queue Architecture                            │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                      Priority Queues                                │ │
│  ├────────────────────────────────────────────────────────────────────┤ │
│  │                                                                     │ │
│  │  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐ │ │
│  │  │ ff:queue:high    │  │ ff:queue:normal  │  │ ff:queue:low     │ │ │
│  │  │ (Sorted Set)     │  │ (Sorted Set)     │  │ (Sorted Set)     │ │ │
│  │  │                  │  │                  │  │                  │ │ │
│  │  │ Score: timestamp │  │ Score: timestamp │  │ Score: timestamp │ │ │
│  │  │ Member: task_id  │  │ Member: task_id  │  │ Member: task_id  │ │ │
│  │  └──────────────────┘  └──────────────────┘  └──────────────────┘ │ │
│  │                                                                     │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                                                          │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                      Processing State                               │ │
│  ├────────────────────────────────────────────────────────────────────┤ │
│  │                                                                     │ │
│  │  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐ │ │
│  │  │ ff:processing    │  │ ff:task:{id}     │  │ ff:heartbeat     │ │ │
│  │  │ (Set)            │  │ (Hash)           │  │ (Sorted Set)     │ │ │
│  │  │                  │  │                  │  │                  │ │ │
│  │  │ Currently        │  │ - workflow_id    │  │ Score: timestamp │ │ │
│  │  │ executing tasks  │  │ - status         │  │ Member: task_id  │ │ │
│  │  │                  │  │ - worker_id      │  │                  │ │ │
│  │  │                  │  │ - started_at     │  │ Last heartbeat   │ │ │
│  │  │                  │  │ - pod_name       │  │ from each task   │ │ │
│  │  └──────────────────┘  └──────────────────┘  └──────────────────┘ │ │
│  │                                                                     │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                                                          │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                      Pub/Sub Channels                               │ │
│  ├────────────────────────────────────────────────────────────────────┤ │
│  │                                                                     │ │
│  │  ┌─────────────────────────┐  ┌─────────────────────────┐         │ │
│  │  │ ff:events:task          │  │ ff:events:workflow      │         │ │
│  │  │                         │  │                         │         │ │
│  │  │ Task state changes:     │  │ Workflow state changes: │         │ │
│  │  │ - QUEUED                │  │ - STARTED               │         │ │
│  │  │ - RUNNING               │  │ - COMPLETED             │         │ │
│  │  │ - SUCCEEDED             │  │ - FAILED                │         │ │
│  │  │ - FAILED                │  │ - CANCELLED             │         │ │
│  │  │ - RETRYING              │  │                         │         │ │
│  │  └─────────────────────────┘  └─────────────────────────┘         │ │
│  │                                                                     │ │
│  │  ┌─────────────────────────┐  ┌─────────────────────────┐         │ │
│  │  │ ff:events:quota         │  │ ff:deadletter           │         │ │
│  │  │                         │  │ (List)                  │         │ │
│  │  │ Quota updates:          │  │                         │         │ │
│  │  │ - QUOTA_EXCEEDED        │  │ Tasks that failed       │         │ │
│  │  │ - QUOTA_RELEASED        │  │ max retries             │         │ │
│  │  │ - QUOTA_UPDATED         │  │                         │         │ │
│  │  └─────────────────────────┘  └─────────────────────────┘         │ │
│  │                                                                     │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                                                          │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                      Tenant-Specific Queues                         │ │
│  ├────────────────────────────────────────────────────────────────────┤ │
│  │                                                                     │ │
│  │  Per-tenant isolation for fair scheduling:                         │ │
│  │                                                                     │ │
│  │  ff:queue:tenant:{tenant_id}:high                                  │ │
│  │  ff:queue:tenant:{tenant_id}:normal                                │ │
│  │  ff:queue:tenant:{tenant_id}:low                                   │ │
│  │                                                                     │ │
│  │  Usage counters (with TTL for rate limiting):                      │ │
│  │  ff:usage:{tenant_id}:pods      (Counter with TTL)                 │ │
│  │  ff:usage:{tenant_id}:cpu       (Counter with TTL)                 │ │
│  │  ff:usage:{tenant_id}:memory    (Counter with TTL)                 │ │
│  │                                                                     │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### 4.2 Queue Implementation

```go
// pkg/queue/task_queue.go

package queue

import (
    "context"
    "encoding/json"
    "time"

    "github.com/redis/go-redis/v9"
)

type TaskQueue struct {
    client *redis.Client
}

type QueuedTask struct {
    TaskID      string            `json:"task_id"`
    WorkflowID  string            `json:"workflow_id"`
    TenantID    string            `json:"tenant_id"`
    ProjectID   string            `json:"project_id"`
    Priority    int               `json:"priority"`
    Resources   ResourceRequest   `json:"resources"`
    EnqueuedAt  time.Time         `json:"enqueued_at"`
}

type ResourceRequest struct {
    CPU    int `json:"cpu"`     // millicores
    Memory int `json:"memory"`  // MiB
    GPU    int `json:"gpu"`
}

const (
    QueueHighPriority   = "ff:queue:high"
    QueueNormalPriority = "ff:queue:normal"
    QueueLowPriority    = "ff:queue:low"
    ProcessingSet       = "ff:processing"
    HeartbeatSet        = "ff:heartbeat"
    DeadLetterQueue     = "ff:deadletter"
)

// Enqueue adds a task to the appropriate priority queue
func (q *TaskQueue) Enqueue(ctx context.Context, task *QueuedTask) error {
    queueName := q.getQueueName(task.Priority, task.TenantID)

    data, err := json.Marshal(task)
    if err != nil {
        return err
    }

    // Score is timestamp for FIFO ordering within priority
    score := float64(time.Now().UnixNano())

    return q.client.ZAdd(ctx, queueName, redis.Z{
        Score:  score,
        Member: string(data),
    }).Err()
}

// Dequeue pops the highest priority task available
// Uses weighted fair queuing across tenants
func (q *TaskQueue) Dequeue(ctx context.Context, workerID string) (*QueuedTask, error) {
    // Try queues in priority order
    queues := []string{QueueHighPriority, QueueNormalPriority, QueueLowPriority}

    for _, queue := range queues {
        // Atomic pop from sorted set
        result, err := q.client.ZPopMin(ctx, queue, 1).Result()
        if err != nil {
            continue
        }
        if len(result) == 0 {
            continue
        }

        var task QueuedTask
        if err := json.Unmarshal([]byte(result[0].Member.(string)), &task); err != nil {
            continue
        }

        // Add to processing set
        q.client.SAdd(ctx, ProcessingSet, task.TaskID)

        // Store task metadata
        q.client.HSet(ctx, "ff:task:"+task.TaskID, map[string]interface{}{
            "worker_id":  workerID,
            "started_at": time.Now().Unix(),
            "status":     "processing",
        })

        return &task, nil
    }

    return nil, nil // No tasks available
}

// Heartbeat updates the last seen time for a task
func (q *TaskQueue) Heartbeat(ctx context.Context, taskID string) error {
    return q.client.ZAdd(ctx, HeartbeatSet, redis.Z{
        Score:  float64(time.Now().Unix()),
        Member: taskID,
    }).Err()
}

// Complete marks a task as completed and removes from processing
func (q *TaskQueue) Complete(ctx context.Context, taskID string, success bool) error {
    pipe := q.client.Pipeline()

    pipe.SRem(ctx, ProcessingSet, taskID)
    pipe.ZRem(ctx, HeartbeatSet, taskID)
    pipe.Del(ctx, "ff:task:"+taskID)

    if !success {
        // Will be handled by retry mechanism
        pipe.LPush(ctx, DeadLetterQueue, taskID)
    }

    _, err := pipe.Exec(ctx)
    return err
}

// RecoverStale finds tasks that haven't sent heartbeat and re-queues them
func (q *TaskQueue) RecoverStale(ctx context.Context, timeout time.Duration) error {
    cutoff := float64(time.Now().Add(-timeout).Unix())

    // Find tasks with old heartbeats
    stale, err := q.client.ZRangeByScore(ctx, HeartbeatSet, &redis.ZRangeBy{
        Min: "-inf",
        Max: fmt.Sprintf("%f", cutoff),
    }).Result()

    if err != nil {
        return err
    }

    for _, taskID := range stale {
        // Re-queue task (scheduler will handle this)
        q.client.Publish(ctx, "ff:events:task", json.Marshal(map[string]string{
            "task_id": taskID,
            "event":   "STALE_DETECTED",
        }))
    }

    return nil
}

func (q *TaskQueue) getQueueName(priority int, tenantID string) string {
    var priorityName string
    switch {
    case priority >= 100:
        priorityName = "high"
    case priority >= 50:
        priorityName = "normal"
    default:
        priorityName = "low"
    }

    // Use tenant-specific queue for isolation
    return fmt.Sprintf("ff:queue:tenant:%s:%s", tenantID, priorityName)
}
```

### 4.3 Event Bus Implementation

```go
// pkg/eventbus/eventbus.go

package eventbus

import (
    "context"
    "encoding/json"

    "github.com/redis/go-redis/v9"
)

type EventBus struct {
    client *redis.Client
    pubsub *redis.PubSub
}

type Event struct {
    Type      string          `json:"type"`
    Timestamp int64           `json:"timestamp"`
    Data      json.RawMessage `json:"data"`
}

type TaskEvent struct {
    TaskID     string `json:"task_id"`
    WorkflowID string `json:"workflow_id"`
    Status     string `json:"status"`
    Message    string `json:"message,omitempty"`
    ExitCode   *int   `json:"exit_code,omitempty"`
}

type WorkflowEvent struct {
    WorkflowID string `json:"workflow_id"`
    Status     string `json:"status"`
    Message    string `json:"message,omitempty"`
}

const (
    ChannelTask     = "ff:events:task"
    ChannelWorkflow = "ff:events:workflow"
    ChannelQuota    = "ff:events:quota"
)

func (eb *EventBus) Publish(ctx context.Context, channel string, event interface{}) error {
    data, err := json.Marshal(event)
    if err != nil {
        return err
    }
    return eb.client.Publish(ctx, channel, data).Err()
}

func (eb *EventBus) Subscribe(ctx context.Context, channels ...string) <-chan *Event {
    eb.pubsub = eb.client.Subscribe(ctx, channels...)
    ch := make(chan *Event, 100)

    go func() {
        defer close(ch)
        for msg := range eb.pubsub.Channel() {
            var event Event
            if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
                continue
            }
            ch <- &event
        }
    }()

    return ch
}
```

### 4.4 Reliable Messaging & Durable Events

Redis Pub/Sub is retained for low-latency fan-out, but **critical state changes** (task/workflow transitions, retries, quota updates) must be durable:

1. **Transactional Outbox**: controllers persist the state change and an `events` outbox row in the same DB transaction.
2. **Outbox Relay**: a relay worker reads pending events and publishes them into Redis Streams (or another durable queue).
3. **Idempotent Consumers**: event handlers store `event_id`/offsets and ignore duplicates.
4. **Dead-letter Routing**: permanently failed events are moved to a DLQ with retry metadata for inspection.

This design ensures at-least-once delivery and safe recovery after component restarts or Redis failover.

---

## 5. Quota Management System

### 5.1 Hierarchical Quota Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    Hierarchical Quota Model                              │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  Level 1: Tenant (Organization)                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  Tenant: "Acme Corp"                                             │   │
│  │  Quota: CPU=100cores, Memory=512GB, GPU=16, Pods=500             │   │
│  │                                                                   │   │
│  │  ┌─────────────────────────────────────────────────────────────┐ │   │
│  │  │  Level 2: Department (Organizational Unit)                   │ │   │
│  │  │  ┌─────────────────┐  ┌─────────────────┐                   │ │   │
│  │  │  │ Engineering     │  │ Data Science    │                   │ │   │
│  │  │  │ CPU=60c Mem=300G│  │ CPU=40c Mem=212G│                   │ │   │
│  │  │  │ GPU=8  Pods=300 │  │ GPU=8  Pods=200 │                   │ │   │
│  │  │  └────────┬────────┘  └────────┬────────┘                   │ │   │
│  │  │           │                    │                             │ │   │
│  │  │  ┌────────┴────────────────────┴────────┐                   │ │   │
│  │  │  │  Level 3: Group (Team)               │                   │ │   │
│  │  │  │  ┌─────────────┐  ┌─────────────┐    │                   │ │   │
│  │  │  │  │ Platform    │  │ ML Team     │    │                   │ │   │
│  │  │  │  │ CPU=30c     │  │ CPU=20c     │    │                   │ │   │
│  │  │  │  │ Mem=150G    │  │ Mem=100G    │    │                   │ │   │
│  │  │  │  └──────┬──────┘  └──────┬──────┘    │                   │ │   │
│  │  │  │         │                │           │                   │ │   │
│  │  │  │  ┌──────┴────────────────┴──────┐    │                   │ │   │
│  │  │  │  │  Level 4: Project            │    │                   │ │   │
│  │  │  │  │  ┌─────────┐  ┌─────────┐    │    │                   │ │   │
│  │  │  │  │  │ API Svc │  │ ETL     │    │    │                   │ │   │
│  │  │  │  │  │ CPU=10c │  │ CPU=20c │    │    │                   │ │   │
│  │  │  │  │  │ Mem=50G │  │ Mem=100G│    │    │                   │ │   │
│  │  │  │  │  └─────────┘  └─────────┘    │    │                   │ │   │
│  │  │  │  └──────────────────────────────┘    │                   │ │   │
│  │  │  └──────────────────────────────────────┘                   │ │   │
│  │  └─────────────────────────────────────────────────────────────┘ │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                          │
│  Quota Inheritance Rules:                                               │
│  1. Child quota cannot exceed parent quota                              │
│  2. Sum of sibling quotas can exceed parent (overcommit allowed)        │
│  3. Actual usage is limited by min(own_quota, parent_remaining)         │
│  4. Priority weight determines scheduling order when contending         │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### 5.2 Atomic Quota Reservation

To avoid over-allocation when multiple schedulers run concurrently, quota updates must be **atomic**:

- **Reservation-first**: scheduler requests a reservation for `CPU/Memory/GPU/Pods` across the hierarchy before enqueueing pods.
- **Atomic update**: use a single DB transaction or Redis Lua script to validate and increment usage across all hierarchy levels.
- **Lease expiry**: reservations carry a TTL; if the pod is not created within the lease, usage is released automatically.
- **Idempotent release**: task completion calls must tolerate duplicates (e.g., by using a unique reservation ID).

This guarantees correctness for Gang Scheduling and prevents race conditions from distributed schedulers.

### 5.3 Quota Manager Implementation

```go
// pkg/quota/manager.go

package quota

import (
    "context"
    "fmt"
    "sync"

    "gorm.io/gorm"
    "github.com/redis/go-redis/v9"
)

type Manager struct {
    db    *gorm.DB
    redis *redis.Client
    mu    sync.RWMutex
    cache map[string]*QuotaInfo
}

type QuotaInfo struct {
    QuotaID       string
    EntityType    string  // tenant, department, group, project
    EntityID      string
    Limits        ResourceLimits
    Used          ResourceUsage
    ParentQuotaID *string
}

type ResourceLimits struct {
    CPULimit           int // millicores
    CPURequest         int
    MemoryLimit        int // MiB
    MemoryRequest      int
    GPULimit           int
    MaxConcurrentPods  int
    MaxConcurrentWF    int
    StorageLimit       int // GiB
    PriorityWeight     int
}

type ResourceUsage struct {
    CPUUsed        int
    MemoryUsed     int
    GPUUsed        int
    PodsRunning    int
    WorkflowsActive int
    StorageUsed    int
}

type ResourceRequest struct {
    CPU    int `json:"cpu"`
    Memory int `json:"memory"`
    GPU    int `json:"gpu"`
}

// CanSchedule checks if resources are available at all hierarchy levels
func (m *Manager) CanSchedule(ctx context.Context, projectID string, req ResourceRequest) (bool, string) {
    // Get full hierarchy path
    hierarchy, err := m.getHierarchy(ctx, projectID)
    if err != nil {
        return false, err.Error()
    }

    // Check each level in hierarchy (bottom-up)
    for _, level := range hierarchy {
        quota, err := m.getQuotaInfo(ctx, level.EntityType, level.EntityID)
        if err != nil {
            continue
        }

        // Check CPU
        if quota.Used.CPUUsed + req.CPU > quota.Limits.CPULimit {
            return false, fmt.Sprintf("CPU quota exceeded at %s level", level.EntityType)
        }

        // Check Memory
        if quota.Used.MemoryUsed + req.Memory > quota.Limits.MemoryLimit {
            return false, fmt.Sprintf("Memory quota exceeded at %s level", level.EntityType)
        }

        // Check GPU
        if quota.Used.GPUUsed + req.GPU > quota.Limits.GPULimit {
            return false, fmt.Sprintf("GPU quota exceeded at %s level", level.EntityType)
        }

        // Check concurrent pods
        if quota.Used.PodsRunning >= quota.Limits.MaxConcurrentPods {
            return false, fmt.Sprintf("Pod limit reached at %s level", level.EntityType)
        }
    }

    return true, ""
}

// Reserve atomically reserves resources at all hierarchy levels
func (m *Manager) Reserve(ctx context.Context, projectID string, req ResourceRequest) error {
    hierarchy, err := m.getHierarchy(ctx, projectID)
    if err != nil {
        return err
    }

    // Use Redis transaction for atomicity
    pipe := m.redis.Pipeline()

    for _, level := range hierarchy {
        key := fmt.Sprintf("ff:usage:%s:%s", level.EntityType, level.EntityID)
        pipe.HIncrBy(ctx, key, "cpu_used", int64(req.CPU))
        pipe.HIncrBy(ctx, key, "memory_used", int64(req.Memory))
        pipe.HIncrBy(ctx, key, "gpu_used", int64(req.GPU))
        pipe.HIncrBy(ctx, key, "pods_running", 1)
    }

    _, err = pipe.Exec(ctx)
    if err != nil {
        // Rollback on failure
        m.Release(ctx, projectID, req)
        return err
    }

    // Async update PostgreSQL for durability
    go m.persistUsage(context.Background(), hierarchy)

    return nil
}

// Release frees reserved resources at all hierarchy levels
func (m *Manager) Release(ctx context.Context, projectID string, req ResourceRequest) error {
    hierarchy, err := m.getHierarchy(ctx, projectID)
    if err != nil {
        return err
    }

    pipe := m.redis.Pipeline()

    for _, level := range hierarchy {
        key := fmt.Sprintf("ff:usage:%s:%s", level.EntityType, level.EntityID)
        pipe.HIncrBy(ctx, key, "cpu_used", int64(-req.CPU))
        pipe.HIncrBy(ctx, key, "memory_used", int64(-req.Memory))
        pipe.HIncrBy(ctx, key, "gpu_used", int64(-req.GPU))
        pipe.HIncrBy(ctx, key, "pods_running", -1)
    }

    _, err = pipe.Exec(ctx)

    // Async update PostgreSQL
    go m.persistUsage(context.Background(), hierarchy)

    return err
}

// getHierarchy returns the full quota hierarchy for a project
func (m *Manager) getHierarchy(ctx context.Context, projectID string) ([]HierarchyLevel, error) {
    var levels []HierarchyLevel

    // Query database for hierarchy
    var project struct {
        ID        string
        GroupID   string
        Group     struct {
            ID           string
            TenantID     string
            DepartmentID *string
        }
    }

    err := m.db.WithContext(ctx).
        Table("projects").
        Select("projects.id, projects.group_id, groups.id, groups.tenant_id, groups.department_id").
        Joins("JOIN groups ON groups.id = projects.group_id").
        Where("projects.id = ?", projectID).
        First(&project).Error

    if err != nil {
        return nil, err
    }

    // Build hierarchy bottom-up
    levels = append(levels, HierarchyLevel{EntityType: "project", EntityID: project.ID})
    levels = append(levels, HierarchyLevel{EntityType: "group", EntityID: project.GroupID})

    if project.Group.DepartmentID != nil {
        levels = append(levels, HierarchyLevel{EntityType: "department", EntityID: *project.Group.DepartmentID})
    }

    levels = append(levels, HierarchyLevel{EntityType: "tenant", EntityID: project.Group.TenantID})

    return levels, nil
}

type HierarchyLevel struct {
    EntityType string
    EntityID   string
}

// GetUsageSummary returns quota usage for a specific entity
func (m *Manager) GetUsageSummary(ctx context.Context, entityType, entityID string) (*QuotaUsageSummary, error) {
    quota, err := m.getQuotaInfo(ctx, entityType, entityID)
    if err != nil {
        return nil, err
    }

    return &QuotaUsageSummary{
        EntityType: entityType,
        EntityID:   entityID,
        Limits:     quota.Limits,
        Used:       quota.Used,
        Available: ResourceUsage{
            CPUUsed:         quota.Limits.CPULimit - quota.Used.CPUUsed,
            MemoryUsed:      quota.Limits.MemoryLimit - quota.Used.MemoryUsed,
            GPUUsed:         quota.Limits.GPULimit - quota.Used.GPUUsed,
            PodsRunning:     quota.Limits.MaxConcurrentPods - quota.Used.PodsRunning,
            WorkflowsActive: quota.Limits.MaxConcurrentWF - quota.Used.WorkflowsActive,
        },
        UtilizationPercent: calculateUtilization(quota),
    }, nil
}

type QuotaUsageSummary struct {
    EntityType         string
    EntityID           string
    Limits             ResourceLimits
    Used               ResourceUsage
    Available          ResourceUsage
    UtilizationPercent float64
}
```

### 5.3 Fair Scheduling with Quotas

```go
// pkg/scheduler/fair_scheduler.go

package scheduler

import (
    "context"
    "sort"
)

type FairScheduler struct {
    quotaManager *quota.Manager
    taskQueue    *queue.TaskQueue
}

type SchedulableTask struct {
    Task           *model.Task
    Project        *model.Project
    EffectivePrio  float64
    QuotaWeight    int
}

// Schedule implements weighted fair scheduling
func (fs *FairScheduler) Schedule(ctx context.Context) error {
    // Get all pending tasks
    pendingTasks, err := fs.getPendingTasks(ctx)
    if err != nil {
        return err
    }

    // Group by tenant for fair distribution
    tasksByTenant := make(map[string][]*SchedulableTask)
    for _, task := range pendingTasks {
        tenantID := task.Project.Group.TenantID
        tasksByTenant[tenantID] = append(tasksByTenant[tenantID], task)
    }

    // Calculate effective priority based on:
    // 1. Base task priority
    // 2. Quota weight
    // 3. Wait time (priority boost for waiting tasks)
    // 4. Remaining quota headroom
    for _, tasks := range tasksByTenant {
        for _, task := range tasks {
            task.EffectivePrio = fs.calculateEffectivePriority(ctx, task)
        }

        // Sort by effective priority
        sort.Slice(tasks, func(i, j int) bool {
            return tasks[i].EffectivePrio > tasks[j].EffectivePrio
        })
    }

    // Round-robin across tenants with priority consideration
    scheduled := 0
    maxBatch := 100  // Limit per scheduling cycle

    for scheduled < maxBatch && len(tasksByTenant) > 0 {
        for tenantID, tasks := range tasksByTenant {
            if len(tasks) == 0 {
                delete(tasksByTenant, tenantID)
                continue
            }

            task := tasks[0]
            tasksByTenant[tenantID] = tasks[1:]

            // Check quota
            canSchedule, reason := fs.quotaManager.CanSchedule(
                ctx,
                task.Project.ID,
                task.Task.GetResourceRequest(),
            )

            if !canSchedule {
                // Skip but don't drop - will retry next cycle
                continue
            }

            // Reserve quota and enqueue
            if err := fs.quotaManager.Reserve(ctx, task.Project.ID, task.Task.GetResourceRequest()); err != nil {
                continue
            }

            if err := fs.taskQueue.Enqueue(ctx, task.ToQueuedTask()); err != nil {
                fs.quotaManager.Release(ctx, task.Project.ID, task.Task.GetResourceRequest())
                continue
            }

            scheduled++
        }
    }

    return nil
}

func (fs *FairScheduler) calculateEffectivePriority(ctx context.Context, task *SchedulableTask) float64 {
    basePriority := float64(task.Task.Priority)

    // Boost based on quota weight (higher weight = higher effective priority)
    quotaBoost := float64(task.QuotaWeight) / 100.0

    // Boost for long-waiting tasks (anti-starvation)
    waitTime := time.Since(task.Task.CreatedAt)
    waitBoost := math.Min(waitTime.Minutes() / 60.0, 1.0) * 10  // Max +10 after 1 hour

    // Penalize if tenant is over-utilizing quota
    usage, _ := fs.quotaManager.GetUsageSummary(ctx, "tenant", task.Project.Group.TenantID)
    utilizationPenalty := 0.0
    if usage != nil && usage.UtilizationPercent > 80 {
        utilizationPenalty = (usage.UtilizationPercent - 80) / 20 * 5  // Max -5 at 100% utilization
    }

    return basePriority + quotaBoost*20 + waitBoost - utilizationPenalty
}
```

---

## 6. Python SDK

### 6.1 SDK Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                         Python SDK Architecture                          │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  Pod Container                                                          │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                                                                     │ │
│  │  User Code                       FlowForge SDK                     │ │
│  │  ┌──────────────────────┐       ┌──────────────────────────────┐  │ │
│  │  │                      │       │                              │  │ │
│  │  │  from flowforge      │       │  ┌────────────────────────┐  │  │ │
│  │  │  import client       │──────►│  │    Client              │  │  │ │
│  │  │                      │       │  │                        │  │  │ │
│  │  │  client.log(...)     │       │  │  - status_reporter     │  │  │ │
│  │  │  client.metric(...)  │       │  │  - log_streamer        │  │  │ │
│  │  │  client.status(...)  │       │  │  - metrics_collector   │  │  │ │
│  │  │  client.output(...)  │       │  │  - output_manager      │  │  │ │
│  │  │                      │       │  │  - heartbeat_manager   │  │  │ │
│  │  └──────────────────────┘       │  └───────────┬────────────┘  │  │ │
│  │                                  │              │               │  │ │
│  │                                  │  ┌───────────▼────────────┐  │  │ │
│  │                                  │  │   Background Workers   │  │  │ │
│  │                                  │  │                        │  │  │ │
│  │                                  │  │  - Async log batching  │  │  │ │
│  │                                  │  │  - Metric aggregation  │  │  │ │
│  │                                  │  │  - Heartbeat thread    │  │  │ │
│  │                                  │  │  - Retry queue         │  │  │ │
│  │                                  │  └───────────┬────────────┘  │  │ │
│  │                                  │              │               │  │ │
│  │                                  └──────────────┼───────────────┘  │ │
│  │                                                 │                   │ │
│  └─────────────────────────────────────────────────┼───────────────────┘ │
│                                                    │                     │
│  ┌─────────────────────────────────────────────────┼───────────────────┐ │
│  │                    Network Layer                │                   │ │
│  │                                                 │                   │ │
│  │  ┌──────────────┐  ┌──────────────┐  ┌─────────▼────────┐          │ │
│  │  │   gRPC       │  │   HTTP/2     │  │   TCP (Redis)   │          │ │
│  │  │   (Status)   │  │   (Logs)     │  │   (Heartbeat)   │          │ │
│  │  └──────┬───────┘  └──────┬───────┘  └─────────┬────────┘          │ │
│  │         │                 │                    │                   │ │
│  └─────────┼─────────────────┼────────────────────┼───────────────────┘ │
│            │                 │                    │                     │
│            ▼                 ▼                    ▼                     │
│  ┌──────────────┐  ┌──────────────────┐  ┌──────────────┐              │
│  │  API Server  │  │  Log Aggregator  │  │    Redis     │              │
│  └──────────────┘  └──────────────────┘  └──────────────┘              │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### 6.2 SDK Implementation

```python
# flowforge-sdk/flowforge/__init__.py

"""
FlowForge Python SDK

Usage:
    from flowforge import client

    # Automatic initialization from environment
    client.log("Processing started")
    client.metric("items_processed", 100)
    client.status("RUNNING", progress=50)
    client.output("result", {"key": "value"})
"""

from .client import FlowForgeClient
from .types import TaskStatus, LogLevel, MetricType

# Singleton client instance (auto-configured from env)
client: FlowForgeClient = None

def _init_client():
    global client
    if client is None:
        client = FlowForgeClient.from_env()
    return client

# Auto-initialize on import
_init_client()

__all__ = ['client', 'FlowForgeClient', 'TaskStatus', 'LogLevel', 'MetricType']
```

```python
# flowforge-sdk/flowforge/client.py

import os
import time
import json
import atexit
import threading
from queue import Queue, Empty
from typing import Any, Dict, Optional, Union
from datetime import datetime
from dataclasses import dataclass, field

import grpc
import redis
import requests
from .proto import flowforge_pb2, flowforge_pb2_grpc
from .types import TaskStatus, LogLevel, MetricType


@dataclass
class Config:
    """SDK Configuration from environment variables."""
    task_id: str
    workflow_id: str
    tenant_id: str
    project_id: str
    api_endpoint: str
    redis_endpoint: str
    log_endpoint: str
    auth_token: str
    heartbeat_interval: int = 5
    log_batch_size: int = 100
    log_flush_interval: float = 1.0
    metric_flush_interval: float = 5.0


class FlowForgeClient:
    """
    FlowForge SDK Client for task status reporting, logging, and metrics.

    This client is designed to be used inside workflow task pods to:
    - Report task status and progress
    - Stream logs to centralized storage
    - Emit custom metrics
    - Store task outputs
    - Send heartbeats

    Example:
        from flowforge import client

        client.log("Starting processing")
        client.status("RUNNING", progress=10)

        for i, item in enumerate(items):
            process(item)
            client.metric("items_processed", i + 1)
            client.status("RUNNING", progress=int((i + 1) / len(items) * 100))

        client.output("result", {"processed": len(items)})
        client.status("SUCCEEDED")
    """

    def __init__(self, config: Config):
        self.config = config
        self._log_queue: Queue = Queue()
        self._metric_queue: Queue = Queue()
        self._shutdown = threading.Event()

        # Initialize connections
        self._init_grpc()
        self._init_redis()
        self._init_http_session()

        # Start background workers
        self._start_workers()

        # Register cleanup
        atexit.register(self.close)

    @classmethod
    def from_env(cls) -> 'FlowForgeClient':
        """Create client from environment variables."""
        config = Config(
            task_id=os.environ['FLOWFORGE_TASK_ID'],
            workflow_id=os.environ['FLOWFORGE_WORKFLOW_ID'],
            tenant_id=os.environ['FLOWFORGE_TENANT_ID'],
            project_id=os.environ['FLOWFORGE_PROJECT_ID'],
            api_endpoint=os.environ['FLOWFORGE_API_ENDPOINT'],
            redis_endpoint=os.environ['FLOWFORGE_REDIS_ENDPOINT'],
            log_endpoint=os.environ['FLOWFORGE_LOG_ENDPOINT'],
            auth_token=os.environ['FLOWFORGE_AUTH_TOKEN'],
            heartbeat_interval=int(os.environ.get('FLOWFORGE_HEARTBEAT_INTERVAL', '5')),
        )
        return cls(config)

    def _init_grpc(self):
        """Initialize gRPC channel for status reporting."""
        self._grpc_channel = grpc.insecure_channel(self.config.api_endpoint)
        self._status_stub = flowforge_pb2_grpc.TaskServiceStub(self._grpc_channel)

    def _init_redis(self):
        """Initialize Redis connection for heartbeats."""
        host, port = self.config.redis_endpoint.split(':')
        self._redis = redis.Redis(host=host, port=int(port), decode_responses=True)

    def _init_http_session(self):
        """Initialize HTTP session for log streaming."""
        self._http_session = requests.Session()
        self._http_session.headers.update({
            'Authorization': f'Bearer {self.config.auth_token}',
            'Content-Type': 'application/json',
        })

    def _start_workers(self):
        """Start background worker threads."""
        # Heartbeat worker
        self._heartbeat_thread = threading.Thread(
            target=self._heartbeat_worker,
            daemon=True,
            name="flowforge-heartbeat"
        )
        self._heartbeat_thread.start()

        # Log flusher worker
        self._log_thread = threading.Thread(
            target=self._log_worker,
            daemon=True,
            name="flowforge-log"
        )
        self._log_thread.start()

        # Metric flusher worker
        self._metric_thread = threading.Thread(
            target=self._metric_worker,
            daemon=True,
            name="flowforge-metric"
        )
        self._metric_thread.start()

    # ==================== Public API ====================

    def status(
        self,
        status: Union[str, TaskStatus],
        message: Optional[str] = None,
        progress: Optional[int] = None,
        details: Optional[Dict[str, Any]] = None
    ) -> None:
        """
        Update task status.

        Args:
            status: Task status (RUNNING, SUCCEEDED, FAILED, etc.)
            message: Optional status message
            progress: Optional progress percentage (0-100)
            details: Optional additional details

        Example:
            client.status("RUNNING", progress=50, message="Processing batch 5/10")
        """
        if isinstance(status, TaskStatus):
            status = status.value

        request = flowforge_pb2.UpdateStatusRequest(
            task_id=self.config.task_id,
            workflow_id=self.config.workflow_id,
            status=status,
            message=message or "",
            progress=progress or 0,
            details=json.dumps(details or {}),
            timestamp=int(time.time() * 1000),
        )

        try:
            self._status_stub.UpdateStatus(request, timeout=5)
        except grpc.RpcError as e:
            # Log locally but don't fail the task
            print(f"[FlowForge] Failed to update status: {e}")

    def log(
        self,
        message: str,
        level: Union[str, LogLevel] = LogLevel.INFO,
        **extra
    ) -> None:
        """
        Send a log message.

        Args:
            message: Log message
            level: Log level (DEBUG, INFO, WARN, ERROR)
            **extra: Additional structured fields

        Example:
            client.log("Processing file", level="INFO", filename="data.csv", size=1024)
        """
        if isinstance(level, LogLevel):
            level = level.value

        log_entry = {
            'timestamp': datetime.utcnow().isoformat() + 'Z',
            'task_id': self.config.task_id,
            'workflow_id': self.config.workflow_id,
            'tenant_id': self.config.tenant_id,
            'level': level,
            'message': message,
            **extra
        }

        self._log_queue.put(log_entry)

    def metric(
        self,
        name: str,
        value: Union[int, float],
        metric_type: Union[str, MetricType] = MetricType.GAUGE,
        tags: Optional[Dict[str, str]] = None
    ) -> None:
        """
        Emit a metric.

        Args:
            name: Metric name
            value: Metric value
            metric_type: Type of metric (gauge, counter, histogram)
            tags: Optional metric tags

        Example:
            client.metric("items_processed", 100, tags={"batch": "1"})
            client.metric("processing_time_ms", 1523.5, metric_type="histogram")
        """
        if isinstance(metric_type, MetricType):
            metric_type = metric_type.value

        metric_entry = {
            'timestamp': int(time.time() * 1000),
            'task_id': self.config.task_id,
            'workflow_id': self.config.workflow_id,
            'tenant_id': self.config.tenant_id,
            'name': name,
            'value': value,
            'type': metric_type,
            'tags': tags or {}
        }

        self._metric_queue.put(metric_entry)

    def output(self, key: str, value: Any) -> None:
        """
        Store a task output that can be used by downstream tasks.

        Args:
            key: Output key name
            value: Output value (will be JSON serialized)

        Example:
            client.output("processed_file", "/data/output.parquet")
            client.output("stats", {"rows": 1000, "columns": 50})
        """
        request = flowforge_pb2.SetOutputRequest(
            task_id=self.config.task_id,
            workflow_id=self.config.workflow_id,
            key=key,
            value=json.dumps(value),
        )

        try:
            self._status_stub.SetOutput(request, timeout=5)
        except grpc.RpcError as e:
            print(f"[FlowForge] Failed to set output: {e}")

    def checkpoint(self, data: Dict[str, Any]) -> None:
        """
        Save a checkpoint for task recovery.

        Args:
            data: Checkpoint data (must be JSON serializable)

        Example:
            client.checkpoint({"last_processed_id": 12345, "batch": 5})
        """
        request = flowforge_pb2.SaveCheckpointRequest(
            task_id=self.config.task_id,
            workflow_id=self.config.workflow_id,
            data=json.dumps(data),
            timestamp=int(time.time() * 1000),
        )

        try:
            self._status_stub.SaveCheckpoint(request, timeout=5)
        except grpc.RpcError as e:
            print(f"[FlowForge] Failed to save checkpoint: {e}")

    def get_checkpoint(self) -> Optional[Dict[str, Any]]:
        """
        Retrieve the last checkpoint for this task.

        Returns:
            Checkpoint data or None if no checkpoint exists

        Example:
            checkpoint = client.get_checkpoint()
            if checkpoint:
                start_from = checkpoint['last_processed_id']
        """
        request = flowforge_pb2.GetCheckpointRequest(
            task_id=self.config.task_id,
            workflow_id=self.config.workflow_id,
        )

        try:
            response = self._status_stub.GetCheckpoint(request, timeout=5)
            if response.data:
                return json.loads(response.data)
        except grpc.RpcError:
            pass

        return None

    # ==================== Background Workers ====================

    def _heartbeat_worker(self):
        """Send periodic heartbeats to indicate task is alive."""
        key = f"ff:heartbeat:{self.config.task_id}"

        while not self._shutdown.is_set():
            try:
                self._redis.zadd(
                    'ff:heartbeat',
                    {self.config.task_id: time.time()}
                )
                self._redis.expire(key, 60)  # Auto-expire if not renewed
            except redis.RedisError as e:
                print(f"[FlowForge] Heartbeat failed: {e}")

            self._shutdown.wait(self.config.heartbeat_interval)

    def _log_worker(self):
        """Batch and send logs."""
        batch = []
        last_flush = time.time()

        while not self._shutdown.is_set():
            try:
                entry = self._log_queue.get(timeout=0.1)
                batch.append(entry)
            except Empty:
                pass

            # Flush on batch size or interval
            should_flush = (
                len(batch) >= self.config.log_batch_size or
                (batch and time.time() - last_flush >= self.config.log_flush_interval)
            )

            if should_flush and batch:
                self._flush_logs(batch)
                batch = []
                last_flush = time.time()

        # Final flush on shutdown
        if batch:
            self._flush_logs(batch)

    def _flush_logs(self, batch: list):
        """Send log batch to log aggregator."""
        try:
            self._http_session.post(
                f"{self.config.log_endpoint}/v1/logs",
                json={'logs': batch},
                timeout=10
            )
        except requests.RequestException as e:
            print(f"[FlowForge] Failed to flush logs: {e}")

    def _metric_worker(self):
        """Batch and send metrics."""
        batch = []
        last_flush = time.time()

        while not self._shutdown.is_set():
            try:
                entry = self._metric_queue.get(timeout=0.1)
                batch.append(entry)
            except Empty:
                pass

            if batch and time.time() - last_flush >= self.config.metric_flush_interval:
                self._flush_metrics(batch)
                batch = []
                last_flush = time.time()

        if batch:
            self._flush_metrics(batch)

    def _flush_metrics(self, batch: list):
        """Send metric batch to metrics collector."""
        try:
            self._http_session.post(
                f"{self.config.log_endpoint}/v1/metrics",
                json={'metrics': batch},
                timeout=10
            )
        except requests.RequestException as e:
            print(f"[FlowForge] Failed to flush metrics: {e}")

    def close(self):
        """Gracefully shutdown the client."""
        self._shutdown.set()

        # Wait for workers to finish
        self._heartbeat_thread.join(timeout=2)
        self._log_thread.join(timeout=5)
        self._metric_thread.join(timeout=5)

        # Close connections
        self._grpc_channel.close()
        self._redis.close()
        self._http_session.close()
```

```python
# flowforge-sdk/flowforge/types.py

from enum import Enum


class TaskStatus(Enum):
    """Task execution status."""
    PENDING = "PENDING"
    QUEUED = "QUEUED"
    RUNNING = "RUNNING"
    SUCCEEDED = "SUCCEEDED"
    FAILED = "FAILED"
    CANCELLED = "CANCELLED"
    RETRYING = "RETRYING"
    SKIPPED = "SKIPPED"


class LogLevel(Enum):
    """Log severity levels."""
    DEBUG = "DEBUG"
    INFO = "INFO"
    WARN = "WARN"
    ERROR = "ERROR"


class MetricType(Enum):
    """Metric types."""
    GAUGE = "gauge"         # Point-in-time value
    COUNTER = "counter"     # Monotonically increasing
    HISTOGRAM = "histogram" # Distribution of values
```

### 6.3 SDK Injection into Pods

```go
// pkg/executor/injector/sdk_injector.go

package injector

import (
    corev1 "k8s.io/api/core/v1"
)

type SDKInjector struct {
    sdkImage   string
    sdkVersion string
}

func NewSDKInjector(sdkImage, sdkVersion string) *SDKInjector {
    return &SDKInjector{
        sdkImage:   sdkImage,
        sdkVersion: sdkVersion,
    }
}

// GetInitContainer returns an init container that installs the SDK
func (i *SDKInjector) GetInitContainer() corev1.Container {
    return corev1.Container{
        Name:  "flowforge-sdk-installer",
        Image: fmt.Sprintf("%s:%s", i.sdkImage, i.sdkVersion),
        Command: []string{"/bin/sh", "-c"},
        Args: []string{
            "cp -r /sdk/* /flowforge/",
        },
        VolumeMounts: []corev1.VolumeMount{
            {
                Name:      "flowforge-sdk",
                MountPath: "/flowforge",
            },
        },
    }
}

// GetEnvVars returns environment variables for SDK configuration
func (i *SDKInjector) GetEnvVars(task *model.Task) []corev1.EnvVar {
    return []corev1.EnvVar{
        {Name: "FLOWFORGE_TASK_ID", Value: task.ID},
        {Name: "FLOWFORGE_WORKFLOW_ID", Value: task.WorkflowID},
        {Name: "FLOWFORGE_TENANT_ID", Value: task.TenantID},
        {Name: "FLOWFORGE_PROJECT_ID", Value: task.ProjectID},
        {Name: "FLOWFORGE_API_ENDPOINT", Value: os.Getenv("FLOWFORGE_API_ENDPOINT")},
        {Name: "FLOWFORGE_REDIS_ENDPOINT", Value: os.Getenv("FLOWFORGE_REDIS_ENDPOINT")},
        {Name: "FLOWFORGE_LOG_ENDPOINT", Value: os.Getenv("FLOWFORGE_LOG_ENDPOINT")},
        {
            Name: "FLOWFORGE_AUTH_TOKEN",
            ValueFrom: &corev1.EnvVarSource{
                SecretKeyRef: &corev1.SecretKeySelector{
                    LocalObjectReference: corev1.LocalObjectReference{
                        Name: "flowforge-task-tokens",
                    },
                    Key: task.ID,
                },
            },
        },
        {Name: "PYTHONPATH", Value: "/flowforge/python:$PYTHONPATH"},
    }
}

// GetVolume returns the shared volume for SDK
func (i *SDKInjector) GetVolume() corev1.Volume {
    return corev1.Volume{
        Name: "flowforge-sdk",
        VolumeSource: corev1.VolumeSource{
            EmptyDir: &corev1.EmptyDirVolumeSource{},
        },
    }
}
```

---

## 7. Log & Metrics Storage

### 7.1 ClickHouse Schema for Logs

```sql
-- clickhouse/schema/logs.sql

-- Log entries table with efficient time-series partitioning
CREATE TABLE IF NOT EXISTS logs (
    -- Identifiers
    tenant_id       String,
    workflow_id     String,
    task_id         String,

    -- Log data
    timestamp       DateTime64(3),
    level           LowCardinality(String),
    message         String,

    -- Structured fields (stored as JSON string for flexibility)
    extra           String DEFAULT '{}',

    -- Pod metadata
    pod_name        String,
    node_name       String,
    container_name  String DEFAULT 'main',

    -- Deduplication
    log_id          UUID DEFAULT generateUUIDv4()
)
ENGINE = MergeTree()
PARTITION BY (tenant_id, toYYYYMMDD(timestamp))
ORDER BY (tenant_id, workflow_id, task_id, timestamp)
TTL timestamp + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;

-- Materialized view for log aggregation by level
CREATE MATERIALIZED VIEW IF NOT EXISTS logs_by_level_mv
ENGINE = SummingMergeTree()
PARTITION BY (tenant_id, toYYYYMMDD(timestamp))
ORDER BY (tenant_id, workflow_id, level, toStartOfHour(timestamp))
AS SELECT
    tenant_id,
    workflow_id,
    level,
    toStartOfHour(timestamp) AS hour,
    count() AS count
FROM logs
GROUP BY tenant_id, workflow_id, level, hour;

-- Create index for fast text search
ALTER TABLE logs ADD INDEX idx_message message TYPE tokenbf_v1(10240, 3, 0) GRANULARITY 4;
```

### 7.2 ClickHouse Schema for Metrics

```sql
-- clickhouse/schema/metrics.sql

-- Metrics table
CREATE TABLE IF NOT EXISTS metrics (
    -- Identifiers
    tenant_id       String,
    workflow_id     String,
    task_id         String,

    -- Metric data
    timestamp       DateTime64(3),
    name            LowCardinality(String),
    value           Float64,
    type            LowCardinality(String),  -- gauge, counter, histogram

    -- Tags stored as arrays for efficient filtering
    tag_keys        Array(String),
    tag_values      Array(String)
)
ENGINE = MergeTree()
PARTITION BY (tenant_id, toYYYYMMDD(timestamp))
ORDER BY (tenant_id, workflow_id, task_id, name, timestamp)
TTL timestamp + INTERVAL 90 DAY
SETTINGS index_granularity = 8192;

-- Aggregated metrics view (1-minute resolution)
CREATE MATERIALIZED VIEW IF NOT EXISTS metrics_1m_mv
ENGINE = SummingMergeTree()
PARTITION BY (tenant_id, toYYYYMMDD(timestamp))
ORDER BY (tenant_id, workflow_id, name, toStartOfMinute(timestamp))
AS SELECT
    tenant_id,
    workflow_id,
    name,
    toStartOfMinute(timestamp) AS minute,
    avg(value) AS avg_value,
    min(value) AS min_value,
    max(value) AS max_value,
    count() AS sample_count
FROM metrics
GROUP BY tenant_id, workflow_id, name, minute;

-- Workflow execution metrics
CREATE TABLE IF NOT EXISTS workflow_metrics (
    tenant_id           String,
    workflow_id         String,
    project_id          String,

    -- Timing
    started_at          DateTime64(3),
    finished_at         Nullable(DateTime64(3)),
    duration_ms         Nullable(Int64),

    -- Status
    status              LowCardinality(String),

    -- Resource usage
    total_cpu_ms        Int64 DEFAULT 0,
    total_memory_mb_sec Int64 DEFAULT 0,

    -- Task counts
    total_tasks         Int32,
    succeeded_tasks     Int32 DEFAULT 0,
    failed_tasks        Int32 DEFAULT 0,
    skipped_tasks       Int32 DEFAULT 0
)
ENGINE = ReplacingMergeTree(finished_at)
PARTITION BY (tenant_id, toYYYYMM(started_at))
ORDER BY (tenant_id, project_id, workflow_id);
```

### 7.3 MongoDB Alternative Schema

```javascript
// mongodb/schema.js

// Logs Collection
db.createCollection("logs", {
    timeseries: {
        timeField: "timestamp",
        metaField: "metadata",
        granularity: "seconds"
    },
    expireAfterSeconds: 2592000  // 30 days
});

// Indexes
db.logs.createIndex({ "metadata.tenant_id": 1, "metadata.workflow_id": 1 });
db.logs.createIndex({ "metadata.task_id": 1 });
db.logs.createIndex({ "timestamp": 1 });
db.logs.createIndex({ "message": "text" });  // Full-text search

// Example log document
{
    timestamp: ISODate("2024-01-15T10:30:00.000Z"),
    metadata: {
        tenant_id: "tenant-123",
        workflow_id: "wf-456",
        task_id: "task-789",
        pod_name: "ff-wf-456-task-789",
        level: "INFO"
    },
    message: "Processing batch 5 of 10",
    extra: {
        batch_size: 1000,
        processed: 5000
    }
}

// Metrics Collection
db.createCollection("metrics", {
    timeseries: {
        timeField: "timestamp",
        metaField: "metadata",
        granularity: "seconds"
    },
    expireAfterSeconds: 7776000  // 90 days
});

db.metrics.createIndex({ "metadata.tenant_id": 1, "metadata.name": 1 });
db.metrics.createIndex({ "metadata.workflow_id": 1 });

// Example metric document
{
    timestamp: ISODate("2024-01-15T10:30:00.000Z"),
    metadata: {
        tenant_id: "tenant-123",
        workflow_id: "wf-456",
        task_id: "task-789",
        name: "items_processed",
        type: "counter"
    },
    value: 5000,
    tags: {
        batch: "5"
    }
}
```

### 7.4 Log Aggregator Service

```go
// pkg/logaggregator/aggregator.go

package logaggregator

import (
    "context"
    "time"

    "github.com/ClickHouse/clickhouse-go/v2"
)

type LogAggregator struct {
    clickhouse clickhouse.Conn
    batchSize  int
    flushInterval time.Duration
    buffer     chan *LogEntry
}

type LogEntry struct {
    TenantID      string    `json:"tenant_id"`
    WorkflowID    string    `json:"workflow_id"`
    TaskID        string    `json:"task_id"`
    Timestamp     time.Time `json:"timestamp"`
    Level         string    `json:"level"`
    Message       string    `json:"message"`
    Extra         string    `json:"extra"`
    PodName       string    `json:"pod_name"`
    NodeName      string    `json:"node_name"`
}

func (la *LogAggregator) Start(ctx context.Context) {
    ticker := time.NewTicker(la.flushInterval)
    batch := make([]*LogEntry, 0, la.batchSize)

    for {
        select {
        case entry := <-la.buffer:
            batch = append(batch, entry)
            if len(batch) >= la.batchSize {
                la.flush(ctx, batch)
                batch = make([]*LogEntry, 0, la.batchSize)
            }

        case <-ticker.C:
            if len(batch) > 0 {
                la.flush(ctx, batch)
                batch = make([]*LogEntry, 0, la.batchSize)
            }

        case <-ctx.Done():
            if len(batch) > 0 {
                la.flush(context.Background(), batch)
            }
            return
        }
    }
}

func (la *LogAggregator) flush(ctx context.Context, batch []*LogEntry) error {
    query := `
        INSERT INTO logs (
            tenant_id, workflow_id, task_id, timestamp,
            level, message, extra, pod_name, node_name
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
    `

    b, err := la.clickhouse.PrepareBatch(ctx, query)
    if err != nil {
        return err
    }

    for _, entry := range batch {
        b.Append(
            entry.TenantID,
            entry.WorkflowID,
            entry.TaskID,
            entry.Timestamp,
            entry.Level,
            entry.Message,
            entry.Extra,
            entry.PodName,
            entry.NodeName,
        )
    }

    return b.Send()
}

// QueryLogs retrieves logs with filtering
func (la *LogAggregator) QueryLogs(ctx context.Context, req *LogQueryRequest) ([]*LogEntry, error) {
    query := `
        SELECT tenant_id, workflow_id, task_id, timestamp, level, message, extra
        FROM logs
        WHERE tenant_id = ?
          AND workflow_id = ?
          AND timestamp >= ?
          AND timestamp <= ?
    `

    args := []interface{}{req.TenantID, req.WorkflowID, req.StartTime, req.EndTime}

    if req.TaskID != "" {
        query += " AND task_id = ?"
        args = append(args, req.TaskID)
    }

    if req.Level != "" {
        query += " AND level = ?"
        args = append(args, req.Level)
    }

    if req.Search != "" {
        query += " AND message LIKE ?"
        args = append(args, "%"+req.Search+"%")
    }

    query += " ORDER BY timestamp DESC LIMIT ?"
    args = append(args, req.Limit)

    rows, err := la.clickhouse.Query(ctx, query, args...)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var logs []*LogEntry
    for rows.Next() {
        var entry LogEntry
        if err := rows.Scan(
            &entry.TenantID, &entry.WorkflowID, &entry.TaskID,
            &entry.Timestamp, &entry.Level, &entry.Message, &entry.Extra,
        ); err != nil {
            continue
        }
        logs = append(logs, &entry)
    }

    return logs, nil
}
```

---

## 8. Backoff & Retry Mechanism

### 8.1 Retry Strategy Design

```
┌─────────────────────────────────────────────────────────────────────────┐
│                      Retry Strategy Architecture                         │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  Backoff Calculation                                                    │
│  ───────────────────                                                    │
│                                                                          │
│  delay = min(initialDelay * (factor ^ retryCount), maxDelay) + jitter   │
│                                                                          │
│  Example with: initial=10s, factor=2, max=5m                            │
│                                                                          │
│  Retry 1: 10s  + jitter                                                 │
│  Retry 2: 20s  + jitter                                                 │
│  Retry 3: 40s  + jitter                                                 │
│  Retry 4: 80s  + jitter                                                 │
│  Retry 5: 160s + jitter                                                 │
│  Retry 6: 300s + jitter (capped at max)                                 │
│                                                                          │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                                                                     │ │
│  │  Time ──►                                                          │ │
│  │                                                                     │ │
│  │  ┌───┐     ┌───┐       ┌───┐           ┌───┐               ┌───┐  │ │
│  │  │ 1 │     │ 2 │       │ 3 │           │ 4 │               │ 5 │  │ │
│  │  │ F │     │ F │       │ F │           │ F │               │ S │  │ │
│  │  └───┘     └───┘       └───┘           └───┘               └───┘  │ │
│  │  │←─10s──►││←──20s───►││←────40s─────►││←──────80s───────►│      │ │
│  │                                                                     │ │
│  │  F = Failed, S = Succeeded                                         │ │
│  │                                                                     │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                                                          │
│  Retry Policies                                                         │
│  ──────────────                                                         │
│                                                                          │
│  ┌─────────────────┬────────────────────────────────────────────────┐  │
│  │ Policy          │ Description                                     │  │
│  ├─────────────────┼────────────────────────────────────────────────┤  │
│  │ Always          │ Retry on any failure                           │  │
│  │ OnError         │ Retry only on non-zero exit codes              │  │
│  │ OnTransient     │ Retry on specific error patterns (OOM, etc.)   │  │
│  │ Never           │ Never retry (fail immediately)                 │  │
│  └─────────────────┴────────────────────────────────────────────────┘  │
│                                                                          │
│  Transient Error Detection                                              │
│  ─────────────────────────                                              │
│                                                                          │
│  - Exit code 137 (OOMKilled)                                           │
│  - Exit code 143 (SIGTERM - node preemption)                           │
│  - Network timeout errors                                               │
│  - Resource unavailable errors                                          │
│  - Rate limit errors (429, 503)                                        │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### 8.2 Retry Manager Implementation

```go
// pkg/retry/manager.go

package retry

import (
    "context"
    "math"
    "math/rand"
    "time"

    "gorm.io/gorm"
    "github.com/redis/go-redis/v9"
)

type Manager struct {
    db    *gorm.DB
    redis *redis.Client
}

type RetryStrategy struct {
    Limit          int           `json:"limit"`           // Max retries
    InitialDelay   time.Duration `json:"initial_delay"`   // Initial backoff
    Factor         float64       `json:"factor"`          // Backoff multiplier
    MaxDelay       time.Duration `json:"max_delay"`       // Maximum delay
    JitterPercent  float64       `json:"jitter_percent"`  // Random jitter (0-1)
    RetryPolicy    RetryPolicy   `json:"retry_policy"`    // When to retry
}

type RetryPolicy string

const (
    RetryAlways     RetryPolicy = "Always"
    RetryOnError    RetryPolicy = "OnError"
    RetryOnTransient RetryPolicy = "OnTransient"
    RetryNever      RetryPolicy = "Never"
)

var DefaultStrategy = RetryStrategy{
    Limit:         3,
    InitialDelay:  10 * time.Second,
    Factor:        2.0,
    MaxDelay:      5 * time.Minute,
    JitterPercent: 0.1,
    RetryPolicy:   RetryOnError,
}

// TransientErrors are errors that should be retried
var TransientErrors = map[int]bool{
    137: true, // OOMKilled
    143: true, // SIGTERM (node preemption)
    255: true, // Generic failure (often transient)
}

// ShouldRetry determines if a failed task should be retried
func (m *Manager) ShouldRetry(task *model.Task, exitCode int, errorMsg string) bool {
    strategy := m.getStrategy(task)

    // Check retry limit
    if task.RetryCount >= strategy.Limit {
        return false
    }

    // Check policy
    switch strategy.RetryPolicy {
    case RetryNever:
        return false

    case RetryAlways:
        return true

    case RetryOnError:
        return exitCode != 0

    case RetryOnTransient:
        return m.isTransientError(exitCode, errorMsg)

    default:
        return false
    }
}

// ScheduleRetry schedules a task for retry with backoff
func (m *Manager) ScheduleRetry(ctx context.Context, task *model.Task) error {
    strategy := m.getStrategy(task)

    // Calculate backoff delay
    delay := m.calculateBackoff(task.RetryCount, strategy)
    nextRetryAt := time.Now().Add(delay)

    // Update task in database
    err := m.db.WithContext(ctx).Model(task).Updates(map[string]interface{}{
        "status":        "RETRYING",
        "retry_count":   task.RetryCount + 1,
        "next_retry_at": nextRetryAt,
        "backoff_seconds": int(delay.Seconds()),
    }).Error

    if err != nil {
        return err
    }

    // Schedule in Redis sorted set (score = retry time)
    return m.redis.ZAdd(ctx, "ff:retry:scheduled", redis.Z{
        Score:  float64(nextRetryAt.Unix()),
        Member: task.ID,
    }).Err()
}

// ProcessRetries checks for tasks ready to retry and re-queues them
func (m *Manager) ProcessRetries(ctx context.Context) error {
    now := float64(time.Now().Unix())

    // Get tasks ready for retry
    taskIDs, err := m.redis.ZRangeByScore(ctx, "ff:retry:scheduled", &redis.ZRangeBy{
        Min: "-inf",
        Max: fmt.Sprintf("%f", now),
    }).Result()

    if err != nil {
        return err
    }

    for _, taskID := range taskIDs {
        // Remove from retry set
        m.redis.ZRem(ctx, "ff:retry:scheduled", taskID)

        // Load task and re-queue
        var task model.Task
        if err := m.db.First(&task, "id = ?", taskID).Error; err != nil {
            continue
        }

        // Update status to QUEUED
        m.db.Model(&task).Update("status", "QUEUED")

        // Publish event for scheduler to pick up
        m.redis.Publish(ctx, "ff:events:task", fmt.Sprintf(
            `{"task_id":"%s","event":"RETRY_READY"}`,
            taskID,
        ))
    }

    return nil
}

// calculateBackoff computes the backoff duration with jitter
func (m *Manager) calculateBackoff(retryCount int, strategy RetryStrategy) time.Duration {
    // Exponential backoff
    delay := float64(strategy.InitialDelay) * math.Pow(strategy.Factor, float64(retryCount))

    // Cap at max delay
    if delay > float64(strategy.MaxDelay) {
        delay = float64(strategy.MaxDelay)
    }

    // Add jitter
    if strategy.JitterPercent > 0 {
        jitter := delay * strategy.JitterPercent * (rand.Float64()*2 - 1)
        delay += jitter
    }

    return time.Duration(delay)
}

// isTransientError checks if an error is transient
func (m *Manager) isTransientError(exitCode int, errorMsg string) bool {
    // Check known transient exit codes
    if TransientErrors[exitCode] {
        return true
    }

    // Check error message patterns
    transientPatterns := []string{
        "connection refused",
        "connection reset",
        "timeout",
        "temporary failure",
        "resource temporarily unavailable",
        "too many requests",
        "service unavailable",
    }

    errorLower := strings.ToLower(errorMsg)
    for _, pattern := range transientPatterns {
        if strings.Contains(errorLower, pattern) {
            return true
        }
    }

    return false
}

func (m *Manager) getStrategy(task *model.Task) RetryStrategy {
    // Check if task has custom retry strategy
    if task.RetryStrategy != nil {
        return *task.RetryStrategy
    }
    return DefaultStrategy
}
```

### 8.3 Circuit Breaker for Persistent Failures

```go
// pkg/retry/circuit_breaker.go

package retry

import (
    "sync"
    "time"
)

type CircuitBreaker struct {
    mu              sync.RWMutex
    failureCount    map[string]int       // Failures per workflow template
    lastFailure     map[string]time.Time
    threshold       int
    resetTimeout    time.Duration
    halfOpenLimit   int
}

type CircuitState string

const (
    StateClosed    CircuitState = "closed"    // Normal operation
    StateOpen      CircuitState = "open"      // Blocking executions
    StateHalfOpen  CircuitState = "half-open" // Testing recovery
)

func NewCircuitBreaker(threshold int, resetTimeout time.Duration) *CircuitBreaker {
    return &CircuitBreaker{
        failureCount:  make(map[string]int),
        lastFailure:   make(map[string]time.Time),
        threshold:     threshold,
        resetTimeout:  resetTimeout,
        halfOpenLimit: 1,
    }
}

// RecordFailure records a task failure
func (cb *CircuitBreaker) RecordFailure(templateKey string) {
    cb.mu.Lock()
    defer cb.mu.Unlock()

    cb.failureCount[templateKey]++
    cb.lastFailure[templateKey] = time.Now()
}

// RecordSuccess records a successful execution
func (cb *CircuitBreaker) RecordSuccess(templateKey string) {
    cb.mu.Lock()
    defer cb.mu.Unlock()

    // Reset failure count on success
    delete(cb.failureCount, templateKey)
    delete(cb.lastFailure, templateKey)
}

// CanExecute checks if a task can be executed
func (cb *CircuitBreaker) CanExecute(templateKey string) (bool, CircuitState) {
    cb.mu.RLock()
    defer cb.mu.RUnlock()

    failures := cb.failureCount[templateKey]
    lastFail := cb.lastFailure[templateKey]

    // Under threshold - closed (normal)
    if failures < cb.threshold {
        return true, StateClosed
    }

    // Check if reset timeout has passed
    if time.Since(lastFail) > cb.resetTimeout {
        return true, StateHalfOpen  // Allow one execution to test
    }

    // Circuit is open - block execution
    return false, StateOpen
}

// GetState returns current circuit state for a template
func (cb *CircuitBreaker) GetState(templateKey string) CircuitState {
    _, state := cb.CanExecute(templateKey)
    return state
}
```

### 8.4 Global Retry Throttling

To prevent retry storms (e.g., cluster-wide outage or dependency failure), retries are additionally gated:

- **Per-tenant retry budget**: each tenant has a token bucket (Redis) limiting concurrent retries.
- **Per-workflow cap**: configurable maximum retries per workflow per time window.
- **Backpressure to scheduler**: when retry budget is exhausted, tasks remain in `RETRYING` with a delayed `next_retry_at`.
- **Observability**: emit metrics for retry throttling events and queue backlog.

This ensures retries do not overload shared services or cause cascading failures.

---

## 9. Go Package Structure

### 9.1 Project Layout

```
flowforge/
├── cmd/
│   ├── api-server/           # API server entrypoint
│   │   └── main.go
│   ├── controller/           # Workflow controller entrypoint
│   │   └── main.go
│   ├── scheduler/            # Scheduler service entrypoint
│   │   └── main.go
│   ├── executor/             # Pod executor entrypoint
│   │   └── main.go
│   └── cli/                  # CLI tool
│       └── main.go
│
├── pkg/
│   ├── api/                  # API definitions
│   │   ├── v1/
│   │   │   ├── workflow.go
│   │   │   ├── task.go
│   │   │   └── quota.go
│   │   └── grpc/
│   │       └── proto/
│   │
│   ├── apiserver/            # REST API server
│   │   ├── server.go
│   │   ├── handlers/
│   │   │   ├── workflow_handler.go
│   │   │   ├── task_handler.go
│   │   │   └── quota_handler.go
│   │   ├── middleware/
│   │   │   ├── auth.go
│   │   │   ├── ratelimit.go
│   │   │   └── logging.go
│   │   └── validator/
│   │
│   ├── controller/           # Workflow controller
│   │   ├── workflow_controller.go
│   │   ├── task_controller.go
│   │   ├── reconciler.go
│   │   └── state/
│   │       └── machine.go
│   │
│   ├── scheduler/            # Task scheduler
│   │   ├── scheduler.go
│   │   ├── fair_scheduler.go
│   │   └── dag/
│   │       ├── parser.go
│   │       └── traversal.go
│   │
│   ├── executor/             # Pod executor
│   │   ├── pod_executor.go
│   │   ├── injector/
│   │   │   └── sdk_injector.go
│   │   └── watcher/
│   │       └── pod_watcher.go
│   │
│   ├── queue/                # Redis task queue
│   │   ├── task_queue.go
│   │   └── priority.go
│   │
│   ├── eventbus/             # Event bus
│   │   ├── eventbus.go
│   │   └── handlers/
│   │
│   ├── quota/                # Quota management
│   │   ├── manager.go
│   │   ├── hierarchy.go
│   │   └── admission.go
│   │
│   ├── retry/                # Retry mechanism
│   │   ├── manager.go
│   │   ├── backoff.go
│   │   └── circuit_breaker.go
│   │
│   ├── logaggregator/        # Log aggregation
│   │   ├── aggregator.go
│   │   └── storage/
│   │       ├── clickhouse.go
│   │       └── mongodb.go
│   │
│   ├── metrics/              # Metrics collection
│   │   ├── collector.go
│   │   └── prometheus.go
│   │
│   ├── model/                # Data models
│   │   ├── tenant.go
│   │   ├── workflow.go
│   │   ├── task.go
│   │   └── quota.go
│   │
│   ├── store/                # Database layer
│   │   ├── postgres/
│   │   │   ├── store.go
│   │   │   └── migrations/
│   │   └── redis/
│   │       └── client.go
│   │
│   ├── auth/                 # Authentication
│   │   ├── jwt.go
│   │   ├── oidc.go
│   │   └── rbac/
│   │
│   └── config/               # Configuration
│       └── config.go
│
├── sdk/                      # Python SDK
│   └── python/
│       └── flowforge/
│           ├── __init__.py
│           ├── client.py
│           ├── types.py
│           └── proto/
│
├── deployments/              # Deployment manifests
│   ├── kubernetes/
│   │   ├── base/
│   │   └── overlays/
│   └── helm/
│       └── flowforge/
│
├── migrations/               # Database migrations
│   └── postgres/
│
├── scripts/                  # Build and utility scripts
│
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

### 9.2 Key Go Dependencies

```go
// go.mod

module github.com/flowforge/flowforge

go 1.21

require (
    // Web Framework
    github.com/gin-gonic/gin v1.9.1

    // gRPC
    google.golang.org/grpc v1.60.0
    google.golang.org/protobuf v1.32.0

    // Database
    gorm.io/gorm v1.25.5
    gorm.io/driver/postgres v1.5.4

    // Redis
    github.com/redis/go-redis/v9 v9.4.0

    // ClickHouse
    github.com/ClickHouse/clickhouse-go/v2 v2.17.1

    // MongoDB
    go.mongodb.org/mongo-driver v1.13.1

    // Kubernetes
    k8s.io/client-go v0.29.0
    k8s.io/api v0.29.0
    k8s.io/apimachinery v0.29.0
    sigs.k8s.io/controller-runtime v0.17.0

    // Authentication
    github.com/golang-jwt/jwt/v5 v5.2.0
    github.com/coreos/go-oidc/v3 v3.9.0
    github.com/casbin/casbin/v2 v2.81.0

    // Observability
    github.com/prometheus/client_golang v1.18.0
    go.opentelemetry.io/otel v1.22.0
    go.opentelemetry.io/otel/trace v1.22.0
    go.uber.org/zap v1.26.0

    // Utilities
    github.com/google/uuid v1.5.0
    github.com/spf13/viper v1.18.2
    github.com/spf13/cobra v1.8.0
    golang.org/x/sync v0.6.0
)
```

---

## 10. API Design

### 10.1 REST API Specification

```yaml
# api/openapi.yaml

openapi: 3.0.3
info:
  title: FlowForge API
  version: 1.0.0
  description: DAG Workflow Engine for Kubernetes

servers:
  - url: /api/v1

security:
  - bearerAuth: []

paths:
  /workflows:
    post:
      summary: Submit a new workflow
      tags: [Workflows]
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/CreateWorkflowRequest'
      responses:
        '201':
          description: Workflow created
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Workflow'
        '400':
          $ref: '#/components/responses/BadRequest'
        '403':
          $ref: '#/components/responses/QuotaExceeded'

    get:
      summary: List workflows
      tags: [Workflows]
      parameters:
        - name: project_id
          in: query
          required: true
          schema:
            type: string
        - name: status
          in: query
          schema:
            type: string
            enum: [PENDING, RUNNING, SUCCEEDED, FAILED, CANCELLED]
        - name: limit
          in: query
          schema:
            type: integer
            default: 20
        - name: offset
          in: query
          schema:
            type: integer
            default: 0
      responses:
        '200':
          description: List of workflows
          content:
            application/json:
              schema:
                type: object
                properties:
                  workflows:
                    type: array
                    items:
                      $ref: '#/components/schemas/Workflow'
                  total:
                    type: integer

  /workflows/{id}:
    get:
      summary: Get workflow details
      tags: [Workflows]
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        '200':
          description: Workflow details
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/WorkflowDetail'
        '404':
          $ref: '#/components/responses/NotFound'

    delete:
      summary: Cancel a workflow
      tags: [Workflows]
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        '200':
          description: Workflow cancelled
        '404':
          $ref: '#/components/responses/NotFound'

  /workflows/{id}/tasks:
    get:
      summary: List tasks in workflow
      tags: [Tasks]
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        '200':
          description: List of tasks
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/Task'

  /workflows/{id}/logs:
    get:
      summary: Get workflow logs
      tags: [Logs]
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
        - name: task_id
          in: query
          schema:
            type: string
        - name: level
          in: query
          schema:
            type: string
            enum: [DEBUG, INFO, WARN, ERROR]
        - name: follow
          in: query
          schema:
            type: boolean
            default: false
      responses:
        '200':
          description: Log entries
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: '#/components/schemas/LogEntry'
            text/event-stream:
              schema:
                type: string

  /tenants/{id}/quota:
    get:
      summary: Get tenant quota usage
      tags: [Quota]
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        '200':
          description: Quota usage
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/QuotaUsage'

    put:
      summary: Update tenant quota
      tags: [Quota]
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/UpdateQuotaRequest'
      responses:
        '200':
          description: Quota updated

components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT

  schemas:
    CreateWorkflowRequest:
      type: object
      required:
        - project_id
        - name
        - dag_spec
      properties:
        project_id:
          type: string
        name:
          type: string
        dag_spec:
          type: object
          description: DAG specification (see DAG spec format)
        parameters:
          type: object
        priority:
          type: integer
          default: 0
        labels:
          type: object
          additionalProperties:
            type: string

    Workflow:
      type: object
      properties:
        id:
          type: string
        project_id:
          type: string
        name:
          type: string
        status:
          type: string
          enum: [PENDING, RUNNING, SUCCEEDED, FAILED, CANCELLED, PAUSED]
        created_at:
          type: string
          format: date-time
        started_at:
          type: string
          format: date-time
        finished_at:
          type: string
          format: date-time
        priority:
          type: integer
        labels:
          type: object

    WorkflowDetail:
      allOf:
        - $ref: '#/components/schemas/Workflow'
        - type: object
          properties:
            dag_spec:
              type: object
            parameters:
              type: object
            tasks:
              type: array
              items:
                $ref: '#/components/schemas/Task'
            error_message:
              type: string

    Task:
      type: object
      properties:
        id:
          type: string
        workflow_id:
          type: string
        name:
          type: string
        status:
          type: string
          enum: [PENDING, QUEUED, RUNNING, SUCCEEDED, FAILED, SKIPPED, RETRYING]
        image:
          type: string
        retry_count:
          type: integer
        exit_code:
          type: integer
        started_at:
          type: string
          format: date-time
        finished_at:
          type: string
          format: date-time
        pod_name:
          type: string
        node_name:
          type: string
        outputs:
          type: object

    LogEntry:
      type: object
      properties:
        timestamp:
          type: string
          format: date-time
        task_id:
          type: string
        level:
          type: string
        message:
          type: string
        extra:
          type: object

    QuotaUsage:
      type: object
      properties:
        entity_type:
          type: string
        entity_id:
          type: string
        limits:
          $ref: '#/components/schemas/ResourceLimits'
        used:
          $ref: '#/components/schemas/ResourceUsage'
        utilization_percent:
          type: number

    ResourceLimits:
      type: object
      properties:
        cpu_limit:
          type: integer
          description: CPU in millicores
        memory_limit:
          type: integer
          description: Memory in MiB
        gpu_limit:
          type: integer
        max_concurrent_pods:
          type: integer
        max_concurrent_workflows:
          type: integer

    ResourceUsage:
      type: object
      properties:
        cpu_used:
          type: integer
        memory_used:
          type: integer
        gpu_used:
          type: integer
        pods_running:
          type: integer
        workflows_active:
          type: integer

    UpdateQuotaRequest:
      type: object
      properties:
        cpu_limit:
          type: integer
        memory_limit:
          type: integer
        gpu_limit:
          type: integer
        max_concurrent_pods:
          type: integer
        max_concurrent_workflows:
          type: integer

  responses:
    BadRequest:
      description: Invalid request
      content:
        application/json:
          schema:
            type: object
            properties:
              error:
                type: string
              details:
                type: array
                items:
                  type: string

    NotFound:
      description: Resource not found
      content:
        application/json:
          schema:
            type: object
            properties:
              error:
                type: string

    QuotaExceeded:
      description: Quota exceeded
      content:
        application/json:
          schema:
            type: object
            properties:
              error:
                type: string
              quota_info:
                $ref: '#/components/schemas/QuotaUsage'
```

### 10.2 gRPC Service Definitions

```protobuf
// api/proto/flowforge.proto

syntax = "proto3";

package flowforge.v1;

option go_package = "github.com/flowforge/flowforge/pkg/api/grpc/v1";

// TaskService handles task status updates from SDK
service TaskService {
    rpc UpdateStatus(UpdateStatusRequest) returns (UpdateStatusResponse);
    rpc SetOutput(SetOutputRequest) returns (SetOutputResponse);
    rpc SaveCheckpoint(SaveCheckpointRequest) returns (SaveCheckpointResponse);
    rpc GetCheckpoint(GetCheckpointRequest) returns (GetCheckpointResponse);
    rpc StreamLogs(stream LogEntry) returns (StreamLogsResponse);
}

message UpdateStatusRequest {
    string task_id = 1;
    string workflow_id = 2;
    string status = 3;
    string message = 4;
    int32 progress = 5;
    string details = 6;  // JSON string
    int64 timestamp = 7;
}

message UpdateStatusResponse {
    bool success = 1;
}

message SetOutputRequest {
    string task_id = 1;
    string workflow_id = 2;
    string key = 3;
    string value = 4;  // JSON string
}

message SetOutputResponse {
    bool success = 1;
}

message SaveCheckpointRequest {
    string task_id = 1;
    string workflow_id = 2;
    string data = 3;  // JSON string
    int64 timestamp = 4;
}

message SaveCheckpointResponse {
    bool success = 1;
}

message GetCheckpointRequest {
    string task_id = 1;
    string workflow_id = 2;
}

message GetCheckpointResponse {
    string data = 1;  // JSON string, empty if no checkpoint
    int64 timestamp = 2;
}

message LogEntry {
    string task_id = 1;
    string workflow_id = 2;
    string tenant_id = 3;
    int64 timestamp = 4;
    string level = 5;
    string message = 6;
    string extra = 7;  // JSON string
}

message StreamLogsResponse {
    int32 received = 1;
    int32 processed = 2;
}

// Internal service for controller communication
service SchedulerService {
    rpc ScheduleTask(ScheduleTaskRequest) returns (ScheduleTaskResponse);
    rpc CancelTask(CancelTaskRequest) returns (CancelTaskResponse);
    rpc GetQueueStatus(GetQueueStatusRequest) returns (GetQueueStatusResponse);
}

message ScheduleTaskRequest {
    string task_id = 1;
    string workflow_id = 2;
    int32 priority = 3;
    ResourceRequest resources = 4;
}

message ResourceRequest {
    int32 cpu = 1;      // millicores
    int32 memory = 2;   // MiB
    int32 gpu = 3;
}

message ScheduleTaskResponse {
    bool queued = 1;
    string reason = 2;  // If not queued
}

message CancelTaskRequest {
    string task_id = 1;
}

message CancelTaskResponse {
    bool success = 1;
}

message GetQueueStatusRequest {
    string tenant_id = 1;
}

message GetQueueStatusResponse {
    int32 pending_high = 1;
    int32 pending_normal = 2;
    int32 pending_low = 3;
    int32 processing = 4;
}
```

---

## 11. Deployment Architecture

### 11.1 Kubernetes Deployment Overview

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    Kubernetes Deployment Architecture                    │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  Namespace: flowforge-system                                            │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                                                                     │ │
│  │  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐    │ │
│  │  │ api-server      │  │ controller      │  │ scheduler       │    │ │
│  │  │ (Deployment)    │  │ (Deployment)    │  │ (Deployment)    │    │ │
│  │  │ Replicas: 3     │  │ Replicas: 2     │  │ Replicas: 2     │    │ │
│  │  │ HPA: 3-10       │  │ Leader Election │  │ Leader Election │    │ │
│  │  └─────────────────┘  └─────────────────┘  └─────────────────┘    │ │
│  │                                                                     │ │
│  │  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐    │ │
│  │  │ executor        │  │ log-aggregator  │  │ metrics-collector│   │ │
│  │  │ (Deployment)    │  │ (Deployment)    │  │ (Deployment)    │    │ │
│  │  │ Replicas: 3     │  │ Replicas: 2     │  │ Replicas: 2     │    │ │
│  │  └─────────────────┘  └─────────────────┘  └─────────────────┘    │ │
│  │                                                                     │ │
│  │  ┌─────────────────┐                                               │ │
│  │  │ status-collector│                                               │ │
│  │  │ (DaemonSet)     │  Runs on every node to collect pod status    │ │
│  │  └─────────────────┘                                               │ │
│  │                                                                     │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                                                          │
│  Namespace: flowforge-data                                              │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                                                                     │ │
│  │  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐    │ │
│  │  │ PostgreSQL      │  │ Redis Cluster   │  │ ClickHouse      │    │ │
│  │  │ (StatefulSet)   │  │ (StatefulSet)   │  │ (StatefulSet)   │    │ │
│  │  │ Primary + 2 Rep │  │ 3 Masters       │  │ 3 Replicas      │    │ │
│  │  │                 │  │ 3 Replicas      │  │                 │    │ │
│  │  └─────────────────┘  └─────────────────┘  └─────────────────┘    │ │
│  │                                                                     │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                                                          │
│  Namespace: flowforge-workloads (per tenant)                            │
│  ┌────────────────────────────────────────────────────────────────────┐ │
│  │                                                                     │ │
│  │  Workflow task pods are created here                               │ │
│  │  Each project gets its own namespace for isolation                 │ │
│  │                                                                     │ │
│  │  ResourceQuota and LimitRange applied per namespace                │ │
│  │  NetworkPolicy for pod-to-pod isolation                            │ │
│  │                                                                     │ │
│  └────────────────────────────────────────────────────────────────────┘ │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### 11.2 Helm Chart Structure

```yaml
# deployments/helm/flowforge/values.yaml

global:
  imageRegistry: "ghcr.io/flowforge"
  imagePullSecrets: []
  storageClass: "standard"

apiServer:
  replicaCount: 3
  image:
    repository: flowforge/api-server
    tag: "1.0.0"
  resources:
    requests:
      cpu: "500m"
      memory: "512Mi"
    limits:
      cpu: "2"
      memory: "2Gi"
  hpa:
    enabled: true
    minReplicas: 3
    maxReplicas: 10
    targetCPU: 70
  service:
    type: ClusterIP
    port: 8080
  ingress:
    enabled: true
    className: "nginx"
    hosts:
      - host: flowforge.example.com
        paths:
          - path: /api
            pathType: Prefix

controller:
  replicaCount: 2
  image:
    repository: flowforge/controller
    tag: "1.0.0"
  resources:
    requests:
      cpu: "500m"
      memory: "512Mi"
    limits:
      cpu: "2"
      memory: "2Gi"
  leaderElection:
    enabled: true
    leaseDuration: "15s"
    renewDeadline: "10s"
    retryPeriod: "2s"

scheduler:
  replicaCount: 2
  image:
    repository: flowforge/scheduler
    tag: "1.0.0"
  resources:
    requests:
      cpu: "500m"
      memory: "512Mi"
  config:
    schedulingInterval: "1s"
    maxBatchSize: 100

executor:
  replicaCount: 3
  image:
    repository: flowforge/executor
    tag: "1.0.0"
  resources:
    requests:
      cpu: "500m"
      memory: "512Mi"
  sdkImage: "flowforge/sdk:1.0.0"

statusCollector:
  image:
    repository: flowforge/status-collector
    tag: "1.0.0"
  resources:
    requests:
      cpu: "100m"
      memory: "128Mi"

logAggregator:
  replicaCount: 2
  image:
    repository: flowforge/log-aggregator
    tag: "1.0.0"
  resources:
    requests:
      cpu: "500m"
      memory: "1Gi"
  config:
    batchSize: 1000
    flushInterval: "1s"

postgresql:
  enabled: true
  architecture: replication
  auth:
    database: flowforge
    username: flowforge
    existingSecret: flowforge-postgres-secret
  primary:
    persistence:
      size: 50Gi
  readReplicas:
    replicaCount: 2
    persistence:
      size: 50Gi

redis:
  enabled: true
  architecture: cluster
  cluster:
    nodes: 6
    replicas: 1
  auth:
    existingSecret: flowforge-redis-secret

clickhouse:
  enabled: true
  replicaCount: 3
  shards: 1
  persistence:
    size: 100Gi
  auth:
    existingSecret: flowforge-clickhouse-secret

monitoring:
  prometheus:
    enabled: true
    serviceMonitor:
      enabled: true
  grafana:
    enabled: true
    dashboards:
      - flowforge-overview
      - flowforge-workflows
      - flowforge-quotas

rbac:
  create: true
  serviceAccount:
    create: true
    name: flowforge
```

### 11.3 Network Policies

```yaml
# deployments/kubernetes/base/network-policies.yaml

apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: flowforge-api-server
  namespace: flowforge-system
spec:
  podSelector:
    matchLabels:
      app: api-server
  policyTypes:
    - Ingress
    - Egress
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              name: ingress-nginx
      ports:
        - protocol: TCP
          port: 8080
    - from:
        - podSelector:
            matchLabels:
              app.kubernetes.io/part-of: flowforge
      ports:
        - protocol: TCP
          port: 9090  # gRPC
  egress:
    - to:
        - podSelector:
            matchLabels:
              app: postgresql
      ports:
        - protocol: TCP
          port: 5432
    - to:
        - podSelector:
            matchLabels:
              app: redis
      ports:
        - protocol: TCP
          port: 6379

---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: flowforge-workload-isolation
  namespace: flowforge-workloads-default
spec:
  podSelector:
    matchLabels:
      flowforge.io/workload: "true"
  policyTypes:
    - Ingress
    - Egress
  ingress: []  # No ingress to workflow pods
  egress:
    - to:
        - namespaceSelector:
            matchLabels:
              name: flowforge-system
        - podSelector:
            matchLabels:
              app: api-server
      ports:
        - protocol: TCP
          port: 9090
    - to:
        - namespaceSelector:
            matchLabels:
              name: flowforge-system
        - podSelector:
            matchLabels:
              app: log-aggregator
      ports:
        - protocol: TCP
          port: 8081
    - to:
        - namespaceSelector:
            matchLabels:
              name: flowforge-data
        - podSelector:
            matchLabels:
              app: redis
      ports:
        - protocol: TCP
          port: 6379
    # Allow DNS
    - to:
        - namespaceSelector: {}
          podSelector:
            matchLabels:
              k8s-app: kube-dns
      ports:
        - protocol: UDP
          port: 53
```

---

## 12. Security Considerations

### 12.1 Authentication & Authorization

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    Security Architecture                                 │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  External Authentication                                                │
│  ───────────────────────                                                │
│                                                                          │
│  ┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐   │
│  │   OIDC/SAML     │     │   API Keys      │     │   Service Acct  │   │
│  │   Provider      │     │   (for CI/CD)   │     │   (K8s)         │   │
│  └────────┬────────┘     └────────┬────────┘     └────────┬────────┘   │
│           │                       │                       │            │
│           └───────────────────────┼───────────────────────┘            │
│                                   │                                     │
│                                   ▼                                     │
│                          ┌────────────────┐                            │
│                          │  API Gateway   │                            │
│                          │  - Token Valid │                            │
│                          │  - Rate Limit  │                            │
│                          └────────┬───────┘                            │
│                                   │                                     │
│                                   ▼                                     │
│  Authorization (RBAC with Casbin)                                      │
│  ────────────────────────────────                                      │
│                                                                          │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │                                                                   │   │
│  │  Roles:                                                          │   │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐              │   │
│  │  │ TenantAdmin │  │ ProjectDev  │  │ Viewer      │              │   │
│  │  │             │  │             │  │             │              │   │
│  │  │ - All CRUD  │  │ - Submit WF │  │ - Read only │              │   │
│  │  │ - Quota Mgmt│  │ - View Logs │  │ - View Logs │              │   │
│  │  │ - User Mgmt │  │ - Cancel WF │  │             │              │   │
│  │  └─────────────┘  └─────────────┘  └─────────────┘              │   │
│  │                                                                   │   │
│  │  Policy Example (Casbin):                                        │   │
│  │  p, TenantAdmin, tenant/*, *                                     │   │
│  │  p, ProjectDev, project/workflows, read|create|delete            │   │
│  │  p, ProjectDev, project/logs, read                               │   │
│  │  p, Viewer, project/*, read                                      │   │
│  │                                                                   │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                          │
│  Task-Level Security                                                    │
│  ───────────────────                                                    │
│                                                                          │
│  1. Each task pod gets a unique short-lived token                       │
│  2. Token scoped to specific task operations only                       │
│  3. Network policies restrict pod communication                         │
│  4. Secrets mounted as tmpfs (not persisted to disk)                   │
│  5. ServiceAccount with minimal RBAC permissions                        │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### 12.2 Secret Management

```go
// pkg/auth/task_token.go

package auth

import (
    "time"

    "github.com/golang-jwt/jwt/v5"
)

type TaskTokenClaims struct {
    jwt.RegisteredClaims
    TaskID     string `json:"task_id"`
    WorkflowID string `json:"workflow_id"`
    TenantID   string `json:"tenant_id"`
    ProjectID  string `json:"project_id"`
    Scope      string `json:"scope"` // "status,logs,metrics,outputs"
}

type TaskTokenManager struct {
    signingKey []byte
    ttl        time.Duration
}

// GenerateTaskToken creates a short-lived token for a specific task
func (m *TaskTokenManager) GenerateTaskToken(task *model.Task) (string, error) {
    claims := TaskTokenClaims{
        RegisteredClaims: jwt.RegisteredClaims{
            ExpiresAt: jwt.NewNumericDate(time.Now().Add(m.ttl)),
            IssuedAt:  jwt.NewNumericDate(time.Now()),
            Subject:   task.ID,
            Issuer:    "flowforge",
        },
        TaskID:     task.ID,
        WorkflowID: task.WorkflowID,
        TenantID:   task.TenantID,
        ProjectID:  task.ProjectID,
        Scope:      "status,logs,metrics,outputs,checkpoint",
    }

    token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
    return token.SignedString(m.signingKey)
}

// ValidateTaskToken validates a task token and returns claims
func (m *TaskTokenManager) ValidateTaskToken(tokenString string) (*TaskTokenClaims, error) {
    token, err := jwt.ParseWithClaims(tokenString, &TaskTokenClaims{}, func(token *jwt.Token) (interface{}, error) {
        return m.signingKey, nil
    })

    if err != nil {
        return nil, err
    }

    claims, ok := token.Claims.(*TaskTokenClaims)
    if !ok || !token.Valid {
        return nil, ErrInvalidToken
    }

    return claims, nil
}

// HasScope checks if token has required scope
func (c *TaskTokenClaims) HasScope(required string) bool {
    scopes := strings.Split(c.Scope, ",")
    for _, s := range scopes {
        if s == required {
            return true
        }
    }
    return false
}
```

### 12.3 Service-to-Service 安全

- **mTLS（可选）**：控制平面组件之间启用双向 TLS，避免跨 namespace 的明文通信。
- **短期令牌**：SDK 与 Collector/Controller 之间使用短期 task token，最小化权限范围与有效期。
- **零信任网络策略**：NetworkPolicy 限制 east-west 流量，仅允许必要端口与命名空间访问。

---

## Appendix A: Quick Start Guide

### A.1 Development Setup

```bash
# Clone repository
git clone https://github.com/flowforge/flowforge
cd flowforge

# Install dependencies
make deps

# Start local development environment (PostgreSQL, Redis, ClickHouse)
make dev-env-up

# Run database migrations
make migrate

# Build all binaries
make build

# Run tests
make test

# Start all services locally
make run-all
```

### A.2 Example Workflow Submission

```python
# examples/submit_workflow.py

import requests

API_URL = "http://localhost:8080/api/v1"
TOKEN = "your-api-token"

workflow = {
    "project_id": "project-123",
    "name": "data-pipeline-001",
    "dag_spec": {
        "version": "1.0",
        "entrypoint": "main",
        "templates": {
            "main": {
                "dag": {
                    "tasks": [
                        {
                            "name": "extract",
                            "template": "python-task",
                            "arguments": {"parameters": [{"name": "script", "value": "extract.py"}]}
                        },
                        {
                            "name": "transform",
                            "template": "python-task",
                            "dependencies": ["extract"],
                            "arguments": {"parameters": [{"name": "script", "value": "transform.py"}]}
                        },
                        {
                            "name": "load",
                            "template": "python-task",
                            "dependencies": ["transform"],
                            "arguments": {"parameters": [{"name": "script", "value": "load.py"}]}
                        }
                    ]
                }
            },
            "python-task": {
                "container": {
                    "image": "python:3.11-slim",
                    "command": ["python"],
                    "args": ["{{inputs.parameters.script}}"]
                },
                "inputs": {"parameters": [{"name": "script"}]},
                "retryStrategy": {"limit": 3, "backoff": {"duration": "10s", "factor": 2}}
            }
        }
    },
    "parameters": {"env": "production"},
    "priority": 50
}

response = requests.post(
    f"{API_URL}/workflows",
    headers={"Authorization": f"Bearer {TOKEN}"},
    json=workflow
)

print(response.json())
```

### A.3 Using the Python SDK

```python
# examples/task_with_sdk.py

from flowforge import client

def main():
    # Get checkpoint if task is being retried
    checkpoint = client.get_checkpoint()
    start_idx = checkpoint.get('last_idx', 0) if checkpoint else 0

    client.log("Starting data processing", level="INFO")
    client.status("RUNNING", progress=0)

    data = load_data()
    total = len(data)

    for idx, item in enumerate(data[start_idx:], start=start_idx):
        try:
            result = process_item(item)

            # Update progress
            progress = int((idx + 1) / total * 100)
            client.status("RUNNING", progress=progress)
            client.metric("items_processed", idx + 1)

            # Save checkpoint periodically
            if idx % 100 == 0:
                client.checkpoint({"last_idx": idx})
                client.log(f"Checkpoint saved at index {idx}")

        except Exception as e:
            client.log(f"Error processing item {idx}: {e}", level="ERROR")
            client.checkpoint({"last_idx": idx})
            raise

    # Store output for downstream tasks
    client.output("processed_count", total)
    client.output("output_path", "/data/output.parquet")

    client.status("SUCCEEDED")
    client.log("Processing completed successfully")

if __name__ == "__main__":
    main()
```

---

## Appendix B: Comparison with Argo Workflows

| Feature | Argo Workflows | FlowForge |
|---------|---------------|-----------|
| State Storage | CRD Status (limited) | PostgreSQL (unlimited) |
| Task Queue | K8s Controller Queue | Redis (distributed) |
| Quota Management | K8s ResourceQuota only | Hierarchical (tenant/dept/group/project) |
| Log Storage | Pod logs only | ClickHouse/MongoDB (long-term) |
| Metrics | Prometheus only | Custom + Prometheus |
| Client SDK | Go only | Python (with Go planned) |
| Retry Mechanism | Basic | Advanced with circuit breaker |
| Multi-tenancy | Namespace-level | Full hierarchy support |
| Scalability | Single controller | Distributed controllers |

---

*Document Version: 1.0.0*
*Last Updated: January 2024*

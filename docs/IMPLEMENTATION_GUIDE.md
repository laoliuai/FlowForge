# FlowForge Implementation Guide

This guide provides step-by-step instructions for implementing each module of the FlowForge workflow engine.

## Table of Contents

1. [Project Setup](#1-project-setup)
2. [Phase 1: Core Infrastructure](#2-phase-1-core-infrastructure)
3. [Phase 2: Workflow Controller](#3-phase-2-workflow-controller)
4. [Phase 3: Scheduler & Queue](#4-phase-3-scheduler--queue)
5. [Phase 4: Pod Executor](#5-phase-4-pod-executor)
6. [Phase 5: Quota Management](#6-phase-5-quota-management)
7. [Phase 6: Python SDK](#7-phase-6-python-sdk)
8. [Phase 7: Observability](#8-phase-7-observability)
9. [Phase 8: API & Authentication](#9-phase-8-api--authentication)
10. [Testing Strategy](#10-testing-strategy)

---

## 1. Project Setup

### 1.1 Initialize Go Project

```bash
mkdir flowforge && cd flowforge
go mod init github.com/flowforge/flowforge

# Create directory structure
mkdir -p cmd/{api-server,controller,scheduler,executor,cli}
mkdir -p pkg/{api,apiserver,controller,scheduler,executor,queue,eventbus,quota,retry,logaggregator,metrics,model,store,auth,config}
mkdir -p sdk/python/flowforge
mkdir -p deployments/{kubernetes,helm}
mkdir -p migrations/postgres
mkdir -p scripts
mkdir -p internal/testutil
```

### 1.2 Install Core Dependencies

```bash
# Web framework
go get github.com/gin-gonic/gin

# gRPC
go get google.golang.org/grpc
go get google.golang.org/protobuf/cmd/protoc-gen-go@latest
go get google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Database
go get gorm.io/gorm
go get gorm.io/driver/postgres
go get github.com/golang-migrate/migrate/v4

# Redis
go get github.com/redis/go-redis/v9

# Kubernetes
go get k8s.io/client-go@latest
go get k8s.io/api@latest
go get sigs.k8s.io/controller-runtime

# Utilities
go get github.com/google/uuid
go get github.com/spf13/viper
go get github.com/spf13/cobra
go get go.uber.org/zap
```

### 1.3 Create Makefile

```makefile
# Makefile

.PHONY: all build test clean proto

VERSION ?= $(shell git describe --tags --always --dirty)
LDFLAGS := -X main.Version=$(VERSION)

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o bin/api-server ./cmd/api-server
	go build -ldflags "$(LDFLAGS)" -o bin/controller ./cmd/controller
	go build -ldflags "$(LDFLAGS)" -o bin/scheduler ./cmd/scheduler
	go build -ldflags "$(LDFLAGS)" -o bin/executor ./cmd/executor
	go build -ldflags "$(LDFLAGS)" -o bin/ffctl ./cmd/cli

proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		pkg/api/grpc/proto/*.proto

test:
	go test -v -race -cover ./...

test-integration:
	go test -v -tags=integration ./...

lint:
	golangci-lint run ./...

migrate:
	migrate -path migrations/postgres -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path migrations/postgres -database "$(DATABASE_URL)" down 1

dev-env-up:
	docker-compose -f docker-compose.dev.yml up -d

dev-env-down:
	docker-compose -f docker-compose.dev.yml down

clean:
	rm -rf bin/
```

---

## 2. Phase 1: Core Infrastructure

### 2.1 Configuration Management

```go
// pkg/config/config.go

package config

import (
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server     ServerConfig
	Database   DatabaseConfig
	Redis      RedisConfig
	Kubernetes KubernetesConfig
	ClickHouse ClickHouseConfig
	Auth       AuthConfig
	Logging    LoggingConfig
}

type ServerConfig struct {
	HTTPPort    int           `mapstructure:"http_port"`
	GRPCPort    int           `mapstructure:"grpc_port"`
	MetricsPort int           `mapstructure:"metrics_port"`
	ReadTimeout time.Duration `mapstructure:"read_timeout"`
}

type DatabaseConfig struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	User         string `mapstructure:"user"`
	Password     string `mapstructure:"password"`
	Database     string `mapstructure:"database"`
	SSLMode      string `mapstructure:"ssl_mode"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
}

type RedisConfig struct {
	Addresses  []string `mapstructure:"addresses"`
	Password   string   `mapstructure:"password"`
	DB         int      `mapstructure:"db"`
	PoolSize   int      `mapstructure:"pool_size"`
	ClusterMode bool    `mapstructure:"cluster_mode"`
}

type KubernetesConfig struct {
	InCluster  bool   `mapstructure:"in_cluster"`
	KubeConfig string `mapstructure:"kubeconfig"`
	Namespace  string `mapstructure:"namespace"`
}

type ClickHouseConfig struct {
	Hosts    []string `mapstructure:"hosts"`
	Database string   `mapstructure:"database"`
	User     string   `mapstructure:"user"`
	Password string   `mapstructure:"password"`
}

type AuthConfig struct {
	JWTSecret     string        `mapstructure:"jwt_secret"`
	TokenTTL      time.Duration `mapstructure:"token_ttl"`
	TaskTokenTTL  time.Duration `mapstructure:"task_token_ttl"`
	OIDCIssuer    string        `mapstructure:"oidc_issuer"`
	OIDCClientID  string        `mapstructure:"oidc_client_id"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"` // json or console
}

func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("/etc/flowforge/")
	viper.AddConfigPath(".")

	// Environment variable support
	viper.SetEnvPrefix("FLOWFORGE")
	viper.AutomaticEnv()

	// Defaults
	viper.SetDefault("server.http_port", 8080)
	viper.SetDefault("server.grpc_port", 9090)
	viper.SetDefault("server.metrics_port", 9091)
	viper.SetDefault("server.read_timeout", "30s")
	viper.SetDefault("database.max_open_conns", 25)
	viper.SetDefault("database.max_idle_conns", 5)
	viper.SetDefault("redis.pool_size", 100)
	viper.SetDefault("auth.token_ttl", "24h")
	viper.SetDefault("auth.task_token_ttl", "24h")
	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.format", "json")

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, err
		}
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Database, c.SSLMode,
	)
}
```

### 2.2 Data Models

```go
// pkg/model/tenant.go

package model

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Tenant struct {
	ID          uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	Name        string         `gorm:"uniqueIndex;not null"`
	Description string
	QuotaID     *uuid.UUID     `gorm:"type:uuid"`
	Quota       *Quota         `gorm:"foreignKey:QuotaID"`
	Groups      []Group        `gorm:"foreignKey:TenantID"`
	Departments []Department   `gorm:"foreignKey:TenantID"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   gorm.DeletedAt `gorm:"index"`
}

type Department struct {
	ID        uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	TenantID  uuid.UUID      `gorm:"type:uuid;not null"`
	Tenant    *Tenant        `gorm:"foreignKey:TenantID"`
	Name      string         `gorm:"not null"`
	ParentID  *uuid.UUID     `gorm:"type:uuid"`
	Parent    *Department    `gorm:"foreignKey:ParentID"`
	Children  []Department   `gorm:"foreignKey:ParentID"`
	QuotaID   *uuid.UUID     `gorm:"type:uuid"`
	Quota     *Quota         `gorm:"foreignKey:QuotaID"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Group struct {
	ID           uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	TenantID     uuid.UUID      `gorm:"type:uuid;not null"`
	Tenant       *Tenant        `gorm:"foreignKey:TenantID"`
	DepartmentID *uuid.UUID     `gorm:"type:uuid"`
	Department   *Department    `gorm:"foreignKey:DepartmentID"`
	Name         string         `gorm:"not null"`
	Description  string
	QuotaID      *uuid.UUID     `gorm:"type:uuid"`
	Quota        *Quota         `gorm:"foreignKey:QuotaID"`
	Projects     []Project      `gorm:"foreignKey:GroupID"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    gorm.DeletedAt `gorm:"index"`
}

type Project struct {
	ID          uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	GroupID     uuid.UUID      `gorm:"type:uuid;not null"`
	Group       *Group         `gorm:"foreignKey:GroupID"`
	Name        string         `gorm:"not null"`
	Description string
	Namespace   string         `gorm:"not null"` // K8s namespace
	QuotaID     *uuid.UUID     `gorm:"type:uuid"`
	Quota       *Quota         `gorm:"foreignKey:QuotaID"`
	Workflows   []Workflow     `gorm:"foreignKey:ProjectID"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   gorm.DeletedAt `gorm:"index"`
}
```

```go
// pkg/model/quota.go

package model

import (
	"time"

	"github.com/google/uuid"
)

type Quota struct {
	ID                uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	Name              string    `gorm:"not null"`
	CPULimit          int       `gorm:"default:4000"`           // millicores
	CPURequest        int       `gorm:"default:2000"`
	MemoryLimit       int       `gorm:"default:8192"`           // MiB
	MemoryRequest     int       `gorm:"default:4096"`
	GPULimit          int       `gorm:"default:0"`
	MaxConcurrentPods int       `gorm:"default:10"`
	MaxConcurrentWF   int       `gorm:"default:5"`
	StorageLimit      int       `gorm:"default:100"`            // GiB
	PriorityWeight    int       `gorm:"default:100"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type QuotaUsage struct {
	ID              uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	QuotaID         uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_quota_entity"`
	Quota           *Quota    `gorm:"foreignKey:QuotaID"`
	EntityType      string    `gorm:"not null;uniqueIndex:idx_quota_entity"` // tenant, group, project, department
	EntityID        uuid.UUID `gorm:"type:uuid;not null;uniqueIndex:idx_quota_entity"`
	CPUUsed         int       `gorm:"default:0"`
	MemoryUsed      int       `gorm:"default:0"`
	GPUUsed         int       `gorm:"default:0"`
	PodsRunning     int       `gorm:"default:0"`
	WorkflowsActive int       `gorm:"default:0"`
	StorageUsed     int       `gorm:"default:0"`
	UpdatedAt       time.Time
}
```

```go
// pkg/model/workflow.go

package model

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type WorkflowStatus string

const (
	WorkflowPending   WorkflowStatus = "PENDING"
	WorkflowRunning   WorkflowStatus = "RUNNING"
	WorkflowSucceeded WorkflowStatus = "SUCCEEDED"
	WorkflowFailed    WorkflowStatus = "FAILED"
	WorkflowCancelled WorkflowStatus = "CANCELLED"
	WorkflowPaused    WorkflowStatus = "PAUSED"
)

type Workflow struct {
	ID           uuid.UUID       `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	ProjectID    uuid.UUID       `gorm:"type:uuid;not null;index"`
	Project      *Project        `gorm:"foreignKey:ProjectID"`
	Name         string          `gorm:"not null"`
	Description  string
	DAGSpec      JSONB           `gorm:"type:jsonb;not null"`
	Parameters   JSONB           `gorm:"type:jsonb;default:'{}'"`
	Status       WorkflowStatus  `gorm:"type:varchar(50);default:'PENDING';index"`
	ErrorMessage string
	StartedAt    *time.Time
	FinishedAt   *time.Time
	SubmittedBy  string
	Priority     int             `gorm:"default:0"`
	Labels       JSONB           `gorm:"type:jsonb;default:'{}'"`
	Tasks        []Task          `gorm:"foreignKey:WorkflowID"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// JSONB is a custom type for PostgreSQL JSONB columns
type JSONB map[string]interface{}

func (j JSONB) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return json.Marshal(j)
}

func (j *JSONB) Scan(value interface{}) error {
	if value == nil {
		*j = nil
		return nil
	}
	bytes, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("failed to scan JSONB: %v", value)
	}
	return json.Unmarshal(bytes, j)
}

func (j JSONB) GormDataType() string {
	return "jsonb"
}
```

```go
// pkg/model/task.go

package model

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

type TaskStatus string

const (
	TaskPending   TaskStatus = "PENDING"
	TaskQueued    TaskStatus = "QUEUED"
	TaskRunning   TaskStatus = "RUNNING"
	TaskSucceeded TaskStatus = "SUCCEEDED"
	TaskFailed    TaskStatus = "FAILED"
	TaskSkipped   TaskStatus = "SKIPPED"
	TaskRetrying  TaskStatus = "RETRYING"
)

type Task struct {
	ID            uuid.UUID      `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	WorkflowID    uuid.UUID      `gorm:"type:uuid;not null;index"`
	Workflow      *Workflow      `gorm:"foreignKey:WorkflowID"`
	Name          string         `gorm:"not null;uniqueIndex:idx_workflow_task_name"`
	TemplateName  string
	Image         string         `gorm:"not null"`
	Command       pq.StringArray `gorm:"type:text[]"`
	Args          pq.StringArray `gorm:"type:text[]"`
	Env           JSONB          `gorm:"type:jsonb;default:'[]'"`
	CPURequest    int            `gorm:"default:100"`  // millicores
	CPULimit      int            `gorm:"default:1000"`
	MemoryRequest int            `gorm:"default:256"`  // MiB
	MemoryLimit   int            `gorm:"default:1024"`
	GPURequest    int            `gorm:"default:0"`
	Status        TaskStatus     `gorm:"type:varchar(50);default:'PENDING';index"`
	ErrorMessage  string
	ExitCode      *int
	RetryLimit    int            `gorm:"default:3"`
	RetryCount    int            `gorm:"default:0"`
	BackoffSecs   int            `gorm:"default:0"`
	NextRetryAt   *time.Time     `gorm:"index"`
	QueuedAt      *time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	PodName       string
	PodUID        string
	NodeName      string
	Outputs       JSONB          `gorm:"type:jsonb;default:'{}'"`
	WhenCondition string
	Dependencies  []TaskDependency `gorm:"foreignKey:TaskID"`
	Events        []TaskEvent      `gorm:"foreignKey:TaskID"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type TaskDependency struct {
	ID          uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	TaskID      uuid.UUID `gorm:"type:uuid;not null;index"`
	DependsOnID uuid.UUID `gorm:"type:uuid;not null;index"`
	Type        string    `gorm:"default:'success'"` // success, completion, failure
}

type TaskEvent struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	TaskID    uuid.UUID `gorm:"type:uuid;not null;index"`
	EventType string    `gorm:"not null"`
	Message   string
	Details   JSONB     `gorm:"type:jsonb"`
	CreatedAt time.Time `gorm:"index"`
}

// GetResourceRequest returns the resource requirements for quota checking
func (t *Task) GetResourceRequest() ResourceRequest {
	return ResourceRequest{
		CPU:    t.CPURequest,
		Memory: t.MemoryRequest,
		GPU:    t.GPURequest,
	}
}

type ResourceRequest struct {
	CPU    int `json:"cpu"`
	Memory int `json:"memory"`
	GPU    int `json:"gpu"`
}
```

### 2.3 Database Store

```go
// pkg/store/postgres/store.go

package postgres

import (
	"context"
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/flowforge/flowforge/pkg/config"
	"github.com/flowforge/flowforge/pkg/model"
)

type Store struct {
	db *gorm.DB
}

func NewStore(cfg *config.DatabaseConfig) (*Store, error) {
	gormConfig := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	}

	db, err := gorm.Open(postgres.Open(cfg.DSN()), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)

	return &Store{db: db}, nil
}

func (s *Store) DB() *gorm.DB {
	return s.db
}

func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// AutoMigrate runs automatic migrations (for development only)
func (s *Store) AutoMigrate() error {
	return s.db.AutoMigrate(
		&model.Tenant{},
		&model.Department{},
		&model.Group{},
		&model.Project{},
		&model.Quota{},
		&model.QuotaUsage{},
		&model.Workflow{},
		&model.Task{},
		&model.TaskDependency{},
		&model.TaskEvent{},
	)
}

// Workflow Repository
type WorkflowRepository struct {
	db *gorm.DB
}

func NewWorkflowRepository(db *gorm.DB) *WorkflowRepository {
	return &WorkflowRepository{db: db}
}

func (r *WorkflowRepository) Create(ctx context.Context, workflow *model.Workflow) error {
	return r.db.WithContext(ctx).Create(workflow).Error
}

func (r *WorkflowRepository) GetByID(ctx context.Context, id string) (*model.Workflow, error) {
	var workflow model.Workflow
	err := r.db.WithContext(ctx).
		Preload("Tasks").
		Preload("Tasks.Dependencies").
		First(&workflow, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &workflow, nil
}

func (r *WorkflowRepository) UpdateStatus(ctx context.Context, id string, status model.WorkflowStatus, errorMsg string) error {
	updates := map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}

	if errorMsg != "" {
		updates["error_message"] = errorMsg
	}

	if status == model.WorkflowRunning {
		now := time.Now()
		updates["started_at"] = &now
	}

	if status == model.WorkflowSucceeded || status == model.WorkflowFailed || status == model.WorkflowCancelled {
		now := time.Now()
		updates["finished_at"] = &now
	}

	return r.db.WithContext(ctx).Model(&model.Workflow{}).Where("id = ?", id).Updates(updates).Error
}

func (r *WorkflowRepository) List(ctx context.Context, projectID string, status *model.WorkflowStatus, limit, offset int) ([]model.Workflow, int64, error) {
	var workflows []model.Workflow
	var total int64

	query := r.db.WithContext(ctx).Model(&model.Workflow{}).Where("project_id = ?", projectID)

	if status != nil {
		query = query.Where("status = ?", *status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	err := query.
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&workflows).Error

	return workflows, total, err
}

// Task Repository
type TaskRepository struct {
	db *gorm.DB
}

func NewTaskRepository(db *gorm.DB) *TaskRepository {
	return &TaskRepository{db: db}
}

func (r *TaskRepository) Create(ctx context.Context, task *model.Task) error {
	return r.db.WithContext(ctx).Create(task).Error
}

func (r *TaskRepository) CreateBatch(ctx context.Context, tasks []*model.Task) error {
	return r.db.WithContext(ctx).CreateInBatches(tasks, 100).Error
}

func (r *TaskRepository) GetByID(ctx context.Context, id string) (*model.Task, error) {
	var task model.Task
	err := r.db.WithContext(ctx).
		Preload("Dependencies").
		First(&task, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &task, nil
}

func (r *TaskRepository) UpdateStatus(ctx context.Context, id string, status model.TaskStatus, updates map[string]interface{}) error {
	if updates == nil {
		updates = make(map[string]interface{})
	}
	updates["status"] = status
	updates["updated_at"] = time.Now()

	return r.db.WithContext(ctx).Model(&model.Task{}).Where("id = ?", id).Updates(updates).Error
}

func (r *TaskRepository) GetPendingTasks(ctx context.Context, workflowID string) ([]model.Task, error) {
	var tasks []model.Task
	err := r.db.WithContext(ctx).
		Preload("Dependencies").
		Where("workflow_id = ? AND status = ?", workflowID, model.TaskPending).
		Find(&tasks).Error
	return tasks, err
}

func (r *TaskRepository) GetRetryableTasks(ctx context.Context) ([]model.Task, error) {
	var tasks []model.Task
	err := r.db.WithContext(ctx).
		Where("status = ? AND next_retry_at <= ?", model.TaskRetrying, time.Now()).
		Find(&tasks).Error
	return tasks, err
}

func (r *TaskRepository) SetOutput(ctx context.Context, taskID, key string, value interface{}) error {
	return r.db.WithContext(ctx).Exec(`
		UPDATE tasks
		SET outputs = outputs || ?::jsonb, updated_at = NOW()
		WHERE id = ?
	`, fmt.Sprintf(`{"%s": %s}`, key, mustMarshal(value)), taskID).Error
}

func mustMarshal(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
```

### 2.4 Redis Client

```go
// pkg/store/redis/client.go

package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/flowforge/flowforge/pkg/config"
)

type Client struct {
	rdb redis.UniversalClient
}

func NewClient(cfg *config.RedisConfig) (*Client, error) {
	var rdb redis.UniversalClient

	if cfg.ClusterMode {
		rdb = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:    cfg.Addresses,
			Password: cfg.Password,
			PoolSize: cfg.PoolSize,
		})
	} else {
		rdb = redis.NewClient(&redis.Options{
			Addr:     cfg.Addresses[0],
			Password: cfg.Password,
			DB:       cfg.DB,
			PoolSize: cfg.PoolSize,
		})
	}

	// Test connection
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to redis: %w", err)
	}

	return &Client{rdb: rdb}, nil
}

func (c *Client) Client() redis.UniversalClient {
	return c.rdb
}

func (c *Client) Close() error {
	return c.rdb.Close()
}
```

---

## 3. Phase 2: Workflow Controller

### 3.1 DAG Parser

```go
// pkg/scheduler/dag/parser.go

package dag

import (
	"encoding/json"
	"fmt"

	"github.com/flowforge/flowforge/pkg/model"
)

type DAGSpec struct {
	Version    string               `json:"version"`
	Entrypoint string               `json:"entrypoint"`
	Templates  map[string]*Template `json:"templates"`
	OnExit     string               `json:"onExit,omitempty"`
}

type Template struct {
	DAG       *DAGTemplate       `json:"dag,omitempty"`
	Container *ContainerTemplate `json:"container,omitempty"`
	Inputs    *Inputs            `json:"inputs,omitempty"`
	Outputs   *Outputs           `json:"outputs,omitempty"`
	Retry     *RetryStrategy     `json:"retryStrategy,omitempty"`
}

type DAGTemplate struct {
	Tasks []DAGTask `json:"tasks"`
}

type DAGTask struct {
	Name         string            `json:"name"`
	Template     string            `json:"template"`
	Dependencies []string          `json:"dependencies,omitempty"`
	Arguments    *Arguments        `json:"arguments,omitempty"`
	When         string            `json:"when,omitempty"`
}

type ContainerTemplate struct {
	Image     string            `json:"image"`
	Command   []string          `json:"command,omitempty"`
	Args      []string          `json:"args,omitempty"`
	Env       []EnvVar          `json:"env,omitempty"`
	Resources *ResourceSpec     `json:"resources,omitempty"`
}

type ResourceSpec struct {
	Requests ResourceRequirements `json:"requests,omitempty"`
	Limits   ResourceRequirements `json:"limits,omitempty"`
}

type ResourceRequirements struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
	GPU    string `json:"nvidia.com/gpu,omitempty"`
}

type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Arguments struct {
	Parameters []Parameter `json:"parameters,omitempty"`
}

type Parameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Inputs struct {
	Parameters []InputParameter `json:"parameters,omitempty"`
}

type InputParameter struct {
	Name    string `json:"name"`
	Default string `json:"default,omitempty"`
}

type Outputs struct {
	Parameters []OutputParameter `json:"parameters,omitempty"`
}

type OutputParameter struct {
	Name      string    `json:"name"`
	ValueFrom ValueFrom `json:"valueFrom"`
}

type ValueFrom struct {
	Path string `json:"path"`
}

type RetryStrategy struct {
	Limit   int            `json:"limit"`
	Backoff *BackoffPolicy `json:"backoff,omitempty"`
}

type BackoffPolicy struct {
	Duration    string  `json:"duration"`
	Factor      float64 `json:"factor"`
	MaxDuration string  `json:"maxDuration"`
}

// Parser parses DAG specifications
type Parser struct{}

func NewParser() *Parser {
	return &Parser{}
}

// Parse parses a DAG spec from JSONB and returns task models
func (p *Parser) Parse(workflowID string, spec model.JSONB) ([]*model.Task, error) {
	// Convert JSONB to DAGSpec
	specBytes, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal spec: %w", err)
	}

	var dagSpec DAGSpec
	if err := json.Unmarshal(specBytes, &dagSpec); err != nil {
		return nil, fmt.Errorf("failed to parse DAG spec: %w", err)
	}

	// Validate
	if err := p.validate(&dagSpec); err != nil {
		return nil, err
	}

	// Get entrypoint template
	entryTemplate, ok := dagSpec.Templates[dagSpec.Entrypoint]
	if !ok {
		return nil, fmt.Errorf("entrypoint template '%s' not found", dagSpec.Entrypoint)
	}

	if entryTemplate.DAG == nil {
		return nil, fmt.Errorf("entrypoint template must be a DAG template")
	}

	// Convert DAG tasks to model.Task
	tasks := make([]*model.Task, 0, len(entryTemplate.DAG.Tasks))
	taskNameToID := make(map[string]string)

	for _, dagTask := range entryTemplate.DAG.Tasks {
		task, err := p.convertTask(workflowID, &dagTask, &dagSpec)
		if err != nil {
			return nil, fmt.Errorf("failed to convert task '%s': %w", dagTask.Name, err)
		}
		tasks = append(tasks, task)
		taskNameToID[dagTask.Name] = task.ID.String()
	}

	// Set up dependencies
	for i, dagTask := range entryTemplate.DAG.Tasks {
		for _, depName := range dagTask.Dependencies {
			depID, ok := taskNameToID[depName]
			if !ok {
				return nil, fmt.Errorf("dependency '%s' not found for task '%s'", depName, dagTask.Name)
			}
			tasks[i].Dependencies = append(tasks[i].Dependencies, model.TaskDependency{
				TaskID:      tasks[i].ID,
				DependsOnID: uuid.MustParse(depID),
				Type:        "success",
			})
		}
	}

	return tasks, nil
}

func (p *Parser) convertTask(workflowID string, dagTask *DAGTask, spec *DAGSpec) (*model.Task, error) {
	template, ok := spec.Templates[dagTask.Template]
	if !ok {
		return nil, fmt.Errorf("template '%s' not found", dagTask.Template)
	}

	if template.Container == nil {
		return nil, fmt.Errorf("template '%s' must have a container definition", dagTask.Template)
	}

	task := &model.Task{
		ID:           uuid.New(),
		WorkflowID:   uuid.MustParse(workflowID),
		Name:         dagTask.Name,
		TemplateName: dagTask.Template,
		Image:        template.Container.Image,
		Command:      template.Container.Command,
		Args:         p.substituteArgs(template.Container.Args, dagTask.Arguments),
		WhenCondition: dagTask.When,
		Status:       model.TaskPending,
	}

	// Parse resources
	if template.Container.Resources != nil {
		if template.Container.Resources.Requests.CPU != "" {
			task.CPURequest = parseCPU(template.Container.Resources.Requests.CPU)
		}
		if template.Container.Resources.Limits.CPU != "" {
			task.CPULimit = parseCPU(template.Container.Resources.Limits.CPU)
		}
		if template.Container.Resources.Requests.Memory != "" {
			task.MemoryRequest = parseMemory(template.Container.Resources.Requests.Memory)
		}
		if template.Container.Resources.Limits.Memory != "" {
			task.MemoryLimit = parseMemory(template.Container.Resources.Limits.Memory)
		}
	}

	// Parse retry strategy
	if template.Retry != nil {
		task.RetryLimit = template.Retry.Limit
	}

	// Convert env vars
	if len(template.Container.Env) > 0 {
		envJSON, _ := json.Marshal(template.Container.Env)
		task.Env = model.JSONB{}
		json.Unmarshal(envJSON, &task.Env)
	}

	return task, nil
}

func (p *Parser) substituteArgs(args []string, arguments *Arguments) []string {
	if arguments == nil {
		return args
	}

	paramMap := make(map[string]string)
	for _, param := range arguments.Parameters {
		paramMap[param.Name] = param.Value
	}

	result := make([]string, len(args))
	for i, arg := range args {
		result[i] = substituteParameters(arg, paramMap)
	}
	return result
}

func (p *Parser) validate(spec *DAGSpec) error {
	if spec.Entrypoint == "" {
		return fmt.Errorf("entrypoint is required")
	}
	if len(spec.Templates) == 0 {
		return fmt.Errorf("at least one template is required")
	}
	// Check for cycles
	return p.detectCycles(spec)
}

func (p *Parser) detectCycles(spec *DAGSpec) error {
	// Build adjacency list
	graph := make(map[string][]string)
	template := spec.Templates[spec.Entrypoint]
	if template.DAG == nil {
		return nil
	}

	for _, task := range template.DAG.Tasks {
		graph[task.Name] = task.Dependencies
	}

	// DFS-based cycle detection
	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var hasCycle func(node string) bool
	hasCycle = func(node string) bool {
		visited[node] = true
		recStack[node] = true

		for _, dep := range graph[node] {
			if !visited[dep] {
				if hasCycle(dep) {
					return true
				}
			} else if recStack[dep] {
				return true
			}
		}

		recStack[node] = false
		return false
	}

	for node := range graph {
		if !visited[node] {
			if hasCycle(node) {
				return fmt.Errorf("cycle detected in DAG")
			}
		}
	}

	return nil
}

// Helper functions
func parseCPU(cpu string) int {
	// Parse CPU string (e.g., "100m", "1", "0.5") to millicores
	if strings.HasSuffix(cpu, "m") {
		v, _ := strconv.Atoi(strings.TrimSuffix(cpu, "m"))
		return v
	}
	v, _ := strconv.ParseFloat(cpu, 64)
	return int(v * 1000)
}

func parseMemory(mem string) int {
	// Parse memory string (e.g., "256Mi", "1Gi") to MiB
	mem = strings.TrimSpace(mem)
	var multiplier int = 1

	switch {
	case strings.HasSuffix(mem, "Gi"):
		multiplier = 1024
		mem = strings.TrimSuffix(mem, "Gi")
	case strings.HasSuffix(mem, "Mi"):
		multiplier = 1
		mem = strings.TrimSuffix(mem, "Mi")
	case strings.HasSuffix(mem, "Ki"):
		multiplier = 1
		mem = strings.TrimSuffix(mem, "Ki")
		// Will be divided by 1024 below
	}

	v, _ := strconv.Atoi(mem)
	if strings.HasSuffix(mem, "Ki") {
		return v / 1024
	}
	return v * multiplier
}

func substituteParameters(s string, params map[string]string) string {
	for k, v := range params {
		s = strings.ReplaceAll(s, "{{inputs.parameters."+k+"}}", v)
	}
	return s
}
```

### 3.2 Workflow Controller

```go
// pkg/controller/workflow_controller.go

package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/eventbus"
	"github.com/flowforge/flowforge/pkg/model"
	"github.com/flowforge/flowforge/pkg/queue"
	"github.com/flowforge/flowforge/pkg/scheduler/dag"
	"github.com/flowforge/flowforge/pkg/store/postgres"
)

type WorkflowController struct {
	workflowRepo *postgres.WorkflowRepository
	taskRepo     *postgres.TaskRepository
	dagParser    *dag.Parser
	taskQueue    *queue.TaskQueue
	eventBus     *eventbus.EventBus
	logger       *zap.Logger

	// Active workflow tracking
	mu              sync.RWMutex
	activeWorkflows map[string]*workflowState
}

type workflowState struct {
	workflow *model.Workflow
	tasks    map[string]*model.Task
	pending  int
	running  int
	finished int
}

func NewWorkflowController(
	workflowRepo *postgres.WorkflowRepository,
	taskRepo *postgres.TaskRepository,
	taskQueue *queue.TaskQueue,
	eventBus *eventbus.EventBus,
	logger *zap.Logger,
) *WorkflowController {
	return &WorkflowController{
		workflowRepo:    workflowRepo,
		taskRepo:        taskRepo,
		dagParser:       dag.NewParser(),
		taskQueue:       taskQueue,
		eventBus:        eventBus,
		logger:          logger,
		activeWorkflows: make(map[string]*workflowState),
	}
}

// Start starts the workflow controller
func (c *WorkflowController) Start(ctx context.Context) error {
	// Subscribe to task events
	taskEvents := c.eventBus.Subscribe(ctx, eventbus.ChannelTask)

	go c.handleTaskEvents(ctx, taskEvents)
	go c.reconcileLoop(ctx)

	c.logger.Info("Workflow controller started")
	return nil
}

// SubmitWorkflow creates and starts a new workflow
func (c *WorkflowController) SubmitWorkflow(ctx context.Context, workflow *model.Workflow) error {
	// Parse DAG and create tasks
	tasks, err := c.dagParser.Parse(workflow.ID.String(), workflow.DAGSpec)
	if err != nil {
		return fmt.Errorf("failed to parse DAG: %w", err)
	}

	// Save workflow to database
	if err := c.workflowRepo.Create(ctx, workflow); err != nil {
		return fmt.Errorf("failed to create workflow: %w", err)
	}

	// Save tasks to database
	if err := c.taskRepo.CreateBatch(ctx, tasks); err != nil {
		return fmt.Errorf("failed to create tasks: %w", err)
	}

	// Initialize workflow state
	c.initWorkflowState(workflow, tasks)

	// Start workflow
	if err := c.startWorkflow(ctx, workflow.ID.String()); err != nil {
		return fmt.Errorf("failed to start workflow: %w", err)
	}

	return nil
}

func (c *WorkflowController) initWorkflowState(workflow *model.Workflow, tasks []*model.Task) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := &workflowState{
		workflow: workflow,
		tasks:    make(map[string]*model.Task),
		pending:  len(tasks),
	}

	for _, task := range tasks {
		state.tasks[task.ID.String()] = task
	}

	c.activeWorkflows[workflow.ID.String()] = state
}

func (c *WorkflowController) startWorkflow(ctx context.Context, workflowID string) error {
	// Update workflow status
	if err := c.workflowRepo.UpdateStatus(ctx, workflowID, model.WorkflowRunning, ""); err != nil {
		return err
	}

	// Schedule initial tasks (those with no dependencies)
	return c.scheduleReadyTasks(ctx, workflowID)
}

func (c *WorkflowController) scheduleReadyTasks(ctx context.Context, workflowID string) error {
	c.mu.RLock()
	state, ok := c.activeWorkflows[workflowID]
	c.mu.RUnlock()

	if !ok {
		return fmt.Errorf("workflow %s not found", workflowID)
	}

	for _, task := range state.tasks {
		if task.Status != model.TaskPending {
			continue
		}

		// Check if all dependencies are satisfied
		if c.allDependenciesSatisfied(task, state) {
			// Evaluate when condition
			if task.WhenCondition != "" && !c.evaluateCondition(task.WhenCondition, state) {
				// Skip task
				c.skipTask(ctx, task)
				continue
			}

			// Queue task
			if err := c.queueTask(ctx, task); err != nil {
				c.logger.Error("Failed to queue task",
					zap.String("task_id", task.ID.String()),
					zap.Error(err))
			}
		}
	}

	return nil
}

func (c *WorkflowController) allDependenciesSatisfied(task *model.Task, state *workflowState) bool {
	for _, dep := range task.Dependencies {
		depTask, ok := state.tasks[dep.DependsOnID.String()]
		if !ok {
			return false
		}

		switch dep.Type {
		case "success":
			if depTask.Status != model.TaskSucceeded {
				return false
			}
		case "completion":
			if depTask.Status != model.TaskSucceeded && depTask.Status != model.TaskFailed {
				return false
			}
		case "failure":
			if depTask.Status != model.TaskFailed {
				return false
			}
		}
	}
	return true
}

func (c *WorkflowController) queueTask(ctx context.Context, task *model.Task) error {
	// Update task status to QUEUED
	now := time.Now()
	updates := map[string]interface{}{
		"queued_at": &now,
	}
	if err := c.taskRepo.UpdateStatus(ctx, task.ID.String(), model.TaskQueued, updates); err != nil {
		return err
	}

	// Get project info for tenant ID
	workflow, err := c.workflowRepo.GetByID(ctx, task.WorkflowID.String())
	if err != nil {
		return err
	}

	// Enqueue to Redis
	queuedTask := &queue.QueuedTask{
		TaskID:     task.ID.String(),
		WorkflowID: task.WorkflowID.String(),
		TenantID:   workflow.Project.Group.TenantID.String(),
		ProjectID:  workflow.ProjectID.String(),
		Priority:   workflow.Priority,
		Resources:  task.GetResourceRequest(),
		EnqueuedAt: time.Now(),
	}

	return c.taskQueue.Enqueue(ctx, queuedTask)
}

func (c *WorkflowController) skipTask(ctx context.Context, task *model.Task) error {
	now := time.Now()
	updates := map[string]interface{}{
		"finished_at": &now,
	}
	if err := c.taskRepo.UpdateStatus(ctx, task.ID.String(), model.TaskSkipped, updates); err != nil {
		return err
	}

	// Update local state
	c.mu.Lock()
	if state, ok := c.activeWorkflows[task.WorkflowID.String()]; ok {
		state.tasks[task.ID.String()].Status = model.TaskSkipped
		state.pending--
		state.finished++
	}
	c.mu.Unlock()

	return nil
}

func (c *WorkflowController) handleTaskEvents(ctx context.Context, events <-chan *eventbus.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-events:
			c.processTaskEvent(ctx, event)
		}
	}
}

func (c *WorkflowController) processTaskEvent(ctx context.Context, event *eventbus.Event) {
	var taskEvent eventbus.TaskEvent
	if err := json.Unmarshal(event.Data, &taskEvent); err != nil {
		c.logger.Error("Failed to unmarshal task event", zap.Error(err))
		return
	}

	c.mu.Lock()
	state, ok := c.activeWorkflows[taskEvent.WorkflowID]
	if !ok {
		c.mu.Unlock()
		return
	}

	task, ok := state.tasks[taskEvent.TaskID]
	if !ok {
		c.mu.Unlock()
		return
	}

	// Update task state
	oldStatus := task.Status
	task.Status = model.TaskStatus(taskEvent.Status)
	state.tasks[taskEvent.TaskID] = task

	// Update counters
	if oldStatus == model.TaskRunning && task.Status != model.TaskRunning {
		state.running--
	}
	if task.Status == model.TaskRunning && oldStatus != model.TaskRunning {
		state.running++
	}
	if task.Status == model.TaskSucceeded || task.Status == model.TaskFailed || task.Status == model.TaskSkipped {
		if oldStatus == model.TaskPending || oldStatus == model.TaskQueued {
			state.pending--
		}
		state.finished++
	}

	c.mu.Unlock()

	// Check if we need to schedule more tasks or complete workflow
	if task.Status == model.TaskSucceeded || task.Status == model.TaskSkipped {
		c.scheduleReadyTasks(ctx, taskEvent.WorkflowID)
	}

	c.checkWorkflowCompletion(ctx, taskEvent.WorkflowID)
}

func (c *WorkflowController) checkWorkflowCompletion(ctx context.Context, workflowID string) {
	c.mu.RLock()
	state, ok := c.activeWorkflows[workflowID]
	if !ok {
		c.mu.RUnlock()
		return
	}

	allFinished := state.pending == 0 && state.running == 0
	hasFailed := false
	for _, task := range state.tasks {
		if task.Status == model.TaskFailed {
			hasFailed = true
			break
		}
	}
	c.mu.RUnlock()

	if !allFinished {
		return
	}

	// Workflow is complete
	var status model.WorkflowStatus
	var errorMsg string

	if hasFailed {
		status = model.WorkflowFailed
		errorMsg = "One or more tasks failed"
	} else {
		status = model.WorkflowSucceeded
	}

	if err := c.workflowRepo.UpdateStatus(ctx, workflowID, status, errorMsg); err != nil {
		c.logger.Error("Failed to update workflow status",
			zap.String("workflow_id", workflowID),
			zap.Error(err))
		return
	}

	// Clean up local state
	c.mu.Lock()
	delete(c.activeWorkflows, workflowID)
	c.mu.Unlock()

	c.logger.Info("Workflow completed",
		zap.String("workflow_id", workflowID),
		zap.String("status", string(status)))
}

func (c *WorkflowController) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.reconcile(ctx)
		}
	}
}

func (c *WorkflowController) reconcile(ctx context.Context) {
	// Reload active workflows from database
	// This handles controller restarts
	// Implementation depends on specific requirements
}

func (c *WorkflowController) evaluateCondition(condition string, state *workflowState) bool {
	// Simple condition evaluation
	// In production, use a proper expression evaluator
	return true
}
```

---

## 4. Phase 3: Scheduler & Queue

The scheduler and queue implementations are already covered in detail in the ARCHITECTURE.md document (sections 4 and 5). Key files to implement:

- `pkg/queue/task_queue.go` - Redis-based task queue
- `pkg/eventbus/eventbus.go` - Redis Pub/Sub event bus
- `pkg/scheduler/scheduler.go` - Main scheduler service
- `pkg/scheduler/fair_scheduler.go` - Fair scheduling algorithm

---

## 5. Phase 4: Pod Executor

```go
// pkg/executor/pod_executor.go

package executor

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/flowforge/flowforge/pkg/model"
)

type PodExecutor struct {
	k8sClient   kubernetes.Interface
	sdkInjector *SDKInjector
	namespace   string
}

func NewPodExecutor(k8sClient kubernetes.Interface, sdkImage string) *PodExecutor {
	return &PodExecutor{
		k8sClient:   k8sClient,
		sdkInjector: NewSDKInjector(sdkImage),
	}
}

// ExecuteTask creates a pod for the given task
func (e *PodExecutor) ExecuteTask(ctx context.Context, task *model.Task, tenantID, projectID, namespace string) (*corev1.Pod, error) {
	pod := e.buildPod(task, tenantID, projectID, namespace)

	created, err := e.k8sClient.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			// Pod already exists, get it
			return e.k8sClient.CoreV1().Pods(namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		}
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}

	return created, nil
}

func (e *PodExecutor) buildPod(task *model.Task, tenantID, projectID, namespace string) *corev1.Pod {
	podName := fmt.Sprintf("ff-%s-%s", task.WorkflowID.String()[:8], task.Name)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "flowforge",
				"flowforge.io/workflow-id":     task.WorkflowID.String(),
				"flowforge.io/task-id":         task.ID.String(),
				"flowforge.io/task-name":       task.Name,
				"flowforge.io/tenant-id":       tenantID,
				"flowforge.io/project-id":      projectID,
				"flowforge.io/workload":        "true",
			},
			Annotations: map[string]string{
				"flowforge.io/created-at": time.Now().Format(time.RFC3339),
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "main",
					Image:   task.Image,
					Command: task.Command,
					Args:    task.Args,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%dm", task.CPURequest)),
							corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dMi", task.MemoryRequest)),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%dm", task.CPULimit)),
							corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dMi", task.MemoryLimit)),
						},
					},
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
			Volumes: []corev1.Volume{
				e.sdkInjector.GetVolume(),
			},
		},
	}

	// Add SDK environment variables
	pod.Spec.Containers[0].Env = append(
		pod.Spec.Containers[0].Env,
		e.sdkInjector.GetEnvVars(task, tenantID, projectID)...,
	)

	// Add GPU if requested
	if task.GPURequest > 0 {
		pod.Spec.Containers[0].Resources.Limits["nvidia.com/gpu"] =
			resource.MustParse(fmt.Sprintf("%d", task.GPURequest))
	}

	return pod
}

// CancelTask deletes the pod for a task
func (e *PodExecutor) CancelTask(ctx context.Context, task *model.Task, namespace string) error {
	podName := fmt.Sprintf("ff-%s-%s", task.WorkflowID.String()[:8], task.Name)
	return e.k8sClient.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{})
}

// GetTaskPod retrieves the pod for a task
func (e *PodExecutor) GetTaskPod(ctx context.Context, task *model.Task, namespace string) (*corev1.Pod, error) {
	podName := fmt.Sprintf("ff-%s-%s", task.WorkflowID.String()[:8], task.Name)
	return e.k8sClient.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
}
```

```go
// pkg/executor/sdk_injector.go

package executor

import (
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"

	"github.com/flowforge/flowforge/pkg/model"
)

type SDKInjector struct {
	sdkImage string
}

func NewSDKInjector(sdkImage string) *SDKInjector {
	return &SDKInjector{sdkImage: sdkImage}
}

func (i *SDKInjector) GetInitContainer() corev1.Container {
	return corev1.Container{
		Name:    "flowforge-sdk-init",
		Image:   i.sdkImage,
		Command: []string{"/bin/sh", "-c"},
		Args:    []string{"cp -r /sdk/* /flowforge/"},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "flowforge-sdk",
				MountPath: "/flowforge",
			},
		},
	}
}

func (i *SDKInjector) GetVolume() corev1.Volume {
	return corev1.Volume{
		Name: "flowforge-sdk",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
}

func (i *SDKInjector) GetEnvVars(task *model.Task, tenantID, projectID string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "FLOWFORGE_TASK_ID", Value: task.ID.String()},
		{Name: "FLOWFORGE_WORKFLOW_ID", Value: task.WorkflowID.String()},
		{Name: "FLOWFORGE_TENANT_ID", Value: tenantID},
		{Name: "FLOWFORGE_PROJECT_ID", Value: projectID},
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
					Key: task.ID.String(),
				},
			},
		},
		{Name: "PYTHONPATH", Value: "/flowforge/python:$PYTHONPATH"},
	}
}
```

---

## 6. Phase 5: Quota Management

Quota management implementation is covered in the ARCHITECTURE.md (section 5). Key files:

- `pkg/quota/manager.go` - Main quota manager
- `pkg/quota/hierarchy.go` - Hierarchical quota traversal
- `pkg/quota/admission.go` - Admission control

---

## 7. Phase 6: Python SDK

The Python SDK implementation is covered in detail in ARCHITECTURE.md (section 6). Key files:

```
sdk/python/flowforge/
├── __init__.py
├── client.py
├── types.py
├── proto/
│   ├── flowforge_pb2.py
│   └── flowforge_pb2_grpc.py
└── setup.py
```

Build and publish:

```bash
cd sdk/python

# Build
python -m build

# Publish to PyPI (or private registry)
python -m twine upload dist/*
```

---

## 8. Phase 7: Observability

### 8.1 Log Aggregator Service

Implementation covered in ARCHITECTURE.md (section 7).

### 8.2 Metrics Exporter

```go
// pkg/metrics/prometheus.go

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	WorkflowsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flowforge_workflows_total",
			Help: "Total number of workflows by status",
		},
		[]string{"tenant_id", "project_id", "status"},
	)

	TasksTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flowforge_tasks_total",
			Help: "Total number of tasks by status",
		},
		[]string{"tenant_id", "workflow_id", "status"},
	)

	TaskDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flowforge_task_duration_seconds",
			Help:    "Task execution duration in seconds",
			Buckets: prometheus.ExponentialBuckets(1, 2, 15), // 1s to ~9h
		},
		[]string{"tenant_id", "template"},
	)

	QueueDepth = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flowforge_queue_depth",
			Help: "Number of tasks in queue by priority",
		},
		[]string{"tenant_id", "priority"},
	)

	QuotaUsage = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flowforge_quota_usage",
			Help: "Current quota usage",
		},
		[]string{"tenant_id", "entity_type", "resource"},
	)

	RetryCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flowforge_task_retries_total",
			Help: "Total number of task retries",
		},
		[]string{"tenant_id", "template"},
	)
)
```

---

## 9. Phase 8: API & Authentication

### 9.1 API Server

```go
// cmd/api-server/main.go

package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/apiserver"
	"github.com/flowforge/flowforge/pkg/config"
	"github.com/flowforge/flowforge/pkg/store/postgres"
	redisclient "github.com/flowforge/flowforge/pkg/store/redis"
)

func main() {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	// Setup logger
	logger, _ := zap.NewProduction()
	defer logger.Sync()

	// Initialize stores
	db, err := postgres.NewStore(&cfg.Database)
	if err != nil {
		logger.Fatal("Failed to connect to database", zap.Error(err))
	}
	defer db.Close()

	redis, err := redisclient.NewClient(&cfg.Redis)
	if err != nil {
		logger.Fatal("Failed to connect to Redis", zap.Error(err))
	}
	defer redis.Close()

	// Create API server
	server := apiserver.NewServer(db, redis, cfg, logger)

	// Setup HTTP server
	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.HTTPPort),
		Handler:      server.Router(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.ReadTimeout * 2,
	}

	// Start server
	go func() {
		logger.Info("Starting API server", zap.Int("port", cfg.Server.HTTPPort))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Server error", zap.Error(err))
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", zap.Error(err))
	}
}
```

```go
// pkg/apiserver/server.go

package apiserver

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/apiserver/handlers"
	"github.com/flowforge/flowforge/pkg/apiserver/middleware"
	"github.com/flowforge/flowforge/pkg/config"
	"github.com/flowforge/flowforge/pkg/store/postgres"
	redisclient "github.com/flowforge/flowforge/pkg/store/redis"
)

type Server struct {
	router *gin.Engine
	db     *postgres.Store
	redis  *redisclient.Client
	cfg    *config.Config
	logger *zap.Logger
}

func NewServer(db *postgres.Store, redis *redisclient.Client, cfg *config.Config, logger *zap.Logger) *Server {
	s := &Server{
		db:     db,
		redis:  redis,
		cfg:    cfg,
		logger: logger,
	}
	s.setupRouter()
	return s
}

func (s *Server) setupRouter() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// Middleware
	r.Use(gin.Recovery())
	r.Use(middleware.Logger(s.logger))
	r.Use(middleware.RequestID())
	r.Use(middleware.CORS())

	// Health check
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// API routes
	api := r.Group("/api/v1")
	{
		// Auth middleware
		api.Use(middleware.Auth(s.cfg.Auth))

		// Workflow handlers
		workflowHandler := handlers.NewWorkflowHandler(s.db, s.redis, s.logger)
		api.POST("/workflows", workflowHandler.Create)
		api.GET("/workflows", workflowHandler.List)
		api.GET("/workflows/:id", workflowHandler.Get)
		api.DELETE("/workflows/:id", workflowHandler.Cancel)
		api.GET("/workflows/:id/tasks", workflowHandler.ListTasks)
		api.GET("/workflows/:id/logs", workflowHandler.StreamLogs)

		// Quota handlers
		quotaHandler := handlers.NewQuotaHandler(s.db, s.logger)
		api.GET("/tenants/:id/quota", quotaHandler.GetUsage)
		api.PUT("/tenants/:id/quota", quotaHandler.Update)

		// Project handlers
		projectHandler := handlers.NewProjectHandler(s.db, s.logger)
		api.GET("/projects", projectHandler.List)
		api.POST("/projects", projectHandler.Create)
		api.GET("/projects/:id", projectHandler.Get)
	}

	s.router = r
}

func (s *Server) Router() *gin.Engine {
	return s.router
}
```

---

## 10. Testing Strategy

### 10.1 Unit Tests

```go
// pkg/scheduler/dag/parser_test.go

package dag

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/flowforge/flowforge/pkg/model"
)

func TestParser_Parse(t *testing.T) {
	tests := []struct {
		name      string
		spec      model.JSONB
		wantTasks int
		wantErr   bool
	}{
		{
			name: "simple dag",
			spec: model.JSONB{
				"version":    "1.0",
				"entrypoint": "main",
				"templates": map[string]interface{}{
					"main": map[string]interface{}{
						"dag": map[string]interface{}{
							"tasks": []interface{}{
								map[string]interface{}{
									"name":     "task1",
									"template": "python",
								},
								map[string]interface{}{
									"name":         "task2",
									"template":     "python",
									"dependencies": []string{"task1"},
								},
							},
						},
					},
					"python": map[string]interface{}{
						"container": map[string]interface{}{
							"image": "python:3.11",
						},
					},
				},
			},
			wantTasks: 2,
			wantErr:   false,
		},
		{
			name: "cyclic dependency",
			spec: model.JSONB{
				"version":    "1.0",
				"entrypoint": "main",
				"templates": map[string]interface{}{
					"main": map[string]interface{}{
						"dag": map[string]interface{}{
							"tasks": []interface{}{
								map[string]interface{}{
									"name":         "task1",
									"template":     "python",
									"dependencies": []string{"task2"},
								},
								map[string]interface{}{
									"name":         "task2",
									"template":     "python",
									"dependencies": []string{"task1"},
								},
							},
						},
					},
					"python": map[string]interface{}{
						"container": map[string]interface{}{
							"image": "python:3.11",
						},
					},
				},
			},
			wantTasks: 0,
			wantErr:   true,
		},
	}

	parser := NewParser()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tasks, err := parser.Parse("workflow-123", tt.spec)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Len(t, tasks, tt.wantTasks)
		})
	}
}
```

### 10.2 Integration Tests

```go
// pkg/controller/workflow_controller_integration_test.go
// +build integration

package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/flowforge/flowforge/internal/testutil"
	"github.com/flowforge/flowforge/pkg/model"
)

type WorkflowControllerSuite struct {
	suite.Suite
	ctx        context.Context
	cancel     context.CancelFunc
	testEnv    *testutil.TestEnvironment
	controller *WorkflowController
}

func (s *WorkflowControllerSuite) SetupSuite() {
	s.ctx, s.cancel = context.WithTimeout(context.Background(), 5*time.Minute)
	s.testEnv = testutil.NewTestEnvironment(s.T())
	s.controller = NewWorkflowController(
		s.testEnv.WorkflowRepo,
		s.testEnv.TaskRepo,
		s.testEnv.TaskQueue,
		s.testEnv.EventBus,
		s.testEnv.Logger,
	)
}

func (s *WorkflowControllerSuite) TearDownSuite() {
	s.cancel()
	s.testEnv.Cleanup()
}

func (s *WorkflowControllerSuite) TestSubmitWorkflow() {
	workflow := &model.Workflow{
		ProjectID: s.testEnv.TestProject.ID,
		Name:      "test-workflow",
		DAGSpec: model.JSONB{
			"version":    "1.0",
			"entrypoint": "main",
			"templates": map[string]interface{}{
				"main": map[string]interface{}{
					"dag": map[string]interface{}{
						"tasks": []interface{}{
							map[string]interface{}{
								"name":     "task1",
								"template": "echo",
							},
						},
					},
				},
				"echo": map[string]interface{}{
					"container": map[string]interface{}{
						"image":   "alpine:latest",
						"command": []string{"echo", "hello"},
					},
				},
			},
		},
	}

	err := s.controller.SubmitWorkflow(s.ctx, workflow)
	require.NoError(s.T(), err)

	// Verify workflow was created
	saved, err := s.testEnv.WorkflowRepo.GetByID(s.ctx, workflow.ID.String())
	require.NoError(s.T(), err)
	require.Equal(s.T(), model.WorkflowRunning, saved.Status)

	// Verify task was created
	require.Len(s.T(), saved.Tasks, 1)
	require.Equal(s.T(), model.TaskQueued, saved.Tasks[0].Status)
}

func TestWorkflowControllerSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}
	suite.Run(t, new(WorkflowControllerSuite))
}
```

### 10.3 End-to-End Tests

```go
// e2e/workflow_e2e_test.go
// +build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWorkflowE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Submit workflow via API
	client := NewAPIClient(t)

	workflow, err := client.SubmitWorkflow(ctx, &SubmitWorkflowRequest{
		ProjectID: testProjectID,
		Name:      "e2e-test-workflow",
		DAGSpec:   testDAGSpec,
	})
	require.NoError(t, err)

	// Wait for workflow to complete
	var finalStatus string
	for i := 0; i < 60; i++ {
		status, err := client.GetWorkflowStatus(ctx, workflow.ID)
		require.NoError(t, err)

		if status == "SUCCEEDED" || status == "FAILED" {
			finalStatus = status
			break
		}

		time.Sleep(5 * time.Second)
	}

	require.Equal(t, "SUCCEEDED", finalStatus)

	// Verify logs were captured
	logs, err := client.GetWorkflowLogs(ctx, workflow.ID)
	require.NoError(t, err)
	require.NotEmpty(t, logs)
}
```

---

## Summary

This implementation guide provides a comprehensive roadmap for building FlowForge. The key phases are:

1. **Core Infrastructure** - Configuration, models, database stores
2. **Workflow Controller** - DAG parsing, state machine, reconciliation
3. **Scheduler & Queue** - Redis-based queuing, fair scheduling
4. **Pod Executor** - Kubernetes pod creation, SDK injection
5. **Quota Management** - Hierarchical quotas, admission control
6. **Python SDK** - Client library for task pods
7. **Observability** - Log aggregation, metrics
8. **API & Auth** - REST API, JWT authentication

Each phase builds on the previous ones, allowing incremental development and testing. Start with Phase 1-2 for a minimal viable product, then add features progressively.

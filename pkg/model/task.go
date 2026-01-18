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
	ID            uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	WorkflowID    uuid.UUID `gorm:"type:uuid;not null;index"`
	Workflow      *Workflow `gorm:"foreignKey:WorkflowID"`
	Name          string    `gorm:"not null;uniqueIndex:idx_workflow_task_name"`
	TemplateName  string
	Image         string         `gorm:"not null"`
	Command       pq.StringArray `gorm:"type:text[]"`
	Args          pq.StringArray `gorm:"type:text[]"`
	Env           JSONB          `gorm:"type:jsonb;default:'[]'"`
	CPURequest    int            `gorm:"default:100"`
	CPULimit      int            `gorm:"default:1000"`
	MemoryRequest int            `gorm:"default:256"`
	MemoryLimit   int            `gorm:"default:1024"`
	GPURequest    int            `gorm:"default:0"`
	Status        TaskStatus     `gorm:"type:varchar(50);default:'PENDING';index"`
	ErrorMessage  string
	ExitCode      *int
	RetryLimit    int        `gorm:"default:3"`
	RetryCount    int        `gorm:"default:0"`
	BackoffSecs   int        `gorm:"default:0"`
	NextRetryAt   *time.Time `gorm:"index"`
	QueuedAt      *time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	PodName       string
	PodUID        string
	NodeName      string
	Outputs       JSONB `gorm:"type:jsonb;default:'{}'"`
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
	Type        string    `gorm:"default:'success'"`
}

type TaskEvent struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	TaskID    uuid.UUID `gorm:"type:uuid;not null;index"`
	EventType string    `gorm:"not null"`
	Message   string
	Details   JSONB     `gorm:"type:jsonb"`
	CreatedAt time.Time `gorm:"index"`
}

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

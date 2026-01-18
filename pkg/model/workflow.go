package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
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
	ID           uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	ProjectID    uuid.UUID `gorm:"type:uuid;not null;index"`
	Project      *Project  `gorm:"foreignKey:ProjectID"`
	Name         string    `gorm:"not null"`
	Description  string
	DAGSpec      JSONB          `gorm:"type:jsonb;not null"`
	Parameters   JSONB          `gorm:"type:jsonb;default:'{}'"`
	Status       WorkflowStatus `gorm:"type:varchar(50);default:'PENDING';index"`
	ErrorMessage string
	StartedAt    *time.Time
	FinishedAt   *time.Time
	SubmittedBy  string
	Priority     int    `gorm:"default:0"`
	Labels       JSONB  `gorm:"type:jsonb;default:'{}'"`
	Tasks        []Task `gorm:"foreignKey:WorkflowID"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    gorm.DeletedAt `gorm:"index"`
}

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

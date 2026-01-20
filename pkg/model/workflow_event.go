package model

import (
	"time"

	"github.com/google/uuid"
)

const (
	OutboxStatusPending   = "pending"
	OutboxStatusPublished = "published"
	OutboxStatusFailed    = "failed"
)

type WorkflowEvent struct {
	EventID     uuid.UUID `gorm:"type:uuid;primary_key;default:gen_random_uuid()"`
	EventType   string    `gorm:"not null"`
	Payload     JSONB     `gorm:"type:jsonb;not null"`
	Status      string    `gorm:"not null;default:'pending'"`
	CreatedAt   time.Time `gorm:"autoCreateTime;not null"`
	PublishedAt *time.Time
}

func (WorkflowEvent) TableName() string {
	return "workflow_events"
}

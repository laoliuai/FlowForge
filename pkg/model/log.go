package model

import (
	"time"

	"github.com/google/uuid"
)

type LogEntry struct {
	ID         uint64    `gorm:"primaryKey;autoIncrement"`
	WorkflowID uuid.UUID `gorm:"type:uuid;not null;index:idx_logs_workflow_time"`
	TaskID     uuid.UUID `gorm:"type:uuid;not null;index:idx_logs_task_time"`
	Timestamp  int64     `gorm:"not null;index:idx_logs_task_time"`
	Level      string    `gorm:"type:varchar(10);default:'INFO'"`
	Message    string    `gorm:"type:text"`
	LineNum    int32     `gorm:"default:0"`
	CreatedAt  time.Time `gorm:"autoCreateTime"`
}

func (LogEntry) TableName() string {
	return "task_logs"
}

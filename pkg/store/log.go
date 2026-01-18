package store

import (
	"context"
	"time"

	"github.com/flowforge/flowforge/pkg/model"
)

// LogStore defines the interface for log storage backends (PostgreSQL, ClickHouse)
type LogStore interface {
	// CreateBatch inserts a batch of logs efficiently
	CreateBatch(ctx context.Context, logs []*model.LogEntry) error

	// List retrieves logs for a specific task with pagination
	List(ctx context.Context, taskID string, sinceTime *time.Time, limit int) ([]model.LogEntry, error)

	// DeleteOldLogs deletes logs older than the specified retention period (if backend requires it)
	DeleteOldLogs(ctx context.Context, retentionDays int) error

	// Close closes the connection to the storage backend
	Close() error
}

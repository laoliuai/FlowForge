package postgres

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/flowforge/flowforge/pkg/model"
)

type LogRepository struct {
	db *gorm.DB
}

func NewLogRepository(db *gorm.DB) *LogRepository {
	return &LogRepository{db: db}
}

func (r *LogRepository) CreateBatch(ctx context.Context, logs []*model.LogEntry) error {
	if len(logs) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).CreateInBatches(logs, 100).Error
}

func (r *LogRepository) List(ctx context.Context, taskID string, sinceTime *time.Time, limit int) ([]model.LogEntry, error) {
	var logs []model.LogEntry
	query := r.db.WithContext(ctx).
		Where("task_id = ?", taskID).
		Order("timestamp ASC, line_num ASC")

	if sinceTime != nil {
		query = query.Where("created_at > ?", sinceTime)
	}

	if limit > 0 {
		query = query.Limit(limit)
	}

	err := query.Find(&logs).Error
	return logs, err
}

func (r *LogRepository) DeleteOldLogs(ctx context.Context, retentionDays int) error {
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	return r.db.WithContext(ctx).
		Where("created_at < ?", cutoff).
		Delete(&model.LogEntry{}).Error
}

func (r *LogRepository) Close() error {
	// GORM manages connection pooling, so typically we don't close it explicitly here
	// unless we want to close the underlying sql.DB
	return nil
}

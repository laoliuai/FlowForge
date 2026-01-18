package postgres

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/flowforge/flowforge/pkg/model"
	"github.com/flowforge/flowforge/pkg/store"
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

func (r *LogRepository) Query(ctx context.Context, query store.LogQuery) ([]model.LogEntry, error) {
	if query.WorkflowID == "" {
		return nil, gorm.ErrInvalidValue
	}

	var logs []model.LogEntry
	dbQuery := r.db.WithContext(ctx).
		Where("workflow_id = ?", query.WorkflowID).
		Order("timestamp ASC, line_num ASC")

	if query.TaskID != "" {
		dbQuery = dbQuery.Where("task_id = ?", query.TaskID)
	}

	if query.StartTime != nil {
		dbQuery = dbQuery.Where("timestamp >= ?", *query.StartTime)
	}

	if query.EndTime != nil {
		dbQuery = dbQuery.Where("timestamp <= ?", *query.EndTime)
	}

	if query.Level != "" {
		dbQuery = dbQuery.Where("level = ?", query.Level)
	}

	if query.Search != "" {
		dbQuery = dbQuery.Where("message ILIKE ?", "%"+query.Search+"%")
	}

	if query.Limit > 0 {
		dbQuery = dbQuery.Limit(query.Limit)
	}

	err := dbQuery.Find(&logs).Error
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

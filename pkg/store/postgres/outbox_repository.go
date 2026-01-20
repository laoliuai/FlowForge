package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/flowforge/flowforge/pkg/model"
)

type OutboxRepository struct {
	db *gorm.DB
}

func NewOutboxRepository(db *gorm.DB) *OutboxRepository {
	return &OutboxRepository{db: db}
}

func (r *OutboxRepository) ListPending(ctx context.Context, limit int) ([]model.WorkflowEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	var events []model.WorkflowEvent
	err := r.db.WithContext(ctx).
		Where("status = ?", model.OutboxStatusPending).
		Order("created_at ASC").
		Limit(limit).
		Find(&events).Error
	return events, err
}

func (r *OutboxRepository) MarkPublished(ctx context.Context, eventID uuid.UUID, publishedAt time.Time) error {
	updates := map[string]interface{}{
		"status":       model.OutboxStatusPublished,
		"published_at": publishedAt,
	}
	return r.db.WithContext(ctx).
		Model(&model.WorkflowEvent{}).
		Where("event_id = ?", eventID).
		Updates(updates).Error
}

func (r *OutboxRepository) MarkFailed(ctx context.Context, eventID uuid.UUID) error {
	updates := map[string]interface{}{
		"status": model.OutboxStatusFailed,
	}
	return r.db.WithContext(ctx).
		Model(&model.WorkflowEvent{}).
		Where("event_id = ?", eventID).
		Updates(updates).Error
}

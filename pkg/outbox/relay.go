package outbox

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/model"
)

type Repository interface {
	ListPending(ctx context.Context, limit int) ([]model.WorkflowEvent, error)
	MarkPublished(ctx context.Context, eventID uuid.UUID, publishedAt time.Time) error
	MarkFailed(ctx context.Context, eventID uuid.UUID) error
}

type Relay struct {
	repo         Repository
	writer       *kafka.Writer
	dlqWriter    *kafka.Writer
	logger       *zap.Logger
	pollInterval time.Duration
	batchSize    int
}

type Message struct {
	EventID   string      `json:"event_id"`
	EventType string      `json:"event_type"`
	Payload   model.JSONB `json:"payload"`
	CreatedAt time.Time   `json:"created_at"`
	Status    string      `json:"status"`
}

type DLQMessage struct {
	Event    Message   `json:"event"`
	Error    string    `json:"error"`
	FailedAt time.Time `json:"failed_at"`
}

func NewRelay(repo Repository, writer, dlqWriter *kafka.Writer, logger *zap.Logger, pollInterval time.Duration, batchSize int) *Relay {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	return &Relay{
		repo:         repo,
		writer:       writer,
		dlqWriter:    dlqWriter,
		logger:       logger,
		pollInterval: pollInterval,
		batchSize:    batchSize,
	}
}

func (r *Relay) Run(ctx context.Context) error {
	r.logger.Info("outbox relay starting",
		zap.Duration("poll_interval", r.pollInterval),
		zap.Int("batch_size", r.batchSize),
	)

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	r.processPending(ctx)

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("outbox relay shutting down")
			return ctx.Err()
		case <-ticker.C:
			r.processPending(ctx)
		}
	}
}

func (r *Relay) processPending(ctx context.Context) {
	events, err := r.repo.ListPending(ctx, r.batchSize)
	if err != nil {
		r.logger.Warn("failed to list pending outbox events", zap.Error(err))
		return
	}

	if len(events) == 0 {
		return
	}

	for _, event := range events {
		if err := r.publishEvent(ctx, event); err != nil {
			r.logger.Warn("failed to publish outbox event", zap.Error(err), zap.String("event_id", event.EventID.String()))
		}
	}
}

func (r *Relay) publishEvent(ctx context.Context, event model.WorkflowEvent) error {
	message := Message{
		EventID:   event.EventID.String(),
		EventType: event.EventType,
		Payload:   event.Payload,
		CreatedAt: event.CreatedAt,
		Status:    event.Status,
	}

	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}

	kafkaMessage := kafka.Message{
		Key:   []byte(event.EventID.String()),
		Value: payload,
		Time:  time.Now(),
	}

	if err := r.writer.WriteMessages(ctx, kafkaMessage); err != nil {
		r.logger.Warn("failed to publish to kafka, sending to DLQ", zap.Error(err), zap.String("event_id", event.EventID.String()))
		return r.publishDLQ(ctx, message, err, event.EventID)
	}

	if err := r.repo.MarkPublished(ctx, event.EventID, time.Now()); err != nil {
		r.logger.Warn("failed to mark event published", zap.Error(err), zap.String("event_id", event.EventID.String()))
		return err
	}

	return nil
}

func (r *Relay) publishDLQ(ctx context.Context, message Message, publishErr error, eventID uuid.UUID) error {
	dlq := DLQMessage{
		Event:    message,
		Error:    publishErr.Error(),
		FailedAt: time.Now(),
	}

	payload, err := json.Marshal(dlq)
	if err != nil {
		return err
	}

	kafkaMessage := kafka.Message{
		Key:   []byte(message.EventID),
		Value: payload,
		Time:  time.Now(),
	}

	if err := r.dlqWriter.WriteMessages(ctx, kafkaMessage); err != nil {
		return err
	}

	if err := r.repo.MarkFailed(ctx, eventID); err != nil {
		r.logger.Warn("failed to mark event failed", zap.Error(err), zap.String("event_id", eventID.String()))
		return err
	}

	return nil
}

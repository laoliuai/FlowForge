package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/flowforge/flowforge/pkg/model"
)

type TaskQueue struct {
	writer *kafka.Writer
	reader *kafka.Reader
	topic  string
}

func NewTaskQueueProducer(brokers []string, clientID, topic string) *TaskQueue {
	return &TaskQueue{
		writer: kafka.NewWriter(kafka.WriterConfig{
			Brokers:  brokers,
			Balancer: &kafka.LeastBytes{},
			Transport: &kafka.Transport{
				ClientID: clientID,
			},
			RequiredAcks: kafka.RequireAll,
			Topic:        topic,
		}),
		topic: topic,
	}
}

func NewTaskQueueConsumer(brokers []string, clientID, groupID, topic string) *TaskQueue {
	return &TaskQueue{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers: brokers,
			GroupID: groupID,
			Topic:   topic,
			Dialer: &kafka.Dialer{
				ClientID: clientID,
			},
		}),
		topic: topic,
	}
}

func (q *TaskQueue) Enqueue(ctx context.Context, task *model.Task) error {
	if q.writer == nil {
		return errors.New("task queue writer is not configured")
	}
	payload, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	message := kafka.Message{
		Topic: q.topic,
		Key:   []byte(task.ID.String()),
		Value: payload,
		Time:  time.Now(),
	}
	return q.writer.WriteMessages(ctx, message)
}

func (q *TaskQueue) Dequeue(ctx context.Context, block time.Duration) (*model.Task, error) {
	if q.reader == nil {
		return nil, errors.New("task queue reader is not configured")
	}
	readCtx := ctx
	cancel := func() {}
	if block > 0 {
		readCtx, cancel = context.WithTimeout(ctx, block)
	}
	defer cancel()

	message, err := q.reader.FetchMessage(readCtx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, nil
		}
		return nil, err
	}

	var task model.Task
	if err := json.Unmarshal(message.Value, &task); err != nil {
		return nil, fmt.Errorf("unmarshal task: %w", err)
	}
	if err := q.reader.CommitMessages(ctx, message); err != nil {
		return nil, fmt.Errorf("commit task offset: %w", err)
	}

	return &task, nil
}

func (q *TaskQueue) Close() error {
	if q.writer != nil {
		if err := q.writer.Close(); err != nil {
			return err
		}
	}
	if q.reader != nil {
		if err := q.reader.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (q *TaskQueue) Length(ctx context.Context) (int64, error) {
	return 0, errors.New("task queue length is not supported for kafka")
}

package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/flowforge/flowforge/pkg/model"
)

const (
	headerTaskRetryCount  = "ff-task-retry-count"
	headerTaskRetryAt     = "ff-task-retry-at"
	headerTaskOriginTopic = "ff-task-origin-topic"
	headerTaskDLQError    = "ff-task-dlq-error"

	defaultTaskRetryLimit = 3
)

type TaskHandler func(context.Context, *model.Task) error

type TaskQueue struct {
	writer       *kafka.Writer
	retryWriter  *kafka.Writer
	dlqWriter    *kafka.Writer
	reader       *kafka.Reader
	retryReader  *kafka.Reader
	topic        string
	retryTopic   string
	dlqTopic     string
	maxRetry     int
	messageGroup sync.WaitGroup
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
		}),
		topic: topic,
	}
}

func NewTaskQueueConsumer(brokers []string, clientID, groupID, topic, retryTopic, dlqTopic string) *TaskQueue {
	var retryReader *kafka.Reader
	if retryTopic != "" {
		retryReader = kafka.NewReader(kafka.ReaderConfig{
			Brokers: brokers,
			GroupID: groupID,
			Topic:   retryTopic,
			Dialer: &kafka.Dialer{
				ClientID: clientID,
			},
		})
	}

	return &TaskQueue{
		writer: kafka.NewWriter(kafka.WriterConfig{
			Brokers:  brokers,
			Balancer: &kafka.LeastBytes{},
			Transport: &kafka.Transport{
				ClientID: clientID,
			},
			RequiredAcks: kafka.RequireAll,
		}),
		retryWriter: kafka.NewWriter(kafka.WriterConfig{
			Brokers:  brokers,
			Balancer: &kafka.LeastBytes{},
			Transport: &kafka.Transport{
				ClientID: clientID,
			},
			RequiredAcks: kafka.RequireAll,
		}),
		dlqWriter: kafka.NewWriter(kafka.WriterConfig{
			Brokers:  brokers,
			Balancer: &kafka.LeastBytes{},
			Transport: &kafka.Transport{
				ClientID: clientID,
			},
			RequiredAcks: kafka.RequireAll,
		}),
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers: brokers,
			GroupID: groupID,
			Topic:   topic,
			Dialer: &kafka.Dialer{
				ClientID: clientID,
			},
		}),
		retryReader: retryReader,
		topic:       topic,
		retryTopic:  retryTopic,
		dlqTopic:    dlqTopic,
		maxRetry:    defaultTaskRetryLimit,
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

func (q *TaskQueue) Consume(ctx context.Context, handler TaskHandler) error {
	if q.reader == nil {
		return errors.New("task queue reader is not configured")
	}
	if handler == nil {
		return errors.New("task handler is required")
	}

	messageCh := make(chan queuedMessage, 2)
	errCh := make(chan error, 2)

	q.messageGroup.Add(1)
	go q.consumeReader(ctx, q.reader, messageCh, errCh)

	if q.retryReader != nil && q.retryTopic != "" {
		q.messageGroup.Add(1)
		go q.consumeReader(ctx, q.retryReader, messageCh, errCh)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			return err
		case msg := <-messageCh:
			if err := q.handleMessage(ctx, msg, handler); err != nil {
				return err
			}
		}
	}
}

type queuedMessage struct {
	reader  *kafka.Reader
	message kafka.Message
}

func (q *TaskQueue) consumeReader(ctx context.Context, reader *kafka.Reader, messageCh chan<- queuedMessage, errCh chan<- error) {
	defer q.messageGroup.Done()

	for {
		msg, err := reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			select {
			case errCh <- err:
			case <-ctx.Done():
			}
			return
		}
		select {
		case messageCh <- queuedMessage{reader: reader, message: msg}:
		case <-ctx.Done():
			return
		}
	}
}

func (q *TaskQueue) handleMessage(ctx context.Context, msg queuedMessage, handler TaskHandler) error {
	if msg.message.Topic == q.retryTopic {
		if retryAt := retryTime(msg.message); !retryAt.IsZero() {
			delay := time.Until(retryAt)
			if delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
	}

	task, err := decodeTask(msg.message)
	if err != nil {
		return q.handleFailure(ctx, msg, err, nil)
	}

	if err := handler(ctx, task); err != nil {
		if handleErr := q.handleFailure(ctx, msg, err, task); handleErr != nil {
			return handleErr
		}
	} else if err := msg.reader.CommitMessages(ctx, msg.message); err != nil {
		return fmt.Errorf("commit task offset: %w", err)
	}

	return nil
}

func decodeTask(message kafka.Message) (*model.Task, error) {
	var task model.Task
	if err := json.Unmarshal(message.Value, &task); err != nil {
		return nil, fmt.Errorf("unmarshal task: %w", err)
	}
	return &task, nil
}

func (q *TaskQueue) handleFailure(ctx context.Context, msg queuedMessage, handlerErr error, task *model.Task) error {
	retryCount := retryAttempt(msg.message)
	retryLimit := q.maxRetry
	if task != nil && task.RetryLimit > 0 {
		retryLimit = task.RetryLimit
	}

	if retryCount < retryLimit && q.retryTopic != "" {
		retryAt := time.Now().Add(calculateBackoff(task, retryCount+1))
		headers := appendHeaders(msg.message.Headers,
			kafka.Header{Key: headerTaskRetryCount, Value: []byte(strconv.Itoa(retryCount + 1))},
			kafka.Header{Key: headerTaskRetryAt, Value: []byte(retryAt.Format(time.RFC3339Nano))},
			kafka.Header{Key: headerTaskOriginTopic, Value: []byte(msg.message.Topic)},
		)
		if err := q.publish(ctx, q.retryWriter, q.retryTopic, msg.message.Key, msg.message.Value, headers); err != nil {
			return err
		}
		if err := msg.reader.CommitMessages(ctx, msg.message); err != nil {
			return fmt.Errorf("commit task offset: %w", err)
		}
		return nil
	}

	if q.dlqTopic != "" {
		headers := appendHeaders(msg.message.Headers,
			kafka.Header{Key: headerTaskOriginTopic, Value: []byte(msg.message.Topic)},
			kafka.Header{Key: headerTaskDLQError, Value: []byte(handlerErr.Error())},
		)
		if err := q.publish(ctx, q.dlqWriter, q.dlqTopic, msg.message.Key, msg.message.Value, headers); err != nil {
			return err
		}
		if err := msg.reader.CommitMessages(ctx, msg.message); err != nil {
			return fmt.Errorf("commit task offset: %w", err)
		}
		return nil
	}

	return handlerErr
}

func calculateBackoff(task *model.Task, attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}
	base := 10
	if task != nil && task.BackoffSecs > 0 {
		base = task.BackoffSecs
	}
	delay := float64(base) * math.Pow(2, float64(attempt-1))
	return time.Duration(delay) * time.Second
}

func retryAttempt(message kafka.Message) int {
	for _, header := range message.Headers {
		if header.Key == headerTaskRetryCount {
			count, err := strconv.Atoi(string(header.Value))
			if err == nil {
				return count
			}
			return 0
		}
	}
	return 0
}

func retryTime(message kafka.Message) time.Time {
	for _, header := range message.Headers {
		if header.Key == headerTaskRetryAt {
			parsed, err := time.Parse(time.RFC3339Nano, string(header.Value))
			if err == nil {
				return parsed
			}
			return time.Time{}
		}
	}
	return time.Time{}
}

func appendHeaders(existing []kafka.Header, headers ...kafka.Header) []kafka.Header {
	merged := make([]kafka.Header, 0, len(existing)+len(headers))
	merged = append(merged, existing...)
	merged = append(merged, headers...)
	return merged
}

func (q *TaskQueue) publish(ctx context.Context, writer *kafka.Writer, topic string, key, value []byte, headers []kafka.Header) error {
	if writer == nil {
		return errors.New("task queue writer is not configured")
	}
	if topic == "" {
		return errors.New("task queue topic is not configured")
	}

	message := kafka.Message{
		Topic:   topic,
		Key:     key,
		Value:   value,
		Headers: headers,
		Time:    time.Now(),
	}
	return writer.WriteMessages(ctx, message)
}

func (q *TaskQueue) Close() error {
	q.messageGroup.Wait()
	if q.writer != nil {
		if err := q.writer.Close(); err != nil {
			return err
		}
	}
	if q.retryWriter != nil {
		if err := q.retryWriter.Close(); err != nil {
			return err
		}
	}
	if q.dlqWriter != nil {
		if err := q.dlqWriter.Close(); err != nil {
			return err
		}
	}
	if q.reader != nil {
		if err := q.reader.Close(); err != nil {
			return err
		}
	}
	if q.retryReader != nil {
		if err := q.retryReader.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (q *TaskQueue) Length(ctx context.Context) (int64, error) {
	return 0, errors.New("task queue length is not supported for kafka")
}

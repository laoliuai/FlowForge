package eventbus

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"
)

const (
	headerEventID     = "ff-event-id"
	headerRetryCount  = "ff-retry-count"
	headerOriginTopic = "ff-origin-topic"
	headerDLQError    = "ff-dlq-error"
)

type KafkaProducerConfig struct {
	Brokers    []string
	ClientID   string
	EventTopic string
	RetryTopic string
	DLQTopic   string
}

type KafkaProducer struct {
	writer     *kafka.Writer
	eventTopic string
	retryTopic string
	dlqTopic   string
}

func NewKafkaProducer(cfg KafkaProducerConfig) *KafkaProducer {
	writer := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  cfg.Brokers,
		Balancer: &kafka.LeastBytes{},
		Transport: &kafka.Transport{
			ClientID: cfg.ClientID,
		},
		RequiredAcks: kafka.RequireAll,
	})

	return &KafkaProducer{
		writer:     writer,
		eventTopic: cfg.EventTopic,
		retryTopic: cfg.RetryTopic,
		dlqTopic:   cfg.DLQTopic,
	}
}

func (p *KafkaProducer) PublishEvent(ctx context.Context, key, value []byte, headers ...kafka.Header) error {
	return p.publish(ctx, p.eventTopic, key, value, headers)
}

func (p *KafkaProducer) PublishRetry(ctx context.Context, key, value []byte, headers ...kafka.Header) error {
	if p.retryTopic == "" {
		return errors.New("retry topic is not configured")
	}
	return p.publish(ctx, p.retryTopic, key, value, headers)
}

func (p *KafkaProducer) PublishDLQ(ctx context.Context, key, value []byte, headers ...kafka.Header) error {
	if p.dlqTopic == "" {
		return errors.New("dlq topic is not configured")
	}
	return p.publish(ctx, p.dlqTopic, key, value, headers)
}

func (p *KafkaProducer) publish(ctx context.Context, topic string, key, value []byte, headers []kafka.Header) error {
	if topic == "" {
		return errors.New("topic is not configured")
	}

	message := kafka.Message{
		Topic:   topic,
		Key:     key,
		Value:   value,
		Headers: headers,
		Time:    time.Now(),
	}

	return p.writer.WriteMessages(ctx, message)
}

func (p *KafkaProducer) Close() error {
	return p.writer.Close()
}

type KafkaConsumerConfig struct {
	Brokers    []string
	ClientID   string
	GroupID    string
	EventTopic string
	RetryTopic string
	DLQTopic   string
	MaxRetries int
}

type KafkaHandler func(ctx context.Context, message kafka.Message) error

type Deduper interface {
	Seen(ctx context.Context, eventID string) (bool, error)
	MarkSeen(ctx context.Context, eventID string) error
}

type KafkaConsumer struct {
	producer   *KafkaProducer
	config     KafkaConsumerConfig
	handler    KafkaHandler
	deduper    Deduper
	readers    []*kafka.Reader
	mu         sync.Mutex
	startOnce  sync.Once
	closeOnce  sync.Once
	closedChan chan struct{}
}

func NewKafkaConsumer(cfg KafkaConsumerConfig, producer *KafkaProducer, handler KafkaHandler, deduper Deduper) *KafkaConsumer {
	return &KafkaConsumer{
		producer:   producer,
		config:     cfg,
		handler:    handler,
		deduper:    deduper,
		closedChan: make(chan struct{}),
	}
}

func (c *KafkaConsumer) Run(ctx context.Context) error {
	var runErr error
	c.startOnce.Do(func() {
		if c.config.MaxRetries <= 0 {
			c.config.MaxRetries = 3
		}

		readers := c.buildReaders()
		c.mu.Lock()
		c.readers = readers
		c.mu.Unlock()

		for _, reader := range readers {
			go func(r *kafka.Reader) {
				if err := c.consumeLoop(ctx, r); err != nil && !errors.Is(err, context.Canceled) {
					runErr = err
				}
			}(reader)
		}
	})

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closedChan:
		return runErr
	}
}

func (c *KafkaConsumer) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		close(c.closedChan)
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, reader := range c.readers {
			if err := reader.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
	})

	return closeErr
}

func (c *KafkaConsumer) buildReaders() []*kafka.Reader {
	config := kafka.ReaderConfig{
		Brokers:  c.config.Brokers,
		GroupID:  c.config.GroupID,
		MinBytes: 1,
		MaxBytes: 10e6,
		Dialer: &kafka.Dialer{
			ClientID: c.config.ClientID,
		},
	}

	readers := []*kafka.Reader{}
	if c.config.EventTopic != "" {
		cfg := config
		cfg.Topic = c.config.EventTopic
		readers = append(readers, kafka.NewReader(cfg))
	}
	if c.config.RetryTopic != "" {
		cfg := config
		cfg.Topic = c.config.RetryTopic
		readers = append(readers, kafka.NewReader(cfg))
	}

	return readers
}

func (c *KafkaConsumer) consumeLoop(ctx context.Context, reader *kafka.Reader) error {
	for {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			return err
		}

		eventID := extractEventID(message)
		if c.deduper != nil && eventID != "" {
			seen, err := c.deduper.Seen(ctx, eventID)
			if err == nil && seen {
				if commitErr := reader.CommitMessages(ctx, message); commitErr != nil {
					return commitErr
				}
				continue
			}
		}

		handlerErr := c.handler(ctx, message)
		if handlerErr != nil {
			if err := c.handleFailure(ctx, message, handlerErr); err != nil {
				return err
			}
		} else if c.deduper != nil && eventID != "" {
			_ = c.deduper.MarkSeen(ctx, eventID)
		}

		if err := reader.CommitMessages(ctx, message); err != nil {
			return err
		}
	}
}

func (c *KafkaConsumer) handleFailure(ctx context.Context, message kafka.Message, handlerErr error) error {
	if c.producer == nil {
		return handlerErr
	}

	retryCount := retryAttempt(message)
	if retryCount < c.config.MaxRetries && c.config.RetryTopic != "" {
		headers := appendHeaders(message.Headers,
			kafka.Header{Key: headerRetryCount, Value: []byte(strconv.Itoa(retryCount + 1))},
			kafka.Header{Key: headerOriginTopic, Value: []byte(message.Topic)},
		)
		return c.producer.PublishRetry(ctx, message.Key, message.Value, headers...)
	}

	if c.config.DLQTopic != "" {
		headers := appendHeaders(message.Headers,
			kafka.Header{Key: headerOriginTopic, Value: []byte(message.Topic)},
			kafka.Header{Key: headerDLQError, Value: []byte(handlerErr.Error())},
		)
		return c.producer.PublishDLQ(ctx, message.Key, message.Value, headers...)
	}

	return handlerErr
}

func retryAttempt(message kafka.Message) int {
	for _, header := range message.Headers {
		if header.Key == headerRetryCount {
			count, err := strconv.Atoi(string(header.Value))
			if err == nil {
				return count
			}
			return 0
		}
	}
	return 0
}

func extractEventID(message kafka.Message) string {
	for _, header := range message.Headers {
		if header.Key == headerEventID {
			return string(header.Value)
		}
	}

	if len(message.Key) > 0 {
		return string(message.Key)
	}

	var payload struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(message.Value, &payload); err == nil {
		return payload.EventID
	}

	return ""
}

func appendHeaders(existing []kafka.Header, headers ...kafka.Header) []kafka.Header {
	merged := make([]kafka.Header, 0, len(existing)+len(headers))
	merged = append(merged, existing...)
	merged = append(merged, headers...)
	return merged
}

type MemoryDeduper struct {
	mu      sync.Mutex
	entries map[string]time.Time
	ttl     time.Duration
}

func NewMemoryDeduper(ttl time.Duration) *MemoryDeduper {
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	return &MemoryDeduper{
		entries: make(map[string]time.Time),
		ttl:     ttl,
	}
}

func (d *MemoryDeduper) Seen(ctx context.Context, eventID string) (bool, error) {
	if eventID == "" {
		return false, nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	d.cleanupLocked(now)

	seenAt, ok := d.entries[eventID]
	if !ok {
		return false, nil
	}
	if now.Sub(seenAt) > d.ttl {
		delete(d.entries, eventID)
		return false, nil
	}
	return true, nil
}

func (d *MemoryDeduper) MarkSeen(ctx context.Context, eventID string) error {
	if eventID == "" {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.entries[eventID] = time.Now()
	return nil
}

func (d *MemoryDeduper) cleanupLocked(now time.Time) {
	for eventID, seenAt := range d.entries {
		if now.Sub(seenAt) > d.ttl {
			delete(d.entries, eventID)
		}
	}
}

type DLQPayload struct {
	OriginTopic string            `json:"origin_topic"`
	Partition   int               `json:"partition"`
	Offset      int64             `json:"offset"`
	Key         string            `json:"key"`
	Headers     map[string]string `json:"headers"`
	Value       string            `json:"value"`
	Error       string            `json:"error"`
	FailedAt    time.Time         `json:"failed_at"`
}

func EncodeDLQPayload(message kafka.Message, err error) ([]byte, error) {
	headers := make(map[string]string, len(message.Headers))
	for _, header := range message.Headers {
		headers[header.Key] = string(header.Value)
	}

	payload := DLQPayload{
		OriginTopic: message.Topic,
		Partition:   message.Partition,
		Offset:      message.Offset,
		Key:         string(message.Key),
		Headers:     headers,
		Value:       base64.StdEncoding.EncodeToString(message.Value),
		Error:       err.Error(),
		FailedAt:    time.Now(),
	}

	return json.Marshal(payload)
}

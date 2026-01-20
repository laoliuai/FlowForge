package eventbus

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/segmentio/kafka-go"
)

type Event struct {
	Type      string          `json:"type"`
	Timestamp int64           `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

type TaskEvent struct {
	TaskID     string `json:"task_id"`
	WorkflowID string `json:"workflow_id"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
}

type WorkflowEvent struct {
	WorkflowID string `json:"workflow_id"`
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
}

const (
	ChannelTask     = "ff:events:task"
	ChannelWorkflow = "ff:events:workflow"
	ChannelQuota    = "ff:events:quota"
)

type Bus struct {
	producer      *KafkaProducer
	brokers       []string
	clientID      string
	eventTopic    string
	consumerGroup string
}

type BusConfig struct {
	Brokers       []string
	ClientID      string
	EventTopic    string
	RetryTopic    string
	DLQTopic      string
	ConsumerGroup string
}

func NewBus(cfg BusConfig) *Bus {
	producer := NewKafkaProducer(KafkaProducerConfig{
		Brokers:    cfg.Brokers,
		ClientID:   cfg.ClientID,
		EventTopic: cfg.EventTopic,
		RetryTopic: cfg.RetryTopic,
		DLQTopic:   cfg.DLQTopic,
	})

	return &Bus{
		producer:      producer,
		brokers:       cfg.Brokers,
		clientID:      cfg.ClientID,
		eventTopic:    cfg.EventTopic,
		consumerGroup: cfg.ConsumerGroup,
	}
}

func NewEvent(eventType string, payload interface{}) (Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	return Event{
		Type:      eventType,
		Timestamp: time.Now().Unix(),
		Data:      data,
	}, nil
}

func (b *Bus) Publish(ctx context.Context, channel string, event Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}

	headers := []kafka.Header{
		{Key: "ff-channel", Value: []byte(channel)},
	}

	return b.producer.PublishEvent(ctx, []byte(channel), payload, headers...)
}

func (b *Bus) Subscribe(ctx context.Context, channels ...string) <-chan *Event {
	ch := make(chan *Event, 100)

	if len(b.brokers) == 0 || b.eventTopic == "" || b.consumerGroup == "" {
		close(ch)
		return ch
	}

	channelFilter := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		channelFilter[channel] = struct{}{}
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  b.brokers,
		GroupID:  b.consumerGroup,
		Topic:    b.eventTopic,
		MinBytes: 1,
		MaxBytes: 10e6,
		Dialer: &kafka.Dialer{
			ClientID: b.clientID,
		},
	})

	go func() {
		defer close(ch)
		defer reader.Close()
		for {
			message, err := reader.FetchMessage(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				continue
			}

			if !shouldDeliver(channelFilter, message) {
				_ = reader.CommitMessages(ctx, message)
				continue
			}

			var event Event
			if err := json.Unmarshal(message.Value, &event); err != nil {
				_ = reader.CommitMessages(ctx, message)
				continue
			}
			ch <- &event
			_ = reader.CommitMessages(ctx, message)
		}
	}()

	return ch
}

func shouldDeliver(filter map[string]struct{}, message kafka.Message) bool {
	if len(filter) == 0 {
		return true
	}

	for _, header := range message.Headers {
		if header.Key == "ff-channel" {
			if _, ok := filter[string(header.Value)]; ok {
				return true
			}
		}
	}

	if _, ok := filter[string(message.Key)]; ok {
		return true
	}

	return false
}

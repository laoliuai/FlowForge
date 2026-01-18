package eventbus

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
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
	client redis.UniversalClient
}

func NewBus(client redis.UniversalClient) *Bus {
	return &Bus{client: client}
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
	return b.client.Publish(ctx, channel, payload).Err()
}

func (b *Bus) Subscribe(ctx context.Context, channels ...string) <-chan *Event {
	sub := b.client.Subscribe(ctx, channels...)
	ch := make(chan *Event, 100)

	go func() {
		defer close(ch)
		for msg := range sub.Channel() {
			var event Event
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				continue
			}
			ch <- &event
		}
	}()

	go func() {
		<-ctx.Done()
		_ = sub.Close()
	}()

	return ch
}

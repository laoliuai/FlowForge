package eventbus

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type Event struct {
	Type    string                 `json:"type"`
	Payload map[string]interface{} `json:"payload"`
}

type Client interface {
	Publish(ctx context.Context, channel string, message interface{}) *redis.IntCmd
	Subscribe(ctx context.Context, channels ...string) *redis.PubSub
}

type Bus struct {
	client Client
}

func NewBus(client Client) *Bus {
	return &Bus{client: client}
}

func (b *Bus) Publish(ctx context.Context, channel string, event Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	return b.client.Publish(ctx, channel, payload).Err()
}

func (b *Bus) Subscribe(ctx context.Context, channel string, handler func(Event)) error {
	sub := b.client.Subscribe(ctx, channel)
	ch := sub.Channel()
	for msg := range ch {
		var event Event
		if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
			continue
		}
		handler(event)
	}
	return nil
}

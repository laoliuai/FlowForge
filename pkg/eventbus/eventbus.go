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

type Bus struct {
	client redis.UniversalClient
}

func NewBus(client redis.UniversalClient) *Bus {
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
	return sub.Err()
}

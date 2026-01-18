package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/flowforge/flowforge/pkg/model"
)

type TaskQueue struct {
	client redis.UniversalClient
	key    string
}

func NewTaskQueue(client redis.UniversalClient, key string) *TaskQueue {
	if key == "" {
		key = "flowforge:tasks"
	}
	return &TaskQueue{client: client, key: key}
}

func (q *TaskQueue) Enqueue(ctx context.Context, task *model.Task) error {
	payload, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	return q.client.RPush(ctx, q.key, payload).Err()
}

func (q *TaskQueue) Dequeue(ctx context.Context, block time.Duration) (*model.Task, error) {
	result, err := q.client.BLPop(ctx, block, q.key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, nil
		}
		return nil, err
	}
	if len(result) < 2 {
		return nil, nil
	}

	var task model.Task
	if err := json.Unmarshal([]byte(result[1]), &task); err != nil {
		return nil, fmt.Errorf("unmarshal task: %w", err)
	}
	return &task, nil
}

func (q *TaskQueue) Length(ctx context.Context) (int64, error) {
	return q.client.LLen(ctx, q.key).Result()
}

package scheduler

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/eventbus"
	"github.com/flowforge/flowforge/pkg/model"
	"github.com/flowforge/flowforge/pkg/queue"
	"github.com/flowforge/flowforge/pkg/quota"
	"github.com/flowforge/flowforge/pkg/store/postgres"
)

type Scheduler struct {
	workflowRepo *postgres.WorkflowRepository
	taskRepo     *postgres.TaskRepository
	queue        *queue.TaskQueue
	quotaManager *quota.Manager
	bus          *eventbus.Bus
	logger       *zap.Logger
	interval     time.Duration
}

func NewScheduler(
	workflowRepo *postgres.WorkflowRepository,
	taskRepo *postgres.TaskRepository,
	queue *queue.TaskQueue,
	quotaManager *quota.Manager,
	bus *eventbus.Bus,
	logger *zap.Logger,
) *Scheduler {
	return &Scheduler{
		workflowRepo: workflowRepo,
		taskRepo:     taskRepo,
		queue:        queue,
		quotaManager: quotaManager,
		bus:          bus,
		logger:       logger,
		interval:     time.Second,
	}
}

func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.schedule(ctx)
		}
	}
}

func (s *Scheduler) schedule(ctx context.Context) {
	pendingTasks, err := s.taskRepo.GetPendingTasks(ctx, "")
	if err != nil {
		s.logger.Error("failed to load pending tasks", zap.Error(err))
		return
	}

	for _, task := range pendingTasks {
		if ok := s.quotaManager.CanScheduleTask(ctx, &task); !ok {
			continue
		}
		if err := s.queue.Enqueue(ctx, &task); err != nil {
			s.logger.Error("failed to enqueue task", zap.Error(err))
			continue
		}
		taskEvent := eventbus.TaskEvent{
			TaskID:     task.ID.String(),
			WorkflowID: task.WorkflowID.String(),
			Status:     string(model.TaskQueued),
		}
		if event, err := eventbus.NewEvent("task_queued", taskEvent); err == nil {
			_ = s.bus.Publish(ctx, eventbus.ChannelTask, event)
		}
	}
}

func (s *Scheduler) UpdateTaskStatus(ctx context.Context, taskID string, status model.TaskStatus) error {
	return s.taskRepo.UpdateStatus(ctx, taskID, status, nil)
}

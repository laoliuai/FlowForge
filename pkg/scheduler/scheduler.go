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
	workflowRepo  *postgres.WorkflowRepository
	taskRepo      *postgres.TaskRepository
	queue         *queue.TaskQueue
	quotaManager  *quota.Manager
	bus           *eventbus.Bus
	logger        *zap.Logger
	interval      time.Duration
	fairScheduler *FairScheduler
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
		workflowRepo:  workflowRepo,
		taskRepo:      taskRepo,
		queue:         queue,
		quotaManager:  quotaManager,
		bus:           bus,
		logger:        logger,
		interval:      time.Second,
		fairScheduler: NewFairScheduler(taskRepo, queue, quotaManager, bus, logger),
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
	if err := s.fairScheduler.Schedule(ctx); err != nil {
		s.logger.Error("failed to run fair scheduler", zap.Error(err))
	}
}

func (s *Scheduler) UpdateTaskStatus(ctx context.Context, taskID string, status model.TaskStatus) error {
	return s.taskRepo.UpdateStatus(ctx, taskID, status, nil)
}

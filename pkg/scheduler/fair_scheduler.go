package scheduler

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/eventbus"
	"github.com/flowforge/flowforge/pkg/model"
	"github.com/flowforge/flowforge/pkg/queue"
	"github.com/flowforge/flowforge/pkg/quota"
	"github.com/flowforge/flowforge/pkg/store/postgres"
)

type FairScheduler struct {
	taskRepo     *postgres.TaskRepository
	taskQueue    *queue.TaskQueue
	quotaManager *quota.Manager
	bus          *eventbus.Bus
	logger       *zap.Logger
	maxBatch     int
}

type SchedulableTask struct {
	Task              *model.Task
	ProjectID         uuid.UUID
	TenantID          uuid.UUID
	EffectivePriority float64
	QuotaWeight       int
}

func NewFairScheduler(
	taskRepo *postgres.TaskRepository,
	taskQueue *queue.TaskQueue,
	quotaManager *quota.Manager,
	bus *eventbus.Bus,
	logger *zap.Logger,
) *FairScheduler {
	return &FairScheduler{
		taskRepo:     taskRepo,
		taskQueue:    taskQueue,
		quotaManager: quotaManager,
		bus:          bus,
		logger:       logger,
		maxBatch:     100,
	}
}

func (fs *FairScheduler) Schedule(ctx context.Context) error {
	pendingTasks, err := fs.taskRepo.GetPendingTasksForScheduling(ctx)
	if err != nil {
		return err
	}

	if len(pendingTasks) == 0 {
		return nil
	}

	dependencyStatus, err := fs.loadDependencyStatuses(ctx, pendingTasks)
	if err != nil {
		return err
	}

	tasksByTenant := make(map[string][]*SchedulableTask)
	for i := range pendingTasks {
		task := pendingTasks[i]
		if !dependenciesSatisfied(&task, dependencyStatus) {
			continue
		}

		if task.Workflow == nil || task.Workflow.Project == nil || task.Workflow.Project.Group == nil {
			continue
		}

		project := task.Workflow.Project
		group := project.Group

		weight, err := fs.quotaManager.GetPriorityWeight(ctx, project.ID)
		if err != nil {
			weight = 100
		}

		schedulable := &SchedulableTask{
			Task:        &task,
			ProjectID:   project.ID,
			TenantID:    group.TenantID,
			QuotaWeight: weight,
		}

		tasksByTenant[group.TenantID.String()] = append(tasksByTenant[group.TenantID.String()], schedulable)
	}

	for _, tasks := range tasksByTenant {
		for _, task := range tasks {
			task.EffectivePriority = fs.calculateEffectivePriority(ctx, task)
		}

		sort.Slice(tasks, func(i, j int) bool {
			return tasks[i].EffectivePriority > tasks[j].EffectivePriority
		})
	}

	scheduled := 0
	for scheduled < fs.maxBatch && len(tasksByTenant) > 0 {
		for tenantID, tasks := range tasksByTenant {
			if len(tasks) == 0 {
				delete(tasksByTenant, tenantID)
				continue
			}

			schedulable := tasks[0]
			tasksByTenant[tenantID] = tasks[1:]

			canSchedule, _ := fs.quotaManager.CanSchedule(ctx, schedulable.ProjectID, schedulable.Task.GetResourceRequest())
			if !canSchedule {
				continue
			}

			if err := fs.quotaManager.Reserve(ctx, schedulable.ProjectID, schedulable.Task.GetResourceRequest()); err != nil {
				fs.logger.Debug("failed to reserve quota", zap.Error(err))
				continue
			}

			queuedAt := time.Now()
			updates := map[string]interface{}{
				"queued_at": &queuedAt,
			}
			if err := fs.taskRepo.UpdateStatus(ctx, schedulable.Task.ID.String(), model.TaskQueued, updates); err != nil {
				_ = fs.quotaManager.Release(ctx, schedulable.ProjectID, schedulable.Task.GetResourceRequest())
				continue
			}

			schedulable.Task.Status = model.TaskQueued
			schedulable.Task.QueuedAt = &queuedAt

			if err := fs.taskQueue.Enqueue(ctx, schedulable.Task); err != nil {
				_ = fs.quotaManager.Release(ctx, schedulable.ProjectID, schedulable.Task.GetResourceRequest())
				_ = fs.taskRepo.UpdateStatus(ctx, schedulable.Task.ID.String(), model.TaskPending, map[string]interface{}{
					"queued_at": nil,
				})
				continue
			}

			fs.publishTaskQueued(ctx, schedulable.Task)
			scheduled++
		}
	}

	return nil
}

func (fs *FairScheduler) calculateEffectivePriority(ctx context.Context, task *SchedulableTask) float64 {
	basePriority := 0.0
	if task.Task.Workflow != nil {
		basePriority = float64(task.Task.Workflow.Priority)
	}

	quotaBoost := float64(task.QuotaWeight) / 100.0

	waitTime := time.Since(task.Task.CreatedAt)
	waitBoost := math.Min(waitTime.Minutes()/60.0, 1.0) * 10

	utilizationPenalty := 0.0
	usage, err := fs.quotaManager.GetUsageSummary(ctx, "tenant", task.TenantID)
	if err == nil && usage != nil && usage.UtilizationPercent > 80 {
		utilizationPenalty = (usage.UtilizationPercent - 80) / 20 * 5
	}

	return basePriority + quotaBoost*20 + waitBoost - utilizationPenalty
}

func (fs *FairScheduler) loadDependencyStatuses(ctx context.Context, tasks []model.Task) (map[uuid.UUID]model.TaskStatus, error) {
	depIDs := make([]uuid.UUID, 0)
	seen := make(map[uuid.UUID]struct{})
	for _, task := range tasks {
		for _, dep := range task.Dependencies {
			if _, exists := seen[dep.DependsOnID]; exists {
				continue
			}
			seen[dep.DependsOnID] = struct{}{}
			depIDs = append(depIDs, dep.DependsOnID)
		}
	}

	return fs.taskRepo.GetDependencyStatuses(ctx, depIDs)
}

func dependenciesSatisfied(task *model.Task, statuses map[uuid.UUID]model.TaskStatus) bool {
	for _, dep := range task.Dependencies {
		status, ok := statuses[dep.DependsOnID]
		if !ok {
			return false
		}

		switch dep.Type {
		case "success":
			if status != model.TaskSucceeded {
				return false
			}
		case "completion":
			if status != model.TaskSucceeded && status != model.TaskFailed && status != model.TaskSkipped {
				return false
			}
		case "failure":
			if status != model.TaskFailed {
				return false
			}
		}
	}
	return true
}

func (fs *FairScheduler) publishTaskQueued(ctx context.Context, task *model.Task) {
	taskEvent := eventbus.TaskEvent{
		TaskID:     task.ID.String(),
		WorkflowID: task.WorkflowID.String(),
		Status:     string(model.TaskQueued),
	}
	if event, err := eventbus.NewEvent("task_queued", taskEvent); err == nil {
		_ = fs.bus.Publish(ctx, eventbus.ChannelTask, event)
	}
}

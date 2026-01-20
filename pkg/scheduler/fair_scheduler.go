package scheduler

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
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

type SchedulableGang struct {
	Tasks             []*model.Task
	ProjectID         uuid.UUID
	TenantID          uuid.UUID
	EffectivePriority float64
	QuotaWeight       int
	CreatedAt         time.Time
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

	workflowTasksByName := make(map[string]map[string]*model.Task)
	gangsByKey := make(map[string]*SchedulableGang)
	readyGang := make(map[string]bool)
	for i := range pendingTasks {
		task := &pendingTasks[i]
		if task.Workflow == nil || task.Workflow.Project == nil || task.Workflow.Project.Group == nil {
			continue
		}

		gangKey := task.GangID
		if gangKey == "" {
			gangKey = task.ID.String()
		}

		if _, ok := readyGang[gangKey]; !ok {
			readyGang[gangKey] = true
		}
		if !dependenciesSatisfied(task, dependencyStatus) {
			readyGang[gangKey] = false
			continue
		}

		if task.WhenCondition != "" {
			tasksByName, err := fs.getWorkflowTasksByName(ctx, workflowTasksByName, task.WorkflowID)
			if err != nil {
				fs.logger.Warn("failed to load workflow tasks for condition evaluation", zap.String("workflow_id", task.WorkflowID.String()), zap.Error(err))
				readyGang[gangKey] = false
				continue
			}
			if !evaluateCondition(task.WhenCondition, tasksByName) {
				if err := fs.skipTask(ctx, task); err != nil {
					fs.logger.Warn("failed to skip task", zap.String("task_id", task.ID.String()), zap.Error(err))
				}
				continue
			}
		}

		project := task.Workflow.Project
		group := project.Group

		weight, err := fs.quotaManager.GetPriorityWeight(ctx, project.ID)
		if err != nil {
			weight = 100
		}

		if _, ok := gangsByKey[gangKey]; !ok {
			gangsByKey[gangKey] = &SchedulableGang{
				Tasks:     []*model.Task{},
				ProjectID: project.ID,
				TenantID:  group.TenantID,
				CreatedAt: task.CreatedAt,
			}
		}

		gang := gangsByKey[gangKey]
		gang.Tasks = append(gang.Tasks, task)
		if task.CreatedAt.Before(gang.CreatedAt) {
			gang.CreatedAt = task.CreatedAt
		}
		gang.QuotaWeight = weight
	}

	gangsByTenant := make(map[string][]*SchedulableGang)
	for gangKey, gang := range gangsByKey {
		if !readyGang[gangKey] || len(gang.Tasks) == 0 {
			continue
		}
		gangsByTenant[gang.TenantID.String()] = append(gangsByTenant[gang.TenantID.String()], gang)
	}

	for _, gangs := range gangsByTenant {
		for _, gang := range gangs {
			gang.EffectivePriority = fs.calculateGangPriority(ctx, gang)
		}

		sort.Slice(gangs, func(i, j int) bool {
			return gangs[i].EffectivePriority > gangs[j].EffectivePriority
		})
	}

	scheduled := 0
	for scheduled < fs.maxBatch && len(gangsByTenant) > 0 {
		for tenantID, gangs := range gangsByTenant {
			if len(gangs) == 0 {
				delete(gangsByTenant, tenantID)
				continue
			}

			schedulable := gangs[0]
			gangsByTenant[tenantID] = gangs[1:]

			if len(schedulable.Tasks) > fs.maxBatch && scheduled > 0 {
				continue
			}
			if scheduled+len(schedulable.Tasks) > fs.maxBatch && scheduled > 0 {
				continue
			}

			if err := fs.scheduleGang(ctx, schedulable); err != nil {
				fs.logger.Debug("failed to schedule gang", zap.Error(err))
				continue
			}

			scheduled += len(schedulable.Tasks)
		}
	}

	return nil
}

func (fs *FairScheduler) getWorkflowTasksByName(
	ctx context.Context,
	cache map[string]map[string]*model.Task,
	workflowID uuid.UUID,
) (map[string]*model.Task, error) {
	workflowKey := workflowID.String()
	if tasksByName, ok := cache[workflowKey]; ok {
		return tasksByName, nil
	}

	tasks, err := fs.taskRepo.ListByWorkflowID(ctx, workflowKey)
	if err != nil {
		return nil, err
	}

	tasksByName := make(map[string]*model.Task, len(tasks))
	for i := range tasks {
		tasksByName[tasks[i].Name] = &tasks[i]
	}
	cache[workflowKey] = tasksByName

	return tasksByName, nil
}

func (fs *FairScheduler) calculateGangPriority(ctx context.Context, gang *SchedulableGang) float64 {
	basePriority := 0.0
	if len(gang.Tasks) > 0 && gang.Tasks[0].Workflow != nil {
		basePriority = float64(gang.Tasks[0].Workflow.Priority)
	}

	quotaBoost := float64(gang.QuotaWeight) / 100.0

	waitTime := time.Since(gang.CreatedAt)
	waitBoost := math.Min(waitTime.Minutes()/60.0, 1.0) * 10

	utilizationPenalty := 0.0
	usage, err := fs.quotaManager.GetUsageSummary(ctx, "tenant", gang.TenantID)
	if err == nil && usage != nil && usage.UtilizationPercent > 80 {
		utilizationPenalty = (usage.UtilizationPercent - 80) / 20 * 5
	}

	return basePriority + quotaBoost*20 + waitBoost - utilizationPenalty
}

func (fs *FairScheduler) scheduleGang(ctx context.Context, gang *SchedulableGang) error {
	reserved := make([]*model.Task, 0, len(gang.Tasks))
	for _, task := range gang.Tasks {
		canSchedule, _ := fs.quotaManager.CanSchedule(ctx, gang.ProjectID, task.GetResourceRequest())
		if !canSchedule {
			fs.releaseQuotaForTasks(ctx, gang.ProjectID, reserved)
			return fmt.Errorf("insufficient quota for gang")
		}

		if err := fs.quotaManager.Reserve(ctx, gang.ProjectID, task.GetResourceRequest()); err != nil {
			fs.releaseQuotaForTasks(ctx, gang.ProjectID, reserved)
			return err
		}
		reserved = append(reserved, task)
	}

	if err := fs.queueGangTasks(ctx, gang.Tasks); err != nil {
		fs.releaseQuotaForTasks(ctx, gang.ProjectID, reserved)
		return err
	}

	return nil
}

func (fs *FairScheduler) queueGangTasks(ctx context.Context, tasks []*model.Task) error {
	queuedAt := time.Now()
	queued := make([]*model.Task, 0, len(tasks))

	for _, task := range tasks {
		updates := map[string]interface{}{
			"queued_at": &queuedAt,
		}
		event := newTaskQueuedEvent(task, queuedAt)
		if err := fs.taskRepo.UpdateStatusWithOutbox(ctx, task.ID.String(), model.TaskQueued, updates, event); err != nil {
			fs.resetQueuedTasks(ctx, queued)
			return err
		}

		task.Status = model.TaskQueued
		task.QueuedAt = &queuedAt

		if err := fs.taskQueue.Enqueue(ctx, task); err != nil {
			queued = append(queued, task)
			fs.resetQueuedTasks(ctx, queued)
			return err
		}

		fs.publishTaskQueued(ctx, task)
		queued = append(queued, task)
	}

	return nil
}

func (fs *FairScheduler) skipTask(ctx context.Context, task *model.Task) error {
	now := time.Now()
	updates := map[string]interface{}{
		"finished_at": &now,
	}
	event := newTaskStatusEvent(task, model.TaskSkipped)
	if err := fs.taskRepo.UpdateStatusWithOutbox(ctx, task.ID.String(), model.TaskSkipped, updates, event); err != nil {
		return err
	}

	task.Status = model.TaskSkipped
	task.FinishedAt = &now
	fs.publishTaskStatus(ctx, task, model.TaskSkipped)

	return nil
}

func (fs *FairScheduler) resetQueuedTasks(ctx context.Context, tasks []*model.Task) {
	for _, task := range tasks {
		event := newTaskStatusEvent(task, model.TaskPending)
		_ = fs.taskRepo.UpdateStatusWithOutbox(ctx, task.ID.String(), model.TaskPending, map[string]interface{}{
			"queued_at": nil,
		}, event)
		task.Status = model.TaskPending
		task.QueuedAt = nil
	}
}

func (fs *FairScheduler) releaseQuotaForTasks(ctx context.Context, projectID uuid.UUID, tasks []*model.Task) {
	for _, task := range tasks {
		_ = fs.quotaManager.Release(ctx, projectID, task.GetResourceRequest())
	}
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

func evaluateCondition(condition string, tasksByName map[string]*model.Task) bool {
	trimmed := strings.TrimSpace(condition)
	if trimmed == "" || trimmed == "true" {
		return true
	}
	if trimmed == "false" {
		return false
	}

	parts := strings.Split(trimmed, "==")
	if len(parts) != 2 {
		return true
	}
	left := strings.TrimSpace(parts[0])
	right := strings.TrimSpace(parts[1])

	expected := strings.EqualFold(right, "true")
	if strings.EqualFold(right, "false") {
		expected = false
	}

	value, ok := resolveOutputValue(left, tasksByName)
	if !ok {
		return false
	}

	boolValue, ok := value.(bool)
	if ok {
		return boolValue == expected
	}

	stringValue, ok := value.(string)
	if ok {
		return strings.EqualFold(stringValue, right)
	}

	return false
}

func resolveOutputValue(expression string, tasksByName map[string]*model.Task) (interface{}, bool) {
	trimmed := strings.TrimPrefix(expression, "{{")
	trimmed = strings.TrimSuffix(trimmed, "}}")
	trimmed = strings.TrimSpace(trimmed)

	parts := strings.Split(trimmed, ".")
	if len(parts) < 4 {
		return nil, false
	}
	if parts[0] != "tasks" || parts[2] != "outputs" {
		return nil, false
	}

	taskName := parts[1]
	outputKey := parts[3]

	task, ok := tasksByName[taskName]
	if !ok {
		return nil, false
	}
	if task.Outputs == nil {
		return nil, false
	}
	value, ok := task.Outputs[outputKey]
	return value, ok
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

func (fs *FairScheduler) publishTaskStatus(ctx context.Context, task *model.Task, status model.TaskStatus) {
	taskEvent := eventbus.TaskEvent{
		TaskID:     task.ID.String(),
		WorkflowID: task.WorkflowID.String(),
		Status:     string(status),
	}
	if event, err := eventbus.NewEvent("task_status", taskEvent); err == nil {
		_ = fs.bus.Publish(ctx, eventbus.ChannelTask, event)
	}
}

func newTaskQueuedEvent(task *model.Task, queuedAt time.Time) *model.WorkflowEvent {
	return &model.WorkflowEvent{
		EventID:   uuid.New(),
		EventType: "task_queued",
		Payload: model.JSONB{
			"task_id":     task.ID.String(),
			"workflow_id": task.WorkflowID.String(),
			"queued_at":   queuedAt,
		},
		Status: model.OutboxStatusPending,
	}
}

func newTaskStatusEvent(task *model.Task, status model.TaskStatus) *model.WorkflowEvent {
	return &model.WorkflowEvent{
		EventID:   uuid.New(),
		EventType: "task_status_changed",
		Payload: model.JSONB{
			"task_id":     task.ID.String(),
			"workflow_id": task.WorkflowID.String(),
			"status":      string(status),
		},
		Status: model.OutboxStatusPending,
	}
}

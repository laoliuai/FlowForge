package controller

import (
	"context"
	"encoding/json"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/controller/dag"
	"github.com/flowforge/flowforge/pkg/eventbus"
	"github.com/flowforge/flowforge/pkg/model"
	"github.com/flowforge/flowforge/pkg/store/postgres"
)

type WorkflowController struct {
	workflowRepo *postgres.WorkflowRepository
	taskRepo     *postgres.TaskRepository
	bus          *eventbus.Bus
	logger       *zap.Logger
	parser       *dag.Parser

	mu              sync.RWMutex
	activeWorkflows map[string]*workflowState
}

type workflowState struct {
	workflow *model.Workflow
	tasks    map[string]*model.Task
	pending  int
	running  int
	finished int
}

func NewWorkflowController(
	workflowRepo *postgres.WorkflowRepository,
	taskRepo *postgres.TaskRepository,
	bus *eventbus.Bus,
	logger *zap.Logger,
) *WorkflowController {
	return &WorkflowController{
		workflowRepo:    workflowRepo,
		taskRepo:        taskRepo,
		bus:             bus,
		logger:          logger,
		parser:          dag.NewParser(),
		activeWorkflows: make(map[string]*workflowState),
	}
}

func (c *WorkflowController) Start(ctx context.Context) error {
	taskEvents := c.bus.Subscribe(ctx, eventbus.ChannelTask)

	go c.handleTaskEvents(ctx, taskEvents)
	go c.RunReconciler(ctx)

	c.logger.Info("workflow controller started")
	return nil
}

func (c *WorkflowController) SubmitWorkflow(ctx context.Context, workflow *model.Workflow) error {
	tasks, err := c.parser.Parse(workflow.ID.String(), workflow.DAGSpec)
	if err != nil {
		return err
	}

	workflow.Status = model.WorkflowPending
	if err := c.workflowRepo.Create(ctx, workflow); err != nil {
		return err
	}

	if err := c.taskRepo.CreateBatch(ctx, tasks); err != nil {
		return err
	}

	c.initWorkflowState(workflow, tasks)

	for _, task := range tasks {
		taskEvent := eventbus.TaskEvent{
			TaskID:     task.ID.String(),
			WorkflowID: task.WorkflowID.String(),
			Status:     string(task.Status),
		}
		if event, err := eventbus.NewEvent("task_created", taskEvent); err == nil {
			_ = c.bus.Publish(ctx, eventbus.ChannelTask, event)
		}
	}

	return c.startWorkflow(ctx, workflow.ID.String())
}

func (c *WorkflowController) initWorkflowState(workflow *model.Workflow, tasks []*model.Task) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := &workflowState{
		workflow: workflow,
		tasks:    make(map[string]*model.Task),
	}

	for _, task := range tasks {
		state.tasks[task.ID.String()] = task
		switch {
		case isRunningStatus(task.Status):
			state.running++
		case isFinishedStatus(task.Status):
			state.finished++
		default:
			state.pending++
		}
	}

	c.activeWorkflows[workflow.ID.String()] = state
}

func (c *WorkflowController) startWorkflow(ctx context.Context, workflowID string) error {
	event := newWorkflowStatusEvent(workflowID, model.WorkflowRunning, "")
	if err := c.workflowRepo.UpdateStatusWithOutbox(ctx, workflowID, model.WorkflowRunning, "", event); err != nil {
		return err
	}

	return nil
}

func (c *WorkflowController) handleTaskEvents(ctx context.Context, events <-chan *eventbus.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			c.processTaskEvent(ctx, event)
		}
	}
}

func (c *WorkflowController) processTaskEvent(ctx context.Context, event *eventbus.Event) {
	var taskEvent eventbus.TaskEvent
	if err := json.Unmarshal(event.Data, &taskEvent); err != nil {
		c.logger.Error("failed to unmarshal task event", zap.Error(err))
		return
	}

	if taskEvent.TaskID == "" {
		return
	}

	if taskEvent.WorkflowID != "" {
		_ = c.ensureWorkflowState(ctx, taskEvent.WorkflowID)
	}

	status := model.TaskStatus(taskEvent.Status)
	if err := c.HandleTaskUpdate(ctx, taskEvent.TaskID, status, taskEvent.Message, taskEvent.ExitCode); err != nil {
		c.logger.Error("failed to handle task update", zap.String("task_id", taskEvent.TaskID), zap.Error(err))
	}
}

func (c *WorkflowController) HandleTaskUpdate(ctx context.Context, taskID string, status model.TaskStatus, errMsg string, exitCode *int) error {
	task, err := c.taskRepo.GetByID(ctx, taskID)
	if err != nil {
		return err
	}
	c.ensureWorkflowState(ctx, task.WorkflowID.String())

	now := time.Now()
	updates := map[string]interface{}{}
	if errMsg != "" {
		updates["error_message"] = errMsg
	}
	if exitCode != nil {
		updates["exit_code"] = exitCode
	}

	switch status {
	case model.TaskRunning:
		updates["started_at"] = &now
	case model.TaskSucceeded, model.TaskFailed, model.TaskSkipped:
		updates["finished_at"] = &now
	}

	if status == model.TaskFailed && task.RetryCount < task.RetryLimit {
		nextRetry := now.Add(c.retryDelay(task, task.RetryCount+1))
		updates["retry_count"] = task.RetryCount + 1
		updates["next_retry_at"] = &nextRetry
		event := newTaskStatusEvent(task, model.TaskRetrying, errMsg, exitCode)
		event.Payload["retry_count"] = task.RetryCount + 1
		event.Payload["next_retry_at"] = nextRetry
		if err := c.taskRepo.UpdateStatusWithOutbox(ctx, taskID, model.TaskRetrying, updates, event); err != nil {
			return err
		}
		c.updateTaskState(task, model.TaskRetrying)
		return nil
	}

	event := newTaskStatusEvent(task, status, errMsg, exitCode)
	if err := c.taskRepo.UpdateStatusWithOutbox(ctx, taskID, status, updates, event); err != nil {
		return err
	}

	c.updateTaskState(task, status)

	c.checkWorkflowCompletion(ctx, task.WorkflowID.String())
	return nil
}

func (c *WorkflowController) retryDelay(task *model.Task, attempt int) time.Duration {
	base := task.BackoffSecs
	if base == 0 {
		base = 10
	}
	factor := math.Pow(2, float64(attempt-1))
	return time.Duration(float64(base)*factor) * time.Second
}

func (c *WorkflowController) updateTaskState(task *model.Task, status model.TaskStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, ok := c.activeWorkflows[task.WorkflowID.String()]
	if !ok {
		return
	}

	existing, ok := state.tasks[task.ID.String()]
	if !ok {
		return
	}

	oldStatus := existing.Status
	if oldStatus == status {
		return
	}

	if isPendingStatus(oldStatus) {
		state.pending--
	}
	if isRunningStatus(oldStatus) {
		state.running--
	}
	if isFinishedStatus(oldStatus) {
		state.finished--
	}

	if isPendingStatus(status) {
		state.pending++
	}
	if isRunningStatus(status) {
		state.running++
	}
	if isFinishedStatus(status) {
		state.finished++
	}

	existing.Status = status
	state.tasks[task.ID.String()] = existing
}

func (c *WorkflowController) RunReconciler(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.reconcile(ctx)
		}
	}
}

func (c *WorkflowController) reconcile(ctx context.Context) {
	c.logger.Debug("reconcile tick")

	c.loadActiveWorkflows(ctx)

	retryable, err := c.taskRepo.GetRetryableTasks(ctx)
	if err != nil {
		c.logger.Error("failed to load retryable tasks", zap.Error(err))
	} else {
		for i := range retryable {
			task := retryable[i]
			_ = c.ensureWorkflowState(ctx, task.WorkflowID.String())
			updates := map[string]interface{}{
				"next_retry_at": nil,
				"queued_at":     nil,
			}
			event := newTaskStatusEvent(&task, model.TaskPending, "", nil)
			if err := c.taskRepo.UpdateStatusWithOutbox(ctx, task.ID.String(), model.TaskPending, updates, event); err != nil {
				c.logger.Error("failed to reset retry task", zap.String("task_id", task.ID.String()), zap.Error(err))
				continue
			}
			c.updateTaskState(&task, model.TaskPending)
		}
	}

	for _, workflowID := range c.activeWorkflowIDs() {
		c.refreshWorkflowState(ctx, workflowID)
		c.checkWorkflowCompletion(ctx, workflowID)
	}
}

func (c *WorkflowController) loadActiveWorkflows(ctx context.Context) {
	workflows, err := c.workflowRepo.ListByStatus(ctx, model.WorkflowRunning)
	if err != nil {
		c.logger.Error("failed to load running workflows", zap.Error(err))
		return
	}

	for i := range workflows {
		workflow := workflows[i]
		workflowID := workflow.ID.String()
		if c.hasWorkflowState(workflowID) {
			continue
		}

		tasks := make([]*model.Task, 0, len(workflow.Tasks))
		for i := range workflow.Tasks {
			tasks = append(tasks, &workflow.Tasks[i])
		}

		c.initWorkflowState(&workflow, tasks)
	}
}

func (c *WorkflowController) ensureWorkflowState(ctx context.Context, workflowID string) *workflowState {
	if c.hasWorkflowState(workflowID) {
		c.mu.RLock()
		state := c.activeWorkflows[workflowID]
		c.mu.RUnlock()
		return state
	}

	workflow, err := c.workflowRepo.GetByID(ctx, workflowID)
	if err != nil {
		c.logger.Error("failed to load workflow", zap.String("workflow_id", workflowID), zap.Error(err))
		return nil
	}

	tasks := make([]*model.Task, 0, len(workflow.Tasks))
	for i := range workflow.Tasks {
		tasks = append(tasks, &workflow.Tasks[i])
	}

	c.initWorkflowState(workflow, tasks)

	c.mu.RLock()
	state := c.activeWorkflows[workflowID]
	c.mu.RUnlock()
	return state
}

func (c *WorkflowController) hasWorkflowState(workflowID string) bool {
	c.mu.RLock()
	_, ok := c.activeWorkflows[workflowID]
	c.mu.RUnlock()
	return ok
}

func (c *WorkflowController) activeWorkflowIDs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ids := make([]string, 0, len(c.activeWorkflows))
	for id := range c.activeWorkflows {
		ids = append(ids, id)
	}
	return ids
}

func (c *WorkflowController) refreshWorkflowState(ctx context.Context, workflowID string) {
	tasks, err := c.taskRepo.ListByWorkflowID(ctx, workflowID)
	if err != nil {
		c.logger.Error("failed to refresh workflow tasks", zap.String("workflow_id", workflowID), zap.Error(err))
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	state, ok := c.activeWorkflows[workflowID]
	if !ok {
		return
	}

	state.pending = 0
	state.running = 0
	state.finished = 0
	state.tasks = make(map[string]*model.Task, len(tasks))

	for i := range tasks {
		task := tasks[i]
		state.tasks[task.ID.String()] = &tasks[i]
		switch {
		case isRunningStatus(task.Status):
			state.running++
		case isFinishedStatus(task.Status):
			state.finished++
		default:
			state.pending++
		}
	}
}

func (c *WorkflowController) checkWorkflowCompletion(ctx context.Context, workflowID string) {
	c.mu.RLock()
	state, ok := c.activeWorkflows[workflowID]
	if !ok {
		c.mu.RUnlock()
		return
	}

	allFinished := state.pending == 0 && state.running == 0
	failed := false
	for _, task := range state.tasks {
		if task.Status == model.TaskFailed {
			failed = true
			break
		}
	}
	c.mu.RUnlock()

	if !allFinished {
		return
	}

	status := model.WorkflowSucceeded
	errorMsg := ""
	if failed {
		status = model.WorkflowFailed
		errorMsg = "one or more tasks failed"
	}

	event := newWorkflowStatusEvent(workflowID, status, errorMsg)
	if err := c.workflowRepo.UpdateStatusWithOutbox(ctx, workflowID, status, errorMsg, event); err != nil {
		c.logger.Error("failed to update workflow status", zap.String("workflow_id", workflowID), zap.Error(err))
		return
	}

	c.mu.Lock()
	delete(c.activeWorkflows, workflowID)
	c.mu.Unlock()
}

func isPendingStatus(status model.TaskStatus) bool {
	return status == model.TaskPending || status == model.TaskQueued || status == model.TaskRetrying
}

func isRunningStatus(status model.TaskStatus) bool {
	return status == model.TaskRunning
}

func isFinishedStatus(status model.TaskStatus) bool {
	return status == model.TaskSucceeded || status == model.TaskFailed || status == model.TaskSkipped
}

func newWorkflowStatusEvent(workflowID string, status model.WorkflowStatus, errorMsg string) *model.WorkflowEvent {
	payload := model.JSONB{
		"workflow_id": workflowID,
		"status":      string(status),
	}
	if errorMsg != "" {
		payload["error_message"] = errorMsg
	}
	return &model.WorkflowEvent{
		EventID:   uuid.New(),
		EventType: "workflow_status_changed",
		Payload:   payload,
		Status:    model.OutboxStatusPending,
	}
}

func newTaskStatusEvent(task *model.Task, status model.TaskStatus, errMsg string, exitCode *int) *model.WorkflowEvent {
	payload := model.JSONB{
		"task_id":     task.ID.String(),
		"workflow_id": task.WorkflowID.String(),
		"status":      string(status),
	}
	if errMsg != "" {
		payload["error_message"] = errMsg
	}
	if exitCode != nil {
		payload["exit_code"] = *exitCode
	}
	return &model.WorkflowEvent{
		EventID:   uuid.New(),
		EventType: "task_status_changed",
		Payload:   payload,
		Status:    model.OutboxStatusPending,
	}
}

package controller

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/controller/dag"
	"github.com/flowforge/flowforge/pkg/eventbus"
	"github.com/flowforge/flowforge/pkg/model"
	"github.com/flowforge/flowforge/pkg/queue"
	"github.com/flowforge/flowforge/pkg/store/postgres"
)

type WorkflowController struct {
	workflowRepo *postgres.WorkflowRepository
	taskRepo     *postgres.TaskRepository
	taskQueue    *queue.TaskQueue
	bus          *eventbus.Bus
	logger       *zap.Logger
	parser       *dag.Parser
}

func NewWorkflowController(
	workflowRepo *postgres.WorkflowRepository,
	taskRepo *postgres.TaskRepository,
	taskQueue *queue.TaskQueue,
	bus *eventbus.Bus,
	logger *zap.Logger,
) *WorkflowController {
	return &WorkflowController{
		workflowRepo: workflowRepo,
		taskRepo:     taskRepo,
		taskQueue:    taskQueue,
		bus:          bus,
		logger:       logger,
		parser:       dag.NewParser(),
	}
}

func (c *WorkflowController) SubmitWorkflow(ctx context.Context, workflow *model.Workflow) error {
	workflow.Status = model.WorkflowRunning
	if err := c.workflowRepo.Create(ctx, workflow); err != nil {
		return err
	}

	tasks, err := c.parser.Parse(workflow.ID.String(), workflow.DAGSpec)
	if err != nil {
		return err
	}

	if err := c.taskRepo.CreateBatch(ctx, tasks); err != nil {
		return err
	}

	for _, task := range tasks {
		queuedAt := time.Now()
		_ = c.taskRepo.UpdateStatus(ctx, task.ID.String(), model.TaskQueued, map[string]interface{}{"queued_at": &queuedAt})
		_ = c.taskQueue.Enqueue(ctx, task)
		_ = c.bus.Publish(ctx, "tasks", eventbus.Event{
			Type: "task_created",
			Payload: map[string]interface{}{
				"task_id": task.ID.String(),
			},
		})
	}

	return nil
}

func (c *WorkflowController) HandleTaskUpdate(ctx context.Context, taskID string, status model.TaskStatus, errMsg string) error {
	updates := map[string]interface{}{}
	if errMsg != "" {
		updates["error_message"] = errMsg
	}
	return c.taskRepo.UpdateStatus(ctx, taskID, status, updates)
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
}

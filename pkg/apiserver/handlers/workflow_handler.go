package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/flowforge/flowforge/pkg/controller/dag"
	"github.com/flowforge/flowforge/pkg/eventbus"
	"github.com/flowforge/flowforge/pkg/model"
	"github.com/flowforge/flowforge/pkg/store"
	"github.com/flowforge/flowforge/pkg/store/postgres"
	redisclient "github.com/flowforge/flowforge/pkg/store/redis"
)

type WorkflowHandler struct {
	db      *postgres.Store
	redis   *redisclient.Client
	logRepo store.LogStore
	logger  *zap.Logger
}

func NewWorkflowHandler(db *postgres.Store, redis *redisclient.Client, logRepo store.LogStore, logger *zap.Logger) *WorkflowHandler {
	return &WorkflowHandler{db: db, redis: redis, logRepo: logRepo, logger: logger}
}

type workflowCreateRequest struct {
	ProjectID  string                 `json:"project_id" binding:"required"`
	Name       string                 `json:"name" binding:"required"`
	DAGSpec    map[string]interface{} `json:"dag_spec" binding:"required"`
	Parameters map[string]interface{} `json:"parameters"`
	Priority   *int                   `json:"priority"`
	Labels     map[string]string      `json:"labels"`
}

type workflowResponse struct {
	ID         string      `json:"id"`
	ProjectID  string      `json:"project_id"`
	Name       string      `json:"name"`
	Status     string      `json:"status"`
	CreatedAt  string      `json:"created_at"`
	StartedAt  *string     `json:"started_at,omitempty"`
	FinishedAt *string     `json:"finished_at,omitempty"`
	Priority   int         `json:"priority"`
	Labels     model.JSONB `json:"labels"`
}

type workflowDetailResponse struct {
	workflowResponse
	DAGSpec      map[string]interface{} `json:"dag_spec"`
	Parameters   map[string]interface{} `json:"parameters"`
	Tasks        []taskResponse         `json:"tasks"`
	ErrorMessage string                 `json:"error_message,omitempty"`
}

type taskResponse struct {
	ID         string      `json:"id"`
	WorkflowID string      `json:"workflow_id"`
	Name       string      `json:"name"`
	Status     string      `json:"status"`
	Image      string      `json:"image"`
	RetryCount int         `json:"retry_count"`
	ExitCode   *int        `json:"exit_code,omitempty"`
	StartedAt  *string     `json:"started_at,omitempty"`
	FinishedAt *string     `json:"finished_at,omitempty"`
	PodName    string      `json:"pod_name,omitempty"`
	NodeName   string      `json:"node_name,omitempty"`
	Outputs    model.JSONB `json:"outputs"`
}

func (h *WorkflowHandler) Create(c *gin.Context) {
	var req workflowCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request", "details": err.Error()})
		return
	}

	projectID, err := uuid.Parse(req.ProjectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid project_id"})
		return
	}

	priority := 0
	if req.Priority != nil {
		priority = *req.Priority
	}

	labels := model.JSONB{}
	if len(req.Labels) > 0 {
		for key, value := range req.Labels {
			labels[key] = value
		}
	}

	workflow := &model.Workflow{
		ID:         uuid.New(),
		ProjectID:  projectID,
		Name:       req.Name,
		DAGSpec:    model.JSONB(req.DAGSpec),
		Parameters: model.JSONB(req.Parameters),
		Priority:   priority,
		Labels:     labels,
		Status:     model.WorkflowPending,
	}

	parser := dag.NewParser()
	tasks, err := parser.Parse(workflow.ID.String(), workflow.DAGSpec)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid dag_spec", "details": err.Error()})
		return
	}

	ctx := c.Request.Context()
	tx := h.db.DB().WithContext(ctx).Begin()
	if tx.Error != nil {
		h.logger.Error("failed to start transaction", zap.Error(tx.Error))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create workflow"})
		return
	}

	if err := tx.Create(workflow).Error; err != nil {
		tx.Rollback()
		h.logger.Error("failed to create workflow", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create workflow"})
		return
	}

	if len(tasks) > 0 {
		if err := tx.CreateInBatches(tasks, 100).Error; err != nil {
			tx.Rollback()
			h.logger.Error("failed to create tasks", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create workflow tasks"})
			return
		}
	}

	startedAt := time.Now().UTC()
	if err := tx.Model(&model.Workflow{}).
		Where("id = ?", workflow.ID).
		Updates(map[string]interface{}{
			"status":     model.WorkflowRunning,
			"started_at": &startedAt,
			"updated_at": startedAt,
		}).Error; err != nil {
		tx.Rollback()
		h.logger.Error("failed to start workflow", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start workflow"})
		return
	}

	if err := tx.Commit().Error; err != nil {
		h.logger.Error("failed to commit workflow", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create workflow"})
		return
	}

	workflow.Status = model.WorkflowRunning
	workflow.StartedAt = &startedAt

	h.publishTaskCreatedEvents(ctx, tasks)

	c.JSON(http.StatusCreated, mapWorkflow(workflow))
}

func (h *WorkflowHandler) List(c *gin.Context) {
	projectID := strings.TrimSpace(c.Query("project_id"))
	if projectID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "project_id is required"})
		return
	}
	projectUUID, err := uuid.Parse(projectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid project_id"})
		return
	}

	var status *model.WorkflowStatus
	if statusValue := strings.TrimSpace(c.Query("status")); statusValue != "" {
		parsed := model.WorkflowStatus(statusValue)
		if !isValidWorkflowStatus(parsed) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status"})
			return
		}
		status = &parsed
	}

	limit := parseLimit(c.Query("limit"), 20)
	offset := parseOffset(c.Query("offset"))

	repo := postgres.NewWorkflowRepository(h.db.DB())
	workflows, total, err := repo.List(c.Request.Context(), projectUUID.String(), status, limit, offset)
	if err != nil {
		h.logger.Error("failed to list workflows", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list workflows"})
		return
	}

	response := make([]workflowResponse, 0, len(workflows))
	for i := range workflows {
		response = append(response, mapWorkflow(&workflows[i]))
	}

	c.JSON(http.StatusOK, gin.H{
		"workflows": response,
		"total":     total,
	})
}

func (h *WorkflowHandler) Get(c *gin.Context) {
	workflowID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workflow id"})
		return
	}

	repo := postgres.NewWorkflowRepository(h.db.DB())
	workflow, err := repo.GetByID(c.Request.Context(), workflowID.String())
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "workflow not found"})
			return
		}
		h.logger.Error("failed to get workflow", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get workflow"})
		return
	}

	tasks := make([]taskResponse, 0, len(workflow.Tasks))
	for _, task := range workflow.Tasks {
		tasks = append(tasks, mapTask(task))
	}

	detail := workflowDetailResponse{
		workflowResponse: mapWorkflow(workflow),
		DAGSpec:          map[string]interface{}(workflow.DAGSpec),
		Parameters:       map[string]interface{}(workflow.Parameters),
		Tasks:            tasks,
		ErrorMessage:     workflow.ErrorMessage,
	}

	c.JSON(http.StatusOK, detail)
}

func (h *WorkflowHandler) Cancel(c *gin.Context) {
	workflowID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workflow id"})
		return
	}

	now := time.Now().UTC()
	result := h.db.DB().WithContext(c.Request.Context()).Model(&model.Workflow{}).
		Where("id = ?", workflowID).
		Updates(map[string]interface{}{
			"status":      model.WorkflowCancelled,
			"finished_at": &now,
			"updated_at":  now,
		})
	if result.Error != nil {
		h.logger.Error("failed to cancel workflow", zap.Error(result.Error))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to cancel workflow"})
		return
	}
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "workflow not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
}

func (h *WorkflowHandler) ListTasks(c *gin.Context) {
	workflowID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workflow id"})
		return
	}

	var workflow model.Workflow
	if err := h.db.DB().WithContext(c.Request.Context()).First(&workflow, "id = ?", workflowID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "workflow not found"})
			return
		}
		h.logger.Error("failed to load workflow", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load workflow"})
		return
	}

	var tasks []model.Task
	if err := h.db.DB().WithContext(c.Request.Context()).Where("workflow_id = ?", workflowID).Find(&tasks).Error; err != nil {
		h.logger.Error("failed to list tasks", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list tasks"})
		return
	}

	response := make([]taskResponse, 0, len(tasks))
	for _, task := range tasks {
		response = append(response, mapTask(task))
	}

	c.JSON(http.StatusOK, response)
}

func (h *WorkflowHandler) GetLogs(c *gin.Context) {
	taskID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid task id"})
		return
	}

	follow, err := strconv.ParseBool(c.DefaultQuery("follow", "false"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid follow flag"})
		return
	}

	workflowIDStr := strings.TrimSpace(c.Query("workflow_id"))
	level := strings.TrimSpace(c.Query("level"))
	search := strings.TrimSpace(c.Query("search"))

	startTimeStr := strings.TrimSpace(c.Query("start_time"))
	endTimeStr := strings.TrimSpace(c.Query("end_time"))
	limitStr := strings.TrimSpace(c.Query("limit"))

	var startTime *int64
	if startTimeStr != "" {
		value, err := strconv.ParseInt(startTimeStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid start_time"})
			return
		}
		startTime = &value
	}

	var endTime *int64
	if endTimeStr != "" {
		value, err := strconv.ParseInt(endTimeStr, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid end_time"})
			return
		}
		endTime = &value
	}

	limit := 100
	if limitStr != "" {
		value, err := strconv.Atoi(limitStr)
		if err != nil || value <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit"})
			return
		}
		limit = value
	}

	var workflowID *uuid.UUID
	if workflowIDStr != "" {
		value, err := uuid.Parse(workflowIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workflow id"})
			return
		}
		workflowID = &value
	}

	query := store.LogQuery{
		TaskID:    taskID.String(),
		StartTime:  startTime,
		EndTime:    endTime,
		Level:      level,
		Search:     search,
		Limit:      limit,
	}

	if workflowID != nil {
		query.WorkflowID = workflowID.String()
	}

	if follow {
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")

		histLogs, err := h.logRepo.Query(c.Request.Context(), query)
		if err != nil {
			h.logger.Error("failed to query historical logs", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query logs"})
			return
		}

		for _, log := range histLogs {
			c.SSEvent("log", log)
		}

		channel := "logs:task:" + taskID.String()
		pubsub := h.redis.Client().Subscribe(c.Request.Context(), channel)
		defer pubsub.Close()

		ch := pubsub.Channel()

		for {
			select {
			case msg := <-ch:
				c.SSEvent("log", msg.Payload)
				c.Writer.Flush()
			case <-c.Request.Context().Done():
				return
			}
		}
	}

	logs, err := h.logRepo.Query(c.Request.Context(), query)
	if err != nil {
		h.logger.Error("failed to query logs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query logs"})
		return
	}

	c.JSON(http.StatusOK, logs)
}

func (h *WorkflowHandler) publishTaskCreatedEvents(ctx context.Context, tasks []*model.Task) {
	if h.redis == nil {
		return
	}
	bus := eventbus.NewBus(h.redis.Client())
	for _, task := range tasks {
		taskEvent := eventbus.TaskEvent{
			TaskID:     task.ID.String(),
			WorkflowID: task.WorkflowID.String(),
			Status:     string(task.Status),
		}
		if event, err := eventbus.NewEvent("task_created", taskEvent); err == nil {
			_ = bus.Publish(ctx, eventbus.ChannelTask, event)
		}
	}
}

func mapWorkflow(workflow *model.Workflow) workflowResponse {
	return workflowResponse{
		ID:         workflow.ID.String(),
		ProjectID:  workflow.ProjectID.String(),
		Name:       workflow.Name,
		Status:     string(workflow.Status),
		CreatedAt:  workflow.CreatedAt.UTC().Format(timeRFC3339Nano),
		StartedAt:  formatTime(workflow.StartedAt),
		FinishedAt: formatTime(workflow.FinishedAt),
		Priority:   workflow.Priority,
		Labels:     workflow.Labels,
	}
}

func mapTask(task model.Task) taskResponse {
	return taskResponse{
		ID:         task.ID.String(),
		WorkflowID: task.WorkflowID.String(),
		Name:       task.Name,
		Status:     string(task.Status),
		Image:      task.Image,
		RetryCount: task.RetryCount,
		ExitCode:   task.ExitCode,
		StartedAt:  formatTime(task.StartedAt),
		FinishedAt: formatTime(task.FinishedAt),
		PodName:    task.PodName,
		NodeName:   task.NodeName,
		Outputs:    task.Outputs,
	}
}

func isValidWorkflowStatus(status model.WorkflowStatus) bool {
	switch status {
	case model.WorkflowPending, model.WorkflowRunning, model.WorkflowSucceeded, model.WorkflowFailed, model.WorkflowCancelled, model.WorkflowPaused:
		return true
	default:
		return false
	}
}

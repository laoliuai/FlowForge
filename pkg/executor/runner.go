package executor

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/flowforge/flowforge/pkg/eventbus"
	"github.com/flowforge/flowforge/pkg/model"
	"github.com/flowforge/flowforge/pkg/queue"
	"github.com/flowforge/flowforge/pkg/quota"
	"github.com/flowforge/flowforge/pkg/store/postgres"
)

type Runner struct {
	queue                 *queue.TaskQueue
	taskRepo              *postgres.TaskRepository
	quotaManager          *quota.Manager
	bus                   *eventbus.Bus
	podExecutor           *PodExecutor
	k8sClient             kubernetes.Interface
	redisClient           redis.UniversalClient
	logger                *zap.Logger
	defaultNamespace      string
	pollInterval          time.Duration
	heartbeatInterval     time.Duration
	heartbeatTimeout      time.Duration
	heartbeatScanInterval time.Duration
}

func NewRunner(
	queue *queue.TaskQueue,
	taskRepo *postgres.TaskRepository,
	quotaManager *quota.Manager,
	bus *eventbus.Bus,
	podExecutor *PodExecutor,
	k8sClient kubernetes.Interface,
	redisClient redis.UniversalClient,
	logger *zap.Logger,
	defaultNamespace string,
	heartbeatInterval time.Duration,
	heartbeatTimeout time.Duration,
	heartbeatScanInterval time.Duration,
) *Runner {
	return &Runner{
		queue:                 queue,
		taskRepo:              taskRepo,
		quotaManager:          quotaManager,
		bus:                   bus,
		podExecutor:           podExecutor,
		k8sClient:             k8sClient,
		redisClient:           redisClient,
		logger:                logger,
		defaultNamespace:      defaultNamespace,
		pollInterval:          2 * time.Second,
		heartbeatInterval:     heartbeatInterval,
		heartbeatTimeout:      heartbeatTimeout,
		heartbeatScanInterval: heartbeatScanInterval,
	}
}

func (r *Runner) Run(ctx context.Context) {
	if r.redisClient != nil && r.heartbeatTimeout > 0 && r.heartbeatScanInterval > 0 {
		go r.monitorHeartbeats(ctx)
	}
	for {
		if err := r.queue.Consume(ctx, r.handleQueuedTask); err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			r.logger.Error("task queue consume failed", zap.Error(err))
			time.Sleep(r.pollInterval)
		}
	}
}

func (r *Runner) handleQueuedTask(ctx context.Context, task *model.Task) error {
	if task == nil {
		return errors.New("task is nil")
	}
	return r.handleTask(ctx, task.ID.String())
}

func (r *Runner) handleTask(ctx context.Context, taskID string) error {
	task, err := r.taskRepo.GetByIDWithWorkflow(ctx, taskID)
	if err != nil {
		r.logger.Error("failed to load task", zap.String("task_id", taskID), zap.Error(err))
		return err
	}

	tenantID, projectID, namespace, err := r.taskContext(task)
	if err != nil {
		return r.recordFailure(ctx, task, err.Error(), nil)
	}

	pod, err := r.podExecutor.ExecuteTaskInNamespace(ctx, task, tenantID, projectID, namespace)
	if err != nil {
		if failErr := r.recordFailure(ctx, task, err.Error(), nil); failErr != nil {
			return failErr
		}
		r.releaseQuota(ctx, task)
		return err
	}

	now := time.Now()
	updates := map[string]interface{}{
		"pod_name":   pod.Name,
		"pod_uid":    string(pod.UID),
		"started_at": &now,
	}
	if pod.Spec.NodeName != "" {
		updates["node_name"] = pod.Spec.NodeName
	}
	event := newTaskStatusEvent(task, model.TaskRunning, "", nil)
	if err := r.taskRepo.UpdateStatusWithOutbox(ctx, task.ID.String(), model.TaskRunning, updates, event); err != nil {
		r.logger.Error("failed to update task to running", zap.String("task_id", task.ID.String()), zap.Error(err))
	} else {
		r.publishTaskEvent(ctx, task, model.TaskRunning, "", nil)
	}

	stopHeartbeat := r.startHeartbeat(ctx, task.ID.String())
	defer stopHeartbeat()

	completed, err := r.waitForPodCompletion(ctx, namespace, pod.Name)
	if err != nil {
		if failErr := r.recordFailure(ctx, task, err.Error(), nil); failErr != nil {
			return failErr
		}
		r.releaseQuota(ctx, task)
		return err
	}

	status, message, exitCode := r.podOutcome(completed)
	if status == model.TaskFailed {
		if failErr := r.recordFailure(ctx, task, message, exitCode); failErr != nil {
			return failErr
		}
		r.releaseQuota(ctx, task)
		return fmt.Errorf("task %s failed", task.ID.String())
	}

	finishTime := time.Now()
	finalUpdates := map[string]interface{}{
		"finished_at": &finishTime,
	}
	if exitCode != nil {
		finalUpdates["exit_code"] = exitCode
	}
	if completed.Spec.NodeName != "" {
		finalUpdates["node_name"] = completed.Spec.NodeName
	}
	if message != "" {
		finalUpdates["error_message"] = message
	}

	finalEvent := newTaskStatusEvent(task, status, message, exitCode)
	if err := r.taskRepo.UpdateStatusWithOutbox(ctx, task.ID.String(), status, finalUpdates, finalEvent); err != nil {
		r.logger.Error("failed to update task completion", zap.String("task_id", task.ID.String()), zap.Error(err))
	} else {
		r.publishTaskEvent(ctx, task, status, message, exitCode)
	}

	r.releaseQuota(ctx, task)
	return nil
}

func (r *Runner) taskContext(task *model.Task) (string, string, string, error) {
	if task.Workflow == nil || task.Workflow.Project == nil || task.Workflow.Project.Group == nil {
		return "", "", "", fmt.Errorf("task is missing workflow/project context")
	}

	project := task.Workflow.Project
	group := project.Group
	namespace := project.Namespace
	if namespace == "" {
		namespace = r.defaultNamespace
	}
	if namespace == "" {
		namespace = "default"
	}

	return group.TenantID.String(), project.ID.String(), namespace, nil
}

func (r *Runner) waitForPodCompletion(ctx context.Context, namespace, podName string) (*corev1.Pod, error) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			pod, err := r.k8sClient.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				return nil, err
			}
			switch pod.Status.Phase {
			case corev1.PodSucceeded, corev1.PodFailed:
				return pod, nil
			}
		}
	}
}

func (r *Runner) podOutcome(pod *corev1.Pod) (model.TaskStatus, string, *int) {
	status := model.TaskFailed
	if pod.Status.Phase == corev1.PodSucceeded {
		status = model.TaskSucceeded
	}

	exitCode := extractExitCode(pod)
	message := ""
	if status == model.TaskFailed {
		message = failureMessage(pod)
	}

	return status, message, exitCode
}

func extractExitCode(pod *corev1.Pod) *int {
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name != "main" {
			continue
		}
		if container.State.Terminated != nil {
			code := int(container.State.Terminated.ExitCode)
			return &code
		}
	}
	return nil
}

func failureMessage(pod *corev1.Pod) string {
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name != "main" {
			continue
		}
		if container.State.Terminated != nil {
			if container.State.Terminated.Message != "" {
				return container.State.Terminated.Message
			}
			if container.State.Terminated.Reason != "" {
				return container.State.Terminated.Reason
			}
		}
	}

	if pod.Status.Message != "" {
		return pod.Status.Message
	}
	return pod.Status.Reason
}

func (r *Runner) recordFailure(ctx context.Context, task *model.Task, message string, exitCode *int) error {
	if task.RetryCount < task.RetryLimit {
		return r.markTaskRetrying(ctx, task, message, exitCode)
	}
	return r.markTaskFailed(ctx, task, message, exitCode)
}

func (r *Runner) markTaskRetrying(ctx context.Context, task *model.Task, message string, exitCode *int) error {
	updates := map[string]interface{}{}
	if message != "" {
		updates["error_message"] = message
	}
	if exitCode != nil {
		updates["exit_code"] = exitCode
	}

	finishTime := time.Now()
	updates["finished_at"] = &finishTime
	updates["retry_count"] = task.RetryCount + 1
	nextRetry := finishTime.Add(r.retryDelay(task, task.RetryCount+1))
	updates["next_retry_at"] = &nextRetry

	event := newTaskStatusEvent(task, model.TaskRetrying, message, exitCode)
	event.Payload["retry_count"] = task.RetryCount + 1
	event.Payload["next_retry_at"] = nextRetry
	if err := r.taskRepo.UpdateStatusWithOutbox(ctx, task.ID.String(), model.TaskRetrying, updates, event); err != nil {
		r.logger.Error("failed to mark task retrying", zap.String("task_id", task.ID.String()), zap.Error(err))
		return err
	}

	r.publishTaskEvent(ctx, task, model.TaskRetrying, message, exitCode)
	return nil
}

func (r *Runner) markTaskFailed(ctx context.Context, task *model.Task, message string, exitCode *int) error {
	updates := map[string]interface{}{}
	if message != "" {
		updates["error_message"] = message
	}
	if exitCode != nil {
		updates["exit_code"] = exitCode
	}

	finishTime := time.Now()
	updates["finished_at"] = &finishTime

	event := newTaskStatusEvent(task, model.TaskFailed, message, exitCode)
	if err := r.taskRepo.UpdateStatusWithOutbox(ctx, task.ID.String(), model.TaskFailed, updates, event); err != nil {
		r.logger.Error("failed to mark task failed", zap.String("task_id", task.ID.String()), zap.Error(err))
		return err
	}

	r.publishTaskEvent(ctx, task, model.TaskFailed, message, exitCode)
	return nil
}

func (r *Runner) releaseQuota(ctx context.Context, task *model.Task) {
	if task.Workflow == nil || task.Workflow.Project == nil {
		return
	}

	if err := r.quotaManager.Release(ctx, task.Workflow.Project.ID, task.GetResourceRequest()); err != nil {
		r.logger.Warn("failed to release quota", zap.String("task_id", task.ID.String()), zap.Error(err))
	}
}

func (r *Runner) publishTaskEvent(ctx context.Context, task *model.Task, status model.TaskStatus, message string, exitCode *int) {
	taskEvent := eventbus.TaskEvent{
		TaskID:     task.ID.String(),
		WorkflowID: task.WorkflowID.String(),
		Status:     string(status),
		Message:    message,
		ExitCode:   exitCode,
	}
	if event, err := eventbus.NewEvent("task_status", taskEvent); err == nil {
		_ = r.bus.Publish(ctx, eventbus.ChannelTask, event)
	}
}

func (r *Runner) retryDelay(task *model.Task, attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}
	base := task.BackoffSecs
	if base == 0 {
		base = 10
	}
	factor := math.Pow(2, float64(attempt-1))
	return time.Duration(float64(base)*factor) * time.Second
}

func newTaskStatusEvent(task *model.Task, status model.TaskStatus, message string, exitCode *int) *model.WorkflowEvent {
	payload := model.JSONB{
		"task_id":     task.ID.String(),
		"workflow_id": task.WorkflowID.String(),
		"status":      string(status),
	}
	if message != "" {
		payload["error_message"] = message
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

func (r *Runner) startHeartbeat(ctx context.Context, taskID string) func() {
	if r.redisClient == nil || r.heartbeatInterval <= 0 {
		return func() {}
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)

	r.sendHeartbeat(heartbeatCtx, taskID)
	go r.heartbeatLoop(heartbeatCtx, taskID)

	return func() {
		cancel()
		r.clearHeartbeat(context.Background(), taskID)
	}
}

func (r *Runner) heartbeatLoop(ctx context.Context, taskID string) {
	ticker := time.NewTicker(r.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sendHeartbeat(ctx, taskID)
		}
	}
}

func (r *Runner) sendHeartbeat(ctx context.Context, taskID string) {
	if r.redisClient == nil {
		return
	}
	if err := r.redisClient.ZAdd(ctx, "ff:heartbeat", redis.Z{
		Score:  float64(time.Now().Unix()),
		Member: taskID,
	}).Err(); err != nil {
		r.logger.Debug("failed to update heartbeat", zap.String("task_id", taskID), zap.Error(err))
		return
	}
	if err := r.redisClient.Set(ctx, fmt.Sprintf("ff:heartbeat:%s", taskID), time.Now().Unix(), r.heartbeatTimeout*2).Err(); err != nil {
		r.logger.Debug("failed to update heartbeat key", zap.String("task_id", taskID), zap.Error(err))
	}
}

func (r *Runner) clearHeartbeat(ctx context.Context, taskID string) {
	if r.redisClient == nil {
		return
	}
	pipe := r.redisClient.Pipeline()
	pipe.ZRem(ctx, "ff:heartbeat", taskID)
	pipe.Del(ctx, fmt.Sprintf("ff:heartbeat:%s", taskID))
	if _, err := pipe.Exec(ctx); err != nil {
		r.logger.Debug("failed to clear heartbeat", zap.String("task_id", taskID), zap.Error(err))
	}
}

func (r *Runner) monitorHeartbeats(ctx context.Context) {
	ticker := time.NewTicker(r.heartbeatScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.recoverStaleTasks(ctx)
		}
	}
}

func (r *Runner) recoverStaleTasks(ctx context.Context) {
	if r.redisClient == nil {
		return
	}

	cutoff := time.Now().Add(-r.heartbeatTimeout).Unix()
	stale, err := r.redisClient.ZRangeByScore(ctx, "ff:heartbeat", &redis.ZRangeBy{
		Min: "-inf",
		Max: fmt.Sprintf("%d", cutoff),
	}).Result()
	if err != nil {
		r.logger.Warn("failed to scan stale heartbeats", zap.Error(err))
		return
	}

	for _, taskID := range stale {
		removed, err := r.redisClient.ZRem(ctx, "ff:heartbeat", taskID).Result()
		if err != nil {
			r.logger.Warn("failed to claim stale heartbeat", zap.String("task_id", taskID), zap.Error(err))
			continue
		}
		if removed == 0 {
			continue
		}

		task, err := r.taskRepo.GetByIDWithWorkflow(ctx, taskID)
		if err != nil {
			r.logger.Warn("failed to load stale task", zap.String("task_id", taskID), zap.Error(err))
			continue
		}
		if task.Status != model.TaskRunning {
			continue
		}

		r.logger.Warn("task heartbeat timeout", zap.String("task_id", taskID))
		if err := r.podExecutor.CancelTask(ctx, task); err != nil {
			r.logger.Warn("failed to cancel stale task pod", zap.String("task_id", taskID), zap.Error(err))
		}
		if err := r.recordFailure(ctx, task, "heartbeat timeout", nil); err != nil {
			r.logger.Warn("failed to mark task retrying after timeout", zap.String("task_id", taskID), zap.Error(err))
		}
		r.releaseQuota(ctx, task)
	}
}

package statuscollector

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/flowforge/flowforge/pkg/eventbus"
	"github.com/flowforge/flowforge/pkg/model"
)

const (
	labelWorkflowID = "flowforge.io/workflow-id"
	labelTaskID     = "flowforge.io/task-id"
	labelWorkload   = "flowforge.io/workload"
	mainContainer   = "main"
)

type statusSnapshot struct {
	status   model.TaskStatus
	message  string
	exitCode *int
}

type Collector struct {
	k8sClient kubernetes.Interface
	bus       *eventbus.Bus
	logger    *zap.Logger
	namespace string
	nodeName  string

	mu      sync.Mutex
	podSeen map[string]statusSnapshot
}

func NewCollector(k8sClient kubernetes.Interface, bus *eventbus.Bus, logger *zap.Logger, namespace, nodeName string) *Collector {
	return &Collector{
		k8sClient: k8sClient,
		bus:       bus,
		logger:    logger,
		namespace: namespace,
		nodeName:  nodeName,
		podSeen:   make(map[string]statusSnapshot),
	}
}

func (c *Collector) Run(ctx context.Context) error {
	namespace := c.namespace
	if namespace == "" {
		namespace = corev1.NamespaceAll
	}

	options := []informers.SharedInformerOption{
		informers.WithNamespace(namespace),
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = fmt.Sprintf("%s=true", labelWorkload)
			if c.nodeName != "" {
				opts.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", c.nodeName).String()
			}
		}),
	}

	factory := informers.NewSharedInformerFactoryWithOptions(c.k8sClient, 0, options...)
	informer := factory.Core().V1().Pods().Informer()

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			c.handlePod(ctx, obj)
		},
		UpdateFunc: func(_, obj interface{}) {
			c.handlePod(ctx, obj)
		},
		DeleteFunc: func(obj interface{}) {
			pod := extractPod(obj)
			if pod == nil {
				return
			}
			c.mu.Lock()
			delete(c.podSeen, string(pod.UID))
			c.mu.Unlock()
		},
	})

	factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return fmt.Errorf("failed to sync pod informer")
	}

	<-ctx.Done()
	return nil
}

func (c *Collector) handlePod(ctx context.Context, obj interface{}) {
	pod := extractPod(obj)
	if pod == nil {
		return
	}

	status, message, exitCode, ok := deriveTaskStatus(pod)
	if !ok {
		return
	}

	taskID := pod.Labels[labelTaskID]
	workflowID := pod.Labels[labelWorkflowID]
	if taskID == "" || workflowID == "" {
		c.logger.Debug("pod missing task identifiers", zap.String("pod", pod.Name))
		return
	}

	if !c.shouldPublish(string(pod.UID), status, message, exitCode) {
		return
	}

	event := eventbus.TaskEvent{
		TaskID:     taskID,
		WorkflowID: workflowID,
		Status:     string(status),
		Message:    message,
		ExitCode:   exitCode,
	}

	payload, err := eventbus.NewEvent("task_status", event)
	if err != nil {
		c.logger.Warn("failed to create task event", zap.Error(err))
		return
	}

	if err := c.bus.Publish(ctx, eventbus.ChannelTask, payload); err != nil {
		c.logger.Warn("failed to publish task status", zap.Error(err))
		return
	}

	c.logger.Info("published task status", zap.String("task_id", taskID), zap.String("status", string(status)))
}

func (c *Collector) shouldPublish(podUID string, status model.TaskStatus, message string, exitCode *int) bool {
	snapshot := statusSnapshot{status: status, message: message, exitCode: exitCode}

	c.mu.Lock()
	defer c.mu.Unlock()

	previous, ok := c.podSeen[podUID]
	if ok && snapshotsEqual(previous, snapshot) {
		return false
	}

	c.podSeen[podUID] = snapshot
	return true
}

func snapshotsEqual(a, b statusSnapshot) bool {
	if a.status != b.status || a.message != b.message {
		return false
	}
	if a.exitCode == nil && b.exitCode == nil {
		return true
	}
	if a.exitCode == nil || b.exitCode == nil {
		return false
	}
	return *a.exitCode == *b.exitCode
}

func extractPod(obj interface{}) *corev1.Pod {
	pod, ok := obj.(*corev1.Pod)
	if ok {
		return pod
	}
	tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
	if !ok {
		return nil
	}
	pod, ok = tombstone.Obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	return pod
}

func deriveTaskStatus(pod *corev1.Pod) (model.TaskStatus, string, *int, bool) {
	switch pod.Status.Phase {
	case corev1.PodRunning:
		return model.TaskRunning, "", nil, true
	case corev1.PodSucceeded:
		return model.TaskSucceeded, "", extractExitCode(pod), true
	case corev1.PodFailed:
		return model.TaskFailed, failureMessage(pod), extractExitCode(pod), true
	default:
		return "", "", nil, false
	}
}

func extractExitCode(pod *corev1.Pod) *int {
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name != mainContainer {
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
		if container.Name != mainContainer {
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

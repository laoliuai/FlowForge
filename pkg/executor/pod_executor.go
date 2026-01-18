package executor

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/flowforge/flowforge/pkg/model"
)

type PodExecutor struct {
	k8sClient   kubernetes.Interface
	sdkInjector *SDKInjector
	namespace   string
}

func NewPodExecutor(k8sClient kubernetes.Interface, sdkImage string, namespace string) *PodExecutor {
	return &PodExecutor{
		k8sClient:   k8sClient,
		sdkInjector: NewSDKInjector(sdkImage),
		namespace:   namespace,
	}
}

func (e *PodExecutor) ExecuteTask(ctx context.Context, task *model.Task, tenantID, projectID string) (*corev1.Pod, error) {
	namespace := e.namespace
	if namespace == "" {
		namespace = "default"
	}
	pod := e.buildPod(task, tenantID, projectID, namespace)

	created, err := e.k8sClient.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		if errors.IsAlreadyExists(err) {
			return e.k8sClient.CoreV1().Pods(namespace).Get(ctx, pod.Name, metav1.GetOptions{})
		}
		return nil, fmt.Errorf("failed to create pod: %w", err)
	}

	return created, nil
}

func (e *PodExecutor) buildPod(task *model.Task, tenantID, projectID, namespace string) *corev1.Pod {
	podName := fmt.Sprintf("ff-%s-%s", task.WorkflowID.String()[:8], task.Name)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "flowforge",
				"flowforge.io/workflow-id":     task.WorkflowID.String(),
				"flowforge.io/task-id":         task.ID.String(),
				"flowforge.io/task-name":       task.Name,
				"flowforge.io/tenant-id":       tenantID,
				"flowforge.io/project-id":      projectID,
				"flowforge.io/workload":        "true",
			},
			Annotations: map[string]string{
				"flowforge.io/created-at": time.Now().Format(time.RFC3339),
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:    "main",
					Image:   task.Image,
					Command: task.Command,
					Args:    task.Args,
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%dm", task.CPURequest)),
							corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dMi", task.MemoryRequest)),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%dm", task.CPULimit)),
							corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dMi", task.MemoryLimit)),
						},
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "flowforge-sdk",
							MountPath: "/flowforge",
						},
					},
				},
			},
			InitContainers: []corev1.Container{
				e.sdkInjector.GetInitContainer(),
			},
			Volumes: []corev1.Volume{
				e.sdkInjector.GetVolume(),
			},
		},
	}

	pod.Spec.Containers[0].Env = append(
		pod.Spec.Containers[0].Env,
		e.sdkInjector.GetEnvVars(task, tenantID, projectID)...,
	)

	if task.GPURequest > 0 {
		pod.Spec.Containers[0].Resources.Limits["nvidia.com/gpu"] =
			resource.MustParse(fmt.Sprintf("%d", task.GPURequest))
	}

	return pod
}

func (e *PodExecutor) CancelTask(ctx context.Context, task *model.Task) error {
	namespace := e.namespace
	if namespace == "" {
		namespace = "default"
	}
	podName := fmt.Sprintf("ff-%s-%s", task.WorkflowID.String()[:8], task.Name)
	return e.k8sClient.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{})
}

func (e *PodExecutor) GetTaskPod(ctx context.Context, task *model.Task) (*corev1.Pod, error) {
	namespace := e.namespace
	if namespace == "" {
		namespace = "default"
	}
	podName := fmt.Sprintf("ff-%s-%s", task.WorkflowID.String()[:8], task.Name)
	return e.k8sClient.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
}

package executor

import (
	"os"

	corev1 "k8s.io/api/core/v1"

	"github.com/flowforge/flowforge/pkg/model"
)

type SDKInjector struct {
	sdkImage string
}

func NewSDKInjector(sdkImage string) *SDKInjector {
	return &SDKInjector{sdkImage: sdkImage}
}

func (i *SDKInjector) GetInitContainer() corev1.Container {
	return corev1.Container{
		Name:    "flowforge-sdk-init",
		Image:   i.sdkImage,
		Command: []string{"/bin/sh", "-c"},
		Args:    []string{"cp -r /sdk/* /flowforge/"},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "flowforge-sdk",
				MountPath: "/flowforge",
			},
		},
	}
}

func (i *SDKInjector) GetVolume() corev1.Volume {
	return corev1.Volume{
		Name: "flowforge-sdk",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	}
}

func (i *SDKInjector) GetEnvVars(task *model.Task, tenantID, projectID string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{Name: "FLOWFORGE_TASK_ID", Value: task.ID.String()},
		{Name: "FLOWFORGE_WORKFLOW_ID", Value: task.WorkflowID.String()},
		{Name: "FLOWFORGE_TENANT_ID", Value: tenantID},
		{Name: "FLOWFORGE_PROJECT_ID", Value: projectID},
		{Name: "FLOWFORGE_API_ENDPOINT", Value: os.Getenv("FLOWFORGE_API_ENDPOINT")},
		{Name: "FLOWFORGE_REDIS_ENDPOINT", Value: os.Getenv("FLOWFORGE_REDIS_ENDPOINT")},
		{Name: "FLOWFORGE_LOG_ENDPOINT", Value: os.Getenv("FLOWFORGE_LOG_ENDPOINT")},
		{
			Name: "FLOWFORGE_AUTH_TOKEN",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "flowforge-task-tokens",
					},
					Key: task.ID.String(),
				},
			},
		},
		{Name: "PYTHONPATH", Value: "/flowforge/python:$PYTHONPATH"},
	}
}

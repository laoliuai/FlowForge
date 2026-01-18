package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	WorkflowsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flowforge_workflows_total",
			Help: "Total number of workflows by status",
		},
		[]string{"tenant_id", "project_id", "status"},
	)

	TasksTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flowforge_tasks_total",
			Help: "Total number of tasks by status",
		},
		[]string{"tenant_id", "workflow_id", "status"},
	)

	TaskDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "flowforge_task_duration_seconds",
			Help:    "Task execution duration in seconds",
			Buckets: prometheus.ExponentialBuckets(1, 2, 15),
		},
		[]string{"tenant_id", "template"},
	)

	QueueDepth = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flowforge_queue_depth",
			Help: "Number of tasks in queue by priority",
		},
		[]string{"tenant_id", "priority"},
	)

	QuotaUsage = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "flowforge_quota_usage",
			Help: "Current quota usage",
		},
		[]string{"tenant_id", "entity_type", "resource"},
	)

	RetryCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "flowforge_task_retries_total",
			Help: "Total number of task retries",
		},
		[]string{"tenant_id", "template"},
	)
)

package quota

import (
	"context"

	"github.com/flowforge/flowforge/pkg/model"
	"github.com/flowforge/flowforge/pkg/store/postgres"
)

type Manager struct {
	store *postgres.Store
}

func NewManager(store *postgres.Store) *Manager {
	return &Manager{store: store}
}

func (m *Manager) CanScheduleTask(ctx context.Context, task *model.Task) bool {
	usage := m.currentUsage(ctx, task)
	limit := m.currentLimits(ctx, task)
	return usage.CPUUsed+task.CPURequest <= limit.CPULimit &&
		usage.MemoryUsed+task.MemoryRequest <= limit.MemoryLimit &&
		usage.GPUUsed+task.GPURequest <= limit.GPULimit &&
		usage.PodsRunning+1 <= limit.MaxConcurrentPods
}

func (m *Manager) currentUsage(ctx context.Context, task *model.Task) *model.QuotaUsage {
	usage := &model.QuotaUsage{}
	_ = m.store.DB().WithContext(ctx).Where("entity_id = ?", task.WorkflowID).First(usage).Error
	return usage
}

func (m *Manager) currentLimits(ctx context.Context, task *model.Task) *model.Quota {
	quota := &model.Quota{}
	_ = m.store.DB().WithContext(ctx).First(quota).Error
	return quota
}

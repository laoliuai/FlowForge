package quota

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/flowforge/flowforge/pkg/model"
	"github.com/flowforge/flowforge/pkg/store/postgres"
)

type Manager struct {
	store *postgres.Store
}

type HierarchyLevel struct {
	EntityType string
	EntityID   uuid.UUID
}

type ResourceLimits struct {
	CPULimit          int
	CPURequest        int
	MemoryLimit       int
	MemoryRequest     int
	GPULimit          int
	MaxConcurrentPods int
	MaxConcurrentWF   int
	StorageLimit      int
	PriorityWeight    int
}

type ResourceUsage struct {
	CPUUsed         int
	MemoryUsed      int
	GPUUsed         int
	PodsRunning     int
	WorkflowsActive int
	StorageUsed     int
}

type QuotaUsageSummary struct {
	EntityType         string
	EntityID           uuid.UUID
	Limits             ResourceLimits
	Used               ResourceUsage
	Available          ResourceUsage
	UtilizationPercent float64
}

func NewManager(store *postgres.Store) *Manager {
	return &Manager{store: store}
}

func (m *Manager) CanScheduleTask(ctx context.Context, task *model.Task) bool {
	projectID, err := m.getProjectIDForTask(ctx, task)
	if err != nil {
		return false
	}
	ok, _ := m.CanSchedule(ctx, projectID, task.GetResourceRequest())
	return ok
}

func (m *Manager) CanSchedule(ctx context.Context, projectID uuid.UUID, req model.ResourceRequest) (bool, string) {
	hierarchy, err := m.getHierarchy(ctx, projectID)
	if err != nil {
		return false, err.Error()
	}

	for _, level := range hierarchy {
		quota, usage, err := m.getQuotaInfo(ctx, level.EntityType, level.EntityID)
		if err != nil {
			return false, err.Error()
		}
		if quota == nil {
			continue
		}

		if usage.CPUUsed+req.CPU > quota.CPULimit {
			return false, fmt.Sprintf("CPU quota exceeded at %s level", level.EntityType)
		}
		if usage.MemoryUsed+req.Memory > quota.MemoryLimit {
			return false, fmt.Sprintf("memory quota exceeded at %s level", level.EntityType)
		}
		if usage.GPUUsed+req.GPU > quota.GPULimit {
			return false, fmt.Sprintf("GPU quota exceeded at %s level", level.EntityType)
		}
		if usage.PodsRunning+1 > quota.MaxConcurrentPods {
			return false, fmt.Sprintf("pod quota exceeded at %s level", level.EntityType)
		}
	}

	return true, ""
}

func (m *Manager) Reserve(ctx context.Context, projectID uuid.UUID, req model.ResourceRequest) error {
	hierarchy, err := m.getHierarchy(ctx, projectID)
	if err != nil {
		return err
	}

	tx := m.store.DB().WithContext(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}

	for _, level := range hierarchy {
		quota, _, err := m.getQuotaInfo(ctx, level.EntityType, level.EntityID)
		if err != nil {
			tx.Rollback()
			return err
		}
		if quota == nil {
			continue
		}

		usage := &model.QuotaUsage{}
		if err := tx.Where("quota_id = ? AND entity_type = ? AND entity_id = ?", quota.ID, level.EntityType, level.EntityID).
			FirstOrCreate(usage, model.QuotaUsage{
				QuotaID:    quota.ID,
				EntityType: level.EntityType,
				EntityID:   level.EntityID,
			}).Error; err != nil {
			tx.Rollback()
			return err
		}

		if err := tx.Model(usage).Updates(map[string]interface{}{
			"cpu_used":     gorm.Expr("cpu_used + ?", req.CPU),
			"memory_used":  gorm.Expr("memory_used + ?", req.Memory),
			"gpu_used":     gorm.Expr("gpu_used + ?", req.GPU),
			"pods_running": gorm.Expr("pods_running + 1"),
			"updated_at":   gorm.Expr("NOW()"),
		}).Error; err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit().Error
}

func (m *Manager) Release(ctx context.Context, projectID uuid.UUID, req model.ResourceRequest) error {
	hierarchy, err := m.getHierarchy(ctx, projectID)
	if err != nil {
		return err
	}

	tx := m.store.DB().WithContext(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}

	for _, level := range hierarchy {
		quota, _, err := m.getQuotaInfo(ctx, level.EntityType, level.EntityID)
		if err != nil {
			tx.Rollback()
			return err
		}
		if quota == nil {
			continue
		}

		if err := tx.Model(&model.QuotaUsage{}).
			Where("quota_id = ? AND entity_type = ? AND entity_id = ?", quota.ID, level.EntityType, level.EntityID).
			Updates(map[string]interface{}{
				"cpu_used":     gorm.Expr("GREATEST(cpu_used - ?, 0)", req.CPU),
				"memory_used":  gorm.Expr("GREATEST(memory_used - ?, 0)", req.Memory),
				"gpu_used":     gorm.Expr("GREATEST(gpu_used - ?, 0)", req.GPU),
				"pods_running": gorm.Expr("GREATEST(pods_running - 1, 0)"),
				"updated_at":   gorm.Expr("NOW()"),
			}).Error; err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit().Error
}

func (m *Manager) GetUsageSummary(ctx context.Context, entityType string, entityID uuid.UUID) (*QuotaUsageSummary, error) {
	quota, usage, err := m.getQuotaInfo(ctx, entityType, entityID)
	if err != nil {
		return nil, err
	}
	if quota == nil {
		return nil, fmt.Errorf("quota not found for %s %s", entityType, entityID)
	}

	limits := ResourceLimits{
		CPULimit:          quota.CPULimit,
		CPURequest:        quota.CPURequest,
		MemoryLimit:       quota.MemoryLimit,
		MemoryRequest:     quota.MemoryRequest,
		GPULimit:          quota.GPULimit,
		MaxConcurrentPods: quota.MaxConcurrentPods,
		MaxConcurrentWF:   quota.MaxConcurrentWF,
		StorageLimit:      quota.StorageLimit,
		PriorityWeight:    quota.PriorityWeight,
	}

	used := ResourceUsage{
		CPUUsed:         usage.CPUUsed,
		MemoryUsed:      usage.MemoryUsed,
		GPUUsed:         usage.GPUUsed,
		PodsRunning:     usage.PodsRunning,
		WorkflowsActive: usage.WorkflowsActive,
		StorageUsed:     usage.StorageUsed,
	}

	available := ResourceUsage{
		CPUUsed:         limits.CPULimit - used.CPUUsed,
		MemoryUsed:      limits.MemoryLimit - used.MemoryUsed,
		GPUUsed:         limits.GPULimit - used.GPUUsed,
		PodsRunning:     limits.MaxConcurrentPods - used.PodsRunning,
		WorkflowsActive: limits.MaxConcurrentWF - used.WorkflowsActive,
		StorageUsed:     limits.StorageLimit - used.StorageUsed,
	}

	return &QuotaUsageSummary{
		EntityType:         entityType,
		EntityID:           entityID,
		Limits:             limits,
		Used:               used,
		Available:          available,
		UtilizationPercent: calculateUtilization(limits, used),
	}, nil
}

func (m *Manager) GetPriorityWeight(ctx context.Context, projectID uuid.UUID) (int, error) {
	hierarchy, err := m.getHierarchy(ctx, projectID)
	if err != nil {
		return 0, err
	}

	for _, level := range hierarchy {
		quota, _, err := m.getQuotaInfo(ctx, level.EntityType, level.EntityID)
		if err != nil {
			return 0, err
		}
		if quota != nil && quota.PriorityWeight > 0 {
			return quota.PriorityWeight, nil
		}
	}

	return 100, nil
}

func (m *Manager) getProjectIDForTask(ctx context.Context, task *model.Task) (uuid.UUID, error) {
	if task.Workflow != nil {
		return task.Workflow.ProjectID, nil
	}

	var workflow model.Workflow
	if err := m.store.DB().WithContext(ctx).Select("project_id").First(&workflow, "id = ?", task.WorkflowID).Error; err != nil {
		return uuid.Nil, err
	}
	return workflow.ProjectID, nil
}

func (m *Manager) getHierarchy(ctx context.Context, projectID uuid.UUID) ([]HierarchyLevel, error) {
	var project model.Project
	if err := m.store.DB().WithContext(ctx).
		Preload("Group").
		Preload("Group.Department").
		Preload("Group.Tenant").
		First(&project, "id = ?", projectID).Error; err != nil {
		return nil, err
	}

	levels := []HierarchyLevel{
		{EntityType: "project", EntityID: project.ID},
		{EntityType: "group", EntityID: project.GroupID},
	}

	if project.Group != nil && project.Group.DepartmentID != nil {
		levels = append(levels, HierarchyLevel{EntityType: "department", EntityID: *project.Group.DepartmentID})
	}

	if project.Group != nil {
		levels = append(levels, HierarchyLevel{EntityType: "tenant", EntityID: project.Group.TenantID})
	}

	return levels, nil
}

func (m *Manager) getQuotaInfo(ctx context.Context, entityType string, entityID uuid.UUID) (*model.Quota, *model.QuotaUsage, error) {
	var quota *model.Quota
	switch entityType {
	case "project":
		var project model.Project
		if err := m.store.DB().WithContext(ctx).Preload("Quota").First(&project, "id = ?", entityID).Error; err != nil {
			return nil, nil, err
		}
		quota = project.Quota
	case "group":
		var group model.Group
		if err := m.store.DB().WithContext(ctx).Preload("Quota").First(&group, "id = ?", entityID).Error; err != nil {
			return nil, nil, err
		}
		quota = group.Quota
	case "department":
		var department model.Department
		if err := m.store.DB().WithContext(ctx).Preload("Quota").First(&department, "id = ?", entityID).Error; err != nil {
			return nil, nil, err
		}
		quota = department.Quota
	case "tenant":
		var tenant model.Tenant
		if err := m.store.DB().WithContext(ctx).Preload("Quota").First(&tenant, "id = ?", entityID).Error; err != nil {
			return nil, nil, err
		}
		quota = tenant.Quota
	default:
		return nil, nil, fmt.Errorf("unknown entity type %s", entityType)
	}

	if quota == nil {
		return nil, nil, nil
	}

	usage := &model.QuotaUsage{}
	if err := m.store.DB().WithContext(ctx).
		Where("quota_id = ? AND entity_type = ? AND entity_id = ?", quota.ID, entityType, entityID).
		First(usage).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return quota, &model.QuotaUsage{
				QuotaID:    quota.ID,
				EntityType: entityType,
				EntityID:   entityID,
			}, nil
		}
		return nil, nil, err
	}

	return quota, usage, nil
}

func calculateUtilization(limits ResourceLimits, used ResourceUsage) float64 {
	values := []float64{
		ratio(used.CPUUsed, limits.CPULimit),
		ratio(used.MemoryUsed, limits.MemoryLimit),
		ratio(used.GPUUsed, limits.GPULimit),
		ratio(used.PodsRunning, limits.MaxConcurrentPods),
		ratio(used.WorkflowsActive, limits.MaxConcurrentWF),
	}

	max := 0.0
	for _, value := range values {
		max = math.Max(max, value)
	}
	return max
}

func ratio(used, limit int) float64 {
	if limit <= 0 {
		return 0
	}
	return float64(used) / float64(limit) * 100
}

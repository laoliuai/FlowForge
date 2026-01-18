package quota

import (
	"context"

	"github.com/google/uuid"

	"github.com/flowforge/flowforge/pkg/model"
	"github.com/flowforge/flowforge/pkg/store/postgres"
)

type Hierarchy struct {
	store *postgres.Store
}

func NewHierarchy(store *postgres.Store) *Hierarchy {
	return &Hierarchy{store: store}
}

func (h *Hierarchy) ResolveProjectQuota(ctx context.Context, projectID uuid.UUID) (*model.Quota, error) {
	var project model.Project
	if err := h.store.DB().WithContext(ctx).Preload("Quota").First(&project, "id = ?", projectID).Error; err != nil {
		return nil, err
	}
	if project.Quota != nil {
		return project.Quota, nil
	}

	var group model.Group
	if err := h.store.DB().WithContext(ctx).Preload("Quota").First(&group, "id = ?", project.GroupID).Error; err != nil {
		return nil, err
	}
	if group.Quota != nil {
		return group.Quota, nil
	}

	var tenant model.Tenant
	if err := h.store.DB().WithContext(ctx).Preload("Quota").First(&tenant, "id = ?", group.TenantID).Error; err != nil {
		return nil, err
	}

	return tenant.Quota, nil
}

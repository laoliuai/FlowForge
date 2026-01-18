package quota

import (
	"context"

	"github.com/flowforge/flowforge/pkg/model"
)

type AdmissionController struct {
	manager *Manager
}

func NewAdmissionController(manager *Manager) *AdmissionController {
	return &AdmissionController{manager: manager}
}

func (a *AdmissionController) AdmitTask(ctx context.Context, task *model.Task) bool {
	return a.manager.CanScheduleTask(ctx, task)
}

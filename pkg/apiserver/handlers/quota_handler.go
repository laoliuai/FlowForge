package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/flowforge/flowforge/pkg/model"
	"github.com/flowforge/flowforge/pkg/quota"
	"github.com/flowforge/flowforge/pkg/store/postgres"
)

type QuotaHandler struct {
	db     *postgres.Store
	logger *zap.Logger
}

func NewQuotaHandler(db *postgres.Store, logger *zap.Logger) *QuotaHandler {
	return &QuotaHandler{db: db, logger: logger}
}

type quotaUpdateRequest struct {
	CPULimit           *int `json:"cpu_limit"`
	MemoryLimit        *int `json:"memory_limit"`
	GPULimit           *int `json:"gpu_limit"`
	MaxConcurrentPods  *int `json:"max_concurrent_pods"`
	MaxConcurrentWorkf *int `json:"max_concurrent_workflows"`
}

type quotaUsageResponse struct {
	EntityType         string              `json:"entity_type"`
	EntityID           string              `json:"entity_id"`
	Limits             quotaLimitsResponse `json:"limits"`
	Used               quotaUsageValues    `json:"used"`
	UtilizationPercent float64             `json:"utilization_percent"`
}

type quotaLimitsResponse struct {
	CPULimit              int `json:"cpu_limit"`
	CPURequest            int `json:"cpu_request"`
	MemoryLimit           int `json:"memory_limit"`
	MemoryRequest         int `json:"memory_request"`
	GPULimit              int `json:"gpu_limit"`
	MaxConcurrentPods     int `json:"max_concurrent_pods"`
	MaxConcurrentWorkflow int `json:"max_concurrent_workflows"`
	StorageLimit          int `json:"storage_limit"`
	PriorityWeight        int `json:"priority_weight"`
}

type quotaUsageValues struct {
	CPUUsed         int `json:"cpu_used"`
	MemoryUsed      int `json:"memory_used"`
	GPUUsed         int `json:"gpu_used"`
	PodsRunning     int `json:"pods_running"`
	WorkflowsActive int `json:"workflows_active"`
	StorageUsed     int `json:"storage_used"`
}

func (h *QuotaHandler) GetUsage(c *gin.Context) {
	tenantID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant id"})
		return
	}

	var tenant model.Tenant
	if err := h.db.DB().WithContext(c.Request.Context()).First(&tenant, "id = ?", tenantID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "tenant not found"})
			return
		}
		h.logger.Error("failed to load tenant", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load tenant"})
		return
	}

	manager := quota.NewManager(h.db)
	summary, err := manager.GetUsageSummary(c.Request.Context(), "tenant", tenant.ID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "quota not found"})
		return
	}

	c.JSON(http.StatusOK, mapQuotaUsage(summary))
}

func (h *QuotaHandler) Update(c *gin.Context) {
	tenantID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant id"})
		return
	}

	var req quotaUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request", "details": err.Error()})
		return
	}

	var tenant model.Tenant
	if err := h.db.DB().WithContext(c.Request.Context()).
		Preload("Quota").
		First(&tenant, "id = ?", tenantID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "tenant not found"})
			return
		}
		h.logger.Error("failed to load tenant", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load tenant"})
		return
	}

	tx := h.db.DB().WithContext(c.Request.Context()).Begin()
	if tx.Error != nil {
		h.logger.Error("failed to start transaction", zap.Error(tx.Error))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update quota"})
		return
	}

	quotaModel := tenant.Quota
	if quotaModel == nil {
		name := strings.TrimSpace(tenant.Name)
		if name == "" {
			name = tenant.ID.String()
		}
		quotaModel = &model.Quota{
			Name: name,
		}
		if err := tx.Create(quotaModel).Error; err != nil {
			tx.Rollback()
			h.logger.Error("failed to create quota", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create quota"})
			return
		}
		if err := tx.Model(&tenant).Update("quota_id", quotaModel.ID).Error; err != nil {
			tx.Rollback()
			h.logger.Error("failed to attach quota", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update quota"})
			return
		}
	}

	updates := map[string]interface{}{}
	if req.CPULimit != nil {
		updates["cpu_limit"] = *req.CPULimit
	}
	if req.MemoryLimit != nil {
		updates["memory_limit"] = *req.MemoryLimit
	}
	if req.GPULimit != nil {
		updates["gpu_limit"] = *req.GPULimit
	}
	if req.MaxConcurrentPods != nil {
		updates["max_concurrent_pods"] = *req.MaxConcurrentPods
	}
	if req.MaxConcurrentWorkf != nil {
		updates["max_concurrent_wf"] = *req.MaxConcurrentWorkf
	}

	if len(updates) > 0 {
		updates["updated_at"] = gorm.Expr("NOW()")
		if err := tx.Model(quotaModel).Updates(updates).Error; err != nil {
			tx.Rollback()
			h.logger.Error("failed to update quota", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update quota"})
			return
		}
	}

	if err := tx.Commit().Error; err != nil {
		h.logger.Error("failed to commit quota update", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update quota"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "updated"})
}

func mapQuotaUsage(summary *quota.QuotaUsageSummary) quotaUsageResponse {
	return quotaUsageResponse{
		EntityType: summary.EntityType,
		EntityID:   summary.EntityID.String(),
		Limits: quotaLimitsResponse{
			CPULimit:              summary.Limits.CPULimit,
			CPURequest:            summary.Limits.CPURequest,
			MemoryLimit:           summary.Limits.MemoryLimit,
			MemoryRequest:         summary.Limits.MemoryRequest,
			GPULimit:              summary.Limits.GPULimit,
			MaxConcurrentPods:     summary.Limits.MaxConcurrentPods,
			MaxConcurrentWorkflow: summary.Limits.MaxConcurrentWF,
			StorageLimit:          summary.Limits.StorageLimit,
			PriorityWeight:        summary.Limits.PriorityWeight,
		},
		Used: quotaUsageValues{
			CPUUsed:         summary.Used.CPUUsed,
			MemoryUsed:      summary.Used.MemoryUsed,
			GPUUsed:         summary.Used.GPUUsed,
			PodsRunning:     summary.Used.PodsRunning,
			WorkflowsActive: summary.Used.WorkflowsActive,
			StorageUsed:     summary.Used.StorageUsed,
		},
		UtilizationPercent: summary.UtilizationPercent,
	}
}

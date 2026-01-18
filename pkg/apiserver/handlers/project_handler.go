package handlers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/flowforge/flowforge/pkg/model"
	"github.com/flowforge/flowforge/pkg/store/postgres"
)

type ProjectHandler struct {
	db     *postgres.Store
	logger *zap.Logger
}

func NewProjectHandler(db *postgres.Store, logger *zap.Logger) *ProjectHandler {
	return &ProjectHandler{db: db, logger: logger}
}

type projectResponse struct {
	ID          string  `json:"id"`
	GroupID     string  `json:"group_id"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Namespace   string  `json:"namespace"`
	QuotaID     *string `json:"quota_id,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
}

type projectCreateRequest struct {
	GroupID     string  `json:"group_id" binding:"required"`
	Name        string  `json:"name" binding:"required"`
	Description string  `json:"description"`
	Namespace   string  `json:"namespace" binding:"required"`
	QuotaID     *string `json:"quota_id"`
}

func (h *ProjectHandler) List(c *gin.Context) {
	groupID := strings.TrimSpace(c.Query("group_id"))
	limit := parseLimit(c.Query("limit"), 20)
	offset := parseOffset(c.Query("offset"))

	var projects []model.Project
	query := h.db.DB().WithContext(c.Request.Context()).Model(&model.Project{})
	if groupID != "" {
		groupUUID, err := uuid.Parse(groupID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
			return
		}
		query = query.Where("group_id = ?", groupUUID)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		h.logger.Error("failed to count projects", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list projects"})
		return
	}

	if err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&projects).Error; err != nil {
		h.logger.Error("failed to list projects", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list projects"})
		return
	}

	response := make([]projectResponse, 0, len(projects))
	for _, project := range projects {
		response = append(response, mapProject(project))
	}

	c.JSON(http.StatusOK, gin.H{
		"projects": response,
		"total":    total,
	})
}

func (h *ProjectHandler) Create(c *gin.Context) {
	var req projectCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request", "details": err.Error()})
		return
	}

	groupID, err := uuid.Parse(req.GroupID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}

	var quotaID *uuid.UUID
	if req.QuotaID != nil && strings.TrimSpace(*req.QuotaID) != "" {
		parsed, err := uuid.Parse(*req.QuotaID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid quota_id"})
			return
		}
		quotaID = &parsed
	}

	project := model.Project{
		GroupID:     groupID,
		Name:        req.Name,
		Description: req.Description,
		Namespace:   req.Namespace,
		QuotaID:     quotaID,
	}

	if err := h.db.DB().WithContext(c.Request.Context()).Create(&project).Error; err != nil {
		h.logger.Error("failed to create project", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create project"})
		return
	}

	c.JSON(http.StatusCreated, mapProject(project))
}

func (h *ProjectHandler) Get(c *gin.Context) {
	projectID := c.Param("id")
	parsedID, err := uuid.Parse(projectID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid project id"})
		return
	}

	var project model.Project
	if err := h.db.DB().WithContext(c.Request.Context()).First(&project, "id = ?", parsedID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "project not found"})
			return
		}
		h.logger.Error("failed to get project", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get project"})
		return
	}

	c.JSON(http.StatusOK, mapProject(project))
}

func mapProject(project model.Project) projectResponse {
	var quotaID *string
	if project.QuotaID != nil {
		id := project.QuotaID.String()
		quotaID = &id
	}

	return projectResponse{
		ID:          project.ID.String(),
		GroupID:     project.GroupID.String(),
		Name:        project.Name,
		Description: project.Description,
		Namespace:   project.Namespace,
		QuotaID:     quotaID,
		CreatedAt:   project.CreatedAt.UTC().Format(timeRFC3339Nano),
		UpdatedAt:   project.UpdatedAt.UTC().Format(timeRFC3339Nano),
	}
}

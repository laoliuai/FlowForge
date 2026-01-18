package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/store/postgres"
)

type ProjectHandler struct {
	db     *postgres.Store
	logger *zap.Logger
}

func NewProjectHandler(db *postgres.Store, logger *zap.Logger) *ProjectHandler {
	return &ProjectHandler{db: db, logger: logger}
}

func (h *ProjectHandler) List(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"message": "list projects not implemented"})
}

func (h *ProjectHandler) Create(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"message": "create project not implemented"})
}

func (h *ProjectHandler) Get(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"message": "get project not implemented"})
}

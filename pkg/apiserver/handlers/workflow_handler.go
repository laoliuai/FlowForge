package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/store/postgres"
	redisclient "github.com/flowforge/flowforge/pkg/store/redis"
)

type WorkflowHandler struct {
	db     *postgres.Store
	redis  *redisclient.Client
	logger *zap.Logger
}

func NewWorkflowHandler(db *postgres.Store, redis *redisclient.Client, logger *zap.Logger) *WorkflowHandler {
	return &WorkflowHandler{db: db, redis: redis, logger: logger}
}

func (h *WorkflowHandler) Create(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"message": "create workflow not implemented"})
}

func (h *WorkflowHandler) List(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"message": "list workflows not implemented"})
}

func (h *WorkflowHandler) Get(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"message": "get workflow not implemented"})
}

func (h *WorkflowHandler) Cancel(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"message": "cancel workflow not implemented"})
}

func (h *WorkflowHandler) ListTasks(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"message": "list tasks not implemented"})
}

func (h *WorkflowHandler) StreamLogs(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"message": "stream logs not implemented"})
}

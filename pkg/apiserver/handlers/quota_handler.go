package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/store/postgres"
)

type QuotaHandler struct {
	db     *postgres.Store
	logger *zap.Logger
}

func NewQuotaHandler(db *postgres.Store, logger *zap.Logger) *QuotaHandler {
	return &QuotaHandler{db: db, logger: logger}
}

func (h *QuotaHandler) GetUsage(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"message": "get quota usage not implemented"})
}

func (h *QuotaHandler) Update(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"message": "update quota not implemented"})
}

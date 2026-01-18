package apiserver

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/apiserver/handlers"
	"github.com/flowforge/flowforge/pkg/apiserver/middleware"
	"github.com/flowforge/flowforge/pkg/config"
	"github.com/flowforge/flowforge/pkg/store/postgres"
	redisclient "github.com/flowforge/flowforge/pkg/store/redis"
)

type Server struct {
	router *gin.Engine
	db     *postgres.Store
	redis  *redisclient.Client
	cfg    *config.Config
	logger *zap.Logger
}

func NewServer(db *postgres.Store, redis *redisclient.Client, cfg *config.Config, logger *zap.Logger) *Server {
	s := &Server{
		db:     db,
		redis:  redis,
		cfg:    cfg,
		logger: logger,
	}
	s.setupRouter()
	return s
}

func (s *Server) setupRouter() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	r.Use(gin.Recovery())
	r.Use(middleware.Logger(s.logger))
	r.Use(middleware.RequestID())
	r.Use(middleware.CORS())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api/v1")
	{
		api.Use(middleware.Auth(s.cfg.Auth))

		workflowHandler := handlers.NewWorkflowHandler(s.db, s.redis, s.logger)
		api.POST("/workflows", workflowHandler.Create)
		api.GET("/workflows", workflowHandler.List)
		api.GET("/workflows/:id", workflowHandler.Get)
		api.DELETE("/workflows/:id", workflowHandler.Cancel)
		api.GET("/workflows/:id/tasks", workflowHandler.ListTasks)
		api.GET("/workflows/:id/logs", workflowHandler.StreamLogs)

		quotaHandler := handlers.NewQuotaHandler(s.db, s.logger)
		api.GET("/tenants/:id/quota", quotaHandler.GetUsage)
		api.PUT("/tenants/:id/quota", quotaHandler.Update)

		projectHandler := handlers.NewProjectHandler(s.db, s.logger)
		api.GET("/projects", projectHandler.List)
		api.POST("/projects", projectHandler.Create)
		api.GET("/projects/:id", projectHandler.Get)
	}

	s.router = r
}

func (s *Server) Router() *gin.Engine {
	return s.router
}

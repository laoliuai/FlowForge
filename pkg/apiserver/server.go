package apiserver

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/apiserver/handlers"
	"github.com/flowforge/flowforge/pkg/apiserver/middleware"
	"github.com/flowforge/flowforge/pkg/config"
	"github.com/flowforge/flowforge/pkg/eventbus"
	"github.com/flowforge/flowforge/pkg/store"
	"github.com/flowforge/flowforge/pkg/store/clickhouse"
	"github.com/flowforge/flowforge/pkg/store/postgres"
	redisclient "github.com/flowforge/flowforge/pkg/store/redis"
)

type Server struct {
	router  *gin.Engine
	db      *postgres.Store
	redis   *redisclient.Client
	logRepo store.LogStore
	cfg     *config.Config
	logger  *zap.Logger
	bus     *eventbus.Bus
}

func NewServer(db *postgres.Store, redis *redisclient.Client, cfg *config.Config, logger *zap.Logger, bus *eventbus.Bus) *Server {
	var logRepo store.LogStore
	var err error

	if cfg.Logging.StorageDriver == "clickhouse" {
		logger.Info("using clickhouse for log storage")
		logRepo, err = clickhouse.NewClickHouseLogStore(
			cfg.ClickHouse.Hosts[0],
			cfg.ClickHouse.Database,
			cfg.ClickHouse.User,
			cfg.ClickHouse.Password,
			logger,
		)
		if err != nil {
			logger.Fatal("failed to initialize clickhouse log store", zap.Error(err))
		}
	} else {
		logger.Info("using postgres for log storage")
		logRepo = postgres.NewLogRepository(db.DB())
	}

	s := &Server{
		db:      db,
		redis:   redis,
		logRepo: logRepo,
		cfg:     cfg,
		logger:  logger,
		bus:     bus,
	}
	s.setupRouter()

	// Only start retention worker if not using ClickHouse (ClickHouse handles TTL natively)
	if cfg.Logging.StorageDriver != "clickhouse" {
		go s.startLogRetentionWorker()
	}

	return s
}

func (s *Server) startLogRetentionWorker() {
	ticker := time.NewTicker(1 * time.Hour)
	retentionDays := 7 // Configurable in future

	for range ticker.C {
		s.logger.Info("starting log retention cleanup", zap.Int("retention_days", retentionDays))
		if err := s.logRepo.DeleteOldLogs(context.Background(), retentionDays); err != nil {
			s.logger.Error("failed to cleanup old logs", zap.Error(err))
		}
	}
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

		workflowHandler := handlers.NewWorkflowHandler(s.db, s.redis, s.logRepo, s.logger, s.bus)
		api.POST("/workflows", workflowHandler.Create)
		api.GET("/workflows", workflowHandler.List)
		api.GET("/workflows/:id", workflowHandler.Get)
		api.DELETE("/workflows/:id", workflowHandler.Cancel)
		api.GET("/workflows/:id/tasks", workflowHandler.ListTasks)
		api.GET("/tasks/:id/logs", workflowHandler.GetLogs)

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

func (s *Server) LogRepo() store.LogStore {
	return s.logRepo
}

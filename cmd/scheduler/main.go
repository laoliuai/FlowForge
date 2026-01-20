package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/config"
	"github.com/flowforge/flowforge/pkg/eventbus"
	"github.com/flowforge/flowforge/pkg/queue"
	"github.com/flowforge/flowforge/pkg/quota"
	"github.com/flowforge/flowforge/pkg/scheduler"
	"github.com/flowforge/flowforge/pkg/store/postgres"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	logger, _ := zap.NewProduction()
	defer logger.Sync()

	db, err := postgres.NewStore(&cfg.Database)
	if err != nil {
		logger.Fatal("failed to connect to database", zap.Error(err))
	}
	defer db.Close()

	workflowRepo := postgres.NewWorkflowRepository(db.DB())
	taskRepo := postgres.NewTaskRepository(db.DB())
	queueClient := queue.NewTaskQueueProducer(cfg.Kafka.Brokers, cfg.Kafka.ClientID, cfg.Kafka.TaskTopic)
	defer queueClient.Close()
	bus := eventbus.NewBus(eventbus.BusConfig{
		Brokers:    cfg.Kafka.Brokers,
		ClientID:   cfg.Kafka.ClientID,
		EventTopic: cfg.Kafka.EventTopic,
		RetryTopic: cfg.Kafka.RetryTopic,
		DLQTopic:   cfg.Kafka.DLQTopic,
	})
	quotaManager := quota.NewManager(db)

	svc := scheduler.NewScheduler(workflowRepo, taskRepo, queueClient, quotaManager, bus, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go svc.Run(ctx)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("scheduler shutting down")
}

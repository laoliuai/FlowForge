package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/config"
	"github.com/flowforge/flowforge/pkg/controller"
	"github.com/flowforge/flowforge/pkg/eventbus"
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
	bus := eventbus.NewBus(eventbus.BusConfig{
		Brokers:       cfg.Kafka.Brokers,
		ClientID:      cfg.Kafka.ClientID,
		EventTopic:    cfg.Kafka.EventTopic,
		RetryTopic:    cfg.Kafka.RetryTopic,
		DLQTopic:      cfg.Kafka.DLQTopic,
		ConsumerGroup: "flowforge-workflow-controller",
	})

	controller := controller.NewWorkflowController(workflowRepo, taskRepo, bus, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := controller.Start(ctx); err != nil {
		logger.Fatal("failed to start controller", zap.Error(err))
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("controller shutting down")
}

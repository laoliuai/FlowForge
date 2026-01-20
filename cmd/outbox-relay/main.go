package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"

	"github.com/flowforge/flowforge/pkg/config"
	"github.com/flowforge/flowforge/pkg/outbox"
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

	writer := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  cfg.Kafka.Brokers,
		Topic:    cfg.Kafka.EventTopic,
		Balancer: &kafka.LeastBytes{},
		Async:    false,
	})
	defer writer.Close()

	dlqWriter := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  cfg.Kafka.Brokers,
		Topic:    cfg.Kafka.DLQTopic,
		Balancer: &kafka.LeastBytes{},
		Async:    false,
	})
	defer dlqWriter.Close()

	repo := postgres.NewOutboxRepository(db.DB())
	relay := outbox.NewRelay(repo, writer, dlqWriter, logger, cfg.Outbox.PollInterval, cfg.Outbox.BatchSize)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		if err := relay.Run(ctx); err != nil && err != context.Canceled {
			logger.Fatal("outbox relay stopped with error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("outbox relay shutting down")
}

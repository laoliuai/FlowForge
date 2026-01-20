package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/flowforge/flowforge/pkg/apiserver"
	apigrpc "github.com/flowforge/flowforge/pkg/apiserver/grpc"
	"github.com/flowforge/flowforge/pkg/config"
	"github.com/flowforge/flowforge/pkg/eventbus"
	"github.com/flowforge/flowforge/pkg/store/postgres"
	redisclient "github.com/flowforge/flowforge/pkg/store/redis"
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
		logger.Fatal("Failed to connect to database", zap.Error(err))
	}
	defer db.Close()

	redis, err := redisclient.NewClient(&cfg.Redis)
	if err != nil {
		logger.Fatal("Failed to connect to Redis", zap.Error(err))
	}
	defer redis.Close()

	bus := eventbus.NewBus(eventbus.BusConfig{
		Brokers:    cfg.Kafka.Brokers,
		ClientID:   cfg.Kafka.ClientID,
		EventTopic: cfg.Kafka.EventTopic,
		RetryTopic: cfg.Kafka.RetryTopic,
		DLQTopic:   cfg.Kafka.DLQTopic,
	})

	server := apiserver.NewServer(db, redis, cfg, logger, bus)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.HTTPPort),
		Handler:      server.Router(),
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.ReadTimeout * 2,
	}

	go func() {
		logger.Info("Starting API server", zap.Int("port", cfg.Server.HTTPPort))
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("Server error", zap.Error(err))
		}
	}()

	grpcServer := grpc.NewServer()
	grpcHandler := apigrpc.NewServer(db, redis, server.LogRepo(), logger, bus)
	grpcHandler.Register(grpcServer)

	grpcAddr := fmt.Sprintf(":%d", cfg.Server.GRPCPort)
	grpcListener, err := net.Listen("tcp", grpcAddr)
	if err != nil {
		logger.Fatal("Failed to listen on gRPC port", zap.Int("port", cfg.Server.GRPCPort), zap.Error(err))
	}

	go func() {
		logger.Info("Starting gRPC server", zap.Int("port", cfg.Server.GRPCPort))
		if err := grpcServer.Serve(grpcListener); err != nil {
			logger.Fatal("gRPC server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("Server forced to shutdown", zap.Error(err))
	}

	grpcServer.GracefulStop()
}

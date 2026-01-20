package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/flowforge/flowforge/pkg/config"
	"github.com/flowforge/flowforge/pkg/eventbus"
	"github.com/flowforge/flowforge/pkg/executor"
	"github.com/flowforge/flowforge/pkg/queue"
	"github.com/flowforge/flowforge/pkg/quota"
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
		logger.Fatal("failed to connect to database", zap.Error(err))
	}
	defer db.Close()

	redis, err := redisclient.NewClient(&cfg.Redis)
	if err != nil {
		logger.Fatal("failed to connect to redis", zap.Error(err))
	}
	defer redis.Close()

	k8sClient, err := newKubernetesClient(cfg)
	if err != nil {
		logger.Fatal("failed to create kubernetes client", zap.Error(err))
	}

	sdkImage := os.Getenv("FLOWFORGE_SDK_IMAGE")
	if sdkImage == "" {
		sdkImage = "flowforge/sdk:latest"
	}

	taskRepo := postgres.NewTaskRepository(db.DB())
	queueClient := queue.NewTaskQueue(redis.Client(), "flowforge:tasks")
	bus := eventbus.NewBus(eventbus.BusConfig{
		Brokers:    cfg.Kafka.Brokers,
		ClientID:   cfg.Kafka.ClientID,
		EventTopic: cfg.Kafka.EventTopic,
		RetryTopic: cfg.Kafka.RetryTopic,
		DLQTopic:   cfg.Kafka.DLQTopic,
	})
	quotaManager := quota.NewManager(db)
	podExecutor := executor.NewPodExecutor(k8sClient, sdkImage, cfg.Kubernetes.Namespace)
	runner := executor.NewRunner(queueClient, taskRepo, quotaManager, bus, podExecutor, k8sClient, logger, cfg.Kubernetes.Namespace)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runner.Run(ctx)

	logger.Info("executor initialized")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("executor shutting down")
}

func newKubernetesClient(cfg *config.Config) (kubernetes.Interface, error) {
	var restConfig *rest.Config
	var err error

	if cfg.Kubernetes.InCluster {
		restConfig, err = rest.InClusterConfig()
	} else {
		restConfig, err = clientcmd.BuildConfigFromFlags("", cfg.Kubernetes.KubeConfig)
	}
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(restConfig)
}

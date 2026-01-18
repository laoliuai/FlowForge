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
	"github.com/flowforge/flowforge/pkg/statuscollector"
	redisclient "github.com/flowforge/flowforge/pkg/store/redis"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	logger, _ := zap.NewProduction()
	defer logger.Sync()

	redis, err := redisclient.NewClient(&cfg.Redis)
	if err != nil {
		logger.Fatal("failed to connect to redis", zap.Error(err))
	}
	defer redis.Close()

	k8sClient, err := newKubernetesClient(cfg)
	if err != nil {
		logger.Fatal("failed to create kubernetes client", zap.Error(err))
	}

	nodeName := os.Getenv("NODE_NAME")

	collector := statuscollector.NewCollector(
		k8sClient,
		eventbus.NewBus(redis.Client()),
		logger,
		cfg.Kubernetes.Namespace,
		nodeName,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	collectorDone := make(chan struct{})
	go func() {
		defer close(collectorDone)
		if err := collector.Run(ctx); err != nil {
			logger.Error("status collector stopped", zap.Error(err))
		}
	}()

	logger.Info("status collector initialized", zap.String("node", nodeName))

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("status collector shutting down")
	cancel()
	<-collectorDone
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

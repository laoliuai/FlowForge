package config

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server     ServerConfig
	Database   DatabaseConfig
	Redis      RedisConfig
	Kubernetes KubernetesConfig
	ClickHouse ClickHouseConfig
	Auth       AuthConfig
	Logging    LoggingConfig
	Kafka      KafkaConfig
	Outbox     OutboxRelayConfig
}

type ServerConfig struct {
	HTTPPort    int           `mapstructure:"http_port"`
	GRPCPort    int           `mapstructure:"grpc_port"`
	MetricsPort int           `mapstructure:"metrics_port"`
	ReadTimeout time.Duration `mapstructure:"read_timeout"`
}

type DatabaseConfig struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	User         string `mapstructure:"user"`
	Password     string `mapstructure:"password"`
	Database     string `mapstructure:"database"`
	SSLMode      string `mapstructure:"ssl_mode"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
}

type RedisConfig struct {
	Addresses   []string `mapstructure:"addresses"`
	Password    string   `mapstructure:"password"`
	DB          int      `mapstructure:"db"`
	PoolSize    int      `mapstructure:"pool_size"`
	ClusterMode bool     `mapstructure:"cluster_mode"`
}

type KubernetesConfig struct {
	InCluster  bool   `mapstructure:"in_cluster"`
	KubeConfig string `mapstructure:"kubeconfig"`
	Namespace  string `mapstructure:"namespace"`
}

type ClickHouseConfig struct {
	Hosts    []string `mapstructure:"hosts"`
	Database string   `mapstructure:"database"`
	User     string   `mapstructure:"user"`
	Password string   `mapstructure:"password"`
}

type AuthConfig struct {
	JWTSecret    string        `mapstructure:"jwt_secret"`
	TokenTTL     time.Duration `mapstructure:"token_ttl"`
	TaskTokenTTL time.Duration `mapstructure:"task_token_ttl"`
	OIDCIssuer   string        `mapstructure:"oidc_issuer"`
	OIDCClientID string        `mapstructure:"oidc_client_id"`
}

type LoggingConfig struct {
	Level         string `mapstructure:"level"`
	Format        string `mapstructure:"format"`         // json or console
	StorageDriver string `mapstructure:"storage_driver"` // postgres or clickhouse
}

type KafkaConfig struct {
	Brokers        []string `mapstructure:"brokers"`
	ClientID       string   `mapstructure:"client_id"`
	EventTopic     string   `mapstructure:"event_topic"`
	RetryTopic     string   `mapstructure:"retry_topic"`
	DLQTopic       string   `mapstructure:"dlq_topic"`
	TaskTopic      string   `mapstructure:"task_topic"`
	TaskRetryTopic string   `mapstructure:"task_retry_topic"`
	TaskDLQTopic   string   `mapstructure:"task_dlq_topic"`
	TaskGroup      string   `mapstructure:"task_group"`
}

type OutboxRelayConfig struct {
	PollInterval time.Duration `mapstructure:"poll_interval"`
	BatchSize    int           `mapstructure:"batch_size"`
}

func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("/etc/flowforge/")
	viper.AddConfigPath(".")

	viper.SetEnvPrefix("FLOWFORGE")
	viper.AutomaticEnv()

	viper.SetDefault("server.http_port", 8080)
	viper.SetDefault("server.grpc_port", 9090)
	viper.SetDefault("server.metrics_port", 9091)
	viper.SetDefault("server.read_timeout", "30s")
	viper.SetDefault("database.max_open_conns", 25)
	viper.SetDefault("database.max_idle_conns", 5)
	viper.SetDefault("redis.pool_size", 100)
	viper.SetDefault("auth.token_ttl", "24h")
	viper.SetDefault("auth.task_token_ttl", "24h")
	viper.SetDefault("logging.level", "info")
	viper.SetDefault("logging.format", "json")
	viper.SetDefault("logging.storage_driver", "postgres")
	viper.SetDefault("kafka.client_id", "flowforge-outbox-relay")
	viper.SetDefault("kafka.event_topic", "flowforge.workflow.events")
	viper.SetDefault("kafka.retry_topic", "flowforge.workflow.events.retry")
	viper.SetDefault("kafka.dlq_topic", "flowforge.workflow.events.dlq")
	viper.SetDefault("kafka.task_topic", "flowforge.tasks")
	viper.SetDefault("kafka.task_retry_topic", "flowforge.tasks.retry")
	viper.SetDefault("kafka.task_dlq_topic", "flowforge.tasks.dlq")
	viper.SetDefault("kafka.task_group", "flowforge-executors")
	viper.SetDefault("outbox.poll_interval", "5s")
	viper.SetDefault("outbox.batch_size", 100)

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, err
		}
	}

	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Database, c.SSLMode,
	)
}

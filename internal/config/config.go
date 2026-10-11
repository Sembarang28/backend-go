package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all configuration values for the application.
type Config struct {
	// Server & Networking
	ServerAddress   string        `json:"server_address"`
	GRPCAddress     string        `json:"grpc_address"`
	PprofAddress    string        `json:"pprof_address"`
	AppEnv          string        `json:"app_env"`
	LogLevel        string        `json:"log_level"`
	ReadTimeout     time.Duration `json:"read_timeout"`
	WriteTimeout    time.Duration `json:"write_timeout"`
	ShutdownTimeout time.Duration `json:"shutdown_timeout"`

	// Database Configuration (Primary / Replica)
	DBPrimaryDSN      string        `json:"db_primary_dsn"`
	DBReplicaDSN      string        `json:"db_replica_dsn"`
	DBReplicaDSNs     []string      `json:"db_replica_dsns"`
	DBMaxOpenConns    int           `json:"db_max_open_conns"`
	DBMaxIdleConns    int           `json:"db_max_idle_conns"`
	DBConnMaxLifetime time.Duration `json:"db_conn_max_lifetime"`
	DBConnMaxIdleTime time.Duration `json:"db_conn_max_idle_time"`

	// Redis & Cache
	RedisMode      string        `json:"redis_mode"`
	RedisAddresses []string      `json:"redis_addresses"`
	RedisPassword  string        `json:"-"`
	RedisDB        int           `json:"redis_db"`
	CacheTTL       time.Duration `json:"cache_ttl"`

	// Message Broker
	NatsAddress string `json:"nats_address"`

	// Outbox Worker
	OutboxPartitions   int           `json:"outbox_partitions"`
	OutboxBatchSize    int           `json:"outbox_batch_size"`
	OutboxPollInterval time.Duration `json:"outbox_poll_interval"`
	OutboxMaxRetries   int           `json:"outbox_max_retries"`

	// Resiliency & Idempotency
	IdempotencyTTL    time.Duration `json:"idempotency_ttl"`
	RateLimitRequests int64         `json:"rate_limit_requests"`
	RateLimitWindow   time.Duration `json:"rate_limit_window"`
}

// LoadConfig loads configuration from environment-specific .env or system environment variables.
func LoadConfig() *Config {
	env := os.Getenv("APP_ENV")
	if env == "" {
		env = "development"
	}

	// Try loading environment-specific file first, then fallback to .env
	envFile := fmt.Sprintf(".env.%s", env)
	if err := godotenv.Load(envFile); err == nil {
		slog.Info("Loaded environment configuration file", "file", envFile)
	} else if err := godotenv.Load(".env"); err == nil {
		slog.Info("Loaded fallback .env configuration file")
	} else {
		slog.Info("No .env file loaded, relying on system environment variables")
	}

	defaultLogLevel := "info"
	if strings.ToLower(env) == "development" {
		defaultLogLevel = "debug"
	}

	return &Config{
		ServerAddress:   getEnv("SERVER_ADDRESS", "127.0.0.1:8080"),
		GRPCAddress:     getEnv("GRPC_ADDRESS", "127.0.0.1:50051"),
		PprofAddress:    getEnv("PPROF_ADDRESS", "127.0.0.1:6060"),
		AppEnv:          getEnv("APP_ENV", env),
		LogLevel:        getEnv("LOG_LEVEL", defaultLogLevel),
		ReadTimeout:     getEnvAsDuration("READ_TIMEOUT", 10*time.Second),
		WriteTimeout:    getEnvAsDuration("WRITE_TIMEOUT", 10*time.Second),
		ShutdownTimeout: getEnvAsDuration("SHUTDOWN_TIMEOUT", 15*time.Second),

		DBPrimaryDSN:      getEnv("DB_PRIMARY_DSN", ""),
		DBReplicaDSN:      getEnv("DB_REPLICA_DSN", ""),
		DBReplicaDSNs:     getEnvAsCSV("DB_REPLICA_DSNS", getEnv("DB_REPLICA_DSN", "")),
		DBMaxOpenConns:    getEnvAsInt("DB_MAX_OPEN_CONNS", 25),
		DBMaxIdleConns:    getEnvAsInt("DB_MAX_IDLE_CONNS", 10),
		DBConnMaxLifetime: getEnvAsDuration("DB_CONN_MAX_LIFETIME", 30*time.Minute),
		DBConnMaxIdleTime: getEnvAsDuration("DB_CONN_MAX_IDLE_TIME", 5*time.Minute),

		RedisMode:      getEnv("REDIS_MODE", "single"),
		RedisAddresses: getEnvAsCSV("REDIS_ADDRESSES", getEnv("REDIS_ADDRESS", "127.0.0.1:6379")),
		RedisPassword:  getEnv("REDIS_PASSWORD", ""),
		RedisDB:        getEnvAsInt("REDIS_DB", 0),
		CacheTTL:       getEnvAsDuration("CACHE_TTL", 5*time.Minute),

		NatsAddress: getEnv("NATS_ADDRESS", "nats://127.0.0.1:4222"),

		OutboxPartitions:   getEnvAsInt("OUTBOX_PARTITIONS", 8),
		OutboxBatchSize:    getEnvAsInt("OUTBOX_BATCH_SIZE", 100),
		OutboxPollInterval: getEnvAsDuration("OUTBOX_POLL_INTERVAL", 200*time.Millisecond),
		OutboxMaxRetries:   getEnvAsInt("OUTBOX_MAX_RETRIES", 5),

		IdempotencyTTL:    getEnvAsDuration("IDEMPOTENCY_TTL", 24*time.Hour),
		RateLimitRequests: getEnvAsInt64("RATE_LIMIT_REQUESTS", 100),
		RateLimitWindow:   getEnvAsDuration("RATE_LIMIT_WINDOW", 1*time.Minute),
	}
}

func getEnvAsCSV(key, fallback string) []string {
	value := fallback
	if configuredValue, ok := os.LookupEnv(key); ok {
		value = configuredValue
	}
	parts := strings.Split(value, ",")
	addresses := make([]string, 0, len(parts))
	for _, part := range parts {
		if address := strings.TrimSpace(part); address != "" {
			addresses = append(addresses, address)
		}
	}
	return addresses
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return fallback
}

func getEnvAsInt(key string, fallback int) int {
	valStr := os.Getenv(key)
	if valStr == "" {
		return fallback
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		slog.Warn("Failed to parse int config", "key", key, "value", valStr, "fallback", fallback)
		return fallback
	}
	return val
}

func getEnvAsInt64(key string, fallback int64) int64 {
	valStr := os.Getenv(key)
	if valStr == "" {
		return fallback
	}
	val, err := strconv.ParseInt(valStr, 10, 64)
	if err != nil {
		slog.Warn("Failed to parse int64 config", "key", key, "value", valStr, "fallback", fallback)
		return fallback
	}
	return val
}

func getEnvAsDuration(key string, fallback time.Duration) time.Duration {
	valStr := os.Getenv(key)
	if valStr == "" {
		return fallback
	}
	d, err := time.ParseDuration(valStr)
	if err != nil {
		slog.Warn("Failed to parse duration config", "key", key, "value", valStr, "fallback", fallback)
		return fallback
	}
	return d
}

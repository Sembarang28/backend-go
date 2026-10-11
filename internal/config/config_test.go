package config_test

import (
	"testing"
	"time"

	"backend-go/internal/config"

	"github.com/stretchr/testify/assert"
)

func TestLoadConfig(t *testing.T) {
	t.Setenv("SERVER_ADDRESS", ":9090")
	t.Setenv("APP_ENV", "test")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("OUTBOX_PARTITIONS", "16")
	t.Setenv("RATE_LIMIT_REQUESTS", "500")

	cfg := config.LoadConfig()
	assert.Equal(t, ":9090", cfg.ServerAddress)
	assert.Equal(t, "test", cfg.AppEnv)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, 16, cfg.OutboxPartitions)
	assert.Equal(t, int64(500), cfg.RateLimitRequests)
	assert.Equal(t, 10*time.Second, cfg.ReadTimeout)
}

func TestLoadRedisClusterConfig(t *testing.T) {
	t.Setenv("REDIS_MODE", "cluster")
	t.Setenv("REDIS_ADDRESSES", "redis-0:6379, redis-1:6379")
	t.Setenv("REDIS_DB", "0")
	t.Setenv("DB_REPLICA_DSNS", "postgres://replica-0/db, postgres://replica-1/db")

	cfg := config.LoadConfig()
	assert.Equal(t, "cluster", cfg.RedisMode)
	assert.Equal(t, []string{"redis-0:6379", "redis-1:6379"}, cfg.RedisAddresses)
	assert.Equal(t, []string{"postgres://replica-0/db", "postgres://replica-1/db"}, cfg.DBReplicaDSNs)
}

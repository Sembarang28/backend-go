package redisclient

import (
	"fmt"

	"github.com/redis/go-redis/v9"
)

const (
	ModeSingle  = "single"
	ModeCluster = "cluster"
)

// New creates a Redis client for a single node or Redis Cluster.
func New(mode string, addresses []string, password string, db int) (redis.UniversalClient, error) {
	if len(addresses) == 0 {
		return nil, fmt.Errorf("at least one Redis address is required")
	}

	switch mode {
	case ModeSingle:
		if len(addresses) != 1 {
			return nil, fmt.Errorf("Redis single mode requires exactly one address, got %d", len(addresses))
		}
		return redis.NewClient(&redis.Options{
			Addr:     addresses[0],
			Password: password,
			DB:       db,
		}), nil
	case ModeCluster:
		if db != 0 {
			return nil, fmt.Errorf("Redis Cluster does not support selecting a logical database: REDIS_DB must be 0")
		}
		return redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:    addresses,
			Password: password,
			ReadOnly: false,
		}), nil
	default:
		return nil, fmt.Errorf("unsupported Redis mode %q: use %q or %q", mode, ModeSingle, ModeCluster)
	}
}

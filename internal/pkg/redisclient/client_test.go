package redisclient_test

import (
	"testing"

	"backend-go/internal/pkg/redisclient"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestNewSingleClient(t *testing.T) {
	client, err := redisclient.New(redisclient.ModeSingle, []string{"localhost:6379"}, "secret", 2)
	require.NoError(t, err)
	require.IsType(t, (*redis.Client)(nil), client)
	require.NoError(t, client.Close())
}

func TestNewClusterClient(t *testing.T) {
	client, err := redisclient.New(redisclient.ModeCluster, []string{"redis-0:6379", "redis-1:6379"}, "secret", 0)
	require.NoError(t, err)
	require.IsType(t, (*redis.ClusterClient)(nil), client)
	require.NoError(t, client.Close())
}

func TestNewClusterClientRejectsLogicalDatabase(t *testing.T) {
	_, err := redisclient.New(redisclient.ModeCluster, []string{"redis-0:6379"}, "", 1)
	require.Error(t, err)
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	_, err := redisclient.New(redisclient.ModeSingle, []string{"redis-0:6379", "redis-1:6379"}, "", 0)
	require.Error(t, err)

	_, err = redisclient.New("sentinel", []string{"redis-0:6379"}, "", 0)
	require.Error(t, err)
}

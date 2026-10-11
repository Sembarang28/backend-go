//go:build !examples

package main

import (
	"backend-go/internal/config"
	"backend-go/internal/pkg/database"

	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

func wireExamples(*gin.Engine, *config.Config, *database.DBGroup, redis.UniversalClient, *nats.Conn) func() {
	return func() {}
}

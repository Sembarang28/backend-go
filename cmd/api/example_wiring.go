//go:build examples

package main

import (
	"context"
	"log/slog"
	"time"

	"backend-go/internal/config"
	"backend-go/internal/domain"
	"backend-go/internal/events"
	handlerhttp "backend-go/internal/handler/http"
	"backend-go/internal/pkg/database"
	"backend-go/internal/pkg/idempotency"
	"backend-go/internal/pkg/outbox"
	"backend-go/internal/pkg/reconciliation"
	"backend-go/internal/pkg/resilience"
	"backend-go/internal/repository"
	"backend-go/internal/usecase"

	"github.com/gin-gonic/gin"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker/v2"
)

func wireExamples(
	router *gin.Engine,
	cfg *config.Config,
	dbGroup *database.DBGroup,
	redisClient redis.UniversalClient,
	natsConn *nats.Conn,
) func() {
	userRepository := repository.NewExampleUserRepository(redisClient, cfg.CacheTTL)
	userUsecase := usecase.NewExampleUserUsecase(
		userRepository,
		events.NewExampleNATSEventPublisher(natsConn),
	)
	handlerhttp.RegisterExampleUserRoutes(router, userUsecase)

	partitions := cfg.OutboxPartitions
	if partitions <= 0 {
		partitions = outbox.DefaultConfig().PartitionCount
	}

	var orderUsecase domain.ExampleOrderUsecase
	var orderRepository domain.ExampleOrderRepository
	var outboxRepository outbox.Repository
	if dbGroup != nil {
		orderRepository = repository.NewExampleOrderRepository(dbGroup, redisClient, cfg.CacheTTL, partitions)
		orderUsecase = usecase.NewExampleOrderUsecase(orderRepository)
		outboxRepository = outbox.NewPostgresRepository(dbGroup.Write())
	}

	var idempotencyStorage idempotency.Storage
	var rateLimiter resilience.RateLimiter
	var distributedLock outbox.DistributedLock
	if redisClient != nil {
		idempotencyStorage = idempotency.NewRedisStorage(redisClient)
		rateLimiter = resilience.NewRedisSlidingWindowLimiter(redisClient)
		distributedLock = outbox.NewRedisDistributedLock(redisClient)
	}

	var cancel context.CancelFunc
	var worker *outbox.Worker
	var cleaner *outbox.Cleaner
	var reconciliationEngine *reconciliation.Engine
	if dbGroup != nil && outboxRepository != nil && distributedLock != nil {
		ctx, stop := context.WithCancel(context.Background())
		cancel = stop

		reconciliationEngine = reconciliation.NewEngine(reconciliation.DefaultConfig(), distributedLock)
		reconciliationEngine.RegisterJob(usecase.NewExampleOrderOutboxReconciliationJob(orderRepository))
		reconciliationEngine.Start(ctx)

		cleaner = outbox.NewCleaner(outbox.DefaultCleanerConfig(), outboxRepository, distributedLock)
		cleaner.Start(ctx)

		if natsConn != nil {
			publishBreaker := resilience.NewCircuitBreaker(resilience.CircuitBreakerConfig{
				Name:                "example-outbox-nats-publisher",
				ConsecutiveFailures: 5,
				Timeout:             10 * time.Second,
				OnStateChange: func(name string, from, to gobreaker.State) {
					slog.Warn("Example outbox publisher circuit breaker state changed", "name", name, "from", from.String(), "to", to.String())
				},
			})
			outboxConfig := outbox.DefaultConfig()
			outboxConfig.PartitionCount = partitions
			outboxConfig.BatchSize = cfg.OutboxBatchSize
			outboxConfig.PollInterval = cfg.OutboxPollInterval
			outboxConfig.MaxRetries = cfg.OutboxMaxRetries
			worker = outbox.NewWorker(outboxConfig, outboxRepository, distributedLock, events.NewNATSOutboxPublisher(natsConn, publishBreaker))
			worker.Start(ctx)
		}
	}

	handlerhttp.RegisterExampleOrderRoutes(
		router,
		orderUsecase,
		idempotencyStorage,
		rateLimiter,
		cfg.RateLimitRequests,
		cfg.RateLimitWindow,
		cfg.IdempotencyTTL,
		dbGroup != nil && redisClient != nil && natsConn != nil,
	)

	return func() {
		if cancel != nil {
			cancel()
		}
		if worker != nil {
			worker.Stop()
		}
		if cleaner != nil {
			cleaner.Stop()
		}
		if reconciliationEngine != nil {
			reconciliationEngine.Stop()
		}
	}
}

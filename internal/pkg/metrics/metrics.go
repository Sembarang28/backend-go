package metrics

import (
	"context"
	"database/sql"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// Metrics holds Prometheus metric vectors for the application.
type Metrics struct {
	// HTTP RED metrics
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec
	HTTPRequestErrors   *prometheus.CounterVec

	// gRPC RED metrics
	GRPCRequestsTotal   *prometheus.CounterVec
	GRPCRequestDuration *prometheus.HistogramVec

	// Outbox metrics
	OutboxUnprocessedEvents *prometheus.GaugeVec
	OutboxProcessingLatency *prometheus.HistogramVec
	OutboxDLQEventsTotal    *prometheus.CounterVec

	// DB Pool metrics
	DBOpenConnections  *prometheus.GaugeVec
	DBIdleConnections  *prometheus.GaugeVec
	DBInUseConnections *prometheus.GaugeVec
	DBWaitCountTotal   *prometheus.CounterVec

	dbWaitMu     sync.Mutex
	dbWaitCounts map[string]int64
}

var globalMetrics *Metrics

// InitMetrics initializes and registers Prometheus metrics with default or custom registry.
func InitMetrics(reg prometheus.Registerer) *Metrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	m := &Metrics{
		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests processed.",
			},
			[]string{"method", "path", "status"},
		),
		HTTPRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "HTTP request latency in seconds.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "path"},
		),
		HTTPRequestErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_request_errors_total",
				Help: "Total number of HTTP request errors.",
			},
			[]string{"method", "path", "code"},
		),
		GRPCRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "grpc_requests_total",
				Help: "Total number of gRPC requests handled.",
			},
			[]string{"service", "method", "code"},
		),
		GRPCRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "grpc_request_duration_seconds",
				Help:    "gRPC request execution duration in seconds.",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"service", "method"},
		),
		OutboxUnprocessedEvents: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "outbox_unprocessed_events_count",
				Help: "Current count of unprocessed events in outbox per partition.",
			},
			[]string{"partition"},
		),
		OutboxProcessingLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "outbox_processing_latency_seconds",
				Help:    "Time taken to process and publish an outbox event batch.",
				Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
			},
			[]string{"partition"},
		),
		OutboxDLQEventsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "outbox_dlq_events_total",
				Help: "Total number of outbox events moved to Dead Letter Queue.",
			},
			[]string{"event_type"},
		),
		DBOpenConnections: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "db_open_connections",
				Help: "Number of established connections in the database pool.",
			},
			[]string{"role"}, // primary, replica
		),
		DBIdleConnections: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "db_idle_connections",
				Help: "Number of idle connections in the database pool.",
			},
			[]string{"role"},
		),
		DBInUseConnections: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "db_in_use_connections",
				Help: "Number of currently active connections in the database pool.",
			},
			[]string{"role"},
		),
		DBWaitCountTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "db_wait_count_total",
				Help: "Total number of connections waited for.",
			},
			[]string{"role"},
		),
		dbWaitCounts: make(map[string]int64),
	}

	reg.MustRegister(
		m.HTTPRequestsTotal,
		m.HTTPRequestDuration,
		m.HTTPRequestErrors,
		m.GRPCRequestsTotal,
		m.GRPCRequestDuration,
		m.OutboxUnprocessedEvents,
		m.OutboxProcessingLatency,
		m.OutboxDLQEventsTotal,
		m.DBOpenConnections,
		m.DBIdleConnections,
		m.DBInUseConnections,
		m.DBWaitCountTotal,
	)

	globalMetrics = m
	return m
}

// GetGlobalMetrics returns registered global metrics instance.
func GetGlobalMetrics() *Metrics {
	return globalMetrics
}

// RecordDBStats updates Prometheus gauges from sql.DBStats.
func (m *Metrics) RecordDBStats(role string, stats sql.DBStats) {
	if m == nil {
		return
	}
	m.DBOpenConnections.WithLabelValues(role).Set(float64(stats.OpenConnections))
	m.DBIdleConnections.WithLabelValues(role).Set(float64(stats.Idle))
	m.DBInUseConnections.WithLabelValues(role).Set(float64(stats.InUse))
	m.dbWaitMu.Lock()
	defer m.dbWaitMu.Unlock()
	delta := stats.WaitCount - m.dbWaitCounts[role]
	if delta < 0 {
		delta = stats.WaitCount
	}
	m.DBWaitCountTotal.WithLabelValues(role).Add(float64(delta))
	m.dbWaitCounts[role] = stats.WaitCount
}

// HTTPMiddleware captures HTTP RED metrics.
func (m *Metrics) HTTPMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}

		c.Next()

		duration := time.Since(start).Seconds()
		statusCode := c.Writer.Status()
		statusStr := strconv.Itoa(statusCode)
		method := c.Request.Method

		m.HTTPRequestsTotal.WithLabelValues(method, path, statusStr).Inc()
		m.HTTPRequestDuration.WithLabelValues(method, path).Observe(duration)

		if statusCode >= 400 {
			m.HTTPRequestErrors.WithLabelValues(method, path, statusStr).Inc()
		}
	}
}

// GRPCUnaryInterceptor captures gRPC RED metrics.
func (m *Metrics) GRPCUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		start := time.Now()
		res, err := handler(ctx, req)
		duration := time.Since(start).Seconds()

		code := status.Code(err).String()
		m.GRPCRequestsTotal.WithLabelValues(info.FullMethod, info.FullMethod, code).Inc()
		m.GRPCRequestDuration.WithLabelValues(info.FullMethod, info.FullMethod).Observe(duration)

		return res, err
	}
}

// Handler returns gin.HandlerFunc for Prometheus metrics endpoint.
func Handler() gin.HandlerFunc {
	h := promhttp.Handler()
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}

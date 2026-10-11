package metrics_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"backend-go/internal/pkg/metrics"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestMetricsCollection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := prometheus.NewRegistry()
	m := metrics.InitMetrics(reg)

	r := gin.New()
	r.Use(m.HTTPMiddleware())
	r.GET("/test", func(c *gin.Context) {
		c.String(http.StatusOK, "pong")
	})

	// Run request
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Check metrics registered
	mfs, err := reg.Gather()
	assert.NoError(t, err)
	assert.NotEmpty(t, mfs)

	m.RecordDBStats("primary", sql.DBStats{WaitCount: 4})
	m.RecordDBStats("primary", sql.DBStats{WaitCount: 6})
	mfs, err = reg.Gather()
	assert.NoError(t, err)
	for _, family := range mfs {
		if family.GetName() == "db_wait_count_total" {
			assert.Equal(t, float64(6), family.GetMetric()[0].GetCounter().GetValue())
			return
		}
	}
	assert.Fail(t, "db_wait_count_total metric was not registered")
}

package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"backend-go/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

const (
	HeaderIdempotencyKey = "X-Idempotency-Key"
	DefaultInFlightTTL   = 30 * time.Second
	DefaultCompletedTTL  = 24 * time.Hour
)

type responseBodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w *responseBodyWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

func (w *responseBodyWriter) WriteString(s string) (int, error) {
	w.body.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}

// HTTPMiddleware creates universal Idempotency middleware for Gin routes.
func HTTPMiddleware(storage Storage, inFlightTTL, completedTTL time.Duration) gin.HandlerFunc {
	if inFlightTTL <= 0 {
		inFlightTTL = DefaultInFlightTTL
	}
	if completedTTL <= 0 {
		completedTTL = DefaultCompletedTTL
	}

	return func(c *gin.Context) {
		idempotencyKey := c.GetHeader(HeaderIdempotencyKey)
		// If no idempotency key is supplied, proceed normally
		if idempotencyKey == "" {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				response.Error(c, http.StatusRequestEntityTooLarge, "REQUEST_BODY_TOO_LARGE", "Idempotent request body exceeds 1 MiB")
			} else {
				slog.ErrorContext(ctx, "Failed to read idempotent request body", "error", err)
				response.Error(c, http.StatusBadRequest, "INVALID_REQUEST_BODY", "Request body could not be read")
			}
			c.Abort()
			return
		}
		if storage == nil {
			response.Error(c, http.StatusServiceUnavailable, "IDEMPOTENCY_UNAVAILABLE", "Idempotency storage is unavailable")
			c.Abort()
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		fingerprintInput := c.Request.Method + "\n" + c.FullPath() + "\n" + string(body)
		fingerprintBytes := sha256.Sum256([]byte(fingerprintInput))
		fingerprint := hex.EncodeToString(fingerprintBytes[:])

		// 1. Try to acquire lock
		var acquired bool
		var existing *Record
		if fingerprintStorage, ok := storage.(FingerprintStorage); ok {
			acquired, existing, err = fingerprintStorage.TryLockWithFingerprint(ctx, idempotencyKey, fingerprint, inFlightTTL)
		} else {
			acquired, existing, err = storage.TryLock(ctx, idempotencyKey, inFlightTTL)
		}
		if err != nil {
			response.Error(c, http.StatusInternalServerError, "IDEMPOTENCY_ERROR", "Failed to verify idempotency state")
			c.Abort()
			return
		}

		// 2. Handle duplicate request
		if !acquired && existing != nil {
			if existing.Fingerprint != "" && existing.Fingerprint != fingerprint {
				response.Error(c, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "This idempotency key was already used with a different request")
				c.Abort()
				return
			}
			if existing.Status == StatusStarted {
				response.Error(c, http.StatusConflict, "REQUEST_IN_PROGRESS", "A request with this idempotency key is currently processing")
				c.Abort()
				return
			}

			if existing.Status == StatusCompleted {
				// Replay cached response
				for k, v := range existing.Headers {
					c.Header(k, v)
				}
				c.Header("X-Cache-Lookup", "HIT-IDEMPOTENT")
				c.Data(existing.StatusCode, "application/json", existing.ResponseBody)
				c.Abort()
				return
			}
		}

		// 3. Intercept response writer to capture output
		w := &responseBodyWriter{body: bytes.NewBufferString(""), ResponseWriter: c.Writer}
		c.Writer = w

		c.Next()

		// 4. Save response if request was processed
		statusCode := c.Writer.Status()
		if statusCode < 500 {
			rec := &Record{
				Key:          idempotencyKey,
				Fingerprint:  fingerprint,
				Status:       StatusCompleted,
				StatusCode:   statusCode,
				Headers:      map[string]string{"Content-Type": c.Writer.Header().Get("Content-Type")},
				ResponseBody: w.body.Bytes(),
				CreatedAt:    time.Now(),
			}
			if err := storage.SaveResponse(ctx, idempotencyKey, rec, completedTTL); err != nil {
				slog.ErrorContext(ctx, "Failed to save idempotent response", "error", err)
			}
		} else {
			// On server error, release lock so client can retry
			if err := storage.ReleaseLock(ctx, idempotencyKey); err != nil {
				slog.ErrorContext(ctx, "Failed to release idempotency lock after server error", "error", err)
			}
		}
	}
}

// GRPCUnaryInterceptor creates a gRPC unary interceptor for idempotency handling.
type GRPCServerHandler func(ctx context.Context, req interface{}) (interface{}, error)

type GRPCUnaryInterceptor func(
	ctx context.Context,
	req interface{},
	key string,
	storage Storage,
	handler GRPCServerHandler,
) (interface{}, error)

// CheckGRPCIdempotency handles idempotency logic for gRPC handlers.
func CheckGRPCIdempotency(ctx context.Context, key string, storage Storage, handler func(ctx context.Context) (interface{}, error)) (interface{}, error) {
	if key == "" || storage == nil {
		return handler(ctx)
	}

	acquired, existing, err := storage.TryLock(ctx, key, DefaultInFlightTTL)
	if err != nil {
		return nil, err
	}

	if !acquired && existing != nil {
		if existing.Status == StatusStarted {
			return nil, ErrRequestInProgress
		}
	}

	res, err := handler(ctx)
	if err != nil {
		_ = storage.ReleaseLock(ctx, key)
		return nil, err
	}

	_ = storage.SaveResponse(ctx, key, &Record{
		Key:       key,
		Status:    StatusCompleted,
		CreatedAt: time.Now(),
	}, DefaultCompletedTTL)

	return res, nil
}

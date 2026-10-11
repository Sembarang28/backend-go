//go:build examples

package domain

import "errors"

var (
	ErrExampleUserNotFound    = errors.New("user not found")
	ErrInvalidExampleUserID   = errors.New("invalid user ID")
	ErrExampleOrderNotFound   = errors.New("order not found")
	ErrInvalidExampleOrder    = errors.New("invalid order")
	ErrExampleOrderExists     = errors.New("order already exists")
	ErrExampleIdempotencyUsed = errors.New("idempotency key was already used with a different request")
)

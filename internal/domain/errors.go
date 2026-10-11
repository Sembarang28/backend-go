package domain

import "errors"

var (
	// ErrInternal is returned when an unhandled internal error occurs.
	ErrInternal = errors.New("internal server error")
)

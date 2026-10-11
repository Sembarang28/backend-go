//go:build examples

package domain

import (
	"context"
	"time"
)

// ExampleUser represents the user data model in the sample API.
type ExampleUser struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// ExampleUserAccessedEvent defines the sample event payload for user access tracking.
type ExampleUserAccessedEvent struct {
	UserID     int       `json:"user_id"`
	UserName   string    `json:"user_name"`
	AccessedAt time.Time `json:"accessed_at"`
	Source     string    `json:"source"`
}

// ExampleUserRepository defines the sample data access contract.
type ExampleUserRepository interface {
	GetByID(ctx context.Context, id int) (*ExampleUser, error)
}

// ExampleUserUsecase defines the sample business logic contract.
type ExampleUserUsecase interface {
	GetUser(ctx context.Context, id int) (*ExampleUser, error)
}

// ExampleEventPublisher defines the sample event publishing contract.
type ExampleEventPublisher interface {
	PublishUserAccessed(ctx context.Context, event ExampleUserAccessedEvent) error
}

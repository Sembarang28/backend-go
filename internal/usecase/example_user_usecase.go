//go:build examples

package usecase

import (
	"context"
	"log/slog"
	"time"

	"backend-go/internal/domain"
)

type userUsecase struct {
	repo           domain.ExampleUserRepository
	eventPublisher domain.ExampleEventPublisher
}

// NewExampleUserUsecase creates a new instance of UserUsecase with optional event publisher injection.
func NewExampleUserUsecase(repo domain.ExampleUserRepository, eventPublisher domain.ExampleEventPublisher) domain.ExampleUserUsecase {
	return &userUsecase{
		repo:           repo,
		eventPublisher: eventPublisher,
	}
}

// GetUser retrieves user data by ID after performing business validation.
func (u *userUsecase) GetUser(ctx context.Context, id int) (*domain.ExampleUser, error) {
	if id <= 0 {
		return nil, domain.ErrInvalidExampleUserID
	}

	user, err := u.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Publish domain event asynchronously / non-blocking
	if u.eventPublisher != nil {
		event := domain.ExampleUserAccessedEvent{
			UserID:     user.ID,
			UserName:   user.Name,
			AccessedAt: time.Now(),
			Source:     "database_or_cache",
		}
		go func(evt domain.ExampleUserAccessedEvent) {
			// Create a detached background context for async event publishing
			asyncCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			if pubErr := u.eventPublisher.PublishUserAccessed(asyncCtx, evt); pubErr != nil {
				slog.Error("Failed to publish user accessed event in background", "user_id", evt.UserID, "error", pubErr)
			}
		}(event)
	}

	return user, nil
}

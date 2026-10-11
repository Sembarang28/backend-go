//go:build examples

package usecase

import (
	"context"

	"backend-go/internal/domain"
)

// ConsistencyExampleRepository separates reads that may be stale from reads
// that must use the authoritative source.
//
// Implement GetByID with cache/replica routing only when the caller tolerates
// stale data. Implement GetByIDCritical by bypassing cache and reading the
// PostgreSQL primary (or the configured authoritative store).
type ConsistencyExampleRepository interface {
	GetByID(ctx context.Context, id int) (*domain.ExampleUser, error)
	GetByIDCritical(ctx context.Context, id int) (*domain.ExampleUser, error)
}

// ConsistencyExampleUsecase demonstrates choosing a read path based on
// business consistency requirements.
type ConsistencyExampleUsecase interface {
	GetUserForDisplay(ctx context.Context, id int) (*domain.ExampleUser, error)
	GetUserForCriticalDecision(ctx context.Context, id int) (*domain.ExampleUser, error)
}

type consistencyExampleUsecase struct {
	repo ConsistencyExampleRepository
}

// NewConsistencyExampleUsecase creates a use case demonstrating eventual and
// primary-backed reads. It is an example pattern, not wired into the API.
func NewConsistencyExampleUsecase(repo ConsistencyExampleRepository) ConsistencyExampleUsecase {
	return &consistencyExampleUsecase{repo: repo}
}

// GetUserForDisplay allows the repository to use a cache or read replica.
// Use this only when briefly stale profile data is acceptable.
func (u *consistencyExampleUsecase) GetUserForDisplay(ctx context.Context, id int) (*domain.ExampleUser, error) {
	if id <= 0 {
		return nil, domain.ErrInvalidExampleUserID
	}
	return u.repo.GetByID(ctx, id)
}

// GetUserForCriticalDecision requires the repository to bypass cache and read
// from its authoritative source, such as PostgreSQL primary.
func (u *consistencyExampleUsecase) GetUserForCriticalDecision(ctx context.Context, id int) (*domain.ExampleUser, error) {
	if id <= 0 {
		return nil, domain.ErrInvalidExampleUserID
	}
	return u.repo.GetByIDCritical(ctx, id)
}

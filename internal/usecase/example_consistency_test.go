//go:build examples

package usecase_test

import (
	"context"
	"testing"

	"backend-go/internal/domain"
	"backend-go/internal/usecase"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type consistencyExampleRepository struct {
	displayCalls  int
	criticalCalls int
	user          *domain.ExampleUser
}

func (r *consistencyExampleRepository) GetByID(context.Context, int) (*domain.ExampleUser, error) {
	r.displayCalls++
	return r.user, nil
}

func (r *consistencyExampleRepository) GetByIDCritical(context.Context, int) (*domain.ExampleUser, error) {
	r.criticalCalls++
	return r.user, nil
}

func TestConsistencyExampleUsecaseSelectsReadPath(t *testing.T) {
	repo := &consistencyExampleRepository{
		user: &domain.ExampleUser{ID: 7, Name: "Example"},
	}
	uc := usecase.NewConsistencyExampleUsecase(repo)

	displayUser, err := uc.GetUserForDisplay(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, repo.user, displayUser)
	assert.Equal(t, 1, repo.displayCalls)
	assert.Zero(t, repo.criticalCalls)

	decisionUser, err := uc.GetUserForCriticalDecision(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, repo.user, decisionUser)
	assert.Equal(t, 1, repo.displayCalls)
	assert.Equal(t, 1, repo.criticalCalls)
}

func TestConsistencyExampleUsecaseRejectsInvalidID(t *testing.T) {
	repo := &consistencyExampleRepository{}
	uc := usecase.NewConsistencyExampleUsecase(repo)

	_, err := uc.GetUserForCriticalDecision(context.Background(), 0)

	assert.ErrorIs(t, err, domain.ErrInvalidExampleUserID)
	assert.Zero(t, repo.displayCalls)
	assert.Zero(t, repo.criticalCalls)
}

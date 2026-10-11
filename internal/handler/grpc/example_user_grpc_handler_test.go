//go:build examples

package grpc_test

import (
	"context"
	"testing"

	"backend-go/internal/domain"
	handlergrpc "backend-go/internal/handler/grpc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type mockUserUsecase struct {
	mock.Mock
}

func (m *mockUserUsecase) GetUser(ctx context.Context, id int) (*domain.ExampleUser, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ExampleUser), args.Error(1)
}

func TestExampleUserGRPCHandler_GetUser(t *testing.T) {
	mockUC := new(mockUserUsecase)
	h := handlergrpc.NewExampleUserGRPCHandler(mockUC)

	// Success case
	mockUC.On("GetUser", mock.Anything, 1).Return(&domain.ExampleUser{
		ID:    1,
		Name:  "Aditya",
		Email: "aditya@example.com",
	}, nil)

	res, err := h.GetUser(context.Background(), &handlergrpc.GetUserRequest{ID: 1})
	assert.NoError(t, err)
	assert.Equal(t, int32(1), res.ID)
	assert.Equal(t, "Aditya", res.Name)

	// Not found case
	mockUC.On("GetUser", mock.Anything, 99).Return(nil, domain.ErrExampleUserNotFound)
	_, err = h.GetUser(context.Background(), &handlergrpc.GetUserRequest{ID: 99})
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

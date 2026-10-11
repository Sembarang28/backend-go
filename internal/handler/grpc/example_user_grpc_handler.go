//go:build examples

package grpc

import (
	"context"
	"errors"

	"backend-go/internal/domain"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetUserRequest DTO for gRPC communication.
type GetUserRequest struct {
	ID int32 `json:"id"`
}

// GetUserResponse DTO for gRPC communication.
type GetUserResponse struct {
	ID    int32  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// ExampleUserGRPCHandler exposes the sample user use case over gRPC.
type ExampleUserGRPCHandler struct {
	usecase domain.ExampleUserUsecase
}

// NewExampleUserGRPCHandler initializes the sample gRPC adapter for UserService.
func NewExampleUserGRPCHandler(u domain.ExampleUserUsecase) *ExampleUserGRPCHandler {
	return &ExampleUserGRPCHandler{usecase: u}
}

// GetUser handles gRPC RPC call to fetch user by ID.
func (h *ExampleUserGRPCHandler) GetUser(ctx context.Context, req *GetUserRequest) (*GetUserResponse, error) {
	if req == nil || req.ID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "id must be greater than 0")
	}

	user, err := h.usecase.GetUser(ctx, int(req.ID))
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidExampleUserID):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		case errors.Is(err, domain.ErrExampleUserNotFound):
			return nil, status.Error(codes.NotFound, err.Error())
		default:
			return nil, status.Error(codes.Internal, "internal server error")
		}
	}

	return &GetUserResponse{
		ID:    int32(user.ID),
		Name:  user.Name,
		Email: user.Email,
	}, nil
}

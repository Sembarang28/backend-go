//go:build examples

package http

import (
	"errors"
	nethttp "net/http"
	"strconv"

	"backend-go/internal/domain"
	"backend-go/internal/pkg/response"

	"github.com/gin-gonic/gin"
)

// ExampleUserHandler holds a reference to the logic layer (usecase).
type ExampleUserHandler struct {
	usecase domain.ExampleUserUsecase
}

// RegisterExampleUserRoutes registers routes related to users.
func RegisterExampleUserRoutes(r *gin.Engine, us domain.ExampleUserUsecase) {
	handler := &ExampleUserHandler{usecase: us}
	r.GET("/users/:id", handler.GetByID)
}

// GetByID handles HTTP requests to retrieve user data by ID.
func (h *ExampleUserHandler) GetByID(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		response.Error(c, nethttp.StatusBadRequest, "INVALID_ID_FORMAT", "ID must be a valid integer")
		return
	}

	user, err := h.usecase.GetUser(c.Request.Context(), id)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidExampleUserID):
			response.Error(c, nethttp.StatusBadRequest, "INVALID_USER_ID", err.Error())
		case errors.Is(err, domain.ErrExampleUserNotFound):
			response.Error(c, nethttp.StatusNotFound, "USER_NOT_FOUND", err.Error())
		default:
			response.Error(c, nethttp.StatusInternalServerError, "INTERNAL_SERVER_ERROR", "An unexpected error occurred")
		}
		return
	}

	response.Success(c, nethttp.StatusOK, user)
}

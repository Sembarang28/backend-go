//go:build examples

package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"backend-go/internal/domain"
	handlerhttp "backend-go/internal/handler/http"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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

func TestUserHTTPHandler_GetByID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockUC := new(mockUserUsecase)

	r := gin.New()
	handlerhttp.RegisterExampleUserRoutes(r, mockUC)

	// Success
	mockUC.On("GetUser", mock.Anything, 1).Return(&domain.ExampleUser{
		ID:    1,
		Name:  "Test",
		Email: "test@example.com",
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/users/1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"email":"test@example.com"`)

	// Invalid ID
	reqInvalid := httptest.NewRequest(http.MethodGet, "/users/abc", nil)
	wInvalid := httptest.NewRecorder()
	r.ServeHTTP(wInvalid, reqInvalid)

	assert.Equal(t, http.StatusBadRequest, wInvalid.Code)
}

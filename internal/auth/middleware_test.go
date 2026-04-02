package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btc/drill/internal/auth"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAuthUser_FromContext(t *testing.T) {
	user := &auth.AuthUser{
		ID:    uuid.New(),
		Email: "test@example.com",
		Role:  "candidate",
	}
	ctx := auth.WithUser(context.Background(), user)

	got := auth.UserFromContext(ctx)
	require.NotNil(t, got)
	require.Equal(t, user.ID, got.ID)
	require.Equal(t, user.Email, got.Email)
}

func TestAuthUser_FromContext_Missing(t *testing.T) {
	got := auth.UserFromContext(context.Background())
	require.Nil(t, got)
}

func TestRequireAuth_NoCookie(t *testing.T) {
	handler := auth.RequireAuth(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}

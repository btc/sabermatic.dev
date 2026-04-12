package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/handler"
)

// --- SecurityHeaders ---

func TestSecurityHeaders_AlwaysPresent(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	h := handler.SecurityHeaders(false, mux)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.Equal(t, "strict-origin-when-cross-origin", w.Header().Get("Referrer-Policy"))
	assert.Equal(t, "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https://storage.googleapis.com; connect-src 'self' wss:; font-src 'self'; frame-ancestors 'none'", w.Header().Get("Content-Security-Policy"))
	assert.Empty(t, w.Header().Get("Strict-Transport-Security"), "HSTS should not be set for non-secure")
}

func TestSecurityHeaders_HSTSWhenSecure(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	h := handler.SecurityHeaders(true, mux)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, "max-age=63072000; includeSubDomains", w.Header().Get("Strict-Transport-Security"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.Equal(t, "strict-origin-when-cross-origin", w.Header().Get("Referrer-Policy"))
}

// --- RequireAuth / RequireAdmin ---

// stubAuthenticator implements auth.SessionAuthenticator for tests. It records
// whether it was called and returns a configurable user/error.
type stubAuthenticator struct {
	user   *auth.AuthUser
	err    error
	called bool
}

func (s *stubAuthenticator) AuthenticateSession(_ context.Context, _ string) (*auth.AuthUser, error) {
	s.called = true
	return s.user, s.err
}

func TestRequireAuth_NoCookie(t *testing.T) {
	t.Parallel()

	sa := &stubAuthenticator{}
	h := handler.RequireAuth(sa)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.False(t, sa.called, "authenticator must not be called when cookie is missing")
}

func TestRequireAuth_ValidSession(t *testing.T) {
	t.Parallel()

	wantUser := &auth.AuthUser{ID: uuid.New(), Email: "user@example.com", Role: "candidate"}
	sa := &stubAuthenticator{user: wantUser}

	var gotUser *auth.AuthUser
	h := handler.RequireAuth(sa)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = auth.UserFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "raw-token"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.True(t, sa.called)
	require.NotNil(t, gotUser)
	require.Equal(t, wantUser.ID, gotUser.ID)
}

func TestRequireAuth_InvalidSession(t *testing.T) {
	t.Parallel()

	sa := &stubAuthenticator{err: errors.New("expired")}
	h := handler.RequireAuth(sa)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called on invalid session")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "stale-token"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.True(t, sa.called)
}

func TestRequireAuth_EmptyCookieValue(t *testing.T) {
	t.Parallel()

	sa := &stubAuthenticator{}
	h := handler.RequireAuth(sa)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: ""})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.False(t, sa.called, "authenticator must not be called on empty cookie value")
}

// TestRequireAuth_SetsUserIDSpanAttribute pins the telemetry contract: on a
// successful session, the middleware writes `user_id` onto the ambient span.
// Without this test, a refactor could silently drop the attribute and we'd
// lose per-user trace correlation.
func TestRequireAuth_SetsUserIDSpanAttribute(t *testing.T) {
	t.Parallel()

	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	userID := uuid.New()
	sa := &stubAuthenticator{user: &auth.AuthUser{ID: userID, Role: "candidate"}}

	h := handler.RequireAuth(sa)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	ctx, span := tp.Tracer("test").Start(context.Background(), "req")
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "raw-token"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	span.End()

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	var got string
	for _, a := range spans[0].Attributes {
		if string(a.Key) == "user_id" {
			got = a.Value.AsString()
			break
		}
	}
	require.Equal(t, userID.String(), got, "expected user_id attribute on span")
}

func TestRequireAdmin_NoUser(t *testing.T) {
	t.Parallel()

	h := handler.RequireAdmin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin/jobs", nil)
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRequireAdmin_CandidateRole(t *testing.T) {
	t.Parallel()

	h := handler.RequireAdmin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin/jobs", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.AuthUser{ID: uuid.New(), Role: "candidate"}))
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRequireAdmin_AdminRole(t *testing.T) {
	t.Parallel()

	h := handler.RequireAdmin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin/jobs", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.AuthUser{ID: uuid.New(), Role: "admin"}))
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

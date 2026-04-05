package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/handler"
	"github.com/btc/drill/internal/testutil"
)

// authedRequest creates an HTTP request with the given session cookie.
func authedRequest(t *testing.T, method, path string, body any, cookie *http.Cookie) *http.Request {
	t.Helper()
	var reqBody *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reqBody = bytes.NewReader(b)
	} else {
		reqBody = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reqBody)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)
	return req
}

// ---------------------------------------------------------------------------
// CreateSession
// ---------------------------------------------------------------------------

func TestCreateSession_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)

	question := createTestQuestion(t, pool)
	cookie := createAuthCookie(t, pool, userID)

	req := authedRequest(t, http.MethodPost, "/api/sessions", map[string]any{
		"question_id":      question.ID,
		"duration_minutes": 30,
		"tts_enabled":      true,
	}, cookie)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "active", resp["status"])
	assert.Equal(t, float64(30), resp["config_duration_minutes"])
	assert.Equal(t, true, resp["config_tts_enabled"])
}

func TestCreateSession_InvalidDuration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	cookie := createAuthCookie(t, pool, userID)

	tests := []struct {
		name     string
		duration int
	}{
		{"zero", 0},
		{"negative", -1},
		{"over max", 181},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := authedRequest(t, http.MethodPost, "/api/sessions", map[string]any{
				"question_id":      question.ID,
				"duration_minutes": tt.duration,
			}, cookie)

			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

func TestCreateSession_QuestionNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)

	cookie := createAuthCookie(t, pool, userID)

	req := authedRequest(t, http.MethodPost, "/api/sessions", map[string]any{
		"question_id":      uuid.New(), // nonexistent
		"duration_minutes": 30,
	}, cookie)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCreateSession_InvalidJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	cookie := createAuthCookie(t, pool, userID)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString("{bad json"))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookie)

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateSession_Unauthenticated(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	req := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString("{}"))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// ---------------------------------------------------------------------------
// ListSessions
// ---------------------------------------------------------------------------

func TestListSessions_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	cookie := createAuthCookie(t, pool, userID)

	// Create two sessions.
	createTestSession(t, pool, userID, question.ID)
	createTestSession(t, pool, userID, question.ID)

	req := authedRequest(t, http.MethodGet, "/api/sessions", nil, cookie)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var sessions []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sessions))
	assert.Len(t, sessions, 2)
}

func TestListSessions_EmptyForOtherUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	pool := b.Pool()
	userA := backendtest.SeedUser(t, b)
	userB := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	cookieB := createAuthCookie(t, pool, userB)

	// Session belongs to user A.
	createTestSession(t, pool, userA, question.ID)

	// User B lists — should see nothing.
	req := authedRequest(t, http.MethodGet, "/api/sessions", nil, cookieB)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var sessions []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sessions))
	assert.Len(t, sessions, 0)
}

// ---------------------------------------------------------------------------
// GetSession
// ---------------------------------------------------------------------------

func TestGetSession_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userID, question.ID)
	cookie := createAuthCookie(t, pool, userID)

	req := authedRequest(t, http.MethodGet, "/api/sessions/"+session.ID.String(), nil, cookie)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, session.ID.String(), resp["id"])
}

func TestGetSession_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	cookie := createAuthCookie(t, pool, userID)

	req := authedRequest(t, http.MethodGet, "/api/sessions/"+uuid.New().String(), nil, cookie)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetSession_WrongUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	pool := b.Pool()
	userA := backendtest.SeedUser(t, b)
	userB := backendtest.SeedUser(t, b)
	question := createTestQuestion(t, pool)
	session := createTestSession(t, pool, userA, question.ID)
	cookieB := createAuthCookie(t, pool, userB)

	req := authedRequest(t, http.MethodGet, "/api/sessions/"+session.ID.String(), nil, cookieB)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGetSession_InvalidUUID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	cookie := createAuthCookie(t, pool, userID)

	req := authedRequest(t, http.MethodGet, "/api/sessions/not-a-uuid", nil, cookie)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ---------------------------------------------------------------------------
// ListQuestions
// ---------------------------------------------------------------------------

func TestListQuestions_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)
	createTestQuestion(t, pool) // seed question
	cookie := createAuthCookie(t, pool, userID)

	req := authedRequest(t, http.MethodGet, "/api/questions", nil, cookie)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var questions []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &questions))
	assert.GreaterOrEqual(t, len(questions), 1)

	// Verify question has expected fields.
	q := questions[0].(map[string]any)
	assert.NotEmpty(t, q["title"])
}

// ---------------------------------------------------------------------------
// Boundary: duration 1 and 180 (min/max valid)
// ---------------------------------------------------------------------------

func TestCreateSession_BoundaryDurations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	b := testutil.NewTestBackend(t)
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	pool := b.Pool()
	userID := backendtest.SeedUser(t, b)

	question := createTestQuestion(t, pool)
	cookie := createAuthCookie(t, pool, userID)

	// Free plan allows max 30 min and 60 min/month.
	// Test boundaries within plan limits: 1 (min valid) and 30 (plan max).
	// Each session is completed before the next to avoid concurrent limit.
	for _, dur := range []int{1, 30} {
		t.Run(fmt.Sprintf("duration_%d", dur), func(t *testing.T) {
			req := authedRequest(t, http.MethodPost, "/api/sessions", map[string]any{
				"question_id":      question.ID,
				"duration_minutes": dur,
			}, cookie)

			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, http.StatusCreated, w.Code)

			// Mark the session completed so the next iteration doesn't hit the concurrent limit.
			var resp map[string]any
			json.Unmarshal(w.Body.Bytes(), &resp)
			sessionID, _ := uuid.Parse(resp["id"].(string))
			db.New(pool).MarkSessionCompleted(context.Background(), sessionID)
		})
	}
}

package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/testutil"
	"github.com/btc/drill/internal/db"
)

// ---------------------------------------------------------------------------
// Evaluation-specific DB helpers
// ---------------------------------------------------------------------------

// setSessionStatus updates a session's status directly via SQL.
func setSessionStatus(t *testing.T, pool *pgxpool.Pool, sessionID uuid.UUID, status string) {
	t.Helper()
	err := db.New(pool).UpdateSessionStatusOnly(context.Background(), db.UpdateSessionStatusOnlyParams{
		ID:     sessionID,
		Status: status,
	})
	require.NoError(t, err)
}

// seedEvaluation inserts an evaluation row and returns its ID.
func seedEvaluation(t *testing.T, pool *pgxpool.Pool, sessionID uuid.UUID) uuid.UUID {
	t.Helper()
	evalID, err := db.New(pool).InsertEvaluation(context.Background(), db.InsertEvaluationParams{
		SessionID:          sessionID,
		ScoreRequirements:  3,
		ScoreArchitecture:  4,
		ScoreDeepDive:      2,
		ScoreScalability:   3,
		ScoreCommunication: 4,
		ScoreOverall:       3,
		Strengths:          []byte(`["Good requirements gathering"]`),
		Gaps:               []byte(`["Missing cache layer"]`),
		Advice:             "Focus on depth.",
	})
	require.NoError(t, err)
	return evalID
}

// seedMessage inserts a message into a session and returns it.
func seedMessage(t *testing.T, pool *pgxpool.Pool, sessionID uuid.UUID, seq int32, role, content string) db.Message {
	t.Helper()
	msg, err := db.New(pool).InsertMessage(context.Background(), db.InsertMessageParams{
		ID:          uuid.New(),
		SessionID:   sessionID,
		Seq:         seq,
		Role:        role,
		Content:     content,
		InputMethod: pgtype.Text{String: "text", Valid: true},
	})
	require.NoError(t, err)
	return msg
}

// seedAnnotation inserts an annotation for the given evaluation and message.
func seedAnnotation(t *testing.T, pool *pgxpool.Pool, evalID, msgID uuid.UUID, annType, content string) {
	t.Helper()
	err := db.New(pool).InsertAnnotation(context.Background(), db.InsertAnnotationParams{
		EvaluationID:   evalID,
		MessageID:      msgID,
		AnnotationType: annType,
		Content:        content,
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// evalTestEnv bundles the common objects used by each evaluation test.
type evalTestEnv struct {
	backend *backend.Backend
	mux     *http.ServeMux
	pool    *pgxpool.Pool
}

func newEvalTestEnv(t *testing.T) evalTestEnv {
	t.Helper()
	b := testutil.NewTestBackend(t)
	mux := http.NewServeMux()
	testutil.MustRegisterRoutes(t, mux, b)
	return evalTestEnv{backend: b, mux: mux, pool: b.Pool()}
}

// doRequest builds an authenticated HTTP request and returns the recorder.
func (e evalTestEnv) doRequest(t *testing.T, method, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

// ---------------------------------------------------------------------------
// GET /api/sessions/{id}/evaluation — HTTP concerns only
// ---------------------------------------------------------------------------

func TestGetEvaluation_Returns200WithJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	env := newEvalTestEnv(t)

	userID := createTestUser(t, env.pool)
	question := createTestQuestion(t, env.pool)
	session := createTestSession(t, env.pool, userID, question.ID)
	setSessionStatus(t, env.pool, session.ID, "reviewed")

	msg1 := seedMessage(t, env.pool, session.ID, 1, "interviewer", "Tell me about requirements.")
	evalID := seedEvaluation(t, env.pool, session.ID)
	seedAnnotation(t, env.pool, evalID, msg1.ID, "strength", "Good opening question.")

	cookie := createAuthCookie(t, env.pool, userID)

	rec := env.doRequest(t, http.MethodGet, "/api/sessions/"+session.ID.String()+"/evaluation", cookie)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp backend.EvaluationResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))

	// Verify JSON shape — detailed value assertions live in backend tests.
	assert.Equal(t, "reviewed", resp.Status)
	assert.NotNil(t, resp.Scores)
	assert.NotEmpty(t, resp.Strengths)
	assert.NotEmpty(t, resp.Gaps)
	assert.NotEmpty(t, resp.Advice)
	assert.NotEmpty(t, resp.Annotations)
}

func TestGetEvaluation_Returns403ForWrongUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	env := newEvalTestEnv(t)

	ownerID := createTestUser(t, env.pool)
	otherID := createTestUser(t, env.pool)
	question := createTestQuestion(t, env.pool)
	session := createTestSession(t, env.pool, ownerID, question.ID)
	setSessionStatus(t, env.pool, session.ID, "reviewed")

	cookie := createAuthCookie(t, env.pool, otherID)

	rec := env.doRequest(t, http.MethodGet, "/api/sessions/"+session.ID.String()+"/evaluation", cookie)

	require.Equal(t, http.StatusForbidden, rec.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Contains(t, body["error"], "does not belong")
}

func TestGetEvaluation_Returns404WhenNotReady(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	env := newEvalTestEnv(t)

	userID := createTestUser(t, env.pool)
	question := createTestQuestion(t, env.pool)
	session := createTestSession(t, env.pool, userID, question.ID) // status=active

	cookie := createAuthCookie(t, env.pool, userID)

	rec := env.doRequest(t, http.MethodGet, "/api/sessions/"+session.ID.String()+"/evaluation", cookie)

	require.Equal(t, http.StatusNotFound, rec.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Contains(t, body["error"], "not ready")
}

// ---------------------------------------------------------------------------
// POST /api/sessions/{id}/evaluate — HTTP concerns only
// ---------------------------------------------------------------------------

func TestRetryEvaluation_Returns202(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	env := newEvalTestEnv(t)

	userID := createTestUser(t, env.pool)
	question := createTestQuestion(t, env.pool)
	session := createTestSession(t, env.pool, userID, question.ID)
	setSessionStatus(t, env.pool, session.ID, "evaluation_failed")

	cookie := createAuthCookie(t, env.pool, userID)

	rec := env.doRequest(t, http.MethodPost, "/api/sessions/"+session.ID.String()+"/evaluate", cookie)

	require.Equal(t, http.StatusAccepted, rec.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Equal(t, "evaluating", body["status"])
}

func TestRetryEvaluation_Returns409WhenNotFailed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	env := newEvalTestEnv(t)

	userID := createTestUser(t, env.pool)
	question := createTestQuestion(t, env.pool)
	session := createTestSession(t, env.pool, userID, question.ID)
	setSessionStatus(t, env.pool, session.ID, "reviewed")

	cookie := createAuthCookie(t, env.pool, userID)

	rec := env.doRequest(t, http.MethodPost, "/api/sessions/"+session.ID.String()+"/evaluate", cookie)

	require.Equal(t, http.StatusConflict, rec.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Contains(t, body["error"], "not in evaluation_failed")
}

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
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/handler"
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
	b := newTestBackend(t)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)
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
// Tests
// ---------------------------------------------------------------------------

func TestGetEvaluation_Reviewed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	env := newEvalTestEnv(t)

	// Seed data.
	userID := createTestUser(t, env.pool)
	question := createTestQuestion(t, env.pool)
	session := createTestSession(t, env.pool, userID, question.ID)
	setSessionStatus(t, env.pool, session.ID, "reviewed")

	msg1 := seedMessage(t, env.pool, session.ID, 1, "interviewer", "Tell me about requirements.")
	msg2 := seedMessage(t, env.pool, session.ID, 2, "candidate", "We need to handle 10k RPS.")

	evalID := seedEvaluation(t, env.pool, session.ID)
	seedAnnotation(t, env.pool, evalID, msg1.ID, "strength", "Good opening question.")
	seedAnnotation(t, env.pool, evalID, msg2.ID, "gap", "Did not discuss caching.")

	cookie := createAuthCookie(t, env.pool, userID)

	// Hit endpoint.
	rec := env.doRequest(t, http.MethodGet, "/api/sessions/"+session.ID.String()+"/evaluation", cookie)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp backend.EvaluationResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))

	assert.Equal(t, "reviewed", resp.Status)

	require.NotNil(t, resp.Scores)
	assert.Equal(t, int32(3), resp.Scores.Requirements)
	assert.Equal(t, int32(4), resp.Scores.Architecture)
	assert.Equal(t, int32(2), resp.Scores.DeepDive)
	assert.Equal(t, int32(3), resp.Scores.Scalability)
	assert.Equal(t, int32(4), resp.Scores.Communication)
	assert.Equal(t, int32(3), resp.Scores.Overall)

	assert.Equal(t, []string{"Good requirements gathering"}, resp.Strengths)
	assert.Equal(t, []string{"Missing cache layer"}, resp.Gaps)
	assert.Equal(t, "Focus on depth.", resp.Advice)

	require.Len(t, resp.Annotations, 2)
	assert.Equal(t, int32(1), resp.Annotations[0].MessageSeq)
	assert.Equal(t, "strength", resp.Annotations[0].Type)
	assert.Equal(t, "Good opening question.", resp.Annotations[0].Content)
	assert.Equal(t, int32(2), resp.Annotations[1].MessageSeq)
	assert.Equal(t, "gap", resp.Annotations[1].Type)
	assert.Equal(t, "Did not discuss caching.", resp.Annotations[1].Content)
}

func TestGetEvaluation_Failed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	env := newEvalTestEnv(t)

	userID := createTestUser(t, env.pool)
	question := createTestQuestion(t, env.pool)
	session := createTestSession(t, env.pool, userID, question.ID)
	setSessionStatus(t, env.pool, session.ID, "evaluation_failed")

	cookie := createAuthCookie(t, env.pool, userID)

	rec := env.doRequest(t, http.MethodGet, "/api/sessions/"+session.ID.String()+"/evaluation", cookie)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp backend.EvaluationResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))

	assert.Equal(t, "evaluation_failed", resp.Status)
	assert.Nil(t, resp.Scores)
	assert.Empty(t, resp.Strengths)
	assert.Empty(t, resp.Gaps)
	assert.Empty(t, resp.Advice)
	assert.Empty(t, resp.Annotations)
}

func TestGetEvaluation_NotReady(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	env := newEvalTestEnv(t)

	userID := createTestUser(t, env.pool)
	question := createTestQuestion(t, env.pool)
	session := createTestSession(t, env.pool, userID, question.ID) // status=active by default

	cookie := createAuthCookie(t, env.pool, userID)

	rec := env.doRequest(t, http.MethodGet, "/api/sessions/"+session.ID.String()+"/evaluation", cookie)

	require.Equal(t, http.StatusNotFound, rec.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Contains(t, body["error"], "not ready")
}

func TestGetEvaluation_WrongUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	env := newEvalTestEnv(t)

	ownerID := createTestUser(t, env.pool)
	otherID := createTestUser(t, env.pool)
	question := createTestQuestion(t, env.pool)
	session := createTestSession(t, env.pool, ownerID, question.ID)
	setSessionStatus(t, env.pool, session.ID, "reviewed")

	// Authenticate as the other user, not the session owner.
	cookie := createAuthCookie(t, env.pool, otherID)

	rec := env.doRequest(t, http.MethodGet, "/api/sessions/"+session.ID.String()+"/evaluation", cookie)

	require.Equal(t, http.StatusForbidden, rec.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Contains(t, body["error"], "does not belong")
}

func TestGetEvaluation_ReviewedButMissingEvaluation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	env := newEvalTestEnv(t)

	// Seed data: session with status "reviewed" but NO evaluation row.
	userID := createTestUser(t, env.pool)
	question := createTestQuestion(t, env.pool)
	session := createTestSession(t, env.pool, userID, question.ID)
	setSessionStatus(t, env.pool, session.ID, "reviewed")

	cookie := createAuthCookie(t, env.pool, userID)

	rec := env.doRequest(t, http.MethodGet, "/api/sessions/"+session.ID.String()+"/evaluation", cookie)

	// Backend returns ErrEvaluationNotReady when status=reviewed but no eval row exists.
	require.Equal(t, http.StatusNotFound, rec.Code)

	var body map[string]string
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	assert.Contains(t, body["error"], "not ready")
}

func TestRetryEvaluation_Success(t *testing.T) {
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

	// Verify session status was updated in DB.
	updated, err := db.New(env.pool).GetSession(context.Background(), session.ID)
	require.NoError(t, err)
	assert.Equal(t, "evaluating", updated.Status)
}

func TestRetryEvaluation_NotFailed(t *testing.T) {
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

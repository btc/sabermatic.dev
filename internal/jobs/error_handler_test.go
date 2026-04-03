package jobs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

func TestErrorHandler_FinalAttemptSetsEvaluationFailed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	pool := startTestPostgres(t)

	seed := seedSessionWithMessages(t, ctx, pool, 2)

	// Set session status to "evaluating" (the state it would be in during eval).
	q := db.New(pool)
	err := q.UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
		ID:     seed.SessionID,
		Status: "evaluating",
	})
	require.NoError(t, err)

	// Construct a rivertype.JobRow simulating a final attempt.
	encodedArgs, err := json.Marshal(jobs.EvaluateSessionArgs{SessionID: seed.SessionID})
	require.NoError(t, err)

	jobRow := &rivertype.JobRow{
		Kind:        "evaluate_session",
		Attempt:     4,
		MaxAttempts: 4,
		EncodedArgs: encodedArgs,
	}

	handler := &jobs.ErrorHandler{Pool: pool}
	handler.HandleError(ctx, jobRow, fmt.Errorf("some LLM error"))

	// Assert: session status is now evaluation_failed.
	session, err := q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	assert.Equal(t, "evaluation_failed", session.Status)
}

func TestErrorHandler_NonFinalAttemptIsNoOp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	pool := startTestPostgres(t)

	seed := seedSessionWithMessages(t, ctx, pool, 2)

	// Set session status to "evaluating".
	q := db.New(pool)
	err := q.UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
		ID:     seed.SessionID,
		Status: "evaluating",
	})
	require.NoError(t, err)

	// Construct a rivertype.JobRow with a non-final attempt.
	encodedArgs, err := json.Marshal(jobs.EvaluateSessionArgs{SessionID: seed.SessionID})
	require.NoError(t, err)

	jobRow := &rivertype.JobRow{
		Kind:        "evaluate_session",
		Attempt:     2,
		MaxAttempts: 4,
		EncodedArgs: encodedArgs,
	}

	handler := &jobs.ErrorHandler{Pool: pool}
	handler.HandleError(ctx, jobRow, fmt.Errorf("transient error"))

	// Assert: session status is still "evaluating" (unchanged).
	session, err := q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	assert.Equal(t, "evaluating", session.Status)
}

func TestErrorHandler_NonEvaluateSessionKindIsNoOp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	pool := startTestPostgres(t)

	seed := seedSessionWithMessages(t, ctx, pool, 2)

	// Set session status to "evaluating".
	q := db.New(pool)
	err := q.UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
		ID:     seed.SessionID,
		Status: "evaluating",
	})
	require.NoError(t, err)

	// Construct a jobRow with a different kind (e.g., "send_email"), final attempt.
	encodedArgs, err := json.Marshal(jobs.EvaluateSessionArgs{SessionID: seed.SessionID})
	require.NoError(t, err)

	jobRow := &rivertype.JobRow{
		Kind:        "send_email",
		Attempt:     4,
		MaxAttempts: 4,
		EncodedArgs: encodedArgs,
	}

	handler := &jobs.ErrorHandler{Pool: pool}
	handler.HandleError(ctx, jobRow, fmt.Errorf("email error"))

	// Assert: session status is still "evaluating" (no change for non-evaluate_session).
	session, err := q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	assert.Equal(t, "evaluating", session.Status)
}

func TestErrorHandler_HandlePanic_FinalAttempt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	pool := startTestPostgres(t)

	seed := seedSessionWithMessages(t, ctx, pool, 2)

	q := db.New(pool)
	err := q.UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
		ID:     seed.SessionID,
		Status: "evaluating",
	})
	require.NoError(t, err)

	encodedArgs, err := json.Marshal(jobs.EvaluateSessionArgs{SessionID: seed.SessionID})
	require.NoError(t, err)

	jobRow := &rivertype.JobRow{
		Kind:        "evaluate_session",
		Attempt:     4,
		MaxAttempts: 4,
		EncodedArgs: encodedArgs,
	}

	handler := &jobs.ErrorHandler{Pool: pool}
	handler.HandlePanic(ctx, jobRow, "nil pointer dereference", "goroutine 1 [running]:\n...")

	// Assert: session status is evaluation_failed even for panics on final attempt.
	session, err := q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	assert.Equal(t, "evaluation_failed", session.Status)
}

func TestErrorHandler_HandlePanic_NonFinalAttempt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	pool := startTestPostgres(t)

	seed := seedSessionWithMessages(t, ctx, pool, 2)

	q := db.New(pool)
	err := q.UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
		ID:     seed.SessionID,
		Status: "evaluating",
	})
	require.NoError(t, err)

	encodedArgs, err := json.Marshal(jobs.EvaluateSessionArgs{SessionID: seed.SessionID})
	require.NoError(t, err)

	jobRow := &rivertype.JobRow{
		Kind:        "evaluate_session",
		Attempt:     1,
		MaxAttempts: 4,
		EncodedArgs: encodedArgs,
	}

	handler := &jobs.ErrorHandler{Pool: pool}
	handler.HandlePanic(ctx, jobRow, "some panic", "stack trace")

	// Assert: non-final panic does not change status.
	session, err := q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	assert.Equal(t, "evaluating", session.Status)
}

func TestErrorHandler_InvalidEncodedArgs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	pool := startTestPostgres(t)

	seed := seedSessionWithMessages(t, ctx, pool, 2)

	q := db.New(pool)
	err := q.UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
		ID:     seed.SessionID,
		Status: "evaluating",
	})
	require.NoError(t, err)

	// Invalid JSON in EncodedArgs — handler should return nil without panicking.
	jobRow := &rivertype.JobRow{
		Kind:        "evaluate_session",
		Attempt:     4,
		MaxAttempts: 4,
		EncodedArgs: []byte(`{invalid json`),
	}

	handler := &jobs.ErrorHandler{Pool: pool}
	result := handler.HandleError(ctx, jobRow, fmt.Errorf("some error"))
	assert.Nil(t, result)

	// Session status should be unchanged since unmarshal failed.
	session, err := q.GetSession(ctx, seed.SessionID)
	require.NoError(t, err)
	assert.Equal(t, "evaluating", session.Status)
}

func TestErrorHandler_NonExistentSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	pool := startTestPostgres(t)

	// Use a random session ID that doesn't exist in DB.
	fakeSessionID := uuid.New()
	encodedArgs, err := json.Marshal(jobs.EvaluateSessionArgs{SessionID: fakeSessionID})
	require.NoError(t, err)

	jobRow := &rivertype.JobRow{
		Kind:        "evaluate_session",
		Attempt:     4,
		MaxAttempts: 4,
		EncodedArgs: encodedArgs,
	}

	// Should not panic — the UpdateSessionStatusOnly will silently affect 0 rows.
	handler := &jobs.ErrorHandler{Pool: pool}
	result := handler.HandleError(ctx, jobRow, fmt.Errorf("some error"))
	assert.Nil(t, result)
}

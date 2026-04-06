# ConnectRPC Batch 2 Stub Implementations

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement 4 unimplemented ConnectRPC stubs: GetTranscript, ArchiveSessions, DeleteAccount, UpdateProfile.

**Architecture:** Add sqlc queries → backend methods → RPC handlers, following the existing layered pattern. All business logic in backend methods; RPC handlers are thin validation + mapping. TDD throughout.

**Tech Stack:** Go, sqlc, ConnectRPC, pgx/v5, testify, shared Postgres test container

**Spec:** `docs/superpowers/specs/2026-04-05-connectrpc-batch2-stubs-design.md`

---

## Important Design Note: AuthService Has No Auth Interceptor

`AuthService` is registered with `publicOpts` (no auth interceptor) in `internal/rpc/register.go:51`. Most auth endpoints are public (signup, login, etc.). `Logout` authenticates manually by reading the session cookie from the request header and calling `b.Logout()`.

`DeleteAccount` MUST follow the same pattern — it cannot use `auth.UserFromContext(ctx)`. It must read the cookie, authenticate the session via `b.AuthenticateSession()`, and then proceed.

---

## File Map

| File | Action | Purpose |
|------|--------|---------|
| `sql/queries/users.sql` | Modify | Add `DeleteAccount` CTE, `UpdateUserDisplayName` queries |
| `sql/queries/sessions.sql` | Modify | Add `ArchiveSessionsBulk` query |
| `internal/db/*` | Regenerate | `sqlc generate` |
| `internal/backend/auth.go` | Modify | Add `DeleteAccount` method |
| `internal/backend/session.go` | Modify | Add `ArchiveSessions`, `GetTranscript` methods |
| `internal/backend/user.go` | Create | Add `UpdateDisplayName` method |
| `internal/backend/auth_test.go` | Modify | Add `TestDeleteAccount` |
| `internal/backend/session_test.go` | Modify | Add `TestArchiveSessions`, `TestGetTranscript` |
| `internal/backend/user_test.go` | Create | Add `TestUpdateDisplayName` |
| `internal/rpc/auth/server.go` | Modify | Implement `DeleteAccount`, extract `clearSessionCookie` helper |
| `internal/rpc/auth/server_test.go` | Modify | Unskip + fix `DeleteAccount` tests |
| `internal/rpc/session/server.go` | Modify | Implement `ArchiveSessions`, `GetTranscript`, add `messageToProto` |
| `internal/rpc/session/server_test.go` | Modify | Add `ArchiveSessions`, `GetTranscript` tests |
| `internal/rpc/user/server.go` | Modify | Implement `UpdateProfile`, add `dbUserToProto` |
| `internal/rpc/user/server_test.go` | Modify | Add `UpdateProfile` tests |

---

### Task 1: SQL Queries + sqlc Regenerate

**Files:**
- Modify: `sql/queries/users.sql`
- Modify: `sql/queries/sessions.sql`
- Regenerate: `internal/db/`

- [ ] **Step 1: Add `DeleteAccount` query to users.sql**

Append to `sql/queries/users.sql`:

```sql
-- name: DeleteAccount :exec
-- Soft-deletes the user and wipes all auth sessions in one round-trip.
-- Idempotent: re-calling on a deleted user is a no-op on the user row.
WITH soft_delete AS (
    UPDATE users SET deleted_at = NOW(), updated_at = NOW()
    WHERE id = @id AND deleted_at IS NULL
)
DELETE FROM auth_sessions WHERE user_id = @id;
```

- [ ] **Step 2: Add `UpdateUserDisplayName` query to users.sql**

Append to `sql/queries/users.sql`:

```sql
-- name: UpdateUserDisplayName :one
UPDATE users SET display_name = @display_name, updated_at = NOW()
WHERE id = @id AND deleted_at IS NULL
RETURNING *;
```

- [ ] **Step 3: Add `ArchiveSessionsBulk` query to sessions.sql**

Append to `sql/queries/sessions.sql`:

```sql
-- name: ArchiveSessionsBulk :execrows
UPDATE interview_sessions
SET archived_at = CASE WHEN @archive::bool THEN NOW() ELSE NULL END,
    updated_at = NOW()
WHERE id = ANY(@session_ids::uuid[])
  AND user_id = @user_id;
```

- [ ] **Step 4: Run sqlc generate**

Run: `sqlc generate`
Expected: Clean exit, regenerated files in `internal/db/`

- [ ] **Step 5: Verify build compiles**

Run: `go build ./...`
Expected: Clean compile

- [ ] **Step 6: Commit**

```bash
git add sql/queries/users.sql sql/queries/sessions.sql internal/db/
git commit -m "feat(db): add DeleteAccount, UpdateUserDisplayName, ArchiveSessionsBulk queries"
```

---

### Task 2: Backend Method — GetTranscript

**Files:**
- Modify: `internal/backend/session.go`
- Create: `internal/backend/session_transcript_test.go`

This is the simplest — both queries already exist. Just wire them together with an ownership check.

- [ ] **Step 1: Write failing backend test**

Create `internal/backend/session_transcript_test.go`:

```go
package backend_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/db"
)

func TestGetTranscript_Success(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)
	session := createTestSession(t, b, userID, questionID)

	// Insert messages.
	queries := db.New(b.Pool())
	_, err := queries.InsertMessage(ctx, db.InsertMessageParams{
		ID:        uuid.New(),
		SessionID: session.ID,
		Seq:       1,
		Role:      "interviewer",
		Content:   "Tell me about system design.",
	})
	require.NoError(t, err)
	_, err = queries.InsertMessage(ctx, db.InsertMessageParams{
		ID:          uuid.New(),
		SessionID:   session.ID,
		Seq:         2,
		Role:        "candidate",
		Content:     "I would start by...",
		InputMethod: pgtype.Text{String: "voice", Valid: true},
	})
	require.NoError(t, err)

	msgs, err := b.GetTranscript(ctx, session.ID, userID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, int32(1), msgs[0].Seq)
	require.Equal(t, int32(2), msgs[1].Seq)
	require.Equal(t, "interviewer", msgs[0].Role)
	require.Equal(t, "candidate", msgs[1].Role)
}

func TestGetTranscript_EmptyTranscript(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)
	session := createTestSession(t, b, userID, questionID)

	msgs, err := b.GetTranscript(ctx, session.ID, userID)
	require.NoError(t, err)
	require.NotNil(t, msgs, "empty transcript must be non-nil slice")
	require.Empty(t, msgs)
}

func TestGetTranscript_NotOwned(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	ownerID := backendtest.SeedUser(t, b)
	otherID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)
	session := createTestSession(t, b, ownerID, questionID)

	_, err := b.GetTranscript(ctx, session.ID, otherID)
	require.ErrorIs(t, err, backend.ErrSessionNotOwned)
}

func TestGetTranscript_NotFound(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)

	_, err := b.GetTranscript(ctx, uuid.New(), userID)
	require.ErrorIs(t, err, backend.ErrSessionNotFound)
}
```

This file needs `seedQuestion` and `createTestSession` helpers. Check if they already exist in the backend test package. If not, add them to this file:

```go
// seedQuestion inserts a test question and returns its ID.
func seedQuestion(t *testing.T, b *backend.Backend) uuid.UUID {
	t.Helper()
	queries := db.New(b.Pool())
	id, err := queries.InsertQuestion(context.Background(), db.InsertQuestionParams{
		UserID:         pgtype.UUID{},
		Title:          "Test Question",
		Prompt:         "Design a test system",
		Difficulty:     "medium",
		Tags:           []string{"testing"},
		Source:         "seed",
		CoachRationale: pgtype.Text{},
	})
	require.NoError(t, err)
	return id
}

// createTestSession creates a session via the real Backend.CreateSession flow.
func createTestSession(t *testing.T, b *backend.Backend, userID, questionID uuid.UUID) db.InterviewSession {
	t.Helper()
	session, err := b.CreateSession(context.Background(), backend.CreateSessionParams{
		UserID:          userID,
		QuestionID:      questionID,
		DurationMinutes: 15,
		TTSEnabled:      false,
		Plan:            "free",
	})
	require.NoError(t, err)
	return session
}
```

Check whether `seedQuestion` and `createTestSession` (or equivalents) already exist in existing `backend/*_test.go` files. If they do, reuse them. If not, add them to this new file.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/backend/ -run TestGetTranscript -v`
Expected: FAIL — `b.GetTranscript` does not exist yet

- [ ] **Step 3: Implement GetTranscript**

Add to `internal/backend/session.go`, after the `GetMessagesBySession` method:

```go
// GetTranscript returns all messages for a session after verifying ownership.
// Returns ErrSessionNotFound if the session does not exist,
// ErrSessionNotOwned if it belongs to a different user.
func (b *Backend) GetTranscript(ctx context.Context, sessionID, userID uuid.UUID) (_ []db.Message, err error) {
	ctx, span := tracer.Start(ctx, "Backend.GetTranscript")
	defer func() { drilotel.End(span, err) }()

	if _, err := b.GetSessionForUser(ctx, sessionID, userID); err != nil {
		return nil, err
	}

	msgs, err := b.GetMessagesBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if msgs == nil {
		msgs = []db.Message{}
	}
	return msgs, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/backend/ -run TestGetTranscript -v`
Expected: All 4 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/backend/session.go internal/backend/session_transcript_test.go
git commit -m "feat(backend): add GetTranscript method with ownership check"
```

---

### Task 3: Backend Method — ArchiveSessions

**Files:**
- Modify: `internal/backend/session.go`
- Create: `internal/backend/session_archive_test.go`

- [ ] **Step 1: Write failing backend test**

Create `internal/backend/session_archive_test.go`:

```go
package backend_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/db"
)

func TestArchiveSessions_Archive(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)
	s1 := createTestSession(t, b, userID, questionID)
	s2 := createTestSession(t, b, userID, questionID)

	count, err := b.ArchiveSessions(ctx, userID, []uuid.UUID{s1.ID, s2.ID}, true)
	require.NoError(t, err)
	require.Equal(t, int64(2), count)

	// Verify archived_at is set.
	queries := db.New(b.Pool())
	row, err := queries.GetSessionByID(ctx, s1.ID)
	require.NoError(t, err)
	require.True(t, row.ArchivedAt.Valid, "expected archived_at to be set")
}

func TestArchiveSessions_Unarchive(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)
	s1 := createTestSession(t, b, userID, questionID)

	// Archive first.
	_, err := b.ArchiveSessions(ctx, userID, []uuid.UUID{s1.ID}, true)
	require.NoError(t, err)

	// Unarchive.
	count, err := b.ArchiveSessions(ctx, userID, []uuid.UUID{s1.ID}, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)

	queries := db.New(b.Pool())
	row, err := queries.GetSessionByID(ctx, s1.ID)
	require.NoError(t, err)
	require.False(t, row.ArchivedAt.Valid, "expected archived_at to be cleared")
}

func TestArchiveSessions_OwnershipFilter(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	ownerID := backendtest.SeedUser(t, b)
	otherID := backendtest.SeedUser(t, b)
	questionID := seedQuestion(t, b)
	s1 := createTestSession(t, b, ownerID, questionID)

	// Other user tries to archive owner's session — silently skipped.
	count, err := b.ArchiveSessions(ctx, otherID, []uuid.UUID{s1.ID}, true)
	require.NoError(t, err)
	require.Equal(t, int64(0), count)
}

func TestArchiveSessions_EmptyList(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)

	count, err := b.ArchiveSessions(ctx, userID, []uuid.UUID{}, true)
	require.NoError(t, err)
	require.Equal(t, int64(0), count)
}
```

Note: `seedQuestion` and `createTestSession` were defined in Task 2. If they are in a separate file, ensure they are accessible from this file (same package `backend_test`).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/backend/ -run TestArchiveSessions -v`
Expected: FAIL — `b.ArchiveSessions` does not exist yet

- [ ] **Step 3: Implement ArchiveSessions**

Add to `internal/backend/session.go`, after the `GetTranscript` method:

```go
// ArchiveSessions bulk archives or unarchives sessions owned by the given user.
// Sessions not owned by the user are silently skipped. Returns the number of
// rows updated.
func (b *Backend) ArchiveSessions(ctx context.Context, userID uuid.UUID, sessionIDs []uuid.UUID, archive bool) (_ int64, err error) {
	ctx, span := tracer.Start(ctx, "Backend.ArchiveSessions")
	defer func() { drilotel.End(span, err) }()

	count, err := db.New(b.pool).ArchiveSessionsBulk(ctx, db.ArchiveSessionsBulkParams{
		Archive:    archive,
		SessionIds: sessionIDs,
		UserID:     userID,
	})
	if err != nil {
		return 0, fmt.Errorf("archive sessions: %w", err)
	}
	return count, nil
}
```

**Important:** Check the exact generated param struct name and field names from `internal/db/sessions.sql.go` after sqlc generation (Task 1). The struct is likely `ArchiveSessionsBulkParams` with fields `Archive bool`, `SessionIds []uuid.UUID`, `UserID uuid.UUID`. Adjust if the generated names differ.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/backend/ -run TestArchiveSessions -v`
Expected: All 4 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/backend/session.go internal/backend/session_archive_test.go
git commit -m "feat(backend): add ArchiveSessions bulk archive/unarchive method"
```

---

### Task 4: Backend Method — DeleteAccount

**Files:**
- Modify: `internal/backend/auth.go`
- Modify: `internal/backend/auth_test.go`

- [ ] **Step 1: Write failing backend test**

Add to `internal/backend/auth_test.go`:

```go
func TestDeleteAccount_Success(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	// Signup creates user + auth session (via login).
	res, err := b.Signup(ctx, backend.SignupParams{
		Email:       "delete-me@example.com",
		Password:    "strongpass1",
		DisplayName: "Delete Me",
	})
	require.NoError(t, err)

	// Create an auth session (simulating login).
	_, err = b.Login(ctx, backend.LoginParams{
		Email:    "delete-me@example.com",
		Password: "strongpass1",
	})
	require.NoError(t, err)

	// Delete account.
	err = b.DeleteAccount(ctx, res.UserID)
	require.NoError(t, err)

	// User should not be findable by normal lookup.
	queries := db.New(b.Pool())
	_, err = queries.GetUserByID(ctx, res.UserID)
	require.Error(t, err, "expected user not found after soft delete")

	// User should still exist with deleted_at set.
	user, err := queries.GetUserByIDIncludingDeleted(ctx, res.UserID)
	require.NoError(t, err)
	require.True(t, user.DeletedAt.Valid, "expected deleted_at to be set")

	// Login should fail (user is soft-deleted).
	_, err = b.Login(ctx, backend.LoginParams{
		Email:    "delete-me@example.com",
		Password: "strongpass1",
	})
	require.ErrorIs(t, err, backend.ErrInvalidCredentials)
}

func TestDeleteAccount_Idempotent(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	res, err := b.Signup(ctx, backend.SignupParams{
		Email:       "idempotent-delete@example.com",
		Password:    "strongpass1",
		DisplayName: "Idem",
	})
	require.NoError(t, err)

	err = b.DeleteAccount(ctx, res.UserID)
	require.NoError(t, err)

	// Second delete should not error.
	err = b.DeleteAccount(ctx, res.UserID)
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/backend/ -run TestDeleteAccount -v`
Expected: FAIL — `b.DeleteAccount` does not exist yet

- [ ] **Step 3: Implement DeleteAccount**

Add to `internal/backend/auth.go`, after the `Logout` method:

```go
// DeleteAccount soft-deletes the user and wipes all auth sessions in a single
// query (CTE). Idempotent — calling on an already-deleted user is a no-op.
func (b *Backend) DeleteAccount(ctx context.Context, userID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.DeleteAccount")
	defer func() { drilotel.End(span, err) }()

	if err := db.New(b.pool).DeleteAccount(ctx, userID); err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/backend/ -run TestDeleteAccount -v`
Expected: Both tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/backend/auth.go internal/backend/auth_test.go
git commit -m "feat(backend): add DeleteAccount soft-delete method"
```

---

### Task 5: Backend Method — UpdateDisplayName

**Files:**
- Create: `internal/backend/user.go`
- Create: `internal/backend/user_test.go`

- [ ] **Step 1: Write failing backend test**

Create `internal/backend/user_test.go`:

```go
package backend_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/db"
)

func TestUpdateDisplayName_Success(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)

	updated, err := b.UpdateDisplayName(ctx, userID, "New Name")
	require.NoError(t, err)
	require.Equal(t, "New Name", updated.DisplayName)
	require.Equal(t, userID, updated.ID)
}

func TestUpdateDisplayName_DeletedUser(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()

	userID := backendtest.SeedUser(t, b)

	// Soft-delete the user first.
	err := db.New(b.Pool()).SoftDeleteUser(ctx, userID)
	require.NoError(t, err)

	_, err = b.UpdateDisplayName(ctx, userID, "Nope")
	require.ErrorIs(t, err, backend.ErrUserNotFound)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/backend/ -run TestUpdateDisplayName -v`
Expected: FAIL — `b.UpdateDisplayName` does not exist yet

- [ ] **Step 3: Implement UpdateDisplayName**

Create `internal/backend/user.go`:

```go
package backend

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
)

// UpdateDisplayName updates the user's display name.
// Returns ErrUserNotFound if the user doesn't exist or is soft-deleted.
func (b *Backend) UpdateDisplayName(ctx context.Context, userID uuid.UUID, displayName string) (_ db.User, err error) {
	ctx, span := tracer.Start(ctx, "Backend.UpdateDisplayName")
	defer func() { drilotel.End(span, err) }()

	user, err := db.New(b.pool).UpdateUserDisplayName(ctx, db.UpdateUserDisplayNameParams{
		ID:          userID,
		DisplayName: displayName,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.User{}, ErrUserNotFound
		}
		return db.User{}, fmt.Errorf("update display name: %w", err)
	}
	return user, nil
}
```

**Important:** Check the exact generated param struct name from `internal/db/users.sql.go` after sqlc generation (Task 1). It's likely `UpdateUserDisplayNameParams` with fields `ID uuid.UUID` and `DisplayName string`. Adjust if the generated names differ.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/backend/ -run TestUpdateDisplayName -v`
Expected: Both tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/backend/user.go internal/backend/user_test.go
git commit -m "feat(backend): add UpdateDisplayName method"
```

---

### Task 6: RPC Handler — GetTranscript

**Files:**
- Modify: `internal/rpc/session/server.go`
- Modify: `internal/rpc/session/server_test.go`

- [ ] **Step 1: Write failing RPC test**

Add to `internal/rpc/session/server_test.go`:

```go
func TestGetTranscript_Success(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	questionID := seedQuestion(t, b)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	// Create a session.
	createResp, err := client.CreateSession(context.Background(), connect.NewRequest(&drillv1.CreateSessionRequest{
		QuestionId:      questionID.String(),
		DurationMinutes: 15,
	}))
	require.NoError(t, err)
	sessionID := createResp.Msg.Session.Id

	// Insert messages directly.
	queries := db.New(b.Pool())
	sid, _ := uuid.Parse(sessionID)
	_, err = queries.InsertMessage(context.Background(), db.InsertMessageParams{
		ID:        uuid.New(),
		SessionID: sid,
		Seq:       1,
		Role:      "interviewer",
		Content:   "Tell me about system design.",
	})
	require.NoError(t, err)
	_, err = queries.InsertMessage(context.Background(), db.InsertMessageParams{
		ID:          uuid.New(),
		SessionID:   sid,
		Seq:         2,
		Role:        "candidate",
		Content:     "I would start by...",
		InputMethod: pgtype.Text{String: "voice", Valid: true},
	})
	require.NoError(t, err)

	// Fetch transcript.
	resp, err := client.GetTranscript(context.Background(), connect.NewRequest(&drillv1.GetTranscriptRequest{
		SessionId: sessionID,
	}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Messages, 2)
	require.Equal(t, int32(1), resp.Msg.Messages[0].Seq)
	require.Equal(t, "interviewer", resp.Msg.Messages[0].Role)
	require.Equal(t, int32(2), resp.Msg.Messages[1].Seq)
	require.Equal(t, "candidate", resp.Msg.Messages[1].Role)
	require.NotNil(t, resp.Msg.Messages[1].InputMethod)
	require.Equal(t, "voice", *resp.Msg.Messages[1].InputMethod)
}

func TestGetTranscript_Unauthenticated(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startSessionServer(t, b)
	client := drillv1connect.NewSessionServiceClient(&http.Client{}, srvURL)

	_, err := client.GetTranscript(context.Background(), connect.NewRequest(&drillv1.GetTranscriptRequest{
		SessionId: uuid.New().String(),
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestGetTranscript_InvalidID(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.GetTranscript(context.Background(), connect.NewRequest(&drillv1.GetTranscriptRequest{
		SessionId: "not-a-uuid",
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestGetTranscript_NotOwned(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	questionID := seedQuestion(t, b)
	srvURL := startSessionServer(t, b)

	// User A creates a session.
	tokenA := testutil.SignupAndLogin(t, b)
	clientA := authedClient(t, srvURL, tokenA)
	createResp, err := clientA.CreateSession(context.Background(), connect.NewRequest(&drillv1.CreateSessionRequest{
		QuestionId:      questionID.String(),
		DurationMinutes: 15,
	}))
	require.NoError(t, err)

	// User B tries to read User A's transcript.
	// SignupAndLogin always creates "testuser@example.com" — need a second user.
	// Use backendtest.SeedUser + manual login for the second user.
	res, err := b.Signup(context.Background(), backend.SignupParams{
		Email:       "other@example.com",
		Password:    "securepass123",
		DisplayName: "Other",
	})
	require.NoError(t, err)
	loginRes, err := b.Login(context.Background(), backend.LoginParams{
		Email:    "other@example.com",
		Password: "securepass123",
	})
	require.NoError(t, err)
	_ = res

	clientB := authedClient(t, srvURL, loginRes.Token)
	_, err = clientB.GetTranscript(context.Background(), connect.NewRequest(&drillv1.GetTranscriptRequest{
		SessionId: createResp.Msg.Session.Id,
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/rpc/session/ -run TestGetTranscript -v`
Expected: FAIL — handler returns `CodeUnimplemented`

- [ ] **Step 3: Add messageToProto helper and implement GetTranscript handler**

In `internal/rpc/session/server.go`, add the `messageToProto` helper in the conversion helpers section:

```go
// messageToProto converts db.Message to *drillv1.Message.
func messageToProto(m *db.Message) *drillv1.Message {
	msg := &drillv1.Message{
		Id:         m.ID.String(),
		SessionId:  m.SessionID.String(),
		Seq:        m.Seq,
		Role:       m.Role,
		Content:    m.Content,
		CreateTime: timestamppb.New(m.CreatedAt),
	}
	if m.InputMethod.Valid {
		msg.InputMethod = &m.InputMethod.String
	}
	if m.AudioUrl.Valid {
		msg.AudioUrl = &m.AudioUrl.String
	}
	return msg
}
```

Replace the `GetTranscript` stub:

```go
// GetTranscript returns messages for a session.
func (s *Server) GetTranscript(
	ctx context.Context,
	req *connect.Request[drillv1.GetTranscriptRequest],
) (*connect.Response[drillv1.GetTranscriptResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	sessionID, err := uuid.Parse(req.Msg.SessionId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session_id"))
	}

	msgs, err := s.b.GetTranscript(ctx, sessionID, user.ID)
	if err != nil {
		return nil, backendToConnectError(err)
	}

	protoMsgs := make([]*drillv1.Message, len(msgs))
	for i := range msgs {
		protoMsgs[i] = messageToProto(&msgs[i])
	}

	return connect.NewResponse(&drillv1.GetTranscriptResponse{
		Messages: protoMsgs,
	}), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/rpc/session/ -run TestGetTranscript -v`
Expected: All 4 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/rpc/session/server.go internal/rpc/session/server_test.go
git commit -m "feat(rpc): implement GetTranscript with messageToProto helper"
```

---

### Task 7: RPC Handler — ArchiveSessions

**Files:**
- Modify: `internal/rpc/session/server.go`
- Modify: `internal/rpc/session/server_test.go`

- [ ] **Step 1: Write failing RPC test**

Add to `internal/rpc/session/server_test.go`:

```go
func TestArchiveSessions_Success(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	questionID := seedQuestion(t, b)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	// Create two sessions.
	resp1, err := client.CreateSession(context.Background(), connect.NewRequest(&drillv1.CreateSessionRequest{
		QuestionId:      questionID.String(),
		DurationMinutes: 15,
	}))
	require.NoError(t, err)
	resp2, err := client.CreateSession(context.Background(), connect.NewRequest(&drillv1.CreateSessionRequest{
		QuestionId:      questionID.String(),
		DurationMinutes: 15,
	}))
	require.NoError(t, err)

	// Archive both.
	archiveResp, err := client.ArchiveSessions(context.Background(), connect.NewRequest(&drillv1.ArchiveSessionsRequest{
		SessionIds: []string{resp1.Msg.Session.Id, resp2.Msg.Session.Id},
		Archive:    true,
	}))
	require.NoError(t, err)
	require.Equal(t, int32(2), archiveResp.Msg.UpdatedCount)

	// Verify archived_at is set via GetSession.
	getResp, err := client.GetSession(context.Background(), connect.NewRequest(&drillv1.GetSessionRequest{
		Id: resp1.Msg.Session.Id,
	}))
	require.NoError(t, err)
	require.NotNil(t, getResp.Msg.Session.ArchiveTime)

	// Unarchive.
	unarchiveResp, err := client.ArchiveSessions(context.Background(), connect.NewRequest(&drillv1.ArchiveSessionsRequest{
		SessionIds: []string{resp1.Msg.Session.Id},
		Archive:    false,
	}))
	require.NoError(t, err)
	require.Equal(t, int32(1), unarchiveResp.Msg.UpdatedCount)
}

func TestArchiveSessions_InvalidUUID(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startSessionServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.ArchiveSessions(context.Background(), connect.NewRequest(&drillv1.ArchiveSessionsRequest{
		SessionIds: []string{"not-a-uuid"},
		Archive:    true,
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestArchiveSessions_Unauthenticated(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startSessionServer(t, b)
	client := drillv1connect.NewSessionServiceClient(&http.Client{}, srvURL)

	_, err := client.ArchiveSessions(context.Background(), connect.NewRequest(&drillv1.ArchiveSessionsRequest{
		SessionIds: []string{uuid.New().String()},
		Archive:    true,
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/rpc/session/ -run TestArchiveSessions -v`
Expected: FAIL — handler returns `CodeUnimplemented`

- [ ] **Step 3: Implement ArchiveSessions handler**

Replace the `ArchiveSessions` stub in `internal/rpc/session/server.go`:

```go
// ArchiveSessions bulk archives or unarchives sessions.
func (s *Server) ArchiveSessions(
	ctx context.Context,
	req *connect.Request[drillv1.ArchiveSessionsRequest],
) (*connect.Response[drillv1.ArchiveSessionsResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	ids := make([]uuid.UUID, len(req.Msg.SessionIds))
	for i, s := range req.Msg.SessionIds {
		parsed, err := uuid.Parse(s)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid session_id: "+s))
		}
		ids[i] = parsed
	}

	count, err := s.b.ArchiveSessions(ctx, user.ID, ids, req.Msg.Archive)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("archive sessions failed"))
	}

	return connect.NewResponse(&drillv1.ArchiveSessionsResponse{
		UpdatedCount: int32(count),
	}), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/rpc/session/ -run TestArchiveSessions -v`
Expected: All 3 tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/rpc/session/server.go internal/rpc/session/server_test.go
git commit -m "feat(rpc): implement ArchiveSessions bulk handler"
```

---

### Task 8: RPC Handler — DeleteAccount

**Files:**
- Modify: `internal/rpc/auth/server.go`
- Modify: `internal/rpc/auth/server_test.go`

**Important context:** AuthService is registered WITHOUT the auth interceptor (`publicOpts` in `internal/rpc/register.go:51`). `DeleteAccount` must authenticate manually by reading the session cookie from the request header and calling `b.AuthenticateSession()`, following the same pattern as `Logout`.

- [ ] **Step 1: Extract clearSessionCookie helper and refactor Logout**

In `internal/rpc/auth/server.go`, add the helper and refactor `Logout`:

```go
// clearSessionCookie sets a Set-Cookie header that clears the session cookie.
func clearSessionCookie(header http.Header, secureCookies bool) {
	header.Set("Set-Cookie", iauth.SessionCookie("", -1, secureCookies).String())
}
```

Update `Logout` to use it — replace the existing `resp.Header().Set(...)` line:

```go
// Logout invalidates the current session and clears the cookie.
func (s *Server) Logout(
	ctx context.Context,
	req *connect.Request[drillv1.LogoutRequest],
) (*connect.Response[drillv1.LogoutResponse], error) {
	// Read cookie from the request header.
	cookie, err := (&http.Request{Header: req.Header()}).Cookie(iauth.SessionCookieName)
	if err == nil && cookie.Value != "" {
		if logoutErr := s.b.Logout(ctx, cookie.Value); logoutErr != nil {
			slog.Error("logout session delete", "error", logoutErr)
		}
	}

	resp := connect.NewResponse(&drillv1.LogoutResponse{})
	clearSessionCookie(resp.Header(), s.b.Config().Auth.SecureCookies())
	return resp, nil
}
```

Verify existing Logout tests still pass:

Run: `go test ./internal/rpc/auth/ -run TestLogout -v`
Expected: PASS

- [ ] **Step 2: Unskip and update existing DeleteAccount tests**

In `internal/rpc/auth/server_test.go`, the existing tests need to be updated. The `TestDeleteAccount_RequiresAuth` test is correct as-is (just remove `t.Skip`). The `TestDeleteAccount_Success` test needs to verify the cookie is cleared. Update both tests:

Remove the `t.Skip` lines from both `TestDeleteAccount_RequiresAuth` and `TestDeleteAccount_Success`. Also add cookie-clearing verification to the success test:

```go
func TestDeleteAccount_RequiresAuth(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startAuthServer(t, b)
	client := publicClient(srvURL) // No cookies.

	_, err := client.DeleteAccount(context.Background(), connect.NewRequest(&drillv1.DeleteAccountRequest{}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestDeleteAccount_Success(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startAuthServer(t, b)
	client := publicClient(srvURL)

	// Signup and login.
	_, err := client.Signup(context.Background(), connect.NewRequest(&drillv1.SignupRequest{
		Email:       "delete@example.com",
		Password:    "securepass123",
		DisplayName: "Delete User",
	}))
	require.NoError(t, err)

	cookieClient, jar := clientWithCookies(t, srvURL)
	_, err = cookieClient.Login(context.Background(), connect.NewRequest(&drillv1.LoginRequest{
		Email:    "delete@example.com",
		Password: "securepass123",
	}))
	require.NoError(t, err)

	// Delete account.
	_, err = cookieClient.DeleteAccount(context.Background(), connect.NewRequest(&drillv1.DeleteAccountRequest{}))
	require.NoError(t, err)

	// Verify cookie was cleared.
	u, _ := url.Parse(srvURL)
	for _, c := range jar.Cookies(u) {
		if c.Name == iauth.SessionCookieName {
			require.Empty(t, c.Value, "session cookie should be cleared after delete")
		}
	}

	// Login should fail after deletion (user is soft-deleted, sessions cleared).
	_, err = client.Login(context.Background(), connect.NewRequest(&drillv1.LoginRequest{
		Email:    "delete@example.com",
		Password: "securepass123",
	}))
	require.Error(t, err)
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/rpc/auth/ -run TestDeleteAccount -v`
Expected: FAIL — handler returns `CodeUnimplemented`

- [ ] **Step 4: Implement DeleteAccount handler**

Replace the `DeleteAccount` stub in `internal/rpc/auth/server.go`. Since AuthService has no auth interceptor, we authenticate manually by reading the cookie:

```go
// DeleteAccount soft-deletes the authenticated user's account and clears
// the session cookie. AuthService has no auth interceptor, so we authenticate
// by reading the session cookie directly (same pattern as Logout).
func (s *Server) DeleteAccount(
	ctx context.Context,
	req *connect.Request[drillv1.DeleteAccountRequest],
) (*connect.Response[drillv1.DeleteAccountResponse], error) {
	cookie, err := (&http.Request{Header: req.Header()}).Cookie(iauth.SessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	tokenHash := iauth.HashSessionToken(cookie.Value)
	user, err := s.b.AuthenticateSession(ctx, tokenHash)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	if err := s.b.DeleteAccount(ctx, user.ID); err != nil {
		slog.Error("delete account", "error", err, "user_id", user.ID)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	resp := connect.NewResponse(&drillv1.DeleteAccountResponse{})
	clearSessionCookie(resp.Header(), s.b.Config().Auth.SecureCookies())
	return resp, nil
}
```

Make sure the import for `errors` is present (it should be already from other methods in the file). Also ensure `"net/http"` is imported (already used by `Logout`).

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/rpc/auth/ -run TestDeleteAccount -v`
Expected: Both tests PASS

Also run the full auth test suite to verify no regressions:

Run: `go test ./internal/rpc/auth/ -v`
Expected: All tests PASS

- [ ] **Step 6: Commit**

```bash
git add internal/rpc/auth/server.go internal/rpc/auth/server_test.go
git commit -m "feat(rpc): implement DeleteAccount with cookie-based auth and clearSessionCookie helper"
```

---

### Task 9: RPC Handler — UpdateProfile

**Files:**
- Modify: `internal/rpc/user/server.go`
- Modify: `internal/rpc/user/server_test.go`

- [ ] **Step 1: Write failing RPC tests**

Add to `internal/rpc/user/server_test.go`:

```go
func TestUpdateProfile_Success(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startUserServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	resp, err := client.UpdateProfile(context.Background(), connect.NewRequest(&drillv1.UpdateProfileRequest{
		User: &drillv1.User{
			DisplayName: "Updated Name",
		},
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{"display_name"},
		},
	}))
	require.NoError(t, err)
	require.Equal(t, "Updated Name", resp.Msg.User.DisplayName)
	require.Equal(t, "testuser@example.com", resp.Msg.User.Email)
}

func TestUpdateProfile_EmptyMask(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startUserServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.UpdateProfile(context.Background(), connect.NewRequest(&drillv1.UpdateProfileRequest{
		User:       &drillv1.User{DisplayName: "X"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{}},
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestUpdateProfile_UnknownField(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startUserServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.UpdateProfile(context.Background(), connect.NewRequest(&drillv1.UpdateProfileRequest{
		User: &drillv1.User{DisplayName: "X"},
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{"bogus_field"},
		},
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestUpdateProfile_UnsupportedField(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startUserServer(t, b)
	token := testutil.SignupAndLogin(t, b)
	client := authedClient(t, srvURL, token)

	_, err := client.UpdateProfile(context.Background(), connect.NewRequest(&drillv1.UpdateProfileRequest{
		User: &drillv1.User{DisplayName: "X"},
		UpdateMask: &fieldmaskpb.FieldMask{
			Paths: []string{"email"},
		},
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestUpdateProfile_Unauthenticated(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	srvURL := startUserServer(t, b)
	client := drillv1connect.NewUserServiceClient(&http.Client{}, srvURL)

	_, err := client.UpdateProfile(context.Background(), connect.NewRequest(&drillv1.UpdateProfileRequest{
		User:       &drillv1.User{DisplayName: "X"},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"display_name"}},
	}))
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}
```

Add the `fieldmaskpb` import to the test file imports:

```go
import (
	// ... existing imports ...
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/rpc/user/ -run TestUpdateProfile -v`
Expected: FAIL — handler returns `CodeUnimplemented`

- [ ] **Step 3: Implement UpdateProfile handler with dbUserToProto**

In `internal/rpc/user/server.go`, add the `dbUserToProto` helper and `implementedUserFields` allow-list. Then replace the `UpdateProfile` stub.

Add the allow-list and helper at the top of the file (after the `Server` struct):

```go
// implementedUserFields lists field mask paths that UpdateProfile supports.
// Expand this set as more fields become updatable.
var implementedUserFields = map[string]bool{
	"display_name": true,
}
```

Add `dbUserToProto` in the conversion helpers section (after `userToProto`):

```go
// dbUserToProto converts a db.User (sqlc model) to proto. Used by UpdateProfile
// which returns db.User, not auth.AuthUser.
func dbUserToProto(u *db.User) *drillv1.User {
	return &drillv1.User{
		Id:            u.ID.String(),
		Email:         u.Email,
		DisplayName:   u.DisplayName,
		Role:          roleToProto(u.Role),
		Plan:          planToProto(u.Plan),
		EmailVerified: u.EmailVerified,
		CreateTime:    timestamppb.New(u.CreatedAt),
	}
}
```

Replace the `UpdateProfile` stub:

```go
// UpdateProfile updates the authenticated user's profile fields.
// AIP-134: PATCH semantics via update_mask. Only fields in the mask are modified.
func (s *Server) UpdateProfile(
	ctx context.Context,
	req *connect.Request[drillv1.UpdateProfileRequest],
) (*connect.Response[drillv1.UpdateProfileResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	mask := req.Msg.GetUpdateMask()
	if mask == nil || len(mask.GetPaths()) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("update_mask is required"))
	}

	// Validate each path exists on the User proto and is implemented.
	userDesc := (*drillv1.User)(nil).ProtoReflect().Descriptor()
	for _, path := range mask.GetPaths() {
		if userDesc.Fields().ByName(protoreflect.Name(path)) == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown field: "+path))
		}
		if !implementedUserFields[path] {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unsupported field: "+path))
		}
	}

	updated, err := s.b.UpdateDisplayName(ctx, user.ID, req.Msg.GetUser().GetDisplayName())
	if err != nil {
		if errors.Is(err, backend.ErrUserNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("user not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, errors.New("update profile failed"))
	}

	return connect.NewResponse(&drillv1.UpdateProfileResponse{
		User: dbUserToProto(&updated),
	}), nil
}
```

Add the required imports to the file:

```go
"google.golang.org/protobuf/reflect/protoreflect"
```

Also add `"github.com/btc/drill/internal/db"` if not already imported (needed for `dbUserToProto`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/rpc/user/ -run TestUpdateProfile -v`
Expected: All 5 tests PASS

Also run the full user test suite:

Run: `go test ./internal/rpc/user/ -v`
Expected: All tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/rpc/user/server.go internal/rpc/user/server_test.go
git commit -m "feat(rpc): implement UpdateProfile with field mask validation"
```

---

### Task 10: Full Test Suite + Cleanup

**Files:**
- All modified files

- [ ] **Step 1: Run entire test suite**

Run: `go test ./internal/backend/ ./internal/rpc/... -v -count=1`
Expected: All tests PASS

- [ ] **Step 2: Run go vet and build**

Run: `go vet ./... && go build ./...`
Expected: Clean output

- [ ] **Step 3: Verify no remaining stubs (except ExportData)**

Run: `grep -rn 'CodeUnimplemented' internal/rpc/ --include='*.go' | grep -v '_test.go' | grep -v 'pb/'`
Expected: Only `ExportData` should remain:
```
internal/rpc/user/server.go:XXX:	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("ExportData not yet implemented"))
```

- [ ] **Step 4: Final commit if any cleanup was needed**

Only if changes were made in steps 1-3.

# ConnectRPC Stub Implementations: Batch 2

Implements the 4 remaining RPC stubs (excluding ExportData). Closes #86, #87.

## Scope

| RPC | Service | Status |
|-----|---------|--------|
| `GetTranscript` | SessionService | Wire-up only (queries exist) |
| `ArchiveSessions` | SessionService | New bulk sqlc query |
| `DeleteAccount` | AuthService | New CTE query (soft-delete + session wipe) |
| `UpdateProfile` | UserService | New single-field query + field mask validation |

ExportData is out of scope — requires async job pipeline design.

## SQL Queries

### New: `DeleteAccount`

Single CTE query: soft-deletes user and wipes auth sessions in one round-trip. Idempotent — `deleted_at IS NULL` guard means re-calling on a deleted user is a no-op on the user row.

```sql
-- name: DeleteAccount :exec
WITH soft_delete AS (
    UPDATE users SET deleted_at = NOW(), updated_at = NOW()
    WHERE id = @id AND deleted_at IS NULL
)
DELETE FROM auth_sessions WHERE user_id = @id;
```

### New: `ArchiveSessionsBulk`

Bulk archive or unarchive. Ownership enforced in SQL via `user_id` clause — no Go loop needed.

```sql
-- name: ArchiveSessionsBulk :execrows
UPDATE interview_sessions
SET archived_at = CASE WHEN @archive::bool THEN NOW() ELSE NULL END,
    updated_at = NOW()
WHERE id = ANY(@session_ids::uuid[])
  AND user_id = @user_id;
```

Returns affected row count for the `updated_count` response field.

### New: `UpdateUserDisplayName`

Narrow single-field update. Returns full row for proto mapping.

```sql
-- name: UpdateUserDisplayName :one
UPDATE users SET display_name = @display_name, updated_at = NOW()
WHERE id = @id AND deleted_at IS NULL
RETURNING *;
```

### Existing (no changes)

- `GetMessagesBySession` — used by GetTranscript
- `GetSession` — used by GetTranscript for ownership check (via `Backend.GetSessionForUser`)
- `SoftDeleteUser`, `DeleteUserAuthSessions` — superseded by new `DeleteAccount` CTE but left in place (used elsewhere)

## Backend Methods

All methods on `*Backend`, following existing patterns (tracing, error wrapping).

### `DeleteAccount(ctx context.Context, userID uuid.UUID) error`

Calls `DeleteAccount` sqlc query. One round-trip.

### `UpdateDisplayName(ctx context.Context, userID uuid.UUID, displayName string) (db.User, error)`

Calls `UpdateUserDisplayName`. Returns updated user row. Returns `ErrUserNotFound` if zero rows (deleted or nonexistent user).

### `ArchiveSessions(ctx context.Context, userID uuid.UUID, sessionIDs []uuid.UUID, archive bool) (int64, error)`

Calls `ArchiveSessionsBulk`. Returns affected row count. Sessions not owned by the user are silently skipped (count reflects only owned sessions updated).

### `GetTranscript(ctx context.Context, sessionID, userID uuid.UUID) ([]db.Message, error)`

Two existing queries, no new SQL:
1. `GetSessionForUser(ctx, sessionID, userID)` — ownership check, returns `ErrSessionNotFound`/`ErrSessionNotOwned` on failure
2. `GetMessagesBySession(ctx, sessionID)` — returns messages ordered by seq

Returns empty slice (not nil) for sessions with no messages.

## RPC Handlers

### `DeleteAccount` (auth/server.go)

1. Get user from context (or `CodeUnauthenticated`)
2. Call `b.DeleteAccount(ctx, userID)`
3. Clear session cookie via shared helper (see below)
4. Return empty `DeleteAccountResponse`

### `UpdateProfile` (user/server.go)

1. Get user from context (or `CodeUnauthenticated`)
2. Validate field mask: reject empty mask with `CodeInvalidArgument`. Each path must exist on the `User` proto descriptor (reject unknown with `CodeInvalidArgument`). Each path must be in the implemented allow-list `map[string]bool{"display_name": true}` (reject unimplemented with `CodeInvalidArgument`).
3. Call `b.UpdateDisplayName(ctx, userID, req.Msg.User.DisplayName)`
4. Map `db.User` to proto, return `UpdateProfileResponse{User: ...}`

### `ArchiveSessions` (session/server.go)

1. Get user from context (or `CodeUnauthenticated`)
2. Parse `session_ids` to `[]uuid.UUID` (reject invalid with `CodeInvalidArgument`)
3. Call `b.ArchiveSessions(ctx, userID, ids, req.Msg.Archive)`
4. Return `ArchiveSessionsResponse{UpdatedCount: count}`

### `GetTranscript` (session/server.go)

1. Get user from context (or `CodeUnauthenticated`)
2. Parse `session_id` to `uuid.UUID` (reject invalid with `CodeInvalidArgument`)
3. Call `b.GetTranscript(ctx, sessionID, userID)`
4. Map `[]db.Message` to `[]*drillv1.Message` via new `messageToProto` helper
5. Return `GetTranscriptResponse{Messages: msgs}` — uses `make()` for non-nil slice

## Shared Helpers

### `clearSessionCookie` (auth RPC package)

Extract cookie-clearing logic shared by `Logout` and `DeleteAccount`. Takes `http.Header` since the response types differ:

```go
func clearSessionCookie(header http.Header, secureCookies bool) {
    header.Set("Set-Cookie", iauth.SessionCookie("", -1, secureCookies).String())
}
```

### `messageToProto` (session RPC package)

Converts `db.Message` to `*drillv1.Message`. Handles nullable `input_method` and `audio_url` fields (pgtype.Text → optional string).

### `dbUserToProto` (user RPC package)

Converts `db.User` to `*drillv1.User`. Parallel to existing `userToProto(*auth.AuthUser)` but takes the sqlc model type. Needed because `UpdateUserDisplayName` returns `db.User`, not `auth.AuthUser`.

## Nil-Slice Compliance

Per CLAUDE.md: repeated proto fields must map to empty slices, not nil.

- `GetTranscript`: `make([]*drillv1.Message, len(msgs))` in the converter
- `ArchiveSessions`: no repeated field in response (just `updated_count`)

## Testing

### Backend Tests

All use shared test container (`testutil.PG`), `backendtest.SeedUser`.

**`TestDeleteAccount`**:
- Seed user, create auth session, call DeleteAccount
- Verify `GetUserByID` returns not found (filtered by `deleted_at IS NULL`)
- Verify `GetUserByIDIncludingDeleted` returns row with non-null `deleted_at`
- Verify auth sessions wiped (login with same credentials fails)
- Idempotency: calling DeleteAccount again does not error

**`TestUpdateDisplayName`**:
- Seed user, update display name, verify returned row
- Verify not-found for deleted user

**`TestArchiveSessions`**:
- Seed user + multiple sessions
- Archive subset, verify `archived_at` set, count matches
- Unarchive, verify `archived_at` cleared
- Ownership: pass other user's session IDs, verify count = 0
- Empty ID list: verify count = 0, no error

**`TestGetTranscript`**:
- Seed user + session + messages, verify order by seq
- Ownership: other user gets `ErrSessionNotOwned`
- Empty transcript: session with no messages returns empty slice (not nil)
- Not found: nonexistent session ID returns `ErrSessionNotFound`

### RPC Handler Tests

Each in `internal/rpc/{service}/`, using shared test container.

**DeleteAccount** (`auth/server_test.go`):
- Unskip existing two tests (`t.Skip` referencing #87)
- Success: verify empty response, verify `Set-Cookie` clears session
- Unauthenticated: verify `CodeUnauthenticated`

**UpdateProfile** (`user/server_test.go`):
- Success: update display_name, verify response user
- Empty field mask: verify `CodeInvalidArgument`
- Invalid field mask path (e.g. `"bogus"`): verify `CodeInvalidArgument`
- Unimplemented field (e.g. `"email"`): verify `CodeInvalidArgument`
- Unauthenticated: verify `CodeUnauthenticated`

**ArchiveSessions** (`session/server_test.go`):
- Success: archive, verify count
- Invalid UUID in list: verify `CodeInvalidArgument`
- Unauthenticated: verify `CodeUnauthenticated`

**GetTranscript** (`session/server_test.go`):
- Success: verify messages in order
- Not owned: verify `CodeNotFound`
- Invalid session ID: verify `CodeInvalidArgument`
- Unauthenticated: verify `CodeUnauthenticated`

## Files Modified

| File | Change |
|------|--------|
| `sql/queries/users.sql` | Add `DeleteAccount` CTE, `UpdateUserDisplayName` queries |
| `sql/queries/sessions.sql` | Add `ArchiveSessionsBulk` query |
| `internal/db/` | Regenerated by `sqlc generate` |
| `internal/backend/auth.go` | Add `DeleteAccount` method |
| `internal/backend/user.go` or `backend.go` | Add `UpdateDisplayName` method |
| `internal/backend/session.go` | Add `ArchiveSessions`, `GetTranscript` methods |
| `internal/rpc/auth/server.go` | Implement `DeleteAccount`, add `clearSessionCookie` helper, refactor `Logout` to use it |
| `internal/rpc/user/server.go` | Implement `UpdateProfile`, add `dbUserToProto` helper |
| `internal/rpc/session/server.go` | Implement `ArchiveSessions`, `GetTranscript`, add `messageToProto` helper |
| `internal/rpc/auth/server_test.go` | Unskip + expand DeleteAccount tests |
| `internal/rpc/user/server_test.go` | Add UpdateProfile tests |
| `internal/rpc/session/server_test.go` | Add ArchiveSessions, GetTranscript tests |
| `internal/backend/*_test.go` | Add backend-level tests for all 4 methods |

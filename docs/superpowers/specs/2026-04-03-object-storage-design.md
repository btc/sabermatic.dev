# Object Storage Interface Design

**Date:** 2026-04-03
**Status:** Approved

## Problem

Raw audio bytes are discarded after transcription in `conductor.go:316`. The database schema has an `audio_url` column that is never populated. A GCS bucket is provisioned in Terraform but no application code uploads to it. If Whisper hallucinates or truncates, there is no way to re-transcribe.

## Decision Summary

- New `internal/storage/` package with an `ObjectStore` interface
- Two implementations: GCS (production) and local filesystem (development)
- Both use identical string-prefix semantics for deletion
- Audio uploaded in a goroutine concurrent with transcription; failures logged, never block the interview
- OTel spans on upload operations
- Session-scoped keys (`{session_id}/{message_id}.webm`) enable GDPR right-to-erasure via prefix delete

## Interface

```go
// internal/storage/storage.go
package storage

import "context"

// ObjectStore abstracts blob storage over GCS and local filesystem.
type ObjectStore interface {
    // Put writes data and returns the canonical URL of the stored object.
    Put(ctx context.Context, key string, data []byte, contentType string) (url string, err error)

    // Delete removes a single object by key.
    Delete(ctx context.Context, key string) error

    // DeletePrefix removes all objects whose keys match the given string prefix.
    // This is a pure string prefix match (not path-segment-aware).
    // Callers should include a trailing "/" for path-segment-aligned deletes.
    DeletePrefix(ctx context.Context, prefix string) error
}
```

No `Get` method — consumers use the returned URL directly. Can be added later if needed.

## Implementations

### GCS (`internal/storage/gcs.go`)

```go
type GCSStore struct {
    client *gcs.Client
    bucket string
}

func NewGCS(ctx context.Context, bucket string) (*GCSStore, error)
```

- Uses Application Default Credentials (ADC) — no API key config needed. Cloud Run service account already has `roles/storage.objectAdmin`.
- `Put`: `ObjectHandle.NewWriter` with content type. GCS SDK has built-in retry with exponential backoff for transient errors.
- `Delete`: `ObjectHandle.Delete`.
- `DeletePrefix`: `Bucket.Objects(ctx, &storage.Query{Prefix: prefix})` iterate and delete each.
- URL format: `gs://{bucket}/{key}`.

### Local Filesystem (`internal/storage/local.go`)

```go
type LocalStore struct {
    dir string
}

func NewLocal(dir string) (*LocalStore, error)
```

- Constructor validates/creates the base directory.
- `Put`: `os.MkdirAll` for parent directories + `os.WriteFile`. Atomic write not required — these are unique keys.
- `Delete`: `os.Remove`.
- `DeletePrefix`: `filepath.WalkDir` from base directory, compute relative path, string prefix match, `os.Remove` on matches. Cleans up empty parent directories after.
- URL format: `file://{dir}/{key}`.
- Matches GCS `Query.Prefix` semantics exactly — pure string prefix, not directory-aware.

## Configuration

```go
// Added to internal/config/config.go
type Storage struct {
    Backend  string `env:"STORAGE_BACKEND,default=local"`
    Bucket   string `env:"STORAGE_BUCKET"`
    LocalDir string `env:"STORAGE_LOCAL_DIR,default=data/audio"`
}
```

- `Backend`: `"gcs"` or `"local"`.
- `Bucket`: required when `Backend=gcs`. Validated in `config.validate()`.
- `LocalDir`: path for local storage, defaults to `data/audio` relative to working directory.
- Added to top-level `Config` struct as `Storage Storage`.

## Wiring

In `backend.New()`:

1. Based on `cfg.Storage.Backend`, construct either `storage.NewGCS(ctx, cfg.Storage.Bucket)` or `storage.NewLocal(cfg.Storage.LocalDir)`.
2. Store as `store storage.ObjectStore` field on `Backend`.
3. Expose via `Backend.StoreAudio(ctx, key, data, contentType) (string, error)` — delegates to `store.Put`.

Same pattern as `Transcriber`, `Synthesizer`, and `Sender`.

## Conductor Integration

In `endTurn()` (`internal/interview/conductor.go`), after audio validation and before transcription:

```go
// Generate messageID before both operations need it.
messageID := uuid.New()

// Fire upload goroutine — does not block transcription.
go func() {
    ctx, span := tracer.Start(ctx, "storage.upload_audio")
    defer span.End()

    key := fmt.Sprintf("%s/%s.webm", c.sessionID, messageID)
    url, err := c.backend.StoreAudio(ctx, key, msg.Audio, "audio/webm")
    if err != nil {
        span.RecordError(err)
        span.SetStatus(codes.Error, "audio upload failed")
        slog.Error("conductor: audio upload failed",
            "error", err,
            "session_id", c.sessionID,
            "message_id", messageID)
        return
    }
    if err := c.backend.SetAudioURL(ctx, messageID, url); err != nil {
        slog.Error("conductor: failed to set audio_url",
            "error", err,
            "session_id", c.sessionID,
            "message_id", messageID)
    }
}()

// Transcription proceeds concurrently.
text, err := c.backend.Transcribe(ctx, msg.Audio, "webm")
```

### Failure behavior

- Upload failure: logged with structured fields, OTel span records error. `audio_url` remains NULL. Interview continues.
- Transcription failure: existing error handling unchanged — returns error, interview turn fails.
- Both succeed: `audio_url` updated on the message row after persist.

### Message ID generation

Currently `messageID` is generated inside `persistMessage`. It needs to be generated earlier so both the upload goroutine and persist call can reference it. The `PersistMessageParams` struct already has a `MessageID` field.

## Database

New SQL query:

```sql
-- name: SetAudioURL :exec
UPDATE messages SET audio_url = $2 WHERE id = $1;
```

New Backend method:

```go
func (b *Backend) SetAudioURL(ctx context.Context, id uuid.UUID, url string) error
```

The `audio_url` column already exists in the `messages` table (`sql/migrations/001_initial.up.sql:83`).

## Key Format

```
{session_id}/{message_id}.webm
```

- Session-scoped: all audio for a session shares a prefix.
- GDPR erasure: `DeletePrefix("{session_id}/")` removes all audio for a session.
- Unique: `message_id` is a UUID.

## Testing

- `ObjectStore` interface enables mock injection in conductor tests.
- `LocalStore` can be tested with `t.TempDir()`.
- `GCSStore` tested with integration tests against a real bucket (or emulator).

## Dependencies

- `cloud.google.com/go/storage` — added to `go.mod` for GCS implementation.
- `go.opentelemetry.io/otel` — already in project for tracing.

## Infrastructure

Already provisioned:
- GCS bucket: `${project_id}-audio` (`terraform/gcs.tf`)
- IAM: Cloud Run service account has `roles/storage.objectAdmin` (`terraform/iam.tf`)

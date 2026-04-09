# Image Pipeline Fixes

**Date:** 2026-04-08
**Status:** Approved

## Context

Debugging session revealed four issues in the image generation pipeline:

1. **Local storage returns `file://` URLs** — browsers block `file://` from HTTP pages. All 6 generated images show as broken in the UI.
2. **No dedicated Gemini queue** — image generation shares the `ai` queue with Anthropic-powered jobs. 10 concurrent workers exhaust Gemini's per-minute quota (429 RESOURCE_EXHAUSTED).
3. **No slog output for image gen failures** — `ErrorHandler` only handles `evaluate_session` and `generate_educator_content`. Image gen errors go silently to `river_job.errors` in the DB.
4. **Broken DB records** — 6 questions have `file://` image URLs that need updating (not regenerating).

## A) Dedicated Gemini Queue

Add `QueueGemini = "gemini"` in `internal/jobs/queues.go`.

Register in `backend.go` with `MaxWorkers: 2`. This bounds concurrent Gemini calls, preventing quota exhaustion while still allowing parallelism.

Change `GenerateQuestionImageInsertOpts` to use `QueueGemini` instead of `QueueAI`.

### Files changed
- `internal/jobs/queues.go` — add constant
- `internal/backend/backend.go` — register queue
- `internal/jobs/generate_image.go` — use new queue

## B) Fix Local Storage URLs

### Problem

`localBucket.Put()` returns `"file://" + absolutePath` (line 61 of `local.go`). This applies to both `Audio()` and `Public()` buckets. GCS returns `https://storage.googleapis.com/{bucket}/{key}` — proper HTTP URLs.

### Design

**`internal/storage/local.go`:**

`NewLocal(cfg *config.Config) (*LocalStore, error)` — reads `cfg.Storage.LocalDir` and `cfg.Server.Port`. Constructs `baseURL = fmt.Sprintf("http://localhost:%d", cfg.Server.Port)` internally. Stores it on the struct.

Both `Audio()` and `Public()` buckets return `{baseURL}/storage/{audio|public}/{key}` from `Put()`. Consistent URL scheme across both buckets.

The `localBucket` struct gains a `urlPrefix string` field (e.g., `http://localhost:8080/storage/audio`). `Put()` returns `urlPrefix + "/" + key` instead of `"file://" + absolutePath`.

**`cmd/drill/main.go`:**

After `handler.NewHandler` returns `h`, wrap with a local file server when `cfg.Storage.Backend == "local"`:

```go
if cfg.Storage.Backend == "local" {
    inner := h
    mux := http.NewServeMux()
    mux.Handle("/storage/", http.StripPrefix("/storage/",
        http.FileServer(http.Dir(cfg.Storage.LocalDir))))
    mux.Handle("/", inner)
    h = mux
}
```

Single route serves both `audio/` and `public/` subdirectories. Bypasses CSRF and OTel — acceptable for local-dev static file serving.

**`internal/storage/local_test.go`:**

Tests currently assert `file://` URLs and call `NewLocal(dir)`. After the signature change, tests use `testutil.ConfigWithOverrides(t, "postgres://unused", map[string]string{"STORAGE_LOCAL_DIR": dir})` to get a config with the test's temp dir. Update all URL assertions to expect `http://localhost:8080/storage/{audio|public}/{key}` (8080 is the default `SERVER_PORT`).

**`internal/backend/backend.go`:**

Change `storage.NewLocal(cfg.Storage.LocalDir)` to `storage.NewLocal(cfg)`.

### Files changed
- `internal/storage/local.go` — new constructor signature, URL construction
- `internal/storage/local_test.go` — updated assertions
- `internal/backend/backend.go` — updated call site
- `cmd/drill/main.go` — local file server wrapper

## C) Slog Logging for Image Gen Failures

Add a `"generate_question_image"` case to `ErrorHandler.HandleError` in `internal/jobs/error_handler.go`.

When the job exhausts all retries:

```go
slog.Warn("image generation exhausted retries",
    "question_id", args.QuestionID,
    "attempts", job.Attempt,
    "error", err,
)
```

No DB status mutation — there's no `image_failed` status column. The question stays without `image_url` and the sweep re-enqueues after the discarded job's unique lock expires.

Also add `"generate_question_image"` to the `HandlePanic` switch for consistency.

### Files changed
- `internal/jobs/error_handler.go`

## D) Fix Broken DB Records

After B is deployed locally, run:

```sql
UPDATE questions
SET image_url = REPLACE(
    image_url,
    'file://data/storage/public/',
    'http://localhost:8080/storage/public/'
)
WHERE image_url LIKE 'file://%';
```

Also reset any stuck running jobs:

```sql
UPDATE river_job
SET state = 'available'
WHERE kind = 'generate_question_image'
AND state = 'running';
```

This preserves the 6 existing images on disk — no regeneration needed.

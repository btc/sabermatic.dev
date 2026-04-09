# Image Pipeline Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix four issues in the image generation pipeline: quota exhaustion, broken local URLs, silent failures, and stale DB records.

**Architecture:** Four independent changes — a new River queue for Gemini jobs, HTTP-serving local storage URLs, error handler coverage for image gen, and a one-time SQL fixup.

**Tech Stack:** Go, River (job queue), net/http (file server), slog, PostgreSQL

**Spec:** `docs/superpowers/specs/2026-04-08-image-pipeline-fixes-design.md`

---

### Task 1: Dedicated Gemini Queue

**Files:**
- Modify: `internal/jobs/queues.go:4-8`
- Modify: `internal/config/config.go:84-90`
- Modify: `internal/backend/backend.go:115-120`
- Modify: `internal/jobs/generate_image.go:37-45`

- [ ] **Step 1: Add queue constant and config field**

In `internal/jobs/queues.go`, add `QueueGemini`:

```go
const (
	QueueNotifications = "notifications"
	QueueAI            = "ai"
	QueueGemini        = "gemini"
	QueueMaintenance   = "maintenance"
)
```

In `internal/config/config.go`, add `NumGeminiWorkers` to the `River` struct (after `NumAIWorkers`):

```go
type River struct {
	ShutdownTimeoutSec int `env:"RIVER_SHUTDOWN_TIMEOUT_SEC,default=15"`
	NumDefaultWorkers  int `env:"RIVER_DEFAULT_WORKERS,default=5"`
	NumNotifyWorkers   int `env:"RIVER_NOTIFY_WORKERS,default=5"`
	NumAIWorkers       int `env:"RIVER_AI_WORKERS,default=10"`
	NumGeminiWorkers   int `env:"RIVER_GEMINI_WORKERS,default=2"`
	NumMaintWorkers    int `env:"RIVER_MAINT_WORKERS,default=2"`
}
```

- [ ] **Step 2: Register queue in backend**

In `internal/backend/backend.go`, add the gemini queue to the `Queues` map (after `QueueAI`):

```go
Queues: map[string]river.QueueConfig{
	river.QueueDefault:      {MaxWorkers: cfg.River.NumDefaultWorkers},
	jobs.QueueNotifications: {MaxWorkers: cfg.River.NumNotifyWorkers},
	jobs.QueueAI:            {MaxWorkers: cfg.River.NumAIWorkers},
	jobs.QueueGemini:        {MaxWorkers: cfg.River.NumGeminiWorkers},
	jobs.QueueMaintenance:   {MaxWorkers: cfg.River.NumMaintWorkers},
},
```

- [ ] **Step 3: Use new queue in insert opts**

In `internal/jobs/generate_image.go`, change `GenerateQuestionImageInsertOpts`:

```go
func GenerateQuestionImageInsertOpts() *river.InsertOpts {
	return &river.InsertOpts{
		Queue:       QueueGemini,
		MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
		},
	}
}
```

- [ ] **Step 4: Verify build**

Run: `go build ./...`
Expected: clean build, no errors.

- [ ] **Step 5: Commit**

```bash
git add internal/jobs/queues.go internal/config/config.go internal/backend/backend.go internal/jobs/generate_image.go
git commit -m "feat: dedicated gemini queue with 2 workers

Isolates Gemini image generation from the AI queue to prevent
quota exhaustion (429 RESOURCE_EXHAUSTED) from concurrent requests."
```

---

### Task 2: Fix Local Storage URLs

**Files:**
- Modify: `internal/storage/local.go`
- Modify: `internal/storage/local_test.go`
- Modify: `internal/backend/backend.go:56-61`

- [ ] **Step 1: Update local_test.go — failing tests first**

Replace the full content of the test constructor calls and URL assertions. Tests use `testutil.ConfigWithOverrides` to get a config with the test's temp dir.

In `internal/storage/local_test.go`, replace the entire file with:

```go
package storage_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/btc/drill/internal/storage"
	"github.com/btc/drill/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func localStore(t *testing.T) (*storage.LocalStore, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := testutil.ConfigWithOverrides(t, "postgres://unused", map[string]string{
		"STORAGE_LOCAL_DIR": dir,
	})
	store, err := storage.NewLocal(cfg)
	require.NoError(t, err)
	return store, dir
}

func TestLocalStore_Put(t *testing.T) {
	store, dir := localStore(t)

	ctx := context.Background()
	url, err := store.Audio().Put(ctx, "sess-1/msg-1.webm", []byte("audio-data"), "audio/webm")
	require.NoError(t, err)

	assert.Equal(t, "http://localhost:8080/storage/audio/sess-1/msg-1.webm", url)

	data, err := os.ReadFile(filepath.Join(dir, "audio", "sess-1", "msg-1.webm"))
	require.NoError(t, err)
	assert.Equal(t, []byte("audio-data"), data)
}

func TestLocalStore_Delete(t *testing.T) {
	store, dir := localStore(t)

	ctx := context.Background()
	_, err := store.Audio().Put(ctx, "sess-1/msg-1.webm", []byte("audio-data"), "audio/webm")
	require.NoError(t, err)

	err = store.Audio().Delete(ctx, "sess-1/msg-1.webm")
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(dir, "audio", "sess-1", "msg-1.webm"))
	assert.True(t, os.IsNotExist(err))
}

func TestLocalStore_DeletePrefix(t *testing.T) {
	store, dir := localStore(t)

	ctx := context.Background()
	bucket := store.Audio()
	_, err := bucket.Put(ctx, "sess-1/msg-1.webm", []byte("a1"), "audio/webm")
	require.NoError(t, err)
	_, err = bucket.Put(ctx, "sess-1/msg-2.webm", []byte("a2"), "audio/webm")
	require.NoError(t, err)
	_, err = bucket.Put(ctx, "sess-2/msg-3.webm", []byte("a3"), "audio/webm")
	require.NoError(t, err)

	err = bucket.DeletePrefix(ctx, "sess-1/")
	require.NoError(t, err)

	// sess-1 files deleted.
	_, err = os.Stat(filepath.Join(dir, "audio", "sess-1", "msg-1.webm"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(dir, "audio", "sess-1", "msg-2.webm"))
	assert.True(t, os.IsNotExist(err))

	// Parent directory cleaned up.
	_, err = os.Stat(filepath.Join(dir, "audio", "sess-1"))
	assert.True(t, os.IsNotExist(err))

	// sess-2 untouched.
	data, err := os.ReadFile(filepath.Join(dir, "audio", "sess-2", "msg-3.webm"))
	require.NoError(t, err)
	assert.Equal(t, []byte("a3"), data)
}

func TestLocalStore_DeletePrefix_StringSemantics(t *testing.T) {
	store, dir := localStore(t)

	ctx := context.Background()
	bucket := store.Audio()
	_, err := bucket.Put(ctx, "abc/file.webm", []byte("a1"), "audio/webm")
	require.NoError(t, err)
	_, err = bucket.Put(ctx, "abcdef/file.webm", []byte("a2"), "audio/webm")
	require.NoError(t, err)

	// "abc" (no trailing slash) matches both "abc/" and "abcdef/".
	err = bucket.DeletePrefix(ctx, "abc")
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(dir, "audio", "abc", "file.webm"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(dir, "audio", "abcdef", "file.webm"))
	assert.True(t, os.IsNotExist(err))
}

func TestLocalStore_NewLocal_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "storage")
	cfg := testutil.ConfigWithOverrides(t, "postgres://unused", map[string]string{
		"STORAGE_LOCAL_DIR": dir,
	})
	store, err := storage.NewLocal(cfg)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = store.Audio().Put(ctx, "test.webm", []byte("data"), "audio/webm")
	require.NoError(t, err)
}

func TestLocalStore_Delete_Idempotent(t *testing.T) {
	store, _ := localStore(t)
	// Ensure audio bucket dir exists before deleting from it.
	_, err := store.Audio().Put(context.Background(), "placeholder", []byte("x"), "text/plain")
	require.NoError(t, err)
	err = store.Audio().Delete(context.Background(), "nonexistent/file.webm")
	require.NoError(t, err)
}

func TestLocalStore_Put_PathTraversal(t *testing.T) {
	store, _ := localStore(t)

	ctx := context.Background()
	_, err := store.Audio().Put(ctx, "../../../etc/passwd", []byte("malicious"), "text/plain")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "escapes storage root")
}

func TestLocalStore_Delete_PathTraversal(t *testing.T) {
	store, _ := localStore(t)

	err := store.Audio().Delete(context.Background(), "../../etc/passwd")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "escapes storage root")
}

func TestLocalStore_PublicBucket(t *testing.T) {
	store, dir := localStore(t)

	ctx := context.Background()
	url, err := store.Public().Put(ctx, "images/test.png", []byte("image-data"), "image/png")
	require.NoError(t, err)

	assert.Equal(t, "http://localhost:8080/storage/public/images/test.png", url)

	data, err := os.ReadFile(filepath.Join(dir, "public", "images", "test.png"))
	require.NoError(t, err)
	assert.Equal(t, []byte("image-data"), data)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/storage/ -v -run TestLocalStore 2>&1 | head -30`
Expected: compilation errors — `NewLocal` signature mismatch.

- [ ] **Step 3: Implement NewLocal with config**

Replace the full content of `internal/storage/local.go` with:

```go
package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/btc/drill/internal/config"
)

// LocalStore stores objects on the local filesystem with separate audio and public directories.
type LocalStore struct {
	baseDir string
	baseURL string // e.g., "http://localhost:8080"
}

// NewLocal creates a LocalStore rooted at cfg.Storage.LocalDir, creating it if needed.
// URLs returned by Put use HTTP paths served by the local file server in main.go.
func NewLocal(cfg *config.Config) (*LocalStore, error) {
	baseDir := cfg.Storage.LocalDir
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}
	baseURL := fmt.Sprintf("http://localhost:%d", cfg.Server.Port)
	return &LocalStore{baseDir: baseDir, baseURL: baseURL}, nil
}

func (s *LocalStore) Audio() Bucket {
	return &localBucket{
		dir:       filepath.Join(s.baseDir, "audio"),
		urlPrefix: s.baseURL + "/storage/audio",
	}
}

func (s *LocalStore) Public() Bucket {
	return &localBucket{
		dir:       filepath.Join(s.baseDir, "public"),
		urlPrefix: s.baseURL + "/storage/public",
	}
}

// Close is a no-op for local filesystem storage.
func (s *LocalStore) Close() error { return nil }

// localBucket operates on a single directory within the local filesystem.
type localBucket struct {
	dir       string
	urlPrefix string // e.g., "http://localhost:8080/storage/audio"
}

// guard validates that key does not escape the bucket root and returns the
// absolute path for the key.
func (b *localBucket) guard(key string) (string, error) {
	p := filepath.Join(b.dir, filepath.FromSlash(key))
	if !strings.HasPrefix(p, b.dir+string(filepath.Separator)) && p != b.dir {
		return "", fmt.Errorf("key %q escapes storage root", key)
	}
	return p, nil
}

func (b *localBucket) Put(_ context.Context, key string, data []byte, _ string) (string, error) {
	// Ensure the bucket directory exists on first Put.
	if err := os.MkdirAll(b.dir, 0o755); err != nil {
		return "", fmt.Errorf("create bucket dir: %w", err)
	}
	p, err := b.guard(key)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", fmt.Errorf("create parent dirs: %w", err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return b.urlPrefix + "/" + key, nil
}

func (b *localBucket) Delete(_ context.Context, key string) error {
	p, err := b.guard(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}

func (b *localBucket) DeletePrefix(_ context.Context, prefix string) error {
	// Ensure the bucket directory exists before walking.
	if _, err := os.Stat(b.dir); os.IsNotExist(err) {
		return nil
	}

	// parentDirs collects unique parent directories of removed files, keyed by
	// their depth (number of path separators) so we can remove deepest-first.
	type dirDepth struct {
		path  string
		depth int
	}
	seen := map[string]int{} // path -> depth

	if err := filepath.WalkDir(b.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(b.dir, path)
		if err != nil {
			return err
		}
		// Use forward slashes for key comparison (matches GCS semantics).
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, prefix) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("delete %s: %w", rel, err)
			}
			// Record the parent directory and its depth for cleanup below.
			parent := filepath.Dir(path)
			depth := strings.Count(parent, string(filepath.Separator))
			if _, exists := seen[parent]; !exists {
				seen[parent] = depth
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// Build a slice sorted deepest-first so we remove children before parents.
	dirs := make([]dirDepth, 0, len(seen))
	for p, d := range seen {
		dirs = append(dirs, dirDepth{path: p, depth: d})
	}
	// Sort in-place: higher depth first.
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].depth > dirs[j].depth })

	for _, dd := range dirs {
		// Ignore the error: Remove returns ENOTEMPTY for non-empty dirs,
		// which is expected when siblings outside the prefix remain.
		_ = os.Remove(dd.path)
	}
	return nil
}
```

- [ ] **Step 4: Update backend.go call site**

In `internal/backend/backend.go`, change the local storage initialization (lines 56-61) from:

```go
	case "local":
		store, err = storage.NewLocal(cfg.Storage.LocalDir)
		if err != nil {
			return nil, fmt.Errorf("local storage: %w", err)
		}
		slog.Info("storage: local", "dir", cfg.Storage.LocalDir)
```

to:

```go
	case "local":
		store, err = storage.NewLocal(cfg)
		if err != nil {
			return nil, fmt.Errorf("local storage: %w", err)
		}
		slog.Info("storage: local", "dir", cfg.Storage.LocalDir)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/storage/ -v -run TestLocalStore`
Expected: all tests pass with HTTP URLs.

- [ ] **Step 6: Add local file server in main.go**

In `cmd/drill/main.go`, after the `handler.NewHandler` call (after line 87), add the local storage file server wrapper before creating `srv`:

```go
	// Local dev: serve storage files via HTTP so browser can load them.
	if cfg.Storage.Backend == "local" {
		inner := h
		storageMux := http.NewServeMux()
		storageMux.Handle("/storage/", http.StripPrefix("/storage/",
			http.FileServer(http.Dir(cfg.Storage.LocalDir))))
		storageMux.Handle("/", inner)
		h = storageMux
	}
```

- [ ] **Step 7: Verify full build**

Run: `go build ./...`
Expected: clean build, no errors.

- [ ] **Step 8: Commit**

```bash
git add internal/storage/local.go internal/storage/local_test.go internal/backend/backend.go cmd/drill/main.go
git commit -m "fix: local storage returns HTTP URLs instead of file://

NewLocal takes *config.Config and constructs http://localhost:{port}
URLs. main.go wraps the handler with a file server at /storage/ when
using local backend. Both audio and public buckets return consistent
HTTP URLs."
```

---

### Task 3: Slog Logging for Image Gen Failures

**Files:**
- Modify: `internal/jobs/error_handler.go`

- [ ] **Step 1: Add image gen case to HandleError**

In `internal/jobs/error_handler.go`, add a new case after the `"generate_educator_content"` case (after line 77, before the closing `}`). Also add the import for `"github.com/google/uuid"` and the new case to `HandlePanic`.

Replace the full file content with:

```go
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/btc/drill/internal/db"
)

// ErrorHandler sets failure status when jobs exhaust all retries.
// Implements river.ErrorHandler.
type ErrorHandler struct {
	Pool *pgxpool.Pool
}

func (h *ErrorHandler) HandleError(ctx context.Context, job *rivertype.JobRow, err error) *river.ErrorHandlerResult {
	if job.Attempt < job.MaxAttempts {
		return nil
	}

	switch job.Kind {
	case "evaluate_session":
		var args EvaluateSessionArgs
		if unmarshalErr := json.Unmarshal(job.EncodedArgs, &args); unmarshalErr != nil {
			slog.Error("unmarshal evaluate_session args in error handler", "error", unmarshalErr)
			return nil
		}

		slog.Warn("evaluation exhausted retries, marking failed",
			"session_id", args.SessionID,
			"attempts", job.Attempt,
			"error", err,
		)

		q := db.New(h.Pool)
		statusErr := q.UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
			ID:     args.SessionID,
			Status: "evaluation_failed",
		})
		if statusErr != nil {
			slog.Error("failed to set evaluation_failed status", "error", statusErr, "session_id", args.SessionID)
		}

	case "generate_educator_content":
		var args GenerateEducatorContentArgs
		if unmarshalErr := json.Unmarshal(job.EncodedArgs, &args); unmarshalErr != nil {
			slog.Error("unmarshal educator args in error handler", "error", unmarshalErr)
			return nil
		}

		slog.Warn("educator exhausted retries, marking failed",
			"session_id", args.SessionID,
			"attempts", job.Attempt,
			"error", err,
		)

		q := db.New(h.Pool)
		ea, getErr := q.GetEducatorAnalysisBySession(ctx, args.SessionID)
		if getErr != nil {
			slog.Error("get educator analysis in error handler", "error", getErr)
			return nil
		}
		statusErr := q.UpdateEducatorAnalysisStatus(ctx, db.UpdateEducatorAnalysisStatusParams{
			ID:     ea.ID,
			Status: "failed",
		})
		if statusErr != nil {
			slog.Error("set educator failed status", "error", statusErr, "session_id", args.SessionID)
		}

	case "generate_question_image":
		var args struct {
			QuestionID uuid.UUID `json:"question_id"`
		}
		if unmarshalErr := json.Unmarshal(job.EncodedArgs, &args); unmarshalErr != nil {
			slog.Error("unmarshal generate_question_image args in error handler", "error", unmarshalErr)
			return nil
		}

		slog.Warn("image generation exhausted retries",
			"question_id", args.QuestionID,
			"attempts", job.Attempt,
			"error", err,
		)
	}

	return nil
}

func (h *ErrorHandler) HandlePanic(ctx context.Context, job *rivertype.JobRow, panicVal any, trace string) *river.ErrorHandlerResult {
	if job.Attempt >= job.MaxAttempts {
		switch job.Kind {
		case "evaluate_session", "generate_educator_content", "generate_question_image":
			panicErr := fmt.Errorf("panic: %v", panicVal)
			slog.Error("job panicked on final attempt", "kind", job.Kind, "panic", panicVal)
			return h.HandleError(ctx, job, panicErr)
		}
	}
	return nil
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: clean build, no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/jobs/error_handler.go
git commit -m "fix: log image generation failures via slog

ErrorHandler now handles generate_question_image — logs a warning
when the job exhausts retries. Previously errors only appeared in
river_job.errors in the DB with no slog output."
```

---

### Task 4: Fix Broken DB Records

This task runs after Task 2 is deployed (server restarted with the local file server).

- [ ] **Step 1: Fix image URLs**

Run against the local database:

```bash
psql "postgresql://localhost:5432/drill_v1" -c "
UPDATE questions
SET image_url = REPLACE(
    image_url,
    'file://data/storage/public/',
    'http://localhost:8080/storage/public/'
)
WHERE image_url LIKE 'file://%'
RETURNING id, image_url;
"
```

Expected: 6 rows updated, each showing an `http://localhost:8080/storage/public/questions/{uuid}/card.png` URL.

- [ ] **Step 2: Reset stuck running jobs**

```bash
psql "postgresql://localhost:5432/drill_v1" -c "
UPDATE river_job
SET state = 'available'
WHERE kind = 'generate_question_image'
AND state = 'running'
RETURNING id, state;
"
```

Expected: 0 or more rows updated (depends on current state — the earlier manual fix may have already cleared these).

- [ ] **Step 3: Verify in browser**

Load `http://localhost:3000` (or the dev server URL). The 6 previously-broken question cards should now display their cubist illustrations. The 13 missing images should begin generating within the next sweep cycle (1 minute).

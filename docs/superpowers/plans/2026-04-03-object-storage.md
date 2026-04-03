# Object Storage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist raw audio to object storage (GCS in prod, local filesystem in dev) concurrently with transcription, with OTel tracing.

**Architecture:** New `internal/storage/` package defines an `ObjectStore` interface with GCS and local implementations. Backend holds the store and exposes `StoreAudio`/`SetAudioURL`. Conductor fires a goroutine to upload audio in parallel with transcription.

**Tech Stack:** `cloud.google.com/go/storage` (GCS SDK), `go.opentelemetry.io/otel` (tracing), `github.com/sethvargo/go-envconfig` (config)

**Spec:** `docs/superpowers/specs/2026-04-03-object-storage-design.md`

---

### Task 1: ObjectStore Interface and Local Implementation

**Files:**
- Create: `internal/storage/storage.go`
- Create: `internal/storage/local.go`
- Create: `internal/storage/local_test.go`

- [ ] **Step 1: Write the interface**

Create `internal/storage/storage.go`:

```go
package storage

import "context"

// ObjectStore abstracts blob storage over GCS and local filesystem.
type ObjectStore interface {
	// Put writes data and returns the canonical URL of the stored object.
	Put(ctx context.Context, key string, data []byte, contentType string) (url string, err error)

	// Delete removes a single object by key.
	Delete(ctx context.Context, key string) error

	// DeletePrefix removes all objects whose keys match the given string prefix.
	// Pure string prefix match — not path-segment-aware.
	// Callers should include trailing "/" for path-segment-aligned deletes.
	DeletePrefix(ctx context.Context, prefix string) error
}
```

- [ ] **Step 2: Write failing tests for LocalStore**

Create `internal/storage/local_test.go`:

```go
package storage_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/btc/drill/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalStore_Put(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewLocal(dir)
	require.NoError(t, err)

	ctx := context.Background()
	url, err := store.Put(ctx, "sess-1/msg-1.webm", []byte("audio-data"), "audio/webm")
	require.NoError(t, err)

	assert.Equal(t, "file://"+filepath.Join(dir, "sess-1/msg-1.webm"), url)

	data, err := os.ReadFile(filepath.Join(dir, "sess-1", "msg-1.webm"))
	require.NoError(t, err)
	assert.Equal(t, []byte("audio-data"), data)
}

func TestLocalStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewLocal(dir)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = store.Put(ctx, "sess-1/msg-1.webm", []byte("audio-data"), "audio/webm")
	require.NoError(t, err)

	err = store.Delete(ctx, "sess-1/msg-1.webm")
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(dir, "sess-1", "msg-1.webm"))
	assert.True(t, os.IsNotExist(err))
}

func TestLocalStore_DeletePrefix(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewLocal(dir)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = store.Put(ctx, "sess-1/msg-1.webm", []byte("a1"), "audio/webm")
	require.NoError(t, err)
	_, err = store.Put(ctx, "sess-1/msg-2.webm", []byte("a2"), "audio/webm")
	require.NoError(t, err)
	_, err = store.Put(ctx, "sess-2/msg-3.webm", []byte("a3"), "audio/webm")
	require.NoError(t, err)

	err = store.DeletePrefix(ctx, "sess-1/")
	require.NoError(t, err)

	// sess-1 files deleted.
	_, err = os.Stat(filepath.Join(dir, "sess-1", "msg-1.webm"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(dir, "sess-1", "msg-2.webm"))
	assert.True(t, os.IsNotExist(err))

	// sess-2 untouched.
	data, err := os.ReadFile(filepath.Join(dir, "sess-2", "msg-3.webm"))
	require.NoError(t, err)
	assert.Equal(t, []byte("a3"), data)
}

func TestLocalStore_DeletePrefix_StringSemantics(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewLocal(dir)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = store.Put(ctx, "abc/file.webm", []byte("a1"), "audio/webm")
	require.NoError(t, err)
	_, err = store.Put(ctx, "abcdef/file.webm", []byte("a2"), "audio/webm")
	require.NoError(t, err)

	// "abc" (no trailing slash) matches both "abc/" and "abcdef/".
	err = store.DeletePrefix(ctx, "abc")
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(dir, "abc", "file.webm"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(dir, "abcdef", "file.webm"))
	assert.True(t, os.IsNotExist(err))
}

func TestLocalStore_NewLocal_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "audio")
	store, err := storage.NewLocal(dir)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = store.Put(ctx, "test.webm", []byte("data"), "audio/webm")
	require.NoError(t, err)

	_ = store // use store to prevent lint error
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/storage/ -v`
Expected: compilation failure — `storage.NewLocal` not defined.

- [ ] **Step 4: Implement LocalStore**

Create `internal/storage/local.go`:

```go
package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LocalStore stores objects on the local filesystem.
type LocalStore struct {
	dir string
}

// NewLocal creates a LocalStore rooted at dir, creating it if needed.
func NewLocal(dir string) (*LocalStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create storage dir: %w", err)
	}
	return &LocalStore{dir: dir}, nil
}

func (s *LocalStore) Put(_ context.Context, key string, data []byte, _ string) (string, error) {
	p := filepath.Join(s.dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", fmt.Errorf("create parent dirs: %w", err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return "file://" + p, nil
}

func (s *LocalStore) Delete(_ context.Context, key string) error {
	p := filepath.Join(s.dir, filepath.FromSlash(key))
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}

func (s *LocalStore) DeletePrefix(_ context.Context, prefix string) error {
	return filepath.WalkDir(s.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(s.dir, path)
		if err != nil {
			return err
		}
		// Use forward slashes for key comparison (matches GCS semantics).
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, prefix) {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("delete %s: %w", rel, err)
			}
		}
		return nil
	})
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/storage/ -v`
Expected: all 5 tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/storage/storage.go internal/storage/local.go internal/storage/local_test.go
git commit -m "feat(storage): add ObjectStore interface and local filesystem implementation"
```

---

### Task 2: GCS Implementation

**Files:**
- Create: `internal/storage/gcs.go`
- Modify: `go.mod` (new dependency)

- [ ] **Step 1: Add the GCS SDK dependency**

Run: `go get cloud.google.com/go/storage`

- [ ] **Step 2: Implement GCSStore**

Create `internal/storage/gcs.go`:

```go
package storage

import (
	"context"
	"fmt"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

// GCSStore stores objects in Google Cloud Storage.
type GCSStore struct {
	client *gcs.Client
	bucket string
}

// NewGCS creates a GCSStore using Application Default Credentials.
func NewGCS(ctx context.Context, bucket string) (*GCSStore, error) {
	client, err := gcs.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create gcs client: %w", err)
	}
	return &GCSStore{client: client, bucket: bucket}, nil
}

func (s *GCSStore) Put(ctx context.Context, key string, data []byte, contentType string) (string, error) {
	w := s.client.Bucket(s.bucket).Object(key).NewWriter(ctx)
	w.ContentType = contentType
	if _, err := w.Write(data); err != nil {
		w.Close()
		return "", fmt.Errorf("gcs write %s: %w", key, err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("gcs close writer %s: %w", key, err)
	}
	return fmt.Sprintf("gs://%s/%s", s.bucket, key), nil
}

func (s *GCSStore) Delete(ctx context.Context, key string) error {
	err := s.client.Bucket(s.bucket).Object(key).Delete(ctx)
	if err != nil && err != gcs.ErrObjectNotExist {
		return fmt.Errorf("gcs delete %s: %w", key, err)
	}
	return nil
}

func (s *GCSStore) DeletePrefix(ctx context.Context, prefix string) error {
	it := s.client.Bucket(s.bucket).Objects(ctx, &gcs.Query{Prefix: prefix})
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return fmt.Errorf("gcs list prefix %s: %w", prefix, err)
		}
		if err := s.client.Bucket(s.bucket).Object(attrs.Name).Delete(ctx); err != nil && err != gcs.ErrObjectNotExist {
			return fmt.Errorf("gcs delete %s: %w", attrs.Name, err)
		}
	}
	return nil
}

// Close closes the underlying GCS client.
func (s *GCSStore) Close() error {
	return s.client.Close()
}
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./internal/storage/`
Expected: compiles without errors.

- [ ] **Step 4: Commit**

```bash
git add internal/storage/gcs.go go.mod go.sum
git commit -m "feat(storage): add GCS implementation of ObjectStore"
```

---

### Task 3: Configuration

**Files:**
- Modify: `internal/config/config.go:14-24` (add Storage to Config struct)
- Modify: `internal/config/config.go:136-150` (add validation)

- [ ] **Step 1: Add Storage config struct and field**

Add the `Storage` struct after the `Otel` struct (after line 117 in `internal/config/config.go`):

```go
type Storage struct {
	Backend  string `env:"STORAGE_BACKEND,default=local"`
	Bucket   string `env:"STORAGE_BUCKET"`
	LocalDir string `env:"STORAGE_LOCAL_DIR,default=data/audio"`
}
```

Add the field to `Config` (line 24, before the closing brace):

```go
type Config struct {
	Server   Server
	Database Database
	LLM      LLM
	Speech   Speech
	Email    Email
	River    River
	Auth     Auth
	OAuth    OAuth
	Otel     Otel
	Storage  Storage
}
```

- [ ] **Step 2: Add validation**

In the `validate` function, add after the `TokenSecret` check (before `return nil`):

```go
if cfg.Storage.Backend != "local" && cfg.Storage.Backend != "gcs" {
	return fmt.Errorf("STORAGE_BACKEND must be \"local\" or \"gcs\", got %q", cfg.Storage.Backend)
}
if cfg.Storage.Backend == "gcs" && cfg.Storage.Bucket == "" {
	return fmt.Errorf("STORAGE_BUCKET is required when STORAGE_BACKEND=gcs")
}
```

- [ ] **Step 3: Verify compilation and existing tests**

Run: `go build ./internal/config/ && go test ./internal/config/ -v`
Expected: compiles, existing tests pass (default `local` means no env changes needed).

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add Storage config (STORAGE_BACKEND, STORAGE_BUCKET, STORAGE_LOCAL_DIR)"
```

---

### Task 4: Wire ObjectStore into Backend

**Files:**
- Modify: `internal/backend/backend.go:30-37` (add store field)
- Modify: `internal/backend/backend.go:41-112` (construct store in New)
- Modify: `internal/backend/backend.go:118-136` (add store to TestOverrides)
- Modify: `internal/backend/backend.go:177-191` (close store in Close)
- Modify: `internal/backend/session.go` (add StoreAudio, SetAudioURL methods)
- Modify: `sql/queries/messages.sql` (add SetAudioURL query)

- [ ] **Step 1: Add SetAudioURL SQL query**

Append to `sql/queries/messages.sql`:

```sql

-- name: SetAudioURL :exec
UPDATE messages SET audio_url = $2 WHERE id = $1;
```

- [ ] **Step 2: Regenerate sqlc**

Run: `sqlc generate`
Expected: `internal/db/messages.sql.go` updated with `SetAudioURL` method.

- [ ] **Step 3: Verify regeneration**

Run: `grep -n "SetAudioURL" internal/db/messages.sql.go`
Expected: shows the generated `SetAudioURL` function.

- [ ] **Step 4: Add store field to Backend struct**

In `internal/backend/backend.go`, add the import and field:

Add to imports:
```go
"github.com/btc/drill/internal/storage"
```

Update the struct (line 30-37):
```go
type Backend struct {
	pool  *pgxpool.Pool
	jobs  Jobs
	cfg   *config.Config
	llm   *ai.Client
	stt   ai.Transcriber
	tts   ai.Synthesizer
	store storage.ObjectStore
}
```

- [ ] **Step 5: Construct store in New()**

In `internal/backend/backend.go`, in the `New` function, add store construction after the AI clients block (after line 67) and before the River client block:

```go
// Object storage
var store storage.ObjectStore
switch cfg.Storage.Backend {
case "gcs":
	store, err = storage.NewGCS(context.Background(), cfg.Storage.Bucket)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("gcs storage: %w", err)
	}
	slog.Info("storage: gcs", "bucket", cfg.Storage.Bucket)
case "local":
	store, err = storage.NewLocal(cfg.Storage.LocalDir)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("local storage: %w", err)
	}
	slog.Info("storage: local", "dir", cfg.Storage.LocalDir)
}
```

Add `store` to the returned struct literal:
```go
return &Backend{
	pool:  pool,
	jobs:  riverClient,
	cfg:   cfg,
	llm:   llmClient,
	stt:   stt,
	tts:   tts,
	store: store,
}, nil
```

- [ ] **Step 6: Add store to TestOverrides**

In `internal/backend/backend.go`, update `TestOverrides` and `ApplyTestOverrides`:

```go
type TestOverrides struct {
	LLM   *ai.Client
	STT   ai.Transcriber
	TTS   ai.Synthesizer
	Store storage.ObjectStore
}

func (b *Backend) ApplyTestOverrides(o TestOverrides) {
	if o.LLM != nil {
		b.llm = o.LLM
	}
	if o.STT != nil {
		b.stt = o.STT
	}
	if o.TTS != nil {
		b.tts = o.TTS
	}
	if o.Store != nil {
		b.store = o.Store
	}
}
```

- [ ] **Step 7: Close store in Backend.Close()**

In `internal/backend/backend.go`, in the `Close` method, add GCS client cleanup before pool close. The GCSStore has a `Close()` method but LocalStore does not, so use a type assertion:

```go
func (b *Backend) Close() error {
	timeout := time.Duration(b.cfg.River.ShutdownTimeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := b.jobs.Stop(ctx); err != nil {
		slog.Warn("river stop error", "error", err)
	}
	slog.Info("river stopped")

	if c, ok := b.store.(interface{ Close() error }); ok {
		if err := c.Close(); err != nil {
			slog.Warn("storage close error", "error", err)
		}
	}

	b.pool.Close()
	slog.Info("database pool closed")
	return nil
}
```

- [ ] **Step 8: Add StoreAudio and SetAudioURL methods to Backend**

Append to `internal/backend/session.go`:

```go
// StoreAudio uploads audio bytes to object storage and returns the URL.
func (b *Backend) StoreAudio(ctx context.Context, key string, data []byte, contentType string) (string, error) {
	return b.store.Put(ctx, key, data, contentType)
}

// SetAudioURL updates the audio_url column for a message.
func (b *Backend) SetAudioURL(ctx context.Context, id uuid.UUID, url string) error {
	return db.New(b.pool).SetAudioURL(ctx, db.SetAudioURLParams{
		ID:       id,
		AudioUrl: pgtype.Text{String: url, Valid: true},
	})
}
```

Note: Check the exact generated `SetAudioURLParams` field names from sqlc output. The param names should be `ID` and `AudioUrl` based on the column names `id` and `audio_url`.

- [ ] **Step 9: Verify compilation**

Run: `go build ./internal/backend/`
Expected: compiles without errors.

- [ ] **Step 10: Commit**

```bash
git add sql/queries/messages.sql internal/db/messages.sql.go internal/db/querier.go internal/backend/backend.go internal/backend/session.go
git commit -m "feat(backend): wire ObjectStore into Backend with StoreAudio and SetAudioURL"
```

---

### Task 5: Conductor Integration — Upload Audio in Goroutine

**Files:**
- Modify: `internal/interview/conductor.go:297-353` (endTurn method)

- [ ] **Step 1: Add OTel tracer import and package-level tracer**

In `internal/interview/conductor.go`, add to imports:

```go
"go.opentelemetry.io/otel"
"go.opentelemetry.io/otel/codes"
```

Add a package-level tracer after the imports (before the `ConductorParams` struct):

```go
var tracer = otel.Tracer("drill/interview")
```

- [ ] **Step 2: Modify endTurn to upload audio concurrently**

Replace the voice branch in `endTurn` (lines 301-326 in `internal/interview/conductor.go`) with:

```go
if msg.InputMethod == "voice" {
	// Validate audio.
	if len(msg.Audio) == 0 {
		c.send(ctx, msgError("audio_validation_failed", "no audio data provided"))
		return nil
	}

	// Transition to Transcribing.
	if err := c.sm.Transition(StateTranscribing); err != nil {
		c.send(ctx, msgError("invalid_state_transition", err.Error()))
		return nil
	}
	c.send(ctx, msgStateChange(StateTranscribing))

	// Generate messageID now — both upload goroutine and persist need it.
	messageID := uuid.New()

	// Fire upload goroutine — does not block transcription.
	go func() {
		uploadCtx, span := tracer.Start(context.Background(), "storage.upload_audio")
		defer span.End()

		key := fmt.Sprintf("%s/%s.webm", c.sessionID, messageID)
		url, err := c.backend.StoreAudio(uploadCtx, key, msg.Audio, "audio/webm")
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "audio upload failed")
			slog.Error("conductor: audio upload failed",
				"error", err,
				"session_id", c.sessionID,
				"message_id", messageID)
			return
		}
		if err := c.backend.SetAudioURL(uploadCtx, messageID, url); err != nil {
			span.RecordError(err)
			slog.Error("conductor: failed to set audio_url",
				"error", err,
				"session_id", c.sessionID,
				"message_id", messageID)
		}
	}()

	// STT (runs concurrently with upload).
	text, err := c.backend.Transcribe(ctx, msg.Audio, "webm")
	if err != nil {
		slog.Error("conductor: transcription failed", "error", err, "session_id", c.sessionID)
		return fmt.Errorf("transcription: %w", err)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("transcription returned empty text")
	}

	c.send(ctx, msgTranscriptionResult(text))
	candidateContent = text
```

- [ ] **Step 3: Update endTurn to use pre-generated messageID for persist**

The `messageID` needs to be shared between the upload goroutine and the persist call. Hoist it to function scope and add a `persistMessageWithID` helper.

At the top of `endTurn`, after `var candidateContent string`, add:

```go
var messageID uuid.UUID
```

In the voice branch (Step 2 above), the line `messageID := uuid.New()` becomes `messageID = uuid.New()` (assign, not declare — it's hoisted).

Add a new helper method after the existing `persistMessage`:

```go
func (c *Conductor) persistMessageWithID(ctx context.Context, id uuid.UUID, role, content, inputMethod string) (db.Message, error) {
	c.sequence++

	msg, err := c.backend.PersistMessage(ctx, backend.PersistMessageParams{
		MessageID:   id,
		SessionID:   c.sessionID,
		Seq:         c.sequence,
		Role:        role,
		Content:     content,
		InputMethod: inputMethod,
	})
	if err != nil {
		c.sequence-- // rollback sequence on failure
		return db.Message{}, fmt.Errorf("insert message: %w", err)
	}
	return msg, nil
}
```

Update the existing `persistMessage` to delegate:

```go
func (c *Conductor) persistMessage(ctx context.Context, role, content, inputMethod string) (db.Message, error) {
	return c.persistMessageWithID(ctx, uuid.New(), role, content, inputMethod)
}
```

Replace the persist call site in `endTurn` (line 344) with:

```go
// Persist candidate message.
var candidateMsg db.Message
var persistErr error
if messageID != uuid.Nil {
	candidateMsg, persistErr = c.persistMessageWithID(ctx, messageID, "candidate", candidateContent, msg.InputMethod)
} else {
	candidateMsg, persistErr = c.persistMessage(ctx, "candidate", candidateContent, msg.InputMethod)
}
if persistErr != nil {
	slog.Error("conductor: persist candidate message", "error", persistErr, "session_id", c.sessionID)
	return fmt.Errorf("persist candidate message: %w", persistErr)
}
c.messages = append(c.messages, candidateMsg)
```

- [ ] **Step 4: Verify compilation**

Run: `go build ./internal/interview/`
Expected: compiles without errors.

- [ ] **Step 5: Commit**

```bash
git add internal/interview/conductor.go
git commit -m "feat(conductor): upload audio to object storage concurrently with transcription"
```

---

### Task 6: Integration Test

**Files:**
- Create: `internal/storage/mock.go`
- Modify: `internal/handler/session_ws_test.go` (if existing WebSocket test exists, add audio upload verification)

- [ ] **Step 1: Create a recording mock for tests**

Create `internal/storage/mock.go`:

```go
package storage

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// MockStore records all calls for test assertions.
type MockStore struct {
	mu      sync.Mutex
	Objects map[string][]byte
	PutErr  error // if set, Put returns this error
}

func NewMockStore() *MockStore {
	return &MockStore{Objects: make(map[string][]byte)}
}

func (m *MockStore) Put(_ context.Context, key string, data []byte, _ string) (string, error) {
	if m.PutErr != nil {
		return "", m.PutErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Objects[key] = data
	return fmt.Sprintf("mock://%s", key), nil
}

func (m *MockStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Objects, key)
	return nil
}

func (m *MockStore) DeletePrefix(_ context.Context, prefix string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.Objects {
		if strings.HasPrefix(k, prefix) {
			delete(m.Objects, k)
		}
	}
	return nil
}

// HasKey returns true if the given key exists in the store.
func (m *MockStore) HasKey(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.Objects[key]
	return ok
}

// Count returns the number of stored objects.
func (m *MockStore) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Objects)
}
```

- [ ] **Step 2: Verify MockStore compiles and satisfies interface**

Run: `go build ./internal/storage/`
Expected: compiles. The `MockStore` implements `ObjectStore`.

- [ ] **Step 3: Commit**

```bash
git add internal/storage/mock.go
git commit -m "feat(storage): add MockStore for testing"
```

---

### Task 7: Terraform Variables Update

**Files:**
- Modify: `terraform/terraform.tfvars.example` (add storage bucket example if not present)

- [ ] **Step 1: Check if terraform.tfvars.example needs updating**

Read `terraform/terraform.tfvars.example` and verify the GCS bucket variable is documented. The bucket name is derived from `project_id` in `gcs.tf`, so no new Terraform variable is needed. However, ensure the deployment docs mention `STORAGE_BACKEND=gcs` and `STORAGE_BUCKET` env vars for Cloud Run.

- [ ] **Step 2: Update deploy workflow if needed**

Check `.github/workflows/deploy.yml` for env vars passed to Cloud Run. If `STORAGE_BACKEND` and `STORAGE_BUCKET` are not set, add them.

In the Cloud Run service definition, add env vars:
```
STORAGE_BACKEND=gcs
STORAGE_BUCKET=${{ vars.GCP_PROJECT_ID }}-audio
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/deploy.yml
git commit -m "deploy: add STORAGE_BACKEND and STORAGE_BUCKET env vars to Cloud Run"
```

---

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
	url, err := store.Audio().Put(ctx, "sess-1/msg-1.webm", []byte("audio-data"), "audio/webm")
	require.NoError(t, err)

	assert.Equal(t, "file://"+filepath.Join(dir, "audio", "sess-1", "msg-1.webm"), url)

	data, err := os.ReadFile(filepath.Join(dir, "audio", "sess-1", "msg-1.webm"))
	require.NoError(t, err)
	assert.Equal(t, []byte("audio-data"), data)
}

func TestLocalStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewLocal(dir)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = store.Audio().Put(ctx, "sess-1/msg-1.webm", []byte("audio-data"), "audio/webm")
	require.NoError(t, err)

	err = store.Audio().Delete(ctx, "sess-1/msg-1.webm")
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(dir, "audio", "sess-1", "msg-1.webm"))
	assert.True(t, os.IsNotExist(err))
}

func TestLocalStore_DeletePrefix(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewLocal(dir)
	require.NoError(t, err)

	ctx := context.Background()
	bucket := store.Audio()
	_, err = bucket.Put(ctx, "sess-1/msg-1.webm", []byte("a1"), "audio/webm")
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
	dir := t.TempDir()
	store, err := storage.NewLocal(dir)
	require.NoError(t, err)

	ctx := context.Background()
	bucket := store.Audio()
	_, err = bucket.Put(ctx, "abc/file.webm", []byte("a1"), "audio/webm")
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
	store, err := storage.NewLocal(dir)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = store.Audio().Put(ctx, "test.webm", []byte("data"), "audio/webm")
	require.NoError(t, err)
}

func TestLocalStore_Delete_Idempotent(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewLocal(dir)
	require.NoError(t, err)
	// Ensure audio bucket dir exists before deleting from it.
	_, err = store.Audio().Put(context.Background(), "placeholder", []byte("x"), "text/plain")
	require.NoError(t, err)
	err = store.Audio().Delete(context.Background(), "nonexistent/file.webm")
	require.NoError(t, err)
}

func TestLocalStore_Put_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewLocal(dir)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = store.Audio().Put(ctx, "../../../etc/passwd", []byte("malicious"), "text/plain")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "escapes storage root")
}

func TestLocalStore_Delete_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewLocal(dir)
	require.NoError(t, err)

	err = store.Audio().Delete(context.Background(), "../../etc/passwd")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "escapes storage root")
}

func TestLocalStore_PublicBucket(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewLocal(dir)
	require.NoError(t, err)

	ctx := context.Background()
	url, err := store.Public().Put(ctx, "images/test.png", []byte("image-data"), "image/png")
	require.NoError(t, err)

	assert.Equal(t, "file://"+filepath.Join(dir, "public", "images", "test.png"), url)

	data, err := os.ReadFile(filepath.Join(dir, "public", "images", "test.png"))
	require.NoError(t, err)
	assert.Equal(t, []byte("image-data"), data)
}

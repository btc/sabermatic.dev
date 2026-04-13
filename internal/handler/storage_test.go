package handler_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/testutil"
)

func TestStorageRoute_Local(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	cfg := b.Config()
	// cfg.Storage.Backend is already "local" and LocalDir is already a
	// unique t.TempDir() (see testutil.baseEnv). Just drop a known file in.
	require.NoError(t, os.WriteFile(filepath.Join(cfg.Storage.LocalDir, "hello.txt"), []byte("hi"), 0o644))

	h := testutil.NewTestHandler(t, b)
	req := httptest.NewRequest(http.MethodGet, "/storage/hello.txt", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "hi", w.Body.String())
}

func TestStorageRoute_GCSNotRegistered(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	cfg := b.Config()
	cfg.Storage.Backend = "gcs"
	cfg.Storage.Bucket = "test-bucket"
	cfg.Storage.PublicBucket = "test-public-bucket"
	b.SetConfig(cfg)

	// With gcs backend, /storage/ is not registered on the mux; the request
	// falls through to the SPA catch-all at "/", which serves index.html so
	// the SPA can handle client-side routing.
	h := testutil.NewTestHandler(t, b)
	req := httptest.NewRequest(http.MethodGet, "/storage/hello.txt", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "<html")
}

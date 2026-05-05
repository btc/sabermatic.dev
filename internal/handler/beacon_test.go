package handler_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/events"
	"github.com/btc/drill/internal/handler"
)

func newRecorder(t *testing.T) (*events.Emitter, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return events.NewEmitter(logger), &buf
}

func TestBeacon_AcceptsAllowedEvent(t *testing.T) {
	em, buf := newRecorder(t)
	body := strings.NewReader(`{"event_name":"landing_view","referrer":"https://news.ycombinator.com/"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/beacon", body)
	rec := httptest.NewRecorder()

	handler.BeaconHandler(em).ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	var rec1 map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec1))
	require.Equal(t, "landing_view", rec1["event_name"])
	require.Equal(t, "https://news.ycombinator.com/", rec1["referer"])
}

func TestBeacon_RejectsDisallowedEvent(t *testing.T) {
	em, _ := newRecorder(t)
	body := strings.NewReader(`{"event_name":"signup_completed"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/beacon", body)
	rec := httptest.NewRecorder()

	handler.BeaconHandler(em).ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBeacon_RejectsBadJSON(t *testing.T) {
	em, _ := newRecorder(t)
	req := httptest.NewRequest(http.MethodPost, "/api/beacon", io.NopCloser(strings.NewReader("not json")))
	rec := httptest.NewRecorder()
	handler.BeaconHandler(em).ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestBeacon_RejectsNonPost(t *testing.T) {
	em, _ := newRecorder(t)
	req := httptest.NewRequest(http.MethodGet, "/api/beacon", nil)
	rec := httptest.NewRecorder()
	handler.BeaconHandler(em).ServeHTTP(rec, req)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestBeacon_PassesPropertiesIntoEmission(t *testing.T) {
	em, buf := newRecorder(t)
	body := strings.NewReader(`{"event_name":"signup_started","properties":{"auth_method":"google"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/beacon", body)
	rec := httptest.NewRecorder()

	handler.BeaconHandler(em).ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	var rec1 map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec1))
	props, ok := rec1["properties"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "google", props["auth_method"])
}

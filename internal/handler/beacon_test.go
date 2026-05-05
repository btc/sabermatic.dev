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

// Body-provided referrer wins over middleware-captured Referer header.
// The frontend SDK is the source of truth for beacon requests because the
// browser's document.referrer (the external site) is only available client-side.
func TestBeacon_BodyReferrerOverridesMiddlewareCtx(t *testing.T) {
	em, buf := newRecorder(t)
	body := strings.NewReader(`{"event_name":"landing_view","referrer":"https://news.ycombinator.com/"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/beacon", body)
	// Simulate middleware having stashed a (wrong-for-beacons) ctx referer.
	ctx := events.WithReferer(req.Context(), "https://sabermatic.dev/")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.BeaconHandler(em).ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	var rec1 map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec1))
	require.Equal(t, "https://news.ycombinator.com/", rec1["referer"])
}

// Empty body referrer also wins (frontend explicitly says "no external referrer"
// for direct nav). Middleware ctx referer must NOT leak through.
func TestBeacon_EmptyBodyReferrerClearsMiddlewareCtx(t *testing.T) {
	em, buf := newRecorder(t)
	body := strings.NewReader(`{"event_name":"landing_view","referrer":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/beacon", body)
	ctx := events.WithReferer(req.Context(), "https://sabermatic.dev/")
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.BeaconHandler(em).ServeHTTP(rec, req)

	require.Equal(t, http.StatusNoContent, rec.Code)
	var rec1 map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec1))
	require.Equal(t, "", rec1["referer"])
}

// Oversized body should be rejected to protect this unauth public endpoint.
func TestBeacon_RejectsOversizedBody(t *testing.T) {
	em, _ := newRecorder(t)
	// 32 KiB of JSON-safe filler (cap is 16 KiB).
	huge := `{"event_name":"landing_view","properties":{"junk":"` + strings.Repeat("x", 32*1024) + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/beacon", strings.NewReader(huge))
	rec := httptest.NewRecorder()
	handler.BeaconHandler(em).ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// Property cardinality cap protects the BQ properties RECORD column from
// schema bloat under buggy or hostile callers.
func TestBeacon_RejectsTooManyProperties(t *testing.T) {
	em, _ := newRecorder(t)
	// 17 keys (cap is 16).
	props := make(map[string]string, 17)
	for i := 0; i < 17; i++ {
		props[fmtKey(i)] = "x"
	}
	body, err := json.Marshal(map[string]any{
		"event_name": "landing_view",
		"properties": props,
	})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/beacon", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.BeaconHandler(em).ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// Non-POST returns 405 with an Allow: POST header per RFC 7231 §6.5.5.
func TestBeacon_NonPostSetsAllowHeader(t *testing.T) {
	em, _ := newRecorder(t)
	req := httptest.NewRequest(http.MethodGet, "/api/beacon", nil)
	rec := httptest.NewRecorder()
	handler.BeaconHandler(em).ServeHTTP(rec, req)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	require.Equal(t, "POST", rec.Header().Get("Allow"))
}

func fmtKey(i int) string {
	return "k" + string(rune('a'+i))
}

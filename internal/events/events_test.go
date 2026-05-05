package events_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/events"
)

func TestEmit_WritesAllStandardFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	em := events.NewEmitter(logger)

	ctx := context.Background()
	ctx = events.WithVisitorID(ctx, "visitor-abc")
	ctx = events.WithUserID(ctx, "user-xyz")
	ctx = events.WithReferer(ctx, "https://news.ycombinator.com/")
	ctx = events.WithUTM(ctx, "hn", "social", "show-hn")

	em.Emit(ctx, "signup_completed", slog.String("auth_method", "password"))

	var record map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))

	require.Equal(t, true, record["analytics_event"])
	require.Equal(t, "signup_completed", record["event_name"])
	require.Equal(t, "visitor-abc", record["visitor_id"])
	require.Equal(t, "user-xyz", record["user_id"])
	require.Equal(t, "https://news.ycombinator.com/", record["referer"])
	require.Equal(t, "hn", record["utm_source"])
	require.NotEmpty(t, record["event_id"], "event_id must be a UUID")

	props, ok := record["properties"].(map[string]any)
	require.True(t, ok, "properties must be a nested object")
	require.Equal(t, "password", props["auth_method"])
}

func TestEmit_NoContextValues_StillEmitsWithEmpties(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	em := events.NewEmitter(logger)

	em.Emit(context.Background(), "landing_view")

	var record map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))
	require.Equal(t, "landing_view", record["event_name"])
	require.Equal(t, "", record["visitor_id"])
	require.Equal(t, "", record["user_id"])
}

func TestEmit_NilEmitter_NoPanic(t *testing.T) {
	var em *events.Emitter
	em.Emit(context.Background(), "anything") // must not panic
}

func TestContextValues_PreserveAcrossWithCalls(t *testing.T) {
	ctx := context.Background()
	ctx = events.WithVisitorID(ctx, "v1")
	ctx = events.WithUserID(ctx, "u1")
	ctx = events.WithReferer(ctx, "r1")
	ctx = events.WithUTM(ctx, "s1", "m1", "c1")

	cv := events.ContextValuesFromContext(ctx)
	require.Equal(t, "v1", cv.VisitorID)
	require.Equal(t, "u1", cv.UserID)
	require.Equal(t, "r1", cv.Referer)
	require.Equal(t, "s1", cv.UTMSource)
	require.Equal(t, "m1", cv.UTMMedium)
	require.Equal(t, "c1", cv.UTMCampaign)
}

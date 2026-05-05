package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/events"
	"github.com/btc/drill/internal/handler"
)

func TestAnalyticsContextMiddleware_SetsCookieOnFirstHit(t *testing.T) {
	mw := handler.AnalyticsContextMiddleware(false)

	var capturedVisitorID string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedVisitorID = events.ContextValuesFromContext(r.Context()).VisitorID
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1)
	require.Equal(t, handler.VisitorCookieName, cookies[0].Name)
	require.NotEmpty(t, cookies[0].Value)
	require.Equal(t, cookies[0].Value, capturedVisitorID, "ctx visitor_id must match the cookie value")
}

func TestAnalyticsContextMiddleware_ReusesExistingCookie(t *testing.T) {
	mw := handler.AnalyticsContextMiddleware(false)

	var capturedVisitorID string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		capturedVisitorID = events.ContextValuesFromContext(r.Context()).VisitorID
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: handler.VisitorCookieName, Value: "existing-visitor-id"})
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	require.Empty(t, rec.Result().Cookies(), "no new cookie should be set when one exists")
	require.Equal(t, "existing-visitor-id", capturedVisitorID)
}

func TestAnalyticsContextMiddleware_CapturesRefererAndUTM(t *testing.T) {
	mw := handler.AnalyticsContextMiddleware(false)

	var cv events.ContextValues
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		cv = events.ContextValuesFromContext(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/?utm_source=hn&utm_medium=social&utm_campaign=show-hn", nil)
	req.Header.Set("Referer", "https://news.ycombinator.com/")
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)

	require.Equal(t, "https://news.ycombinator.com/", cv.Referer)
	require.Equal(t, "hn", cv.UTMSource)
	require.Equal(t, "social", cv.UTMMedium)
	require.Equal(t, "show-hn", cv.UTMCampaign)
}

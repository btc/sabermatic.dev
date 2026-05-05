// Package events provides analytics event emission and per-request context
// propagation for visitor/user identity, referer, and UTM parameters.
package events

import "context"

// ContextValues bundles the per-request analytics context populated by
// AnalyticsContextMiddleware (visitor_id, referer, UTM, path, user_agent),
// the auth middlewares (user_id), and backend handlers (session_id).
//
// IMPORTANT: every field here becomes a top-level column in the BQ view via
// jsonPayload.<field>. Event-specific properties go into the `properties`
// slog.Group instead and become a nested RECORD column.
type ContextValues struct {
	VisitorID   string
	UserID      string
	SessionID   string
	Referer     string
	UTMSource   string
	UTMMedium   string
	UTMCampaign string
	Path        string
	UserAgent   string
}

type ctxKey struct{}

// WithContextValues returns a new context with the given ContextValues stored.
// Callers should populate via the With* helpers below rather than building a
// ContextValues struct directly.
func WithContextValues(ctx context.Context, cv ContextValues) context.Context {
	return context.WithValue(ctx, ctxKey{}, cv)
}

// ContextValuesFromContext returns the ContextValues stored on ctx, or a zero
// struct if none is present.
func ContextValuesFromContext(ctx context.Context) ContextValues {
	cv, _ := ctx.Value(ctxKey{}).(ContextValues)
	return cv
}

// WithVisitorID returns a new ctx with VisitorID set; preserves other fields.
func WithVisitorID(ctx context.Context, id string) context.Context {
	cv := ContextValuesFromContext(ctx)
	cv.VisitorID = id
	return WithContextValues(ctx, cv)
}

// WithUserID returns a new ctx with UserID set; preserves other fields.
func WithUserID(ctx context.Context, id string) context.Context {
	cv := ContextValuesFromContext(ctx)
	cv.UserID = id
	return WithContextValues(ctx, cv)
}

// WithSessionID returns a new ctx with SessionID set; preserves other fields.
// Backend handlers operating on a known session call this before Emit so that
// session_id lands at jsonPayload.session_id (top-level) rather than buried
// inside the per-event properties record.
func WithSessionID(ctx context.Context, id string) context.Context {
	cv := ContextValuesFromContext(ctx)
	cv.SessionID = id
	return WithContextValues(ctx, cv)
}

// WithRequestPath returns a new ctx with Path set; preserves other fields.
func WithRequestPath(ctx context.Context, path string) context.Context {
	cv := ContextValuesFromContext(ctx)
	cv.Path = path
	return WithContextValues(ctx, cv)
}

// WithUserAgent returns a new ctx with UserAgent set; preserves other fields.
func WithUserAgent(ctx context.Context, ua string) context.Context {
	cv := ContextValuesFromContext(ctx)
	cv.UserAgent = ua
	return WithContextValues(ctx, cv)
}

// WithReferer returns a new ctx with Referer set; preserves other fields.
func WithReferer(ctx context.Context, ref string) context.Context {
	cv := ContextValuesFromContext(ctx)
	cv.Referer = ref
	return WithContextValues(ctx, cv)
}

// WithUTM returns a new ctx with UTM source/medium/campaign set; preserves
// other fields.
func WithUTM(ctx context.Context, source, medium, campaign string) context.Context {
	cv := ContextValuesFromContext(ctx)
	cv.UTMSource = source
	cv.UTMMedium = medium
	cv.UTMCampaign = campaign
	return WithContextValues(ctx, cv)
}

package rpc

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/btc/drill/internal/auth"
)

// AuthInterceptor returns a Connect unary interceptor that validates the
// session cookie and injects the authenticated user into the context.
// Reuses auth.SessionAuthenticator — the same interface the HTTP middleware uses.
//
// This covers unary RPCs only. When streaming RPCs are added, extend to
// implement connect.StreamingHandlerInterceptorFunc as well (streaming
// requests access headers via conn.RequestHeader()).
func AuthInterceptor(sa auth.SessionAuthenticator) connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// net/http.Request.Cookie() is a pure header parser; constructing
			// a minimal Request to use it is intentional.
			cookie, err := (&http.Request{Header: req.Header()}).Cookie(auth.SessionCookieName)
			if err != nil || cookie.Value == "" {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
			}

			tokenHash := auth.HashSessionToken(cookie.Value)
			user, err := sa.AuthenticateSession(ctx, tokenHash)
			if err != nil {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or expired session"))
			}

			span := trace.SpanFromContext(ctx)
			span.SetAttributes(attribute.String("user_id", user.ID.String()))

			ctx = auth.WithUser(ctx, user)
			return next(ctx, req)
		}
	}
}

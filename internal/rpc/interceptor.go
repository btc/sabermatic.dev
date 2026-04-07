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

// AuthInterceptor returns a Connect interceptor that validates the session
// cookie and injects the authenticated user into the context. Handles both
// unary and streaming RPCs.
func AuthInterceptor(sa auth.SessionAuthenticator) connect.Interceptor {
	return &authInterceptor{sa: sa}
}

type authInterceptor struct {
	sa auth.SessionAuthenticator
}

func (a *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		ctx, err := a.authenticate(ctx, req.Header())
		if err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

func (a *authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (a *authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		ctx, err := a.authenticate(ctx, conn.RequestHeader())
		if err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

// authenticate extracts the session cookie from headers, validates it, and
// returns a context with the authenticated user.
func (a *authInterceptor) authenticate(ctx context.Context, headers http.Header) (context.Context, *connect.Error) {
	// net/http.Request.Cookie() is a pure header parser; constructing
	// a minimal Request to use it is intentional.
	cookie, err := (&http.Request{Header: headers}).Cookie(auth.SessionCookieName)
	if err != nil || cookie.Value == "" {
		return ctx, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	tokenHash := auth.HashSessionToken(cookie.Value)
	user, authErr := a.sa.AuthenticateSession(ctx, tokenHash)
	if authErr != nil {
		return ctx, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or expired session"))
	}

	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attribute.String("user_id", user.ID.String()))

	return auth.WithUser(ctx, user), nil
}

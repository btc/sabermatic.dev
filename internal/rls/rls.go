// Package rls provides Row-Level Security middleware that scopes database
// queries to the authenticated user by setting a Postgres session variable.
//
// The middleware acquires a dedicated connection from the pool, calls
// SET app.current_user_id, and stores the connection in context. Downstream
// code uses DBFromContext to obtain the RLS-scoped connection (falling back
// to the pool when the middleware is not active, e.g. in unauthenticated
// routes).
package rls

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/btc/drill/internal/auth"
)

// DBTX is the interface accepted by sqlc's New(). Both *pgxpool.Pool and
// *pgx.Conn satisfy it.
type DBTX interface {
	Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error)
	Query(context.Context, string, ...interface{}) (pgx.Rows, error)
	QueryRow(context.Context, string, ...interface{}) pgx.Row
}

type contextKey struct{}

// withDB stores a dedicated RLS-scoped connection in the context.
func withDB(ctx context.Context, conn *pgxpool.Conn) context.Context {
	return context.WithValue(ctx, contextKey{}, conn)
}

// DBFromContext returns the RLS-scoped connection stored in ctx by the
// middleware, or the fallback DBTX (typically the pool) when no scoped
// connection is present.
func DBFromContext(ctx context.Context, fallback DBTX) DBTX {
	if conn, ok := ctx.Value(contextKey{}).(*pgxpool.Conn); ok {
		return conn
	}
	return fallback
}

// Middleware returns HTTP middleware that acquires a connection from pool,
// sets app.current_user_id to the authenticated user's ID, and stores the
// connection in context. If no authenticated user is present, the request
// proceeds without RLS scoping.
//
// The connection is released and the session variable cleared in a defer,
// ensuring no leaked connections or stale user IDs.
func Middleware(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := auth.UserFromContext(r.Context())
			if user == nil {
				next.ServeHTTP(w, r)
				return
			}

			conn, err := pool.Acquire(r.Context())
			if err != nil {
				slog.ErrorContext(r.Context(), "rls: acquire connection", "error", err)
				http.Error(w, "service unavailable", http.StatusServiceUnavailable)
				return
			}
			defer func() {
				// Clear the session variable before returning the connection
				// to the pool. Use set_config with empty string rather than
				// RESET, which may not work for custom GUC params.
				_, _ = conn.Exec(context.Background(), "SELECT set_config('app.current_user_id', '', false)")
				conn.Release()
			}()

			if _, err := conn.Exec(r.Context(), "SELECT set_config('app.current_user_id', $1, false)", user.ID.String()); err != nil {
				slog.ErrorContext(r.Context(), "rls: set_config", "error", err)
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}

			ctx := withDB(r.Context(), conn)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// WithUser scopes a function to the given user ID for non-HTTP contexts
// (e.g., background job workers). It acquires a connection, sets the
// session variable, executes fn with the scoped connection, then cleans up.
func WithUser(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, fn func(ctx context.Context, db DBTX) error) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("rls: acquire connection: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "SELECT set_config('app.current_user_id', '', false)")
		conn.Release()
	}()

	if _, err := conn.Exec(ctx, "SELECT set_config('app.current_user_id', $1, false)", userID.String()); err != nil {
		return fmt.Errorf("rls: set_config: %w", err)
	}

	return fn(withDB(ctx, conn), conn)
}

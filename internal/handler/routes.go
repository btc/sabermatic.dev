package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

// Backend holds shared dependencies for all handlers.
type Backend struct {
	Pool  *pgxpool.Pool
	River *river.Client[pgx.Tx]
}

// RegisterRoutes sets up all HTTP routes on the given mux.
func RegisterRoutes(mux *http.ServeMux, b *Backend) {
	mux.HandleFunc("GET /api/health", Health(b))
}

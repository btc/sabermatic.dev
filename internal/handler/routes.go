package handler

import "net/http"

// RegisterRoutes sets up all HTTP routes on the given mux.
func (b *Backend) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", b.Health())
	mux.HandleFunc("GET /admin/jobs", b.AdminJobsPlaceholder())
}

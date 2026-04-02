package handler

import "net/http"

// RegisterRoutes sets up all HTTP routes on the given mux.
func RegisterRoutes(mux *http.ServeMux, b *Backend) {
	mux.HandleFunc("GET /api/health", Health(b))
	mux.HandleFunc("GET /admin/jobs", AdminJobsPlaceholder())
}

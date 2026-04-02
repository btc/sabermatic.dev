package handler

import (
	"encoding/json"
	"net/http"
)

// AdminJobsPlaceholder returns info about where River UI will be available.
func (b *Backend) AdminJobsPlaceholder() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "placeholder",
			"message": "River UI will be available here after deployment setup",
		})
	}
}

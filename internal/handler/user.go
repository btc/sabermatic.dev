package handler

import (
	"net/http"

	"github.com/btc/drill/internal/auth"
)

// GetMe returns the authenticated user's profile.
func GetMe(b *Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		if user == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"id":             user.ID.String(),
			"email":          user.Email,
			"display_name":   user.DisplayName,
			"role":           user.Role,
			"plan":           user.Plan,
			"email_verified": user.EmailVerified,
		})
	}
}

package handler

import (
	"encoding/json"
	"net/http"
)

// writeJSON encodes body as JSON and writes it with the given HTTP status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writePaidBalanceRequired(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error":   "paid_balance_required",
		"message": "This feature requires a paid plan or minute balance.",
	})
}

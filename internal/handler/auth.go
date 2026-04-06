package handler

import (
	"encoding/json"
	"net/http"
)

// writeJSON encodes body as JSON and writes it with the given HTTP status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

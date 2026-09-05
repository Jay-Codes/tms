package httpserver

import (
	"net/http"

	"tms/backend/internal/httpx"
)

// WriteJSON writes v as a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) { httpx.WriteJSON(w, status, v) }

// NoContent writes a 204 response.
func NoContent(w http.ResponseWriter) { httpx.NoContent(w) }

// DecodeJSON reads a JSON request body into dst, rejecting unknown fields and
// oversized bodies. On failure it writes an RFC 7807 problem and returns false.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return httpx.DecodeJSON(w, r, dst)
}

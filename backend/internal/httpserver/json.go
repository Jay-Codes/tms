package httpserver

import (
	"net/http"

	"tms/backend/internal/httpx"
)

// WriteJSON writes v as a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) { httpx.WriteJSON(w, status, v) }

// NoContent writes a 204 response.
func NoContent(w http.ResponseWriter) { httpx.NoContent(w) }

// WriteRawJSON encodes a body into an already-started response — the status
// code and Content-Type having been written by the caller. It exists for the
// one problem document that carries extra members beyond RFC 7807's
// (`overpay_confirm_required`, which must also say how much and where to).
func WriteRawJSON(w http.ResponseWriter, v any) { httpx.EncodeJSON(w, v) }

// DecodeJSON reads a JSON request body into dst, rejecting unknown fields and
// oversized bodies. On failure it writes an RFC 7807 problem and returns false.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return httpx.DecodeJSON(w, r, dst)
}

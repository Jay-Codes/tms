package httpserver

import (
	"net/http"

	"tms/backend/internal/httpx"
)

// ProblemContentType is the media type mandated by RFC 7807.
const ProblemContentType = httpx.ProblemContentType

// Problem is an RFC 7807 problem detail document.
type Problem = httpx.Problem

// WriteProblem writes an RFC 7807 error response.
func WriteProblem(w http.ResponseWriter, status int, title, detail string) {
	httpx.WriteProblem(w, status, title, detail)
}

// WriteProblemFields writes an RFC 7807 error response carrying per-field
// validation messages in `errors`.
func WriteProblemFields(w http.ResponseWriter, status int, title, detail string, fields map[string]string) {
	httpx.WriteProblemFields(w, status, title, detail, fields)
}

// problemWriter namespaces the RFC 7807 helpers.
type problemWriter struct{}

// Write emits an RFC 7807 problem document.
func (problemWriter) Write(w http.ResponseWriter, status int, title, detail string) {
	httpx.WriteProblem(w, status, title, detail)
}

// Fields emits an RFC 7807 problem document with field errors.
func (problemWriter) Fields(w http.ResponseWriter, status int, title, detail string, fields map[string]string) {
	httpx.WriteProblemFields(w, status, title, detail, fields)
}

// Problem is the package-level entry point for RFC 7807 errors:
//
//	problem.Write(w, http.StatusNotFound, "not found", "no such unit")
//
//nolint:gochecknoglobals // intentional stateless helper namespace
var problem problemWriter

// NotFound writes a 404 problem document.
func NotFound(w http.ResponseWriter, r *http.Request) {
	problem.Write(w, http.StatusNotFound, "not found", "no route matches "+r.URL.Path)
}

// MethodNotAllowed writes a 405 problem document.
func MethodNotAllowed(w http.ResponseWriter, r *http.Request) {
	problem.Write(w, http.StatusMethodNotAllowed, "method not allowed", r.Method+" is not allowed on "+r.URL.Path)
}

package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// ProblemContentType is the media type mandated by RFC 7807.
const ProblemContentType = "application/problem+json"

// Problem is an RFC 7807 problem detail document.
type Problem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

// WriteProblem writes an RFC 7807 error response.
//
// It is exported through the package-level `problem` helper value so callers
// can write `problem.Write(w, status, title, detail)`.
func WriteProblem(w http.ResponseWriter, status int, title, detail string) {
	p := Problem{
		Type:   "about:blank",
		Title:  title,
		Status: status,
		Detail: detail,
	}
	w.Header().Set("Content-Type", ProblemContentType)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(p); err != nil {
		slog.Error("write problem response", "error", err)
	}
}

// problemWriter namespaces the RFC 7807 helper.
type problemWriter struct{}

// Write emits an RFC 7807 problem document.
func (problemWriter) Write(w http.ResponseWriter, status int, title, detail string) {
	WriteProblem(w, status, title, detail)
}

// Problem is the package-level entry point for RFC 7807 errors:
//
//	httpserver.problem.Write(w, http.StatusNotFound, "not found", "no such unit")
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

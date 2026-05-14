// Public API documentation surface.
//
// Two routes:
//
//   GET /api/v1/openapi.yaml  — the hand-curated spec (see docs/openapi.yaml).
//   GET /api/v1/docs/         — Swagger UI loading the spec above.
//
// Both are public — no JWT required — so operators can discover the
// API surface before authenticating. The spec only documents the
// PUBLIC contract; internal endpoints (`/internal/tunnel/k8s/...`,
// chart-repo, WS-only paths) are deliberately omitted so consumers
// don't bind to implementation details.
//
// We embed both files into the server binary at compile time so the
// distributed image carries them — no Cooper-style "we forgot to
// COPY the docs dir into the Dockerfile" deployment surprises.

package handler

import (
	"embed"
	"net/http"
)

//go:embed assets/openapi.yaml
//go:embed assets/swagger-ui.html
var docsFS embed.FS

// DocsHandler serves the OpenAPI spec + Swagger UI.
type DocsHandler struct{}

// NewDocsHandler constructs the handler. No deps — both responses are
// static, embedded into the binary.
func NewDocsHandler() *DocsHandler { return &DocsHandler{} }

// ServeOpenAPI handles GET /api/v1/openapi.yaml.
func (h *DocsHandler) ServeOpenAPI(w http.ResponseWriter, r *http.Request) {
	body, err := docsFS.ReadFile("assets/openapi.yaml")
	if err != nil {
		http.Error(w, "openapi spec not embedded", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	// Cache 5 minutes — the spec is part of the binary so it never
	// drifts mid-process; we still want UA's Reload to fetch fresh
	// after a deploy. ETag would be nicer but the embed.FS doesn't
	// expose stable mtimes.
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(body)
}

// ServeSwaggerUI handles GET /api/v1/docs/ + /api/v1/docs (no trailing slash).
func (h *DocsHandler) ServeSwaggerUI(w http.ResponseWriter, r *http.Request) {
	body, err := docsFS.ReadFile("assets/swagger-ui.html")
	if err != nil {
		http.Error(w, "swagger ui not embedded", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(body)
}

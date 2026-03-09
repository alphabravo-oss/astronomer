package middleware

import (
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// pathResourceMap maps URL path segments to human-readable resource type names,
// matching the Python Django middleware behaviour.
var pathResourceMap = map[string]string{
	"clusters":      "cluster",
	"workloads":     "workload",
	"pods":          "pod",
	"nodes":         "node",
	"namespaces":    "namespace",
	"projects":      "project",
	"users":         "user",
	"global-roles":  "global role",
	"cluster-roles": "cluster role",
	"project-roles": "project role",
	"bindings":      "role binding",
	"argocd":        "ArgoCD",
	"alerting":      "alert",
	"rules":         "alert rule",
	"channels":      "notification channel",
	"silences":      "alert silence",
	"logging":       "logging",
	"outputs":       "log output",
	"pipelines":     "log pipeline",
	"backups":       "backup",
	"schedules":     "backup schedule",
	"storage":       "backup storage",
	"security":      "security",
	"templates":     "security template",
	"policies":      "security policy",
	"scans":         "security scan",
	"catalog":       "catalog",
	"tools":         "tool",
	"repositories":  "Helm repository",
	"charts":        "Helm chart",
	"installed":     "Helm release",
	"sso":           "SSO provider",
	"tokens":        "API token",
	"settings":      "settings",
}

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// mutatingMethods is the set of HTTP methods that trigger audit logging.
var mutatingMethods = map[string]bool{
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

// skipPaths lists API paths that should never be audit-logged.
var skipPaths = map[string]bool{
	"/api/v1/auth/login":        true,
	"/api/v1/auth/refresh":      true,
	"/api/v1/bootstrap/complete": true,
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader captures the status code before delegating to the underlying writer.
func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}

// Write captures an implicit 200 status (the first call to Write sends a 200
// if WriteHeader has not been called) and delegates to the underlying writer.
func (sw *statusWriter) Write(b []byte) (int, error) {
	if sw.status == 0 {
		sw.status = http.StatusOK
	}
	return sw.ResponseWriter.Write(b)
}

// AuditLog returns middleware that logs mutating API requests.
// It only logs POST/PUT/PATCH/DELETE to /api/ paths (excluding skip paths)
// when the response status is < 400.
func AuditLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only log mutating methods.
			if !mutatingMethods[r.Method] {
				next.ServeHTTP(w, r)
				return
			}

			// Only log API paths.
			if !strings.HasPrefix(r.URL.Path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}

			// Skip certain paths.
			if skipPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)

			// Only log successful responses.
			if sw.status >= 400 {
				return
			}

			resourceType, resourceID := parsePathResource(r.URL.Path)

			log.Info("audit",
				"method", r.Method,
				"path", r.URL.Path,
				"resource_type", resourceType,
				"resource_id", resourceID,
				"status", sw.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", GetRequestID(r.Context()),
			)
		})
	}
}

// parsePathResource walks the URL path segments and returns the last matched
// resource type and, if the following segment is a UUID, the resource ID.
func parsePathResource(path string) (string, string) {
	segments := strings.Split(strings.Trim(path, "/"), "/")

	var resourceType, resourceID string
	for i, seg := range segments {
		if rt, ok := pathResourceMap[seg]; ok {
			resourceType = rt
			resourceID = ""
			// Check if the next segment is a UUID resource ID.
			if i+1 < len(segments) && uuidPattern.MatchString(segments[i+1]) {
				resourceID = segments[i+1]
			}
		}
	}
	return resourceType, resourceID
}

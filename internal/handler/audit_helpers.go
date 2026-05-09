package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// auditWriter is the minimal write-side abstraction used by recordAudit. It is
// satisfied by any sqlc-generated *Queries instance (and by every handler-level
// *Querier interface that lifts CreateAuditLog into its method set), which is
// why each handler can simply pass its existing `queries` field to recordAudit
// without a separate dependency wire-up.
//
// We intentionally do NOT reuse handler.AuditQuerier (defined in audit.go) for
// this — that interface is read-side, used by AuditHandler to render the list
// / get / export endpoints. Audit emission and audit reading are independent
// concerns and conflating them would force every handler that needs to emit
// audit rows to also pretend to implement the read APIs.
type auditWriter interface {
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
}

// auditSecretKeys lists the well-known body fields whose values must never
// land in audit_logs.detail. Keys are matched case-insensitively. Keep the
// list short and conservative — over-eager redaction makes audit rows useless
// for debugging.
var auditSecretKeys = map[string]struct{}{
	"password":              {},
	"current_password":      {},
	"new_password":          {},
	"registry_password":     {},
	"token":                 {},
	"auth_token":            {},
	"auth_token_encrypted":  {},
	"client_secret":         {},
	"secret":                {},
	"private_key":           {},
	"ca_bundle":             {},
	"kubeconfig":            {},
	"bind_password":         {},
	"bindpw":                {},
	"access_key":            {},
	"secret_key":            {},
	"aws_secret_access_key": {},
}

// sanitizeDetail returns a copy of detail with well-known secret-bearing keys
// replaced by the literal "[redacted]". Recurses one level into nested maps;
// callers should pre-sanitize anything truly sensitive before calling
// recordAudit.
func sanitizeDetail(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		lk := strings.ToLower(k)
		if _, redact := auditSecretKeys[lk]; redact {
			out[k] = "[redacted]"
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			out[k] = sanitizeDetail(nested)
			continue
		}
		out[k] = v
	}
	return out
}

// remoteIPAddr extracts a parseable IP address from the request, preferring
// X-Forwarded-For when present (load-balancer terminations are the common
// case), then X-Real-IP, then RemoteAddr. Returns nil when no parseable value
// is found — the audit_logs.ip_address column is nullable.
func remoteIPAddr(r *http.Request) *netip.Addr {
	if r == nil {
		return nil
	}
	candidates := []string{}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First entry in the conventional XFF chain is the original client.
		if idx := strings.Index(xff, ","); idx != -1 {
			candidates = append(candidates, strings.TrimSpace(xff[:idx]))
		} else {
			candidates = append(candidates, strings.TrimSpace(xff))
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		candidates = append(candidates, strings.TrimSpace(xri))
	}
	if r.RemoteAddr != "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			candidates = append(candidates, r.RemoteAddr)
		} else {
			candidates = append(candidates, host)
		}
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		addr, err := netip.ParseAddr(c)
		if err != nil {
			continue
		}
		return &addr
	}
	return nil
}

// recordAudit best-effort writes an audit row. It MUST NOT fail the calling
// HTTP request — every error path simply logs a warning. The detail map is
// JSON-encoded into the JSONB column and sanitized for well-known secret
// keys; pass nil to omit detail.
//
// The querier argument is typed `any` so each handler can pass its existing
// concrete *Querier interface field without that interface having to embed
// CreateAuditLog. We type-assert to auditWriter at runtime: the production
// *sqlc.Queries implementation satisfies it, and test fakes that don't
// implement it simply skip the audit write (which keeps the test surface
// small without forcing every fake to grow a stub).
//
// resourceID may be empty (e.g. failed login where no DB row exists);
// resourceName likewise. Action and resourceType are required by convention
// but the helper does not enforce them.
func recordAudit(r *http.Request, q any, action, resourceType, resourceID, resourceName string, detail map[string]any) {
	if r == nil {
		return
	}
	writer, ok := q.(auditWriter)
	if !ok || writer == nil {
		return
	}
	emitAuditRow(r.Context(), r, writer, currentUserUUID(r), action, resourceType, resourceID, resourceName, detail)
}

// recordAuditAs is the variant used when the user_id has to be resolved
// outside of the request context — the canonical case is auth.Login, where
// the user isn't yet authenticated by the middleware so currentUserUUID
// returns zero. Pass pgtype.UUID{} for an anonymous actor (failed login,
// pre-bootstrap).
func recordAuditAs(r *http.Request, q any, userID pgtype.UUID, action, resourceType, resourceID, resourceName string, detail map[string]any) {
	if r == nil {
		return
	}
	writer, ok := q.(auditWriter)
	if !ok || writer == nil {
		return
	}
	emitAuditRow(r.Context(), r, writer, userID, action, resourceType, resourceID, resourceName, detail)
}

func emitAuditRow(ctx context.Context, r *http.Request, q auditWriter, userID pgtype.UUID, action, resourceType, resourceID, resourceName string, detail map[string]any) {
	var raw json.RawMessage
	if detail != nil {
		safe := sanitizeDetail(detail)
		buf, err := json.Marshal(safe)
		if err != nil {
			buf = []byte(`{}`)
		}
		raw = buf
	} else {
		raw = json.RawMessage(`{}`)
	}
	ua := ""
	if r != nil {
		ua = r.UserAgent()
	}
	requestID := middleware.GetRequestID(ctx)
	ip := remoteIPAddr(r)
	if _, err := q.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		UserID:       userID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		ResourceName: resourceName,
		Detail:       raw,
		IpAddress:    ip,
		UserAgent:    ua,
		RequestID:    requestID,
	}); err != nil {
		// Best-effort: never propagate, but make it observable to operators.
		slog.Default().Warn("audit log insert failed",
			"action", action,
			"resource_type", resourceType,
			"resource_id", resourceID,
			"error", err,
		)
	}
}

package handler

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type auditWriterV1 interface {
	CreateAuditLogV1(ctx context.Context, arg sqlc.CreateAuditLogV1Params) error
}

// remoteIPAddr extracts a parseable IP address from the request, preferring
// X-Forwarded-For when present (load-balancer terminations are the common
// case), then X-Real-IP, then RemoteAddr. Returns nil when no parseable value
// is found — the audit ip_address column is nullable.
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
// concrete *Querier interface field without that interface having to embed a
// specific audit write method. The production *sqlc.Queries implementation
// satisfies the v1 interface; narrow test fakes may satisfy it or neither.
//
// resourceID may be empty (e.g. failed login where no DB row exists);
// resourceName likewise. Action and resourceType are required by convention
// but the helper does not enforce them.
func recordAudit(r *http.Request, q any, action, resourceType, resourceID, resourceName string, detail map[string]any) {
	if r == nil {
		return
	}
	if q == nil {
		return
	}
	emitAuditRow(r.Context(), r, q, currentUserUUID(r), action, resourceType, resourceID, resourceName, detail)
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
	if q == nil {
		return
	}
	emitAuditRow(r.Context(), r, q, userID, action, resourceType, resourceID, resourceName, detail)
}

func emitAuditRow(ctx context.Context, r *http.Request, q any, userID pgtype.UUID, action, resourceType, resourceID, resourceName string, detail map[string]any) {
	ua := ""
	if r != nil {
		ua = r.UserAgent()
	}
	requestID := middleware.GetRequestID(ctx)
	correlationID := middleware.GetCorrelationID(ctx)
	ip := remoteIPAddr(r)
	if v1, ok := q.(auditWriterV1); ok && v1 != nil {
		audit.Record(ctx, v1, audit.Event{
			Source:          "service",
			CorrelationID:   correlationID,
			UserID:          userID,
			ActorAuthMethod: authMethodFromRequest(r),
			Action:          action,
			ResourceType:    resourceType,
			ResourceID:      resourceID,
			ResourceName:    resourceName,
			HTTPMethod:      requestMethod(r),
			Path:            requestPath(r),
			StatusCode:      0,
			DurationMs:      0,
			RequestID:       requestID,
			IPAddress:       ip,
			UserAgent:       ua,
			Detail:          detail,
		})
	}
}

func authMethodFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if user, ok := middleware.GetAuthenticatedUser(r.Context()); ok && user != nil {
		return user.AuthMethod
	}
	return ""
}

func requestMethod(r *http.Request) string {
	if r == nil {
		return ""
	}
	return r.Method
}

func requestPath(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	return r.URL.Path
}

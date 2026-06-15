package handler

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type auditWriterV1 interface {
	CreateAuditLogV1(ctx context.Context, arg sqlc.CreateAuditLogV1Params) error
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

// RecordAuditFromRequest is the exported wrapper around recordAudit. It exists
// so callers outside the handler package (e.g. routes.go-level handlers like
// the admin key-status endpoint) can leave audit rows for read-only superuser
// endpoints that the mutating-HTTP audit middleware skips.
func RecordAuditFromRequest(r *http.Request, q any, action, resourceType, resourceID, resourceName string, detail map[string]any) {
	recordAudit(r, q, action, resourceType, resourceID, resourceName, detail)
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
	requestID := middleware.GetRequestID(ctx)
	correlationID := middleware.GetCorrelationID(ctx)
	ip := middleware.RemoteIPAddr(r)
	if v1, ok := q.(auditWriterV1); ok && v1 != nil {
		audit.Record(ctx, v1, audit.NewHTTPRequestEvent(audit.HTTPRequestEvent{
			Request:         r,
			Source:          "service",
			CorrelationID:   correlationID,
			UserID:          userID,
			ActorAuthMethod: authMethodFromRequest(r),
			Action:          action,
			ResourceType:    resourceType,
			ResourceID:      resourceID,
			ResourceName:    resourceName,
			RequestID:       requestID,
			IPAddress:       ip,
			Detail:          detail,
		}))
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

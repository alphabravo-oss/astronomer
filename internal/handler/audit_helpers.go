package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type auditWriterV1 interface {
	CreateAuditLogV1(ctx context.Context, arg sqlc.CreateAuditLogV1Params) error
}

// recordAudit writes mandatory mutations synchronously and leaves only
// explicitly sampled reads eligible for batching. The enclosing mutation
// middleware reserves a content-free audit intent before entering handlers,
// so a database outage fails closed before a side effect can occur. The detail map is
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

// recordMandatoryAudit is the fail-closed variant for sensitive reads and
// downloads. Callers must invoke it before writing any response bytes.
func recordMandatoryAudit(r *http.Request, q any, action, resourceType, resourceID, resourceName string, detail map[string]any) error {
	if r == nil || q == nil {
		return audit.ErrMandatoryPersistenceUnavailable
	}
	v1, ok := q.(auditWriterV1)
	if !ok || v1 == nil {
		return audit.ErrMandatoryPersistenceUnavailable
	}
	return audit.RecordMandatory(r.Context(), v1, audit.NewHTTPRequestEvent(audit.HTTPRequestEvent{
		Request:         r,
		Source:          "service",
		CorrelationID:   middleware.GetCorrelationID(r.Context()),
		UserID:          currentUserUUID(r),
		ActorAuthMethod: authMethodFromRequest(r),
		Action:          action,
		ResourceType:    resourceType,
		ResourceID:      resourceID,
		ResourceName:    resourceName,
		RequestID:       middleware.GetRequestID(r.Context()),
		IPAddress:       middleware.RemoteIPAddr(r),
		Detail:          detail,
	}))
}

// recordMandatoryAuditAs is the identity-aware form used before returning
// login, refresh, and MFA challenge credentials. Those requests have not yet
// populated the normal authenticated-user context, so the actor must be
// supplied from the verified database row.
func recordMandatoryAuditAs(r *http.Request, q any, userID pgtype.UUID, action, resourceType, resourceID, resourceName string, detail map[string]any) error {
	if r == nil || q == nil {
		return audit.ErrMandatoryPersistenceUnavailable
	}
	v1, ok := q.(auditWriterV1)
	if !ok || v1 == nil {
		return audit.ErrMandatoryPersistenceUnavailable
	}
	return audit.RecordMandatory(r.Context(), v1, audit.NewHTTPRequestEvent(audit.HTTPRequestEvent{
		Request: r, Source: "service", CorrelationID: middleware.GetCorrelationID(r.Context()),
		UserID: userID, ActorAuthMethod: authMethodFromRequest(r),
		Action: action, ResourceType: resourceType, ResourceID: resourceID, ResourceName: resourceName,
		RequestID: middleware.GetRequestID(r.Context()), IPAddress: middleware.RemoteIPAddr(r), Detail: detail,
	}))
}

// recordAuditOutbox writes the request-shaped audit-v1 envelope through a
// transaction-bound querier. Callers must invoke it inside the same database
// transaction as their domain mutation.
func recordAuditOutbox(r *http.Request, q audit.OutboxQuerier, action, resourceType, resourceID, resourceName string, status int, detail map[string]any) error {
	return recordAuditOutboxAs(r, q, currentUserUUID(r), action, resourceType, resourceID, resourceName, status, detail)
}

func recordAuditOutboxAs(r *http.Request, q audit.OutboxQuerier, userID pgtype.UUID, action, resourceType, resourceID, resourceName string, status int, detail map[string]any) error {
	if r == nil || q == nil {
		return audit.ErrOutboxUnavailable
	}
	requestID := middleware.GetRequestID(r.Context())
	if requestID == "" {
		requestID = uuid.NewString()
	}
	event := audit.NewHTTPRequestEvent(audit.HTTPRequestEvent{
		Request: r, Source: "service", CorrelationID: middleware.GetCorrelationID(r.Context()),
		UserID: userID, ActorAuthMethod: authMethodFromRequest(r),
		Action: action, ResourceType: resourceType, ResourceID: resourceID,
		ResourceName: resourceName, StatusCode: status, RequestID: requestID,
		IPAddress: middleware.RemoteIPAddr(r), Detail: detail,
	})
	dedupeSeed := requestID
	if values := r.Header.Values("Idempotency-Key"); len(values) == 1 && validOperationIdempotencyKey(values[0]) {
		actor := "anonymous"
		if userID.Valid {
			actor = uuid.UUID(userID.Bytes).String()
		}
		dedupeSeed = audit.MutationDedupeKey(strings.TrimSpace(values[0]), actor, r.Method, r.URL.EscapedPath())
	}
	_, err := audit.RecordOutbox(r.Context(), q, event,
		audit.MutationDedupeKey(dedupeSeed, action, resourceType, resourceID),
		audit.OutboxOptions{})
	if err != nil {
		return fmt.Errorf("%w: %w", audit.ErrOutboxUnavailable, err)
	}
	return nil
}

// respondTransactionalMutationError preserves the handler's ordinary error
// contract while giving audit-intent failures a truthful fail-closed response.
// In particular, a rolled-back DELETE must never be reported as "not found".
func respondTransactionalMutationError(w http.ResponseWriter, r *http.Request, err error, fallbackStatus int, fallbackCode, fallbackMessage string) {
	if errors.Is(err, audit.ErrOutboxUnavailable) {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
			"Mandatory audit storage is unavailable; mutation success was not confirmed")
		return
	}
	RespondRequestError(w, r, fallbackStatus, fallbackCode, fallbackMessage)
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

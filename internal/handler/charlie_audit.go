package handler

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"regexp"

	"github.com/alphabravocompany/astronomer-go/internal/charlie"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/google/uuid"
)

var charlieAuditDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// recordCharlieAdminAudit bypasses the generic HTTP audit envelope so Charlie
// administration never persists paths, query values, IP addresses, user
// agents, resource names, or caller-supplied detail. All detail fields must be
// accepted by the same embedded contract used by runtime lifecycle audits.
func recordCharlieAdminAudit(r *http.Request, writer any, action, resourceType, resourceID string, fields map[string]any) {
	if err := persistCharlieAdminAudit(r, writer, action, resourceType, resourceID, fields, "completed"); err != nil {
		logCharlieAdminAuditFailure(r)
	}
}

// requireCharlieAdminAudit durably records a content-free mutation intent.
// Callers must not perform the associated authority change if this returns an
// error. Emergency disable deliberately remains on the best-effort path.
func requireCharlieAdminAudit(r *http.Request, writer any, action, resourceType, resourceID string, fields map[string]any) error {
	err := persistCharlieAdminAudit(r, writer, action, resourceType, resourceID, fields, "authorized")
	if err != nil {
		logCharlieAdminAuditFailure(r)
	}
	return err
}

func persistCharlieAdminAudit(r *http.Request, writer any, action, resourceType, resourceID string, fields map[string]any, outcome string) error {
	if r == nil || writer == nil {
		return fmt.Errorf("Charlie administrator audit is unavailable")
	}
	w, ok := writer.(auditWriterV1)
	if !ok || w == nil {
		return fmt.Errorf("Charlie administrator audit is unavailable")
	}
	detailFields := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		detailFields[key] = value
	}
	detailFields["outcome_code"] = outcome
	var err error
	if !validCharlieAuditResourceID(resourceID) {
		err = fmt.Errorf("Charlie administrator audit resource is invalid")
	}
	var detail []byte
	if err == nil {
		detail, err = charlie.EncodeCharlieAuditDetail(action, resourceType, detailFields)
	}
	if err == nil {
		err = w.CreateAuditLogV1(r.Context(), sqlc.CreateAuditLogV1Params{
			Source: "service", CorrelationID: appmiddleware.GetCorrelationID(r.Context()), UserID: currentUserUUID(r),
			ActorAuthMethod: authMethodFromRequest(r), Action: action, ResourceType: resourceType, ResourceID: resourceID,
			StatusCode: http.StatusOK, RequestID: appmiddleware.GetRequestID(r.Context()), Detail: detail, ActionClass: "mutation",
		})
	}
	return err
}

func logCharlieAdminAuditFailure(r *http.Request) {
	ctx := context.Background()
	if r != nil {
		ctx = r.Context()
	}
	charlie.LogOperationalFailure(ctx, nil, "charlie.admin_audit_persist_failed", "")
}

func validCharlieAuditResourceID(value string) bool {
	if value == "current" || value == "automation_identity" || value == "feature.charlie" || charlieAuditDigestPattern.MatchString(value) {
		return true
	}
	_, err := uuid.Parse(value)
	return err == nil
}

func charlieAuditOpaque(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

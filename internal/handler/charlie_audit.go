package handler

import (
	"crypto/sha256"
	"fmt"
	"log/slog"
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
	if r == nil || writer == nil {
		return
	}
	w, ok := writer.(auditWriterV1)
	if !ok || w == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["outcome_code"] = "completed"
	var err error
	if !validCharlieAuditResourceID(resourceID) {
		err = fmt.Errorf("Charlie administrator audit resource is invalid")
	}
	var detail []byte
	if err == nil {
		detail, err = charlie.EncodeCharlieAuditDetail(action, resourceType, fields)
	}
	if err == nil {
		err = w.CreateAuditLogV1(r.Context(), sqlc.CreateAuditLogV1Params{
			Source: "service", CorrelationID: appmiddleware.GetCorrelationID(r.Context()), UserID: currentUserUUID(r),
			ActorAuthMethod: authMethodFromRequest(r), Action: action, ResourceType: resourceType, ResourceID: resourceID,
			StatusCode: http.StatusOK, RequestID: appmiddleware.GetRequestID(r.Context()), Detail: detail, ActionClass: "mutation",
		})
	}
	if err != nil {
		slog.WarnContext(r.Context(), "Charlie administrator audit persistence failed", slog.String("failure_code", "charlie.admin_audit_persist_failed"))
	}
}

func validCharlieAuditResourceID(value string) bool {
	if value == "current" || value == "automation_identity" || charlieAuditDigestPattern.MatchString(value) {
		return true
	}
	_, err := uuid.Parse(value)
	return err == nil
}

func charlieAuditOpaque(value string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

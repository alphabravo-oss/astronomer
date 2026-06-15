package tunnel

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

type auditWriterV1 interface {
	CreateAuditLogV1(ctx context.Context, arg sqlc.CreateAuditLogV1Params) error
}

func recordStreamOpenAudit(r *http.Request, writer any, userID uuid.UUID, action, clusterID, namespace, pod, container string) {
	if r == nil || writer == nil {
		return
	}
	v1, ok := writer.(auditWriterV1)
	if !ok || v1 == nil {
		return
	}
	podRef := namespace + "/" + pod
	if container != "" {
		podRef += "/" + container
	}
	detail := map[string]any{
		"cluster_id":  clusterID,
		"namespace":   namespace,
		"pod":         pod,
		"container":   container,
		"stream_kind": streamKindFromAction(action),
	}
	audit.Record(r.Context(), v1, audit.NewHTTPRequestEvent(audit.HTTPRequestEvent{
		Request:         r,
		Source:          "service",
		CorrelationID:   middleware.GetCorrelationID(r.Context()),
		UserID:          audit.UserIDFromUUID(userID),
		ActorAuthMethod: streamAuthMethod(r),
		Action:          action,
		ResourceType:    "cluster",
		ResourceID:      clusterID,
		ResourceName:    podRef,
		RequestID:       middleware.GetRequestID(r.Context()),
		IPAddress:       middleware.RemoteIPAddr(r),
		Detail:          detail,
	}))
}

func streamKindFromAction(action string) string {
	switch action {
	case "pod.exec.opened":
		return "exec"
	case "pod.logs.opened":
		return "logs"
	default:
		return ""
	}
}

func streamAuthMethod(r *http.Request) string {
	if r == nil {
		return ""
	}
	if strings.TrimSpace(r.URL.Query().Get("ticket")) != "" {
		return "stream_ticket"
	}
	token := strings.TrimSpace(r.Header.Get("Authorization"))
	if token == "" {
		return ""
	}
	parts := strings.SplitN(token, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	if strings.HasPrefix(parts[1], "astro_") {
		return "api_token"
	}
	return "jwt"
}

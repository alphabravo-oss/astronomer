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

// recordForwardedK8sMutationAudit emits the cluster.k8s_proxy.forwarded
// audit row for a mutating k8s request that crossed the internal cross-pod
// door (internal_k8s.go). userID is the originating end user the calling
// pod threaded through InternalForwardedUserHeader; uuid.Nil yields an
// anonymous (NULL user) row rather than dropping the audit. This matches
// the action + resource shape the user-facing proxy's
// auditK8sProxyMutations middleware records, so a mutation forwarded
// across a pod boundary is indistinguishable in the audit trail from one
// handled locally.
func recordForwardedK8sMutationAudit(r *http.Request, writer any, userID uuid.UUID, clusterID, method, k8sPath string) {
	v1, ok := writer.(auditWriterV1)
	if !ok || v1 == nil {
		return
	}
	ctx := context.Background()
	if r != nil {
		ctx = r.Context()
	}
	detail := map[string]any{
		"method":   method,
		"k8s_path": k8sPath,
		"proxy":    "internal_door",
	}
	audit.Record(ctx, v1, audit.NewHTTPRequestEvent(audit.HTTPRequestEvent{
		Request:         r,
		Source:          "service",
		CorrelationID:   middleware.GetCorrelationID(ctx),
		UserID:          audit.UserIDFromUUID(userID),
		ActorAuthMethod: "internal_forward",
		Action:          "cluster.k8s_proxy.forwarded",
		ResourceType:    "cluster",
		ResourceID:      clusterID,
		ResourceName:    k8sPath,
		RequestID:       middleware.GetRequestID(ctx),
		IPAddress:       middleware.RemoteIPAddr(r),
		Detail:          detail,
	}))
}

// recordForwardedHelmMutationAudit emits the cluster.helm_proxy.forwarded
// audit row for a mutating helm op (install/upgrade/uninstall/rollback)
// that crossed the internal cross-pod door (internal_helm.go), attributed
// to the originating end user. HELM_STATUS is a read and is not audited
// here. uuid.Nil yields an anonymous row rather than dropping the audit.
func recordForwardedHelmMutationAudit(r *http.Request, writer any, userID uuid.UUID, clusterID, op, releaseName, namespace string) {
	v1, ok := writer.(auditWriterV1)
	if !ok || v1 == nil {
		return
	}
	ctx := context.Background()
	if r != nil {
		ctx = r.Context()
	}
	resourceName := releaseName
	if namespace != "" {
		resourceName = namespace + "/" + releaseName
	}
	detail := map[string]any{
		"op":           op,
		"release_name": releaseName,
		"namespace":    namespace,
		"proxy":        "internal_door",
	}
	audit.Record(ctx, v1, audit.NewHTTPRequestEvent(audit.HTTPRequestEvent{
		Request:         r,
		Source:          "service",
		CorrelationID:   middleware.GetCorrelationID(ctx),
		UserID:          audit.UserIDFromUUID(userID),
		ActorAuthMethod: "internal_forward",
		Action:          "cluster.helm_proxy.forwarded",
		ResourceType:    "cluster",
		ResourceID:      clusterID,
		ResourceName:    resourceName,
		RequestID:       middleware.GetRequestID(ctx),
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

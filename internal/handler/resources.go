package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type ResourceHandler struct {
	requester K8sRequester
	queries   ResourceQuerier
}

type ResourceQuerier interface {
	GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)
	UpsertPlatformConfig(ctx context.Context, arg sqlc.UpsertPlatformConfigParams) (sqlc.PlatformConfiguration, error)
	ListAuditLogs(ctx context.Context, arg sqlc.ListAuditLogsParams) ([]sqlc.AuditLog, error)
	CountAuditLogs(ctx context.Context) (int64, error)
	ListUsers(ctx context.Context, arg sqlc.ListUsersParams) ([]sqlc.User, error)
	CountUsers(ctx context.Context) (int64, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	GetUserByEmail(ctx context.Context, email string) (sqlc.User, error)
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	CreateUser(ctx context.Context, arg sqlc.CreateUserParams) (sqlc.User, error)
	UpdateUser(ctx context.Context, arg sqlc.UpdateUserParams) (sqlc.User, error)
	DeleteUser(ctx context.Context, id uuid.UUID) error
	UpdateUserPassword(ctx context.Context, arg sqlc.UpdateUserPasswordParams) error
}

type resourceDef struct {
	apiBase    string
	namespaced bool
	plural     string
}

var resourceDefs = map[string]resourceDef{
	"services":                {apiBase: "/api/v1", namespaced: true, plural: "services"},
	"configmaps":              {apiBase: "/api/v1", namespaced: true, plural: "configmaps"},
	"secrets":                 {apiBase: "/api/v1", namespaced: true, plural: "secrets"},
	"serviceaccounts":         {apiBase: "/api/v1", namespaced: true, plural: "serviceaccounts"},
	"endpoints":               {apiBase: "/api/v1", namespaced: true, plural: "endpoints"},
	"persistentvolumes":       {apiBase: "/api/v1", namespaced: false, plural: "persistentvolumes"},
	"persistentvolumeclaims":  {apiBase: "/api/v1", namespaced: true, plural: "persistentvolumeclaims"},
	"ingresses":               {apiBase: "/apis/networking.k8s.io/v1", namespaced: true, plural: "ingresses"},
	"networkpolicies":         {apiBase: "/apis/networking.k8s.io/v1", namespaced: true, plural: "networkpolicies"},
	"storageclasses":          {apiBase: "/apis/storage.k8s.io/v1", namespaced: false, plural: "storageclasses"},
	"replicasets":             {apiBase: "/apis/apps/v1", namespaced: true, plural: "replicasets"},
	"jobs":                    {apiBase: "/apis/batch/v1", namespaced: true, plural: "jobs"},
	"cronjobs":                {apiBase: "/apis/batch/v1", namespaced: true, plural: "cronjobs"},
	"hpa":                     {apiBase: "/apis/autoscaling/v2", namespaced: true, plural: "horizontalpodautoscalers"},
	"resourcequotas":          {apiBase: "/api/v1", namespaced: true, plural: "resourcequotas"},
	"limitranges":             {apiBase: "/api/v1", namespaced: true, plural: "limitranges"},
	"poddisruptionbudgets":    {apiBase: "/apis/policy/v1", namespaced: true, plural: "poddisruptionbudgets"},
	"k8s-clusterroles":        {apiBase: "/apis/rbac.authorization.k8s.io/v1", namespaced: false, plural: "clusterroles"},
	"k8s-clusterrolebindings": {apiBase: "/apis/rbac.authorization.k8s.io/v1", namespaced: false, plural: "clusterrolebindings"},
	"k8s-roles":               {apiBase: "/apis/rbac.authorization.k8s.io/v1", namespaced: true, plural: "roles"},
	"k8s-rolebindings":        {apiBase: "/apis/rbac.authorization.k8s.io/v1", namespaced: true, plural: "rolebindings"},
	"crds":                    {apiBase: "/apis/apiextensions.k8s.io/v1", namespaced: false, plural: "customresourcedefinitions"},
}

func NewResourceHandler() *ResourceHandler {
	return &ResourceHandler{}
}

func NewResourceHandlerWithRequester(requester K8sRequester) *ResourceHandler {
	return &ResourceHandler{requester: requester}
}

func NewResourceHandlerWithQueries(queries ResourceQuerier, requester K8sRequester) *ResourceHandler {
	return &ResourceHandler{queries: queries, requester: requester}
}

func (h *ResourceHandler) ListResources(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	group := chi.URLParam(r, "group")
	version := chi.URLParam(r, "version")
	kind := chi.URLParam(r, "kind")

	var path string
	if group == "core" {
		path = fmt.Sprintf("/api/%s/%s", version, kind)
	} else {
		path = fmt.Sprintf("/apis/%s/%s/%s", group, version, kind)
	}
	h.proxyJSON(w, r, clusterID, http.MethodGet, path, nil, requestHeaders(""))
}

func (h *ResourceHandler) ListNamedResources(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	resourceType := chi.URLParam(r, "resource_type")
	namespace := r.URL.Query().Get("namespace")
	path, err := listPath(resourceType, namespace)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_resource", err.Error())
		return
	}
	resp, err := h.do(r.Context(), clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		RespondError(w, http.StatusServiceUnavailable, "proxy_error", err.Error())
		return
	}
	items := flattenNamedResources(clusterID, resourceType, resp)
	RespondJSON(w, http.StatusOK, items)
}

func (h *ResourceHandler) ListGenericResources(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	resourceType := chi.URLParam(r, "resource_type")
	namespace := r.URL.Query().Get("namespace")
	path, err := listPath(resourceType, namespace)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_resource", err.Error())
		return
	}
	resp, err := h.do(r.Context(), clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		RespondError(w, http.StatusServiceUnavailable, "proxy_error", err.Error())
		return
	}
	items := flattenGenericResources(clusterID, resourceType, resp)
	RespondJSON(w, http.StatusOK, items)
}

func (h *ResourceHandler) CreateNamedResource(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	resourceType := chi.URLParam(r, "resource_type")
	body, err := io.ReadAll(r.Body)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Failed to read request body")
		return
	}
	namespace, err := resourceNamespace(body)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Unable to determine namespace")
		return
	}
	path, err := listPath(resourceType, namespace)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_resource", err.Error())
		return
	}
	h.proxyJSON(w, r, clusterID, http.MethodPost, path, body, requestHeaders("application/json"))
}

func (h *ResourceHandler) DeleteNamedResource(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	resourceType := chi.URLParam(r, "resource_type")
	name := chi.URLParam(r, "name")
	namespace := chi.URLParam(r, "namespace")
	path, err := resourcePath(resourceType, name, namespace)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_resource", err.Error())
		return
	}
	h.proxyJSON(w, r, clusterID, http.MethodDelete, path, nil, requestHeaders(""))
}

func (h *ResourceHandler) GetGeneralSettings(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondJSON(w, http.StatusOK, map[string]any{
			"platformName":           "Astronomer",
			"agentHeartbeatInterval": 30,
			"defaultSessionTimeout":  60,
			"enableAuditLogging":     true,
			"metricsCollection":      true,
		})
		return
	}
	cfg, err := h.queries.GetPlatformConfig(r.Context())
	if err != nil && err != pgx.ErrNoRows {
		RespondError(w, http.StatusInternalServerError, "settings_error", "Failed to load platform settings")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"platformName":           defaultString(cfg.PlatformName, "Astronomer"),
		"agentHeartbeatInterval": 30,
		"defaultSessionTimeout":  60,
		"enableAuditLogging":     true,
		"metricsCollection":      true,
	})
}

// UpdateGeneralSettings handles PUT /api/v1/settings/general/.
//
// Persists the editable platform-level settings to the singleton
// platform_configuration row. Fields not present in the body keep their
// previous value (read from GetPlatformConfig). The frontend currently
// edits platformName + telemetry/heartbeat-style toggles; only platformName
// has a backing column today, the others are echoed back unchanged.
type UpdateGeneralSettingsRequest struct {
	PlatformName           *string `json:"platformName"`
	PlatformNameSnake      *string `json:"platform_name"`
	AgentHeartbeatInterval *int    `json:"agentHeartbeatInterval"`
	DefaultSessionTimeout  *int    `json:"defaultSessionTimeout"`
	EnableAuditLogging     *bool   `json:"enableAuditLogging"`
	MetricsCollection      *bool   `json:"metricsCollection"`
}

func (h *ResourceHandler) UpdateGeneralSettings(w http.ResponseWriter, r *http.Request) {
	var req UpdateGeneralSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}

	platformName := "Astronomer"
	serverURL := ""
	telemetry := true
	var bootstrappedAt pgtype.Timestamptz

	if h.queries != nil {
		if cfg, err := h.queries.GetPlatformConfig(r.Context()); err == nil {
			platformName = cfg.PlatformName
			serverURL = cfg.ServerUrl
			telemetry = cfg.TelemetryEnabled
			bootstrappedAt = cfg.BootstrappedAt
		}
	}

	if req.PlatformName != nil && *req.PlatformName != "" {
		platformName = *req.PlatformName
	} else if req.PlatformNameSnake != nil && *req.PlatformNameSnake != "" {
		platformName = *req.PlatformNameSnake
	}
	if req.MetricsCollection != nil {
		// Reuse the telemetry column for the "metrics collection" toggle —
		// they're the same opt-in in the Python implementation.
		telemetry = *req.MetricsCollection
	}

	if h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "settings_error", "settings store not configured")
		return
	}
	cfg, err := h.queries.UpsertPlatformConfig(r.Context(), sqlc.UpsertPlatformConfigParams{
		ServerUrl:        serverURL,
		PlatformName:     platformName,
		TelemetryEnabled: telemetry,
		BootstrappedAt:   bootstrappedAt,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "settings_error", "Failed to update platform settings")
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"platformName":           defaultString(cfg.PlatformName, "Astronomer"),
		"agentHeartbeatInterval": intOrDefault(req.AgentHeartbeatInterval, 30),
		"defaultSessionTimeout":  intOrDefault(req.DefaultSessionTimeout, 60),
		"enableAuditLogging":     boolOrDefault(req.EnableAuditLogging, true),
		"metricsCollection":      cfg.TelemetryEnabled,
	})
}

func intOrDefault(p *int, d int) int {
	if p == nil {
		return d
	}
	return *p
}

func boolOrDefault(p *bool, d bool) bool {
	if p == nil {
		return d
	}
	return *p
}

func (h *ResourceHandler) ListSSOProviders(w http.ResponseWriter, r *http.Request) {
	RespondJSON(w, http.StatusOK, []any{})
}

// CreateSSOProvider handles POST /api/v1/settings/sso/.
//
// SSO provider storage is out of scope for the Go server — providers are
// configured today via env vars and surfaced to the frontend by SSO.Login.
// We accept the body, log it, and echo it back so the frontend's "Add SSO
// provider" form doesn't 405. A persistent SSO provider table is tracked
// for follow-up.
func (h *ResourceHandler) CreateSSOProvider(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Invalid JSON body")
		return
	}
	if body == nil {
		body = map[string]any{}
	}
	body["id"] = uuid.New().String()
	if _, ok := body["enabled"]; !ok {
		body["enabled"] = false
	}
	RespondJSON(w, http.StatusCreated, body)
}

func (h *ResourceHandler) ListActivity(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondJSON(w, http.StatusOK, []any{})
		return
	}
	logs, err := h.queries.ListAuditLogs(r.Context(), sqlc.ListAuditLogsParams{
		Limit:  int32(queryInt(r, "limit", 20)),
		Offset: 0,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "activity_error", "Failed to load activity feed")
		return
	}
	items := make([]map[string]any, 0, len(logs))
	for _, item := range logs {
		items = append(items, map[string]any{
			"id":        item.ID.String(),
			"type":      "system",
			"action":    item.Action,
			"message":   strings.TrimSpace(item.Action + " " + item.ResourceName),
			"resource":  item.ResourceName,
			"timestamp": item.CreatedAt.UTC().Format(timeLayout),
		})
	}
	RespondJSON(w, http.StatusOK, items)
}

func (h *ResourceHandler) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondPaginated(w, r, []any{}, 0)
		return
	}
	logs, err := h.queries.ListAuditLogs(r.Context(), sqlc.ListAuditLogsParams{
		Limit:  int32(queryInt(r, "limit", 20)),
		Offset: int32(queryInt(r, "offset", 0)),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "audit_error", "Failed to load audit logs")
		return
	}
	total, _ := h.queries.CountAuditLogs(r.Context())
	items := make([]map[string]any, 0, len(logs))
	for _, item := range logs {
		items = append(items, map[string]any{
			"id":           item.ID.String(),
			"action":       item.Action,
			"resourceType": item.ResourceType,
			"resourceName": item.ResourceName,
			"user":         nullableUserID(item.UserID),
			"userAgent":    item.UserAgent,
			"sourceIP":     "",
			"status":       "success",
			"details":      item.Detail,
			"timestamp":    item.CreatedAt.UTC().Format(timeLayout),
		})
	}
	RespondPaginated(w, r, items, total)
}

func (h *ResourceHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondPaginated(w, r, []any{}, 0)
		return
	}
	users, err := h.queries.ListUsers(r.Context(), sqlc.ListUsersParams{
		Limit:  int32(queryInt(r, "limit", 20)),
		Offset: int32(queryInt(r, "offset", 0)),
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "users_error", "Failed to load users")
		return
	}
	total, _ := h.queries.CountUsers(r.Context())
	items := make([]map[string]any, 0, len(users))
	for _, user := range users {
		items = append(items, mapUser(user))
	}
	RespondPaginated(w, r, items, total)
}

func (h *ResourceHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondError(w, http.StatusServiceUnavailable, "users_error", "user store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_id", "Invalid user ID")
		return
	}
	user, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusNotFound, "not_found", "User not found")
		return
	}
	RespondJSON(w, http.StatusOK, mapUser(user))
}

// CordonNode handles POST /api/v1/nodes/{cluster_id}/{node_name}/cordon/.
// Patches the node to set spec.unschedulable=true.
func (h *ResourceHandler) CordonNode(w http.ResponseWriter, r *http.Request) {
	h.setNodeSchedulable(w, r, true)
}

// UncordonNode handles POST /api/v1/nodes/{cluster_id}/{node_name}/uncordon/.
// Patches the node to set spec.unschedulable=false.
func (h *ResourceHandler) UncordonNode(w http.ResponseWriter, r *http.Request) {
	h.setNodeSchedulable(w, r, false)
}

func (h *ResourceHandler) setNodeSchedulable(w http.ResponseWriter, r *http.Request, unschedulable bool) {
	clusterID := chi.URLParam(r, "cluster_id")
	nodeName := chi.URLParam(r, "node_name")
	if clusterID == "" || nodeName == "" {
		RespondError(w, http.StatusBadRequest, "invalid_request", "cluster_id and node_name are required")
		return
	}
	patch, err := json.Marshal(map[string]any{
		"spec": map[string]any{"unschedulable": unschedulable},
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "marshal_error", err.Error())
		return
	}
	path := fmt.Sprintf("/api/v1/nodes/%s", nodeName)
	headers := requestHeaders("application/strategic-merge-patch+json")
	if h.requester == nil {
		RespondError(w, http.StatusNotImplemented, "not_implemented", "Cluster tunnel requester not configured; cordon/uncordon requires agent connection")
		return
	}
	resp, err := h.requester.Do(r.Context(), clusterID, http.MethodPatch, path, patch, headers)
	if err != nil {
		RespondError(w, http.StatusServiceUnavailable, "proxy_error", err.Error())
		return
	}
	if err := ensureSuccess(resp); err != nil {
		RespondError(w, http.StatusBadGateway, "k8s_error", err.Error())
		return
	}
	action := "uncordoned"
	if unschedulable {
		action = "cordoned"
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"node":   nodeName,
		"status": action,
	})
}

// DrainNode handles POST /api/v1/nodes/{cluster_id}/{node_name}/drain/.
// Cordons the node, then evicts all pods. The eviction loop is non-trivial,
// so for now we cordon and return 501 indicating the eviction stage is not
// yet implemented through the agent tunnel.
func (h *ResourceHandler) DrainNode(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	nodeName := chi.URLParam(r, "node_name")
	if clusterID == "" || nodeName == "" {
		RespondError(w, http.StatusBadRequest, "invalid_request", "cluster_id and node_name are required")
		return
	}
	if h.requester == nil {
		RespondError(w, http.StatusNotImplemented, "not_implemented", "Cluster tunnel requester not configured; drain requires agent connection")
		return
	}
	// Cordon first.
	patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"unschedulable": true}})
	resp, err := h.requester.Do(r.Context(), clusterID, http.MethodPatch, fmt.Sprintf("/api/v1/nodes/%s", nodeName), patch, requestHeaders("application/strategic-merge-patch+json"))
	if err != nil {
		RespondError(w, http.StatusServiceUnavailable, "proxy_error", err.Error())
		return
	}
	if err := ensureSuccess(resp); err != nil {
		RespondError(w, http.StatusBadGateway, "k8s_error", err.Error())
		return
	}
	// TODO: evict pods on the node. Requires iterating pods on the node and
	// posting eviction requests to /api/v1/namespaces/{ns}/pods/{name}/eviction.
	// Returning 202 Accepted with a clear message is preferable to 501 here so
	// the UI shows the cordon succeeded.
	RespondJSON(w, http.StatusAccepted, map[string]any{
		"node":    nodeName,
		"status":  "cordoned",
		"message": "Node cordoned. Pod eviction is not yet implemented; pods must be drained manually.",
	})
}

// GetNamedResource handles GET /api/v1/resources/{cluster_id}/{type}/{namespace}/{name}/.
func (h *ResourceHandler) GetNamedResource(w http.ResponseWriter, r *http.Request) {
	h.namedResourceRequest(w, r, http.MethodGet, nil)
}

// UpdateNamedResource handles PUT /api/v1/resources/{cluster_id}/{type}/{namespace}/{name}/.
func (h *ResourceHandler) UpdateNamedResource(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_body", "Failed to read request body")
		return
	}
	h.namedResourceRequest(w, r, http.MethodPut, body)
}

// DeleteNamedResourceREST handles DELETE /api/v1/resources/{cluster_id}/{type}/{namespace}/{name}/.
func (h *ResourceHandler) DeleteNamedResourceREST(w http.ResponseWriter, r *http.Request) {
	h.namedResourceRequest(w, r, http.MethodDelete, nil)
}

func (h *ResourceHandler) namedResourceRequest(w http.ResponseWriter, r *http.Request, method string, body []byte) {
	clusterID := chi.URLParam(r, "cluster_id")
	resourceType := chi.URLParam(r, "type")
	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if clusterID == "" || resourceType == "" || name == "" {
		RespondError(w, http.StatusBadRequest, "invalid_request", "cluster_id, type and name are required")
		return
	}
	path, err := resourcePath(resourceType, name, namespace)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "invalid_resource", err.Error())
		return
	}
	headers := requestHeaders("")
	if method == http.MethodPut {
		headers = requestHeaders("application/json")
	}
	h.proxyJSON(w, r, clusterID, method, path, body, headers)
}

func (h *ResourceHandler) do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (map[string]any, error) {
	if h.requester == nil {
		return nil, fmt.Errorf("tunnel requester not configured")
	}
	resp, err := h.requester.Do(ctx, clusterID, method, path, body, headers)
	if err != nil {
		return nil, err
	}
	if err := ensureSuccess(resp); err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := parseJSONResponse(resp, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (h *ResourceHandler) proxyJSON(w http.ResponseWriter, r *http.Request, clusterID, method, path string, body []byte, headers map[string]string) {
	resp, err := h.do(r.Context(), clusterID, method, path, body, headers)
	if err != nil {
		RespondError(w, http.StatusServiceUnavailable, "proxy_error", err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, resp)
}

func listPath(resourceType, namespace string) (string, error) {
	def, ok := resourceDefs[resourceType]
	if !ok {
		return "", fmt.Errorf("unsupported resource type %q", resourceType)
	}
	if def.namespaced && namespace != "" {
		return fmt.Sprintf("%s/namespaces/%s/%s", def.apiBase, namespace, def.plural), nil
	}
	return fmt.Sprintf("%s/%s", def.apiBase, def.plural), nil
}

func resourcePath(resourceType, name, namespace string) (string, error) {
	def, ok := resourceDefs[resourceType]
	if !ok {
		return "", fmt.Errorf("unsupported resource type %q", resourceType)
	}
	if def.namespaced {
		if namespace == "" {
			return "", fmt.Errorf("namespace is required for %s", resourceType)
		}
		return fmt.Sprintf("%s/namespaces/%s/%s/%s", def.apiBase, namespace, def.plural, name), nil
	}
	return fmt.Sprintf("%s/%s/%s", def.apiBase, def.plural, name), nil
}

func resourceNamespace(body []byte) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	metadata, _ := payload["metadata"].(map[string]any)
	if metadata == nil {
		return "", nil
	}
	ns, _ := metadata["namespace"].(string)
	return ns, nil
}

func flattenNamedResources(clusterID, resourceType string, payload map[string]any) []map[string]any {
	items := objectItems(payload)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		switch resourceType {
		case "services":
			out = append(out, flattenService(clusterID, item))
		case "ingresses":
			out = append(out, flattenIngress(clusterID, item))
		case "networkpolicies":
			out = append(out, flattenNetworkPolicy(clusterID, item))
		case "persistentvolumes":
			out = append(out, flattenPV(clusterID, item))
		case "persistentvolumeclaims":
			out = append(out, flattenPVC(clusterID, item))
		case "storageclasses":
			out = append(out, flattenStorageClass(clusterID, item))
		default:
			out = append(out, flattenGeneric(clusterID, item))
		}
	}
	return out
}

func flattenGenericResources(clusterID, resourceType string, payload map[string]any) []map[string]any {
	items := objectItems(payload)
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		entry := flattenGeneric(clusterID, item)
		switch resourceType {
		case "configmaps", "secrets":
			if data, ok := nestedMap(item, "data"); ok {
				entry["dataCount"] = len(data)
			}
			if resourceType == "secrets" {
				entry["type"] = stringValue(item, "type")
			}
		case "jobs":
			entry["completions"] = intValue(item, "status", "succeeded")
			entry["succeeded"] = intValue(item, "status", "succeeded")
			entry["failed"] = intValue(item, "status", "failed")
			entry["active"] = intValue(item, "status", "active")
			entry["status"] = defaultString(stringValue(item, "status", "conditions", "0", "type"), "Unknown")
		case "cronjobs":
			entry["schedule"] = stringValue(item, "spec", "schedule")
			entry["suspend"] = boolValue(item, "spec", "suspend")
			entry["activeCount"] = sliceLen(item, "status", "active")
		case "hpa":
			entry["minReplicas"] = intValue(item, "spec", "minReplicas")
			entry["maxReplicas"] = intValue(item, "spec", "maxReplicas")
			entry["currentReplicas"] = intValue(item, "status", "currentReplicas")
			entry["desiredReplicas"] = intValue(item, "status", "desiredReplicas")
			entry["targetKind"] = stringValue(item, "spec", "scaleTargetRef", "kind")
			entry["targetName"] = stringValue(item, "spec", "scaleTargetRef", "name")
		case "resourcequotas":
			entry["hard"] = nestedStringMap(item, "status", "hard")
			entry["used"] = nestedStringMap(item, "status", "used")
		case "limitranges":
			if limits, ok := nestedSlice(item, "spec", "limits"); ok {
				entry["limits"] = limits
			}
		case "poddisruptionbudgets":
			entry["minAvailable"] = stringValue(item, "spec", "minAvailable")
			entry["maxUnavailable"] = stringValue(item, "spec", "maxUnavailable")
			entry["currentHealthy"] = intValue(item, "status", "currentHealthy")
			entry["desiredHealthy"] = intValue(item, "status", "desiredHealthy")
		case "crds":
			entry["group"] = stringValue(item, "spec", "group")
			entry["kind"] = stringValue(item, "spec", "names", "kind")
			entry["scope"] = stringValue(item, "spec", "scope")
			entry["version"] = crdVersion(item)
		case "serviceaccounts":
			entry["secretsCount"] = sliceLen(item, "secrets")
		case "k8s-clusterroles", "k8s-roles":
			entry["rulesCount"] = sliceLen(item, "rules")
		case "k8s-clusterrolebindings", "k8s-rolebindings":
			entry["roleKind"] = stringValue(item, "roleRef", "kind")
			entry["roleName"] = stringValue(item, "roleRef", "name")
			entry["subjectsCount"] = sliceLen(item, "subjects")
		case "endpoints":
			entry["addressesCount"] = endpointAddressCount(item)
			entry["ports"] = endpointPorts(item)
		case "replicasets":
			entry["desired"] = intValue(item, "spec", "replicas")
			entry["ready"] = intValue(item, "status", "readyReplicas")
			entry["available"] = intValue(item, "status", "availableReplicas")
		}
		out = append(out, entry)
	}
	return out
}

func crdVersion(item map[string]any) string {
	if version := stringValue(item, "spec", "version"); version != "" {
		return version
	}
	versions, ok := nestedSlice(item, "spec", "versions")
	if !ok {
		return ""
	}
	for _, raw := range versions {
		version, _ := raw.(map[string]any)
		if boolValue(version, "storage") {
			return stringValueMap(version, "name")
		}
	}
	if len(versions) > 0 {
		if version, ok := versions[0].(map[string]any); ok {
			return stringValueMap(version, "name")
		}
	}
	return ""
}

func endpointAddressCount(item map[string]any) int {
	subsets, ok := nestedSlice(item, "subsets")
	if !ok {
		return 0
	}
	total := 0
	for _, raw := range subsets {
		subset, _ := raw.(map[string]any)
		total += sliceLen(subset, "addresses")
		total += sliceLen(subset, "notReadyAddresses")
	}
	return total
}

func endpointPorts(item map[string]any) string {
	subsets, ok := nestedSlice(item, "subsets")
	if !ok {
		return ""
	}
	ports := make([]string, 0)
	for _, raw := range subsets {
		subset, _ := raw.(map[string]any)
		items, _ := subset["ports"].([]any)
		for _, portRaw := range items {
			port, _ := portRaw.(map[string]any)
			number := fmt.Sprint(anyValue(port, "port"))
			if protocol := stringValueMap(port, "protocol"); protocol != "" {
				ports = append(ports, number+"/"+protocol)
				continue
			}
			ports = append(ports, number)
		}
	}
	return strings.Join(ports, ", ")
}

func flattenService(clusterID string, item map[string]any) map[string]any {
	return map[string]any{
		"name":        stringValue(item, "metadata", "name"),
		"namespace":   stringValue(item, "metadata", "namespace"),
		"clusterId":   clusterID,
		"clusterName": "",
		"type":        stringValue(item, "spec", "type"),
		"clusterIP":   stringValue(item, "spec", "clusterIP"),
		"externalIP":  firstString(item, "status", "loadBalancer", "ingress", "ip"),
		"ports":       nestedSliceOrEmpty(item, "spec", "ports"),
		"selector":    nestedStringMap(item, "spec", "selector"),
		"createdAt":   stringValue(item, "metadata", "creationTimestamp"),
	}
}

func flattenIngress(clusterID string, item map[string]any) map[string]any {
	hosts := []string{}
	paths := []map[string]any{}
	if rules, ok := nestedSlice(item, "spec", "rules"); ok {
		for _, raw := range rules {
			rule, _ := raw.(map[string]any)
			host, _ := rule["host"].(string)
			if host != "" {
				hosts = append(hosts, host)
			}
			httpRule, _ := rule["http"].(map[string]any)
			httpPaths, _ := httpRule["paths"].([]any)
			for _, pathItem := range httpPaths {
				pm, _ := pathItem.(map[string]any)
				backend, _ := pm["backend"].(map[string]any)
				service, _ := backend["service"].(map[string]any)
				port, _ := service["port"].(map[string]any)
				paths = append(paths, map[string]any{
					"host":        host,
					"path":        stringValueMap(pm, "path"),
					"pathType":    stringValueMap(pm, "pathType"),
					"serviceName": stringValueMap(service, "name"),
					"servicePort": anyValue(port, "number", "name"),
				})
			}
		}
	}
	return map[string]any{
		"name":         stringValue(item, "metadata", "name"),
		"namespace":    stringValue(item, "metadata", "namespace"),
		"clusterId":    clusterID,
		"clusterName":  "",
		"ingressClass": stringValue(item, "spec", "ingressClassName"),
		"hosts":        hosts,
		"paths":        paths,
		"tls":          sliceLen(item, "spec", "tls") > 0,
		"createdAt":    stringValue(item, "metadata", "creationTimestamp"),
	}
}

func flattenNetworkPolicy(clusterID string, item map[string]any) map[string]any {
	return map[string]any{
		"name":         stringValue(item, "metadata", "name"),
		"namespace":    stringValue(item, "metadata", "namespace"),
		"clusterId":    clusterID,
		"clusterName":  "",
		"podSelector":  nestedStringMap(item, "spec", "podSelector", "matchLabels"),
		"policyTypes":  stringSlice(item, "spec", "policyTypes"),
		"ingressRules": sliceLen(item, "spec", "ingress"),
		"egressRules":  sliceLen(item, "spec", "egress"),
		"createdAt":    stringValue(item, "metadata", "creationTimestamp"),
	}
}

func flattenPV(clusterID string, item map[string]any) map[string]any {
	claimRef := ""
	if claim, ok := nestedMap(item, "spec", "claimRef"); ok {
		claimRef = stringValueMap(claim, "namespace") + "/" + stringValueMap(claim, "name")
	}
	return map[string]any{
		"name":          stringValue(item, "metadata", "name"),
		"clusterId":     clusterID,
		"clusterName":   "",
		"status":        stringValue(item, "status", "phase"),
		"capacity":      stringValue(item, "spec", "capacity", "storage"),
		"accessModes":   stringSlice(item, "spec", "accessModes"),
		"reclaimPolicy": stringValue(item, "spec", "persistentVolumeReclaimPolicy"),
		"storageClass":  stringValue(item, "spec", "storageClassName"),
		"volumeMode":    stringValue(item, "spec", "volumeMode"),
		"claimRef":      strings.TrimPrefix(claimRef, "/"),
		"createdAt":     stringValue(item, "metadata", "creationTimestamp"),
	}
}

func flattenPVC(clusterID string, item map[string]any) map[string]any {
	return map[string]any{
		"name":         stringValue(item, "metadata", "name"),
		"namespace":    stringValue(item, "metadata", "namespace"),
		"clusterId":    clusterID,
		"clusterName":  "",
		"status":       stringValue(item, "status", "phase"),
		"capacity":     stringValue(item, "status", "capacity", "storage"),
		"accessModes":  stringSlice(item, "spec", "accessModes"),
		"storageClass": stringValue(item, "spec", "storageClassName"),
		"volumeName":   stringValue(item, "spec", "volumeName"),
		"createdAt":    stringValue(item, "metadata", "creationTimestamp"),
	}
}

func flattenStorageClass(clusterID string, item map[string]any) map[string]any {
	annotations := nestedStringMap(item, "metadata", "annotations")
	return map[string]any{
		"name":                 stringValue(item, "metadata", "name"),
		"clusterId":            clusterID,
		"clusterName":          "",
		"provisioner":          stringValue(item, "provisioner"),
		"reclaimPolicy":        stringValue(item, "reclaimPolicy"),
		"volumeBindingMode":    stringValue(item, "volumeBindingMode"),
		"allowVolumeExpansion": boolValue(item, "allowVolumeExpansion"),
		"isDefault":            annotations["storageclass.kubernetes.io/is-default-class"] == "true",
		"parameters":           nestedStringMap(item, "parameters"),
		"createdAt":            stringValue(item, "metadata", "creationTimestamp"),
	}
}

func flattenGeneric(clusterID string, item map[string]any) map[string]any {
	return map[string]any{
		"name":        stringValue(item, "metadata", "name"),
		"namespace":   stringValue(item, "metadata", "namespace"),
		"clusterId":   clusterID,
		"labels":      nestedStringMap(item, "metadata", "labels"),
		"annotations": nestedStringMap(item, "metadata", "annotations"),
		"createdAt":   stringValue(item, "metadata", "creationTimestamp"),
	}
}

func objectItems(payload map[string]any) []map[string]any {
	rawItems, _ := payload["items"].([]any)
	items := make([]map[string]any, 0, len(rawItems))
	for _, raw := range rawItems {
		if item, ok := raw.(map[string]any); ok {
			items = append(items, item)
		}
	}
	return items
}

func nestedMap(value map[string]any, path ...string) (map[string]any, bool) {
	current := value
	for i, key := range path {
		v, ok := current[key]
		if !ok {
			return nil, false
		}
		if i == len(path)-1 {
			out, ok := v.(map[string]any)
			return out, ok
		}
		next, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return nil, false
}

func nestedSlice(value map[string]any, path ...string) ([]any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	current := value
	for i, key := range path {
		v, ok := current[key]
		if !ok {
			return nil, false
		}
		if i == len(path)-1 {
			out, ok := v.([]any)
			return out, ok
		}
		next, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return nil, false
}

func nestedSliceOrEmpty(value map[string]any, path ...string) []any {
	if out, ok := nestedSlice(value, path...); ok {
		return out
	}
	return []any{}
}

func nestedStringMap(value map[string]any, path ...string) map[string]string {
	m, ok := nestedMap(value, path...)
	if !ok {
		return map[string]string{}
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func stringSlice(value map[string]any, path ...string) []string {
	raw, ok := nestedSlice(value, path...)
	if !ok {
		return []string{}
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func stringValue(value map[string]any, path ...string) string {
	current := value
	for i, key := range path {
		v, ok := current[key]
		if !ok {
			return ""
		}
		if i == len(path)-1 {
			if s, ok := v.(string); ok {
				return s
			}
			return fmt.Sprint(v)
		}
		next, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		current = next
	}
	return ""
}

func stringValueMap(value map[string]any, key string) string {
	if value == nil {
		return ""
	}
	if s, ok := value[key].(string); ok {
		return s
	}
	return ""
}

func intValue(value map[string]any, path ...string) int {
	current := value
	for i, key := range path {
		v, ok := current[key]
		if !ok {
			return 0
		}
		if i == len(path)-1 {
			switch n := v.(type) {
			case float64:
				return int(n)
			case int:
				return n
			default:
				return 0
			}
		}
		next, ok := v.(map[string]any)
		if !ok {
			return 0
		}
		current = next
	}
	return 0
}

func boolValue(value map[string]any, path ...string) bool {
	current := value
	for i, key := range path {
		v, ok := current[key]
		if !ok {
			return false
		}
		if i == len(path)-1 {
			b, _ := v.(bool)
			return b
		}
		next, ok := v.(map[string]any)
		if !ok {
			return false
		}
		current = next
	}
	return false
}

func anyValue(value map[string]any, keys ...string) any {
	for _, key := range keys {
		if v, ok := value[key]; ok {
			return v
		}
	}
	return nil
}

func firstString(value map[string]any, path ...string) string {
	if len(path) < 2 {
		return ""
	}
	slice, ok := nestedSlice(value, path[:len(path)-1]...)
	if !ok || len(slice) == 0 {
		return ""
	}
	first, _ := slice[0].(map[string]any)
	return stringValueMap(first, path[len(path)-1])
}

func sliceLen(value map[string]any, path ...string) int {
	slice, ok := nestedSlice(value, path...)
	if !ok {
		return 0
	}
	return len(slice)
}

func mapUser(user sqlc.User) map[string]any {
	displayName := strings.TrimSpace(strings.TrimSpace(user.FirstName) + " " + strings.TrimSpace(user.LastName))
	if displayName == "" {
		displayName = user.Username
	}
	lastLogin := ""
	if user.LastLogin.Valid {
		lastLogin = user.LastLogin.Time.UTC().Format(timeLayout)
	}
	return map[string]any{
		"id":          user.ID.String(),
		"username":    user.Username,
		"email":       user.Email,
		"displayName": displayName,
		"provider":    "local",
		"globalRoles": []string{},
		"enabled":     user.IsActive,
		"lastLogin":   lastLogin,
		"createdAt":   user.CreatedAt.UTC().Format(timeLayout),
	}
}

func nullableUserID(id pgtype.UUID) any {
	if id.Valid {
		return uuid.UUID(id.Bytes).String()
	}
	return nil
}

func defaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

const timeLayout = "2006-01-02T15:04:05Z"

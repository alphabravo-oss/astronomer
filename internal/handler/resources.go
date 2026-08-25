package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/kubeutil"
)

// rbacCacheInvalidator mirrors middleware.RBACCacheInvalidator but is
// duplicated here to avoid an import cycle (server/middleware -> handler is
// already a dependency direction for the cache wiring).
type rbacCacheInvalidator interface {
	Invalidate(userID string)
	InvalidateAll()
}

type ResourceHandler struct {
	requester K8sRequester
	queries   ResourceQuerier
	sso       SSOSettingsQuerier
	encryptor *auth.Encryptor
	ssoMgr    SSOProviderRegistrar
	rbacCache rbacCacheInvalidator
	// jwt is the optional JWT manager used by the admin force-logout
	// endpoint to flush its positive-validation cache. Nil-safe.
	jwt *auth.JWTManager
	// emails is the optional email-enqueue hook used by the admin
	// UnlockUser path to FYI the user. Optional; best-effort.
	emails EmailNotifier
	// ssoSessions surfaces the sso_sessions reader/deleter used by
	// admin force-logout (migration 054) to fire upstream back-channel
	// logout against every device the target user is signed in from.
	// Optional; nil keeps the legacy behaviour where force-logout
	// only stamps tokens_invalidated_at.
	ssoSessions ResourceSSOSessionStore
	// ssoBackchannel posts the upstream end_session POST when the
	// admin path is run. Optional; nil disables back-channel logout
	// and force-logout falls back to "local invalidation + delete
	// rows" (the user's existing browsers redirect-loop on next
	// request because the JWT cutoff was stamped).
	ssoBackchannel SSOBackchannelClient
	// settingsCache resolves password.* platform settings for local
	// account create/reset (AUTH-R01). Nil falls back to defaults.
	settingsCache *SettingsCache
	// userRunTx commits administrative identity mutations together with their
	// durable audit intent. Production always wires it; narrow resource tests
	// retain the direct-query compatibility path.
	userRunTx userRunTxFunc
	// runTx is the production transaction boundary for the legacy general
	// settings and SSO-provider endpoints. It keeps domain state and mandatory
	// audit intent in one commit; runtime/cache effects are applied only after
	// that commit succeeds.
	runTx resourceSettingsRunTxFunc
	// resourceOperations is deliberately separate from ResourceQuerier so the
	// durable member-cluster mutation surface does not widen every legacy test
	// fake. resourceMutationRunTx is the only authorized write boundary: the
	// operation, task intent and mandatory audit intent commit together.
	resourceOperations    ResourceOperationQuerier
	resourceMutationRunTx resourceMutationRunTxFunc
	authz                 authorizationSupport
	// Node effects use a dedicated durable operation model because drains are
	// resumable, non-atomic workflows with progress distinct from generic
	// resource apply/delete receipts.
	nodeOperations              NodeOperationQuerier
	nodeMutationRunTx           nodeMutationRunTxFunc
	nodeOperationReadAuthorizer func(context.Context, string, string) (bool, error)
}

// openapi:request NodeDrainRequest
type drainNodeRequest struct {
	IgnoreDaemonSets   *bool  `json:"ignore_daemonsets,omitempty"`
	DeleteEmptyDirData bool   `json:"delete_empty_dir_data,omitempty"`
	GracePeriodSeconds *int64 `json:"grace_period_seconds,omitempty"`
	DryRun             bool   `json:"dry_run,omitempty"`
	// Force mirrors `kubectl drain --force`: it must be set explicitly to
	// evict standalone pods (no ownerReferences). Without it such pods are
	// reported as blockers, because deleting them is irreversible — there
	// is no controller to recreate them.
	Force bool `json:"force,omitempty"`
}

type drainNodePodRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Reason    string `json:"reason,omitempty"`
}

type drainNodeResponse struct {
	Node     string            `json:"node"`
	Status   string            `json:"status"`
	Message  string            `json:"message"`
	Evicted  []drainNodePodRef `json:"evicted"`
	Skipped  []drainNodePodRef `json:"skipped"`
	Failed   []drainNodePodRef `json:"failed"`
	Blockers []string          `json:"blockers,omitempty"`
}

// openapi:request NodeKeyValueRequest
type nodeKeyValueRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// openapi:request NodeKeyRequest
type nodeKeyRequest struct {
	Key string `json:"key"`
}

// openapi:request NodeTaintRequest
type nodeTaintRequest struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Effect string `json:"effect"`
}

// openapi:request NodeTaintRemoveRequest
type nodeTaintRemoveRequest struct {
	Key    string `json:"key"`
	Effect string `json:"effect"`
}

type drainPodList struct {
	Items []drainPod `json:"items"`
}

type drainPod struct {
	Metadata struct {
		Name              string            `json:"name"`
		Namespace         string            `json:"namespace"`
		Annotations       map[string]string `json:"annotations"`
		DeletionTimestamp string            `json:"deletionTimestamp"`
		OwnerReferences   []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"ownerReferences"`
	} `json:"metadata"`
	Spec struct {
		Volumes []struct {
			Name     string         `json:"name"`
			EmptyDir map[string]any `json:"emptyDir,omitempty"`
		} `json:"volumes"`
	} `json:"spec"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

// ResourceSSOSessionStore is the narrow sso_sessions surface the
// admin force-logout handler uses to enumerate + clear a target
// user's upstream sessions.
type ResourceSSOSessionStore interface {
	ListSSOSessionsByUser(ctx context.Context, userID uuid.UUID) ([]sqlc.SsoSession, error)
	DeleteSSOSessionsByUser(ctx context.Context, userID uuid.UUID) error
}

// SSOBackchannelClient fires the upstream RP-initiated logout POST.
// Implemented in production by a tiny http.Client wrapper; tests
// substitute a recorder. The method intentionally returns nothing
// useful — the caller emits a metric + audit but never blocks on
// upstream success, because some IdPs don't implement back-channel
// logout at all.
type SSOBackchannelClient interface {
	PostEndSession(ctx context.Context, endpoint, idTokenHint string) error
}

// SetSettingsCache wires platform settings for password policy (AUTH-R01).
func (h *ResourceHandler) SetSettingsCache(c *SettingsCache) {
	if h != nil {
		h.settingsCache = c
	}
}

// passwordPolicy returns the live password policy from platform settings
// when wired, else DefaultPasswordPolicy.
func (h *ResourceHandler) passwordPolicy(ctx context.Context) auth.PasswordPolicy {
	if h == nil {
		return auth.DefaultPasswordPolicy()
	}
	return auth.LoadPasswordPolicy(ctx, h.settingsCache)
}

// SetSSOSessionStore wires the sso_sessions reader/deleter into the
// admin handler. Optional; nil keeps the pre-054 force-logout shape.
func (h *ResourceHandler) SetSSOSessionStore(s ResourceSSOSessionStore) {
	if h == nil {
		return
	}
	h.ssoSessions = s
}

// SetSSOBackchannelClient wires the back-channel logout POSTer.
// Optional; nil disables the upstream POST and force-logout falls
// back to "stamp cutoff + delete rows".
func (h *ResourceHandler) SetSSOBackchannelClient(c SSOBackchannelClient) {
	if h == nil {
		return
	}
	h.ssoBackchannel = c
}

// SetEmailNotifier wires the email-enqueue hook.
func (h *ResourceHandler) SetEmailNotifier(n EmailNotifier) { h.emails = n }

// SetJWTManager wires the JWT manager so admin endpoints that change
// session state (force-logout) can invalidate the in-process validation
// cache. Optional; nil-safe.
func (h *ResourceHandler) SetJWTManager(j *auth.JWTManager) {
	h.jwt = j
}

type ResourceQuerier interface {
	GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)
	UpsertPlatformConfig(ctx context.Context, arg sqlc.UpsertPlatformConfigParams) (sqlc.PlatformConfiguration, error)
	ListAuditLogV1(ctx context.Context, arg sqlc.ListAuditLogsParams) ([]sqlc.AuditLog, error)
	CountAuditLogV1(ctx context.Context) (int64, error)
	ListUsers(ctx context.Context, arg sqlc.ListUsersParams) ([]sqlc.User, error)
	CountUsers(ctx context.Context) (int64, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	GetUserByEmail(ctx context.Context, email string) (sqlc.User, error)
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	CreateUser(ctx context.Context, arg sqlc.CreateUserParams) (sqlc.User, error)
	UpdateUser(ctx context.Context, arg sqlc.UpdateUserParams) (sqlc.User, error)
	DeleteUser(ctx context.Context, id uuid.UUID) error
	UpdateUserPassword(ctx context.Context, arg sqlc.UpdateUserPasswordParams) error
	// Auth hardening (migration 039). UnlockUser clears the per-account
	// lockout fields; InvalidateAllTokens bumps the per-user cutoff
	// timestamp so every in-flight JWT for the user is rejected on its
	// next validation.
	UnlockUser(ctx context.Context, id uuid.UUID) error
	InvalidateAllTokens(ctx context.Context, arg sqlc.InvalidateAllTokensParams) error
}

type SSOSettingsQuerier interface {
	ListSSOConfigurations(ctx context.Context, arg sqlc.ListSSOConfigurationsParams) ([]sqlc.SsoConfiguration, error)
	GetEnabledSSOProviders(ctx context.Context) ([]sqlc.SsoConfiguration, error)
	GetSSOConfigurationByProvider(ctx context.Context, provider string) (sqlc.SsoConfiguration, error)
	GetSSOConfigurationByID(ctx context.Context, id uuid.UUID) (sqlc.SsoConfiguration, error)
	CreateSSOConfiguration(ctx context.Context, arg sqlc.CreateSSOConfigurationParams) (sqlc.SsoConfiguration, error)
	DeleteSSOConfiguration(ctx context.Context, id uuid.UUID) error
}

type SSOProviderRegistrar interface {
	RegisterProvider(name, clientID, clientSecretEncrypted, redirectURL string, scopes []string) error
	RegisterOIDCProvider(ctx context.Context, name, issuerURL, clientID, clientSecretEncrypted, redirectURL string, scopes []string) error
	HasProvider(name string) bool
	RemoveProvider(name string)
}

type resourceDef struct {
	apiBase    string
	namespaced bool
	plural     string
}

var resourceDefs = map[string]resourceDef{
	"namespaces":             {apiBase: "/api/v1", namespaced: false, plural: "namespaces"},
	"services":               {apiBase: "/api/v1", namespaced: true, plural: "services"},
	"configmaps":             {apiBase: "/api/v1", namespaced: true, plural: "configmaps"},
	"secrets":                {apiBase: "/api/v1", namespaced: true, plural: "secrets"},
	"serviceaccounts":        {apiBase: "/api/v1", namespaced: true, plural: "serviceaccounts"},
	"endpoints":              {apiBase: "/api/v1", namespaced: true, plural: "endpoints"},
	"persistentvolumes":      {apiBase: "/api/v1", namespaced: false, plural: "persistentvolumes"},
	"persistentvolumeclaims": {apiBase: "/api/v1", namespaced: true, plural: "persistentvolumeclaims"},
	"ingresses":              {apiBase: "/apis/networking.k8s.io/v1", namespaced: true, plural: "ingresses"},
	"networkpolicies":        {apiBase: "/apis/networking.k8s.io/v1", namespaced: true, plural: "networkpolicies"},
	"storageclasses":         {apiBase: "/apis/storage.k8s.io/v1", namespaced: false, plural: "storageclasses"},
	"replicasets":            {apiBase: "/apis/apps/v1", namespaced: true, plural: "replicasets"},
	// apps/v1 workload kinds. Historically the sidebar drove these
	// through the dedicated WorkloadHandler; the generic listing here
	// was missing the three most common workload types, so the
	// sidebar counts read 0 (or rendered "unknown") on every cluster.
	"deployments":             {apiBase: "/apis/apps/v1", namespaced: true, plural: "deployments"},
	"daemonsets":              {apiBase: "/apis/apps/v1", namespaced: true, plural: "daemonsets"},
	"statefulsets":            {apiBase: "/apis/apps/v1", namespaced: true, plural: "statefulsets"},
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

	// Gateway API. Most resources are GA at gateway.networking.k8s.io/v1.
	// TCPRoute and UDPRoute remain in v1alpha2 (experimental channel) — clusters
	// without those CRDs will see "No resources found", which is the correct
	// behavior.
	"gateways":        {apiBase: "/apis/gateway.networking.k8s.io/v1", namespaced: true, plural: "gateways"},
	"httproutes":      {apiBase: "/apis/gateway.networking.k8s.io/v1", namespaced: true, plural: "httproutes"},
	"gatewayclasses":  {apiBase: "/apis/gateway.networking.k8s.io/v1", namespaced: false, plural: "gatewayclasses"},
	"grpcroutes":      {apiBase: "/apis/gateway.networking.k8s.io/v1", namespaced: true, plural: "grpcroutes"},
	"tlsroutes":       {apiBase: "/apis/gateway.networking.k8s.io/v1", namespaced: true, plural: "tlsroutes"},
	"referencegrants": {apiBase: "/apis/gateway.networking.k8s.io/v1", namespaced: true, plural: "referencegrants"},
	"tcproutes":       {apiBase: "/apis/gateway.networking.k8s.io/v1alpha2", namespaced: true, plural: "tcproutes"},
	"udproutes":       {apiBase: "/apis/gateway.networking.k8s.io/v1alpha2", namespaced: true, plural: "udproutes"},
}

func NewResourceHandler() *ResourceHandler {
	return &ResourceHandler{}
}

func NewResourceHandlerWithRequester(requester K8sRequester) *ResourceHandler {
	return &ResourceHandler{requester: requester}
}

func NewResourceHandlerWithQueries(queries ResourceQuerier, requester K8sRequester) *ResourceHandler {
	h := &ResourceHandler{queries: queries, requester: requester}
	if sso, ok := any(queries).(SSOSettingsQuerier); ok {
		h.sso = sso
	}
	if operations, ok := any(queries).(ResourceOperationQuerier); ok {
		h.resourceOperations = operations
	}
	if operations, ok := any(queries).(NodeOperationQuerier); ok {
		h.nodeOperations = operations
	}
	return h
}

func (h *ResourceHandler) SetSSOManager(mgr SSOProviderRegistrar) {
	if h != nil {
		h.ssoMgr = mgr
	}
}

func (h *ResourceHandler) SetEncryptor(enc *auth.Encryptor) {
	if h != nil {
		h.encryptor = enc
	}
}

// SetRBACCacheInvalidator wires the middleware-side RBAC cache so user-delete
// (which CASCADEs through *_role_bindings) can invalidate the per-user entry
// immediately. Optional; without it the cache TTL expires the stale entry
// within 15s, which is acceptable for a deleted-user race window.
func (h *ResourceHandler) SetRBACCacheInvalidator(inv rbacCacheInvalidator) {
	if h != nil {
		h.rbacCache = inv
	}
}

func (h *ResourceHandler) ListNamedResources(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	resourceType := chi.URLParam(r, "resource_type")
	namespace := r.URL.Query().Get("namespace")
	path, err := listPath(resourceType, namespace)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidResource, err.Error())
		return
	}
	if h.requester == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, "tunnel requester not configured")
		return
	}
	// Bypass h.do so we can treat 404 as "CRD not installed" and return [].
	// This matters for optional resources like the v1alpha2 Gateway API
	// routes (TCPRoute / UDPRoute) that many clusters lack — the UI should
	// show "No resources found", not surface a 503.
	rawResp, err := h.requester.Do(r.Context(), clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	if rawResp == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, "empty response")
		return
	}
	if rawResp.StatusCode == http.StatusNotFound {
		RespondJSON(w, http.StatusOK, []map[string]any{})
		return
	}
	if err := ensureSuccess(rawResp); err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	var resp map[string]any
	if err := parseJSONResponse(rawResp, &resp); err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
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
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidResource, err.Error())
		return
	}
	resp, err := h.do(r.Context(), clusterID, http.MethodGet, path, nil, requestHeaders(""))
	if err != nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
		return
	}
	items := flattenGenericResources(clusterID, resourceType, resp)
	RespondJSON(w, http.StatusOK, items)
}

func (h *ResourceHandler) CreateNamedResource(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	resourceType := chi.URLParam(r, "resource_type")
	body, err := readResourceManifest(w, r)
	if err != nil {
		respondResourceManifestReadError(w, r, err)
		return
	}
	namespace, err := resourceNamespace(body)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Unable to determine namespace")
		return
	}
	name, err := resourceName(body)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Unable to determine resource name")
		return
	}
	path, err := resourcePath(resourceType, name, namespace)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidResource, err.Error())
		return
	}
	h.enqueueResourceOperation(w, r, resourceMutationIntent{
		ClusterID: clusterID, ResourceType: resourceType, Namespace: namespace,
		Name: name, Action: "apply", AuditVerb: "create", APIPath: path, Manifest: body,
	})
}

func (h *ResourceHandler) DeleteNamedResource(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	resourceType := chi.URLParam(r, "resource_type")
	name := chi.URLParam(r, "name")
	namespace := chi.URLParam(r, "namespace")
	path, err := resourcePath(resourceType, name, namespace)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidResource, err.Error())
		return
	}
	h.enqueueResourceOperation(w, r, resourceMutationIntent{
		ClusterID: clusterID, ResourceType: resourceType, Namespace: namespace,
		Name: name, Action: "delete", AuditVerb: "delete", APIPath: path,
	})
}

func (h *ResourceHandler) ListActivity(w http.ResponseWriter, r *http.Request) {
	limit := queryLimit(r, 20)
	if h.queries == nil {
		RespondList(w, []any{}, NewPagination(0, limit, 0, 0))
		return
	}
	logs, err := listAuditLogsForResource(r.Context(), h.queries, sqlc.ListAuditLogsParams{
		Limit:  int32(limit),
		Offset: 0,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ActivityError, "Failed to load activity feed")
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
	// The activity feed is a fixed-size recent window, so total is unknown.
	RespondList(w, items, NewPaginationFromPage(limit, 0, len(items)))
}

func (h *ResourceHandler) ListAuditLogs(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondPaginated(w, r, []any{}, 0)
		return
	}
	logs, err := listAuditLogsForResource(r.Context(), h.queries, sqlc.ListAuditLogsParams{
		Limit:  int32(queryLimit(r, 20)),
		Offset: int32(queryInt(r, "offset", 0)),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.AuditError, "Failed to load audit logs")
		return
	}
	total, _ := countAuditLogsForResource(r.Context(), h.queries)
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

func listAuditLogsForResource(ctx context.Context, q any, arg sqlc.ListAuditLogsParams) ([]sqlc.AuditLog, error) {
	if v1, ok := q.(auditReaderV1); ok && v1 != nil {
		return v1.ListAuditLogV1(ctx, arg)
	}
	return nil, fmt.Errorf("audit v1 reader not configured")
}

func countAuditLogsForResource(ctx context.Context, q any) (int64, error) {
	if v1, ok := q.(auditReaderV1); ok && v1 != nil {
		return v1.CountAuditLogV1(ctx)
	}
	return 0, fmt.Errorf("audit v1 reader not configured")
}

func (h *ResourceHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	if h.queries == nil {
		RespondPaginated(w, r, []any{}, 0)
		return
	}
	users, err := h.queries.ListUsers(r.Context(), sqlc.ListUsersParams{
		Limit:  int32(queryLimit(r, 20)),
		Offset: int32(queryInt(r, "offset", 0)),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UsersError, "Failed to load users")
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
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.UsersError, "user store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid user ID")
		return
	}
	user, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "User not found")
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

func (h *ResourceHandler) SetNodeLabel(w http.ResponseWriter, r *http.Request) {
	h.setNodeMetadataKey(w, r, "labels")
}

func (h *ResourceHandler) RemoveNodeLabel(w http.ResponseWriter, r *http.Request) {
	h.removeNodeMetadataKey(w, r, "labels")
}

func (h *ResourceHandler) SetNodeAnnotation(w http.ResponseWriter, r *http.Request) {
	h.setNodeMetadataKey(w, r, "annotations")
}

func (h *ResourceHandler) RemoveNodeAnnotation(w http.ResponseWriter, r *http.Request) {
	h.removeNodeMetadataKey(w, r, "annotations")
}

func (h *ResourceHandler) AddNodeTaint(w http.ResponseWriter, r *http.Request) {
	var req nodeTaintRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	req.Effect = strings.TrimSpace(req.Effect)
	if req.Key == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidTaint, "key is required")
		return
	}
	if !validTaintEffect(req.Effect) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidTaint, "effect must be NoSchedule, PreferNoSchedule, or NoExecute")
		return
	}
	h.enqueueNodeOperation(w, r, "add_taint", nodeOperationParameters{Key: req.Key, Value: req.Value, Effect: req.Effect})
}

func (h *ResourceHandler) RemoveNodeTaint(w http.ResponseWriter, r *http.Request) {
	var req nodeTaintRemoveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	req.Effect = strings.TrimSpace(req.Effect)
	if req.Key == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidTaint, "key is required")
		return
	}
	if req.Effect != "" && !validTaintEffect(req.Effect) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidTaint, "effect must be NoSchedule, PreferNoSchedule, or NoExecute")
		return
	}
	h.enqueueNodeOperation(w, r, "remove_taint", nodeOperationParameters{Key: req.Key, Effect: req.Effect})
}

func (h *ResourceHandler) setNodeSchedulable(w http.ResponseWriter, r *http.Request, unschedulable bool) {
	action := "uncordon"
	if unschedulable {
		action = "cordon"
	}
	h.enqueueNodeOperation(w, r, action, nodeOperationParameters{})
}

func nodeActionParams(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	clusterID := chi.URLParam(r, "cluster_id")
	nodeName := chi.URLParam(r, "node_name")
	if clusterID == "" || nodeName == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, "cluster_id and node_name are required")
		return "", "", false
	}
	return clusterID, nodeName, true
}

func validTaintEffect(effect string) bool {
	switch effect {
	case "NoSchedule", "PreferNoSchedule", "NoExecute":
		return true
	default:
		return false
	}
}

func (h *ResourceHandler) setNodeMetadataKey(w http.ResponseWriter, r *http.Request, field string) {
	var req nodeKeyValueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidKey, "key is required")
		return
	}
	action := "set_label"
	if field == "annotations" {
		action = "set_annotation"
	}
	h.enqueueNodeOperation(w, r, action, nodeOperationParameters{Key: req.Key, Value: req.Value})
}

func (h *ResourceHandler) removeNodeMetadataKey(w http.ResponseWriter, r *http.Request, field string) {
	var req nodeKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidKey, "key is required")
		return
	}
	action := "remove_label"
	if field == "annotations" {
		action = "remove_annotation"
	}
	h.enqueueNodeOperation(w, r, action, nodeOperationParameters{Key: req.Key})
}

func drainPodOwnedByDaemonSet(pod drainPod) bool {
	for _, ref := range pod.Metadata.OwnerReferences {
		if ref.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func drainPodHasEmptyDir(pod drainPod) bool {
	for _, volume := range pod.Spec.Volumes {
		if volume.EmptyDir != nil {
			return true
		}
	}
	return false
}

func podRefString(ref drainNodePodRef) string {
	if ref.Namespace == "" {
		return ref.Name
	}
	return ref.Namespace + "/" + ref.Name
}

// GetNamedResource handles GET /api/v1/resources/{cluster_id}/{type}/{namespace}/{name}/.
func (h *ResourceHandler) GetNamedResource(w http.ResponseWriter, r *http.Request) {
	h.proxyNamedResourceReadOrDryRun(w, r, false, nil)
}

// UpdateNamedResource handles PUT /api/v1/resources/{cluster_id}/{type}/{namespace}/{name}/.
// DIR-01: mutates via Kubernetes server-side apply (PATCH apply-patch+yaml)
// so concurrent edits get field-manager ownership instead of last-write-wins PUT.
func (h *ResourceHandler) UpdateNamedResource(w http.ResponseWriter, r *http.Request) {
	body, err := readResourceManifest(w, r)
	if err != nil {
		respondResourceManifestReadError(w, r, err)
		return
	}
	if strings.EqualFold(r.URL.Query().Get("dry_run"), "true") {
		// Kubernetes server-side dry-run is synchronous by contract and has no
		// member-cluster side effect. Preserve it as the validation endpoint.
		h.proxyNamedResourceReadOrDryRun(w, r, true, body)
		return
	}
	clusterID := chi.URLParam(r, "cluster_id")
	resourceType := chi.URLParam(r, "type")
	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	path, err := resourcePath(resourceType, name, namespace)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidResource, err.Error())
		return
	}
	if err := validateResourceIdentity(body, name, namespace); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, err.Error())
		return
	}
	h.enqueueResourceOperation(w, r, resourceMutationIntent{
		ClusterID: clusterID, ResourceType: resourceType, Namespace: namespace,
		Name: name, Action: "apply", AuditVerb: "update", APIPath: path, Manifest: body,
		Force: strings.EqualFold(r.URL.Query().Get("force"), "true"),
	})
}

// DeleteNamedResourceREST handles DELETE /api/v1/resources/{cluster_id}/{type}/{namespace}/{name}/.
func (h *ResourceHandler) DeleteNamedResourceREST(w http.ResponseWriter, r *http.Request) {
	clusterID := chi.URLParam(r, "cluster_id")
	resourceType := chi.URLParam(r, "type")
	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	path, err := resourcePath(resourceType, name, namespace)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidResource, err.Error())
		return
	}
	h.enqueueResourceOperation(w, r, resourceMutationIntent{
		ClusterID: clusterID, ResourceType: resourceType, Namespace: namespace,
		Name: name, Action: "delete", AuditVerb: "delete", APIPath: path,
	})
}

// proxyNamedResourceReadOrDryRun is the only synchronous named-resource
// proxy boundary. It permits reads and Kubernetes dry-run validation only;
// every real mutation must pass through the durable resource operation saga.
func (h *ResourceHandler) proxyNamedResourceReadOrDryRun(w http.ResponseWriter, r *http.Request, dryRun bool, body []byte) {
	clusterID := chi.URLParam(r, "cluster_id")
	resourceType := chi.URLParam(r, "type")
	namespace := chi.URLParam(r, "namespace")
	name := chi.URLParam(r, "name")
	if clusterID == "" || resourceType == "" || name == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, "cluster_id, type and name are required")
		return
	}
	path, err := resourcePath(resourceType, name, namespace)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidResource, err.Error())
		return
	}
	headers := requestHeaders("")
	method := http.MethodGet
	if dryRun {
		// Server-side apply preserves conflicts by default. Taking ownership is
		// an explicit query option and is separately gated on the manage verb.
		path = kubeutil.ServerSideApplyPath(path, kubeutil.ApplyOptions{
			FieldManager: "astronomer",
			Force:        strings.EqualFold(r.URL.Query().Get("force"), "true"),
			DryRun:       true,
		})
		headers = kubeutil.ApplyPatchHeaders()
		method = http.MethodPatch
		recordAudit(r, h.queries, "cluster.resource.update", resourceType, "", name, map[string]any{
			"cluster_id": clusterID, "namespace": namespace,
		})
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
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.ProxyError, err.Error())
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
		return fmt.Sprintf("%s/namespaces/%s/%s/%s", def.apiBase, url.PathEscape(namespace), def.plural, url.PathEscape(name)), nil
	}
	return fmt.Sprintf("%s/%s/%s", def.apiBase, def.plural, url.PathEscape(name)), nil
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

func resourceName(body []byte) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	metadata, _ := payload["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("metadata.name is required")
	}
	return name, nil
}

func validateResourceIdentity(body []byte, name, namespace string) error {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return errors.New("request body must be valid JSON")
	}
	metadata, _ := payload["metadata"].(map[string]any)
	if metadata == nil {
		return errors.New("metadata is required")
	}
	if bodyName, _ := metadata["name"].(string); strings.TrimSpace(bodyName) != "" && bodyName != name {
		return errors.New("metadata.name must match the URL resource name")
	}
	if bodyNamespace, _ := metadata["namespace"].(string); strings.TrimSpace(bodyNamespace) != "" && bodyNamespace != namespace {
		return errors.New("metadata.namespace must match the URL namespace")
	}
	return nil
}

func readResourceManifest(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(w, r.Body, maxResourceManifestBytes))
}

func respondResourceManifestReadError(w http.ResponseWriter, r *http.Request, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		RespondRequestError(w, r, http.StatusRequestEntityTooLarge, apierror.InvalidBody, "Resource manifest exceeds the 2 MiB limit")
		return
	}
	RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Failed to read request body")
}

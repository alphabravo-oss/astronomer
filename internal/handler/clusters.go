package handler

import (
	"context"
	"errors"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/clustermetrics"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
)

// ClusterQuerier abstracts the cluster-related database queries needed by ClusterHandler.
type ClusterQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	GetClusterByName(ctx context.Context, name string) (sqlc.Cluster, error)
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	CreateCluster(ctx context.Context, arg sqlc.CreateClusterParams) (sqlc.Cluster, error)
	UpdateCluster(ctx context.Context, arg sqlc.UpdateClusterParams) (sqlc.Cluster, error)
	DeleteCluster(ctx context.Context, id uuid.UUID) error
	CountClusters(ctx context.Context) (int64, error)
	// Cluster decommission. The DELETE handler no longer hard-deletes the
	// row; it inserts a cluster_decommissions row and enqueues the worker
	// reconciler. GetLatest backs the GET /decommission status endpoint.
	CreateClusterDecommission(ctx context.Context, arg sqlc.CreateClusterDecommissionParams) (sqlc.ClusterDecommission, error)
	GetLatestClusterDecommissionByCluster(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterDecommission, error)
	// ListPendingClusterDecommissionsForClusters returns in-flight
	// ('pending'/'running') decommissions for an already-authorized page. Keeping
	// the page scope in SQL avoids scanning or exposing estate-wide state.
	ListPendingClusterDecommissionsForClusters(ctx context.Context, clusterIds []uuid.UUID) ([]sqlc.ClusterDecommission, error)
	// SetClusterDecommissionForce escalates an in-flight decommission to force
	// (skip the cleanup grace window) when the operator re-deletes with ?force.
	SetClusterDecommissionForce(ctx context.Context, id uuid.UUID) (sqlc.ClusterDecommission, error)
	// Health
	GetClusterHealthStatus(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterHealthStatus, error)
	ListClusterConditions(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ClusterCondition, error)
	// Registration
	CreateClusterRegistrationToken(ctx context.Context, arg sqlc.CreateClusterRegistrationTokenParams) (sqlc.ClusterRegistrationToken, error)
	GetRegistrationTokenByToken(ctx context.Context, token string) (sqlc.ClusterRegistrationToken, error)
	MarkRegistrationTokenUsed(ctx context.Context, id uuid.UUID) error
	// Durable agent-token rotation / revocation (task A2). Rotate sets
	// rotation_pending_at so the agent's next CONNECT performs the grace
	// rotation; Revoke immediately denies the token from the next CONNECT.
	// Both return the rows affected so the handler can 404 a cluster that
	// has no agent token yet.
	SetClusterAgentTokenRotationPending(ctx context.Context, clusterID uuid.UUID) (int64, error)
	RevokeClusterAgentToken(ctx context.Context, clusterID uuid.UUID) (int64, error)
	// Registry config
	GetClusterRegistryConfig(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterRegistryConfig, error)
	UpsertClusterRegistryConfig(ctx context.Context, arg sqlc.UpsertClusterRegistryConfigParams) (sqlc.ClusterRegistryConfig, error)
	DeleteClusterRegistryConfig(ctx context.Context, clusterID uuid.UUID) error
	GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)
	// Platform-TLS surface for the public CA-bundle endpoint
	// (GET /api/v1/register/ca.crt) used by the Rancher-style
	// `curl --cacert ca.crt -sfL …` registration variant.
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
	// Sprint 086 — cluster-condition remediation history. Read by
	// the cluster detail page so operators can see what the
	// reconciler has done in response to red condition pills.
	ListClusterConditionRemediationByCluster(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ClusterConditionRemediationAttempt, error)
}

// EventPublisher is the minimal contract ClusterHandler depends on for
// fan-out of cluster.* lifecycle events. Declared here (rather than imported
// from internal/events) so this package stays free of an events dependency
// — the cluster handler is a hot path and we don't want a transitive import
// cycle. *events.Bus implements this interface naturally.
type EventPublisher interface {
	Publish(eventType string, data any)
}

// ClusterMutationTx is the transaction-bound write surface for cluster
// administration. Production passes sqlc.New(tx), so the domain mutation,
// any durable task intent, and the sanitized audit intent share one commit
// decision.
type ClusterMutationTx interface {
	audit.OutboxQuerier
	tasks.TaskOutboxWriter
	CreateCluster(context.Context, sqlc.CreateClusterParams) (sqlc.Cluster, error)
	GetClusterByIDForUpdate(context.Context, uuid.UUID) (sqlc.Cluster, error)
	UpdateCluster(context.Context, sqlc.UpdateClusterParams) (sqlc.Cluster, error)
	CreateAPIToken(context.Context, sqlc.CreateAPITokenParams) (sqlc.ApiToken, error)
	GetClusterOwnership(context.Context, uuid.UUID) (sqlc.GetClusterOwnershipRow, error)
	SetClusterOwnership(context.Context, sqlc.SetClusterOwnershipParams) (sqlc.SetClusterOwnershipRow, error)
	CreateClusterDecommission(context.Context, sqlc.CreateClusterDecommissionParams) (sqlc.ClusterDecommission, error)
	SetClusterDecommissionForce(context.Context, uuid.UUID) (sqlc.ClusterDecommission, error)
	CreateClusterRegistrationToken(context.Context, sqlc.CreateClusterRegistrationTokenParams) (sqlc.ClusterRegistrationToken, error)
	SetClusterAgentTokenRotationPending(context.Context, uuid.UUID) (int64, error)
	RevokeClusterAgentToken(context.Context, uuid.UUID) (int64, error)
	UpsertClusterRegistryConfig(context.Context, sqlc.UpsertClusterRegistryConfigParams) (sqlc.ClusterRegistryConfig, error)
	DeleteClusterRegistryConfig(context.Context, uuid.UUID) error
}

type clusterRunTxFunc func(context.Context, func(ClusterMutationTx) error) error

var (
	errClusterOwnershipTransferUnsupported = errors.New("cluster ownership can only be transferred from crd to api")
	errAgentTokenRotationIneligible        = errors.New("no agent token is eligible for rotation")
	errAgentTokenNotActive                 = errors.New("cluster has no active agent token")
)

// ClusterHandler handles cluster endpoints.
type ClusterHandler struct {
	queries ClusterQuerier
	runTx   clusterRunTxFunc
	// directRequester is used only for the exact TokenRequest subresource of
	// astronomer-direct-reader. It is kept separate from metrics wiring so a
	// partially-wired server fails direct credential issuance closed.
	directRequester K8sRequester
	// metrics is an optional, lazily-wired aggregator that enriches list/get
	// responses with CPU%, memory%, and pod_count. When nil (or before
	// SetMetrics* is called) the handler returns zeros for those fields —
	// this is intentional: the dashboard renders zeros gracefully and we'd
	// rather degrade than 500 the cluster list when metrics-server is
	// unreachable.
	metrics *clustermetrics.Provider
	// publisher fans out cluster.created / cluster.updated / cluster.deleted
	// events. Optional and nil-safe: when not wired the CRUD path simply
	// doesn't notify SSE subscribers.
	publisher EventPublisher
	// grafanaFolders nudges fleet Grafana folder-per-cluster provisioning
	// after create/update/delete. Optional; the 30s Grafana folder
	// reconciler is the catch-up path. Folders are UX, not a security
	// boundary (PromQL/LogQL rewrite is).
	grafanaFolders       grafanaFolderReconciler
	agentImage           string
	systemArtifactURL    string
	systemArtifactDigest string
	systemOIDCIssuer     string
	systemOIDCIdentity   string
	// enforcer gates Create against the estate-wide cluster cap
	// configured by the 'global' quota plan (migration 051).
	// Optional; nil disables the check (test fakes, pre-migration).
	enforcer *quota.Enforcer
	// maintenanceGate (sprint 057) gates destructive mutations.
	maintenanceGate *MaintenanceGate
	// registration (sprint 078) is the shared phase-machine service.
	// Create writes the initial cluster_registration_steps rows so the
	// wizard page-3 timeline has something to render. nil-safe.
	registration *registration.Service
	encryptor    *auth.Encryptor
	// manifestSigningSecret keys the HMAC over (cluster_id, expiry) that
	// gates the short-TTL signed manifest-download URL. When empty the
	// signed-URL endpoint refuses every request (503) — there is no
	// unsigned fallback that path could degrade to.
	manifestSigningSecret []byte
	// agentDisconnector severs a live agent tunnel session immediately.
	// Wired to the tunnel hub. Optional and nil-safe: revoke still records
	// revoked_at (denying the next CONNECT) when this is nil, but the live
	// session would then persist until it happens to reconnect.
	agentDisconnector AgentDisconnector
	// registrationTokenTTL (task A3) is the single TTL every operator-facing
	// registration-token mint path applies. Defaults to time.Hour; wired from
	// cfg.RegistrationTokenTTLHours via SetRegistrationTokenTTL.
	registrationTokenTTL time.Duration
	// authz scope-filters the list page to the clusters the caller may see.
	// Fails closed when unwired (500 for an authenticated caller), so
	// SetAuthorization is mandatory wherever the list route is served.
	authz authorizationSupport
}

// SetAuthorization wires the RBAC engine + binding querier used to scope-filter
// GET /clusters/. Must be set wherever RequireCollectionPermission gates the
// route: the gate admits cluster-scoped callers and this is what narrows their
// page.
func (h *ClusterHandler) SetAuthorization(engine *rbac.Engine, querier rbac.BindingQuerier) {
	if h == nil {
		return
	}
	h.authz.SetAuthorization(engine, querier)
}

// AgentDisconnector force-closes a cluster's live agent tunnel session.
// Satisfied by *tunnel.Hub.Disconnect. Returns true if a session was closed.
type AgentDisconnector interface {
	Disconnect(clusterID string) bool
}

// NewClusterHandler creates a new cluster handler.
func NewClusterHandler(queries ClusterQuerier) *ClusterHandler {
	return &ClusterHandler{
		queries:              queries,
		metrics:              clustermetrics.NewProvider(),
		agentImage:           "ghcr.io/alphabravo-oss/astronomer-go-agent:latest",
		registrationTokenTTL: time.Hour,
	}
}

// SetRunTx enables the fail-closed cluster mutation + audit-outbox path.
func (h *ClusterHandler) SetRunTx(runTx clusterRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *ClusterHandler) TransactionalAuditWired() bool {
	return h != nil && h.runTx != nil
}

func (h *ClusterHandler) SetEncryptor(e *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = e
}

// SetAgentDisconnector wires the tunnel hub so revoke can sever a live agent
// session immediately. Set once at startup; nil-safe.
func (h *ClusterHandler) SetAgentDisconnector(d AgentDisconnector) {
	if h == nil {
		return
	}
	h.agentDisconnector = d
}

func (h *ClusterHandler) SetGrafanaFolderReconciler(r grafanaFolderReconciler) {
	if h == nil {
		return
	}
	h.grafanaFolders = r
}

func (h *ClusterHandler) triggerGrafanaFolders() {
	if h != nil && h.grafanaFolders != nil {
		h.grafanaFolders.TriggerGrafanaFolderReconcile()
	}
}

// SetEventPublisher wires the SSE bus so cluster CRUD operations fan out
// to subscribers. Set once at startup; nil-safe.
func (h *ClusterHandler) SetEventPublisher(p EventPublisher) {
	if h == nil {
		return
	}
	h.publisher = p
}

// SetQuotaEnforcer wires the per-tenant quota enforcer that gates Create
// against the estate-wide cluster cap (migration 051). Optional; nil
// disables the check so tests can construct the handler without it.
func (h *ClusterHandler) SetQuotaEnforcer(e *quota.Enforcer) {
	if h == nil {
		return
	}
	h.enforcer = e
}

// SetMaintenanceGate wires the migration-057 gate that refuses or
// defers cluster.delete during an active maintenance window. Optional;
// nil-safe — pre-wiring the field disables the gate (every Delete
// proceeds as before).
func (h *ClusterHandler) SetMaintenanceGate(g *MaintenanceGate) {
	if h == nil {
		return
	}
	h.maintenanceGate = g
}

// publishEvent is a nil-safe wrapper around the optional publisher.
func (h *ClusterHandler) publishEvent(eventType string, data any) {
	if h == nil || h.publisher == nil {
		return
	}
	h.publisher.Publish(eventType, data)
}

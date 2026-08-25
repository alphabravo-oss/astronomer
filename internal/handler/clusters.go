package handler

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/handler/clustermetrics"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"k8s.io/client-go/kubernetes"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
	"sigs.k8s.io/yaml"
)

// maxSignedManifestTTL bounds how far in the future an attacker-presented
// signed-manifest expiry may sit. The wizard mints 15m windows; anything
// claiming validity past this ceiling is rejected by the verifier even if
// the HMAC checks out, so a leaked signing key can't be used to forge
// effectively-permanent URLs.
const maxSignedManifestTTL = 30 * time.Minute

// clusterScopeQuerier is the OPTIONAL capability a ClusterQuerier may provide
// to serve a scope-filtered list page. Kept off ClusterQuerier on purpose: only
// the list path needs it, so the dozen existing test fakes that implement the
// full interface stay untouched. A scope-restricted caller whose querier does
// NOT implement it gets a 500, never an unfiltered fleet.
type clusterScopeQuerier interface {
	ListClustersForScopes(ctx context.Context, arg sqlc.ListClustersForScopesParams) ([]sqlc.Cluster, error)
	CountClustersForScopes(ctx context.Context, clusterIds []uuid.UUID) (int64, error)
}

type clusterFilteredQuerier interface {
	ListClustersFiltered(ctx context.Context, arg sqlc.ListClustersFilteredParams) ([]sqlc.Cluster, error)
	CountClustersFiltered(ctx context.Context, arg sqlc.CountClustersFilteredParams) (int64, error)
	ListClustersFilteredForScopes(ctx context.Context, arg sqlc.ListClustersFilteredForScopesParams) ([]sqlc.Cluster, error)
	CountClustersFilteredForScopes(ctx context.Context, arg sqlc.CountClustersFilteredForScopesParams) (int64, error)
}

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
	// ListPendingClusterDecommissions returns in-flight ('pending'/'running')
	// decommissions; the list/get handlers use it to mark clusters
	// Decommissioning so the UI shows a stable "Decommissioning" state.
	ListPendingClusterDecommissions(ctx context.Context, limit int32) ([]sqlc.ClusterDecommission, error)
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

type clusterOwnershipQuerier interface {
	GetClusterOwnership(ctx context.Context, id uuid.UUID) (sqlc.FleetOwnership, error)
}

type clusterOwnershipTransferQuerier interface {
	clusterOwnershipQuerier
	SetClusterOwnership(ctx context.Context, arg sqlc.SetClusterOwnershipParams) (sqlc.FleetOwnership, error)
}

// sqlc generates distinct row types for Get/Set/List even though the columns
// are identical. Keep the legacy FleetOwnership-shaped seam for narrow fakes,
// and adapt the generated production surface explicitly.
type clusterOwnershipSQLQuerier interface {
	GetClusterOwnership(context.Context, uuid.UUID) (sqlc.GetClusterOwnershipRow, error)
	SetClusterOwnership(context.Context, sqlc.SetClusterOwnershipParams) (sqlc.SetClusterOwnershipRow, error)
}

type clusterDecommissionTaskOutboxQuerier interface {
	CreateClusterDecommissionWithTaskOutbox(ctx context.Context, arg sqlc.CreateClusterDecommissionWithTaskOutboxParams) (sqlc.ClusterDecommission, error)
}

// ClusterMutationTx is the transaction-bound write surface for cluster
// administration. Production passes sqlc.New(tx), so the domain mutation,
// any durable task intent, and the sanitized audit intent share one commit
// decision.
type ClusterMutationTx interface {
	audit.OutboxQuerier
	CreateCluster(context.Context, sqlc.CreateClusterParams) (sqlc.Cluster, error)
	GetClusterByIDForUpdate(context.Context, uuid.UUID) (sqlc.Cluster, error)
	UpdateCluster(context.Context, sqlc.UpdateClusterParams) (sqlc.Cluster, error)
	CreateAPIToken(context.Context, sqlc.CreateAPITokenParams) (sqlc.ApiToken, error)
	GetClusterOwnership(context.Context, uuid.UUID) (sqlc.GetClusterOwnershipRow, error)
	SetClusterOwnership(context.Context, sqlc.SetClusterOwnershipParams) (sqlc.SetClusterOwnershipRow, error)
	CreateClusterDecommission(context.Context, sqlc.CreateClusterDecommissionParams) (sqlc.ClusterDecommission, error)
	CreateClusterDecommissionWithTaskOutbox(context.Context, sqlc.CreateClusterDecommissionWithTaskOutboxParams) (sqlc.ClusterDecommission, error)
	SetClusterDecommissionForce(context.Context, uuid.UUID) (sqlc.ClusterDecommission, error)
	CreateClusterRegistrationToken(context.Context, sqlc.CreateClusterRegistrationTokenParams) (sqlc.ClusterRegistrationToken, error)
	SetClusterAgentTokenRotationPending(context.Context, uuid.UUID) (int64, error)
	RevokeClusterAgentToken(context.Context, uuid.UUID) (int64, error)
	UpsertClusterRegistryConfig(context.Context, sqlc.UpsertClusterRegistryConfigParams) (sqlc.ClusterRegistryConfig, error)
	DeleteClusterRegistryConfig(context.Context, uuid.UUID) error
}

type clusterRunTxFunc func(context.Context, func(ClusterMutationTx) error) error

type clusterAuditEvent struct {
	action       string
	resourceType string
	resourceID   string
	resourceName string
	status       int
	detail       map[string]any
}

type clusterDecommissionMutationResult struct {
	row      sqlc.ClusterDecommission
	enqueued bool
}

var (
	errClusterOwnershipTransferUnsupported = errors.New("cluster ownership can only be transferred from crd to api")
	errAgentTokenRotationIneligible        = errors.New("no agent token is eligible for rotation")
	errAgentTokenNotActive                 = errors.New("cluster has no active agent token")
)

// ClusterDecommissionEnqueuer abstracts the asynq client surface the Delete
// handler needs. *asynq.Client satisfies this interface natively; tests can
// supply a stub. Nil-safe: when not wired, the handler still creates the
// cluster_decommissions row but the worker reconciler only fires via the
// periodic sweep instead of the immediate enqueue.
type ClusterDecommissionEnqueuer interface {
	Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
}

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
	grafanaFolders grafanaFolderReconciler
	// decommissionQueue is the asynq client used to enqueue
	// cluster_decommission tasks from the DELETE handler. Optional —
	// when nil, the row is still inserted but the worker doesn't pick it up
	// until the periodic sweep runs (slower path).
	decommissionQueue ClusterDecommissionEnqueuer
	// taskOutbox persists critical task intents before Redis delivery.
	// Optional; when nil the handler falls back to direct enqueue and
	// periodic sweeps.
	taskOutbox           tasks.TaskOutboxWriter
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
func (h *ClusterHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
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

// executeClusterMutation preserves narrow fake compatibility while ensuring
// that production never commits a cluster-management change without its
// corresponding durable audit intent.
func executeClusterMutation[T any](
	r *http.Request,
	h *ClusterHandler,
	mutate func(ClusterMutationTx) (T, error),
	fallback func() (T, error),
	describe func(T) clusterAuditEvent,
) (T, error) {
	var zero T
	if h == nil {
		return zero, fmt.Errorf("cluster handler is nil")
	}
	if h.runTx != nil {
		var result T
		err := h.runTx(r.Context(), func(q ClusterMutationTx) error {
			var mutationErr error
			result, mutationErr = mutate(q)
			if mutationErr != nil {
				return mutationErr
			}
			event := describe(result)
			if event.action == "" {
				return nil
			}
			return recordAuditOutbox(r, q, event.action, event.resourceType, event.resourceID, event.resourceName, event.status, event.detail)
		})
		return result, err
	}

	result, err := fallback()
	if err != nil {
		return zero, err
	}
	event := describe(result)
	recordAudit(r, h.queries, event.action, event.resourceType, event.resourceID, event.resourceName, event.detail)
	return result, nil
}

// SetRegistrationTokenTTL overrides the registration-token TTL (task A3).
// Non-positive values clamp to the 1h default so a missing/zero config never
// mints a zero-lifetime token.
func (h *ClusterHandler) SetRegistrationTokenTTL(d time.Duration) {
	if h == nil {
		return
	}
	if d <= 0 {
		d = time.Hour
	}
	h.registrationTokenTTL = d
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

// SetManifestSigningSecret wires the HMAC key for the signed
// manifest-download URL. Set once at startup; nil-safe. Empty secret
// leaves the signed-URL endpoint disabled (503).
func (h *ClusterHandler) SetManifestSigningSecret(secret string) {
	if h == nil {
		return
	}
	if secret == "" {
		h.manifestSigningSecret = nil
		return
	}
	// Domain-separate the manifest HMAC key from the raw secret. When the
	// wiring layer falls back to cfg.SecretKey (the JWT signing secret),
	// using it verbatim would make the manifest signer and the JWT signer
	// share an identical key; derive a distinct subkey so the two are not
	// the same bytes.
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("manifest-signing"))
	h.manifestSigningSecret = mac.Sum(nil)
}

// manifestSignature computes the HMAC-SHA256 over "cluster_id|expiry"
// (expiry as unix seconds). Hex-encoded so it's URL-safe and constant
// across encodings.
func (h *ClusterHandler) manifestSignature(clusterID uuid.UUID, expiry int64) string {
	mac := hmac.New(sha256.New, h.manifestSigningSecret)
	mac.Write([]byte(clusterID.String() + "|" + strconv.FormatInt(expiry, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

// SignManifestURL returns a relative, time-limited signed path the
// wizard can hand to operators:
//
//	/api/v1/register/signed/{cluster_id}?expires=<unix>&sig=<hmac>
//
// ttl bounds the validity window (caller passes 15m). Returns "" when
// no signing secret is configured.
func (h *ClusterHandler) SignManifestURL(clusterID uuid.UUID, ttl time.Duration) string {
	if h == nil || len(h.manifestSigningSecret) == 0 {
		return ""
	}
	expiry := time.Now().Add(ttl).Unix()
	sig := h.manifestSignature(clusterID, expiry)
	return fmt.Sprintf("/api/v1/register/signed/%s?expires=%d&sig=%s",
		clusterID.String(), expiry, url.QueryEscape(sig))
}

// verifyManifestSignature checks expiry and the constant-time HMAC.
// Returns nil when valid.
func (h *ClusterHandler) verifyManifestSignature(clusterID uuid.UUID, expiry int64, sig string) error {
	if len(h.manifestSigningSecret) == 0 {
		return errors.New("signing disabled")
	}
	now := time.Now()
	if now.Unix() > expiry {
		return errors.New("expired")
	}
	// Reject expiries further out than we'd ever legitimately mint, so a
	// forged or replayed URL can't claim a long-lived window.
	if expiry > now.Add(maxSignedManifestTTL).Unix() {
		return errors.New("expiry too far in future")
	}
	want := h.manifestSignature(clusterID, expiry)
	if subtle.ConstantTimeCompare([]byte(want), []byte(sig)) != 1 {
		return errors.New("bad signature")
	}
	return nil
}

func (h *ClusterHandler) encryptLegacyRegistryPassword(password string) (string, string, error) {
	if h == nil || h.encryptor == nil || password == "" {
		return password, "", nil
	}
	encrypted, err := h.encryptor.Encrypt(password)
	if err != nil {
		return "", "", err
	}
	return "", encrypted, nil
}

// SetMetricsLocalClient wires the in-process kubernetes clientset used to
// gather metrics for the local (is_local=true) cluster row. Metrics-server
// access is optional; pass a nil metricsClient when it isn't installed and
// CPU/memory percentages will simply remain zero.
//
// The setter pattern lets the wiring layer (cmd/server) inject the clients
// without ClusterHandler taking a hard dependency on rest.InClusterConfig in
// its constructor — which would break unit tests and offline `go build`.
func (h *ClusterHandler) SetMetricsLocalClient(cs *kubernetes.Clientset, metricsClient metricsv.Interface) {
	if h == nil || h.metrics == nil {
		return
	}
	h.metrics.SetLocalClient(cs, metricsClient)
}

// SetMetricsRequester wires the tunnel-backed K8sRequester used to gather
// metrics for non-local clusters. The handler-level K8sRequester returns
// protocol.K8sResponsePayload; the clustermetrics package uses a smaller
// transport-agnostic shape, so this method bridges between them.
func (h *ClusterHandler) SetMetricsRequester(r K8sRequester) {
	if h == nil || h.metrics == nil || r == nil {
		return
	}
	h.metrics.SetRemoteRequester(metricsRequesterAdapter{r: r})
}

// SetDirectKubeconfigRequester wires the tunnel used for the dedicated
// read-only ServiceAccount TokenRequest. No direct credential can be issued
// while this dependency is absent.
func (h *ClusterHandler) SetDirectKubeconfigRequester(r K8sRequester) {
	if h != nil {
		h.directRequester = r
	}
}

// MetricsProvider returns the clustermetrics provider this handler uses.
// Exposed so the metrics publisher (which fans CPU/mem snapshots out to
// SSE subscribers) can share the same cache the dashboard list endpoint
// already populates — avoids stampeding the agent tunnel with parallel
// independent metric reads.
func (h *ClusterHandler) MetricsProvider() *clustermetrics.Provider {
	if h == nil {
		return nil
	}
	return h.metrics
}

func (h *ClusterHandler) SetAgentImage(repository, tag string) {
	if h == nil {
		return
	}
	if repository == "" {
		repository = "ghcr.io/alphabravo-oss/astronomer-go-agent"
	}
	if tag == "" {
		tag = "latest"
	}
	h.agentImage = targetAgentImage(repository, tag)
}

// SetDeliverySystemBootstrap configures the immutable, signed Flux system
// artifact embedded in new-cluster registration manifests. Invalid or partial
// values are omitted by the renderer; production startup validation rejects
// that configuration before the API is served.
func (h *ClusterHandler) SetDeliverySystemBootstrap(repository, digest, issuer, identity string) {
	if h == nil {
		return
	}
	h.systemArtifactURL = strings.TrimSpace(repository)
	h.systemArtifactDigest = strings.TrimSpace(digest)
	h.systemOIDCIssuer = strings.TrimSpace(issuer)
	h.systemOIDCIdentity = strings.TrimSpace(identity)
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

// SetRegistrationService wires the wizard-phase service so cluster
// Create can stamp the first two cluster_registration_steps rows
// (cluster_created + manifest_generated). nil-safe.
func (h *ClusterHandler) SetRegistrationService(s *registration.Service) {
	if h == nil {
		return
	}
	h.registration = s
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

// SetDecommissionQueue wires the asynq client used by the DELETE handler to
// schedule the cluster_decommission reconciler. Optional: nil means the
// handler still records the cluster_decommissions row, but the worker only
// picks it up via the periodic sweep.
func (h *ClusterHandler) SetDecommissionQueue(q ClusterDecommissionEnqueuer) {
	if h == nil {
		return
	}
	h.decommissionQueue = q
}

// SetTaskOutbox wires the durable task outbox used before direct Redis enqueue.
// Optional and nil-safe.
func (h *ClusterHandler) SetTaskOutbox(q tasks.TaskOutboxWriter) {
	if h == nil {
		return
	}
	h.taskOutbox = q
}

// publishEvent is a nil-safe wrapper around the optional publisher.
func (h *ClusterHandler) publishEvent(eventType string, data any) {
	if h == nil || h.publisher == nil {
		return
	}
	h.publisher.Publish(eventType, data)
}

// The previous clusterWithMetrics struct (anonymous-embed sqlc.Cluster +
// CPU/Memory/Pod scalars) was replaced by the explicit ClusterResponse DTO
// in clusters_response.go. See TestClusterResponse_WireCompat for the
// byte-for-byte wire compat guarantee.

// metricsRequesterAdapter bridges the handler-level K8sRequester (which
// returns *protocol.K8sResponsePayload with a base64-encoded body) into the
// transport-agnostic shape consumed by clustermetrics. Decoding the body
// here keeps the clustermetrics package free of protocol/tunnel imports.
type metricsRequesterAdapter struct{ r K8sRequester }

func (a metricsRequesterAdapter) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*clustermetrics.RawResponse, error) {
	resp, err := a.r.Do(ctx, clusterID, method, path, body, headers)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("nil response")
	}
	decoded, err := decodeResponseBody(resp)
	if err != nil {
		return nil, err
	}
	return &clustermetrics.RawResponse{StatusCode: resp.StatusCode, Body: decoded}, nil
}

// enrichClusterFromCache copies the sqlc.Cluster row plus the most-recent
// CACHED metrics snapshot into the wire-format struct. Cache-only on
// purpose: this is called from List which iterates every cluster, and a
// slow agent on a single cluster previously stalled the entire response
// for up to 5s × N clusters. The background metrics
// publisher (internal/metrics/publisher.go) keeps the cache warm; stale
// or missing entries return zero values rather than blocking.
func (h *ClusterHandler) enrichClusterFromCache(_ context.Context, c sqlc.Cluster) ClusterResponse {
	out := clusterToResponse(c)
	if h.metrics == nil {
		return out
	}
	snap := h.metrics.Peek(c.ID.String())
	out.CPUPercentage = snap.CPUPercentage
	out.MemoryPercentage = snap.MemoryPercentage
	out.PodCount = snap.PodCount
	out.MetricsServerPresent = snap.MetricsServerPresent
	return out
}

// enrichClusterFresh is the slow-path counterpart to enrichClusterFromCache.
// Called from single-cluster endpoints (Get) where the caller is willing to
// wait for an up-to-date snapshot. Bounded by a 5s per-cluster timeout to
// keep a hung agent from holding the HTTP handler indefinitely.
func (h *ClusterHandler) enrichClusterFresh(ctx context.Context, c sqlc.Cluster) ClusterResponse {
	out := clusterToResponse(c)
	if h.metrics == nil {
		return out
	}
	mctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	snap := h.metrics.Get(mctx, c.ID.String(), c.IsLocal)
	out.CPUPercentage = snap.CPUPercentage
	out.MemoryPercentage = snap.MemoryPercentage
	out.PodCount = snap.PodCount
	out.MetricsServerPresent = snap.MetricsServerPresent
	return out
}

// --- Request / Response types ---

// --- Request / Response types ---

// CreateClusterRequest represents the request body for creating a cluster.
// openapi:request CreateClusterRequest
type CreateClusterRequest struct {
	Name         string          `json:"name" validate:"required,rfc1123"`
	DisplayName  string          `json:"display_name"`
	Description  string          `json:"description"`
	Environment  string          `json:"environment"`
	Region       string          `json:"region"`
	Provider     string          `json:"provider"`
	Distribution string          `json:"distribution"`
	Labels       json.RawMessage `json:"labels"`
	// Annotations carry agent settings at adoption time, notably
	// astronomer.io/agent-privilege-profile (viewer|admin) from the wizard.
	Annotations json.RawMessage `json:"annotations"`
	// ApiServerUrl and CaCertificate are optional direct-access coordinates.
	// They never contain a credential. The download endpoint validates HTTPS,
	// TLS identity and reachability again before minting a short-lived token.
	ApiServerUrl  string `json:"api_server_url"`
	CaCertificate string `json:"ca_certificate"`
}

// UpdateClusterRequest represents the request body for updating a cluster.
// openapi:request UpdateClusterRequest
type UpdateClusterRequest struct {
	DisplayName   *string          `json:"display_name,omitempty"`
	Description   *string          `json:"description,omitempty"`
	Environment   *string          `json:"environment,omitempty"`
	Region        *string          `json:"region,omitempty"`
	Labels        *json.RawMessage `json:"labels,omitempty"`
	Annotations   *json.RawMessage `json:"annotations,omitempty"`
	ApiServerUrl  *string          `json:"api_server_url,omitempty"`
	CaCertificate *string          `json:"ca_certificate,omitempty"`
}

// UnmarshalJSON retains an explicitly supplied JSON null for free-form JSONB
// fields. A pointer alone cannot distinguish null from omission, but the API's
// partial-update contract must preserve omission while retaining the existing
// accepted explicit-null behavior.
func (r *UpdateClusterRequest) UnmarshalJSON(data []byte) error {
	type updateClusterRequestAlias UpdateClusterRequest
	var decoded updateClusterRequestAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if raw, ok := fields["labels"]; ok {
		decoded.Labels = &raw
	}
	if raw, ok := fields["annotations"]; ok {
		decoded.Annotations = &raw
	}
	*r = UpdateClusterRequest(decoded)
	return nil
}

// UpdateRegistryConfigRequest represents the request body for upserting registry config.
// openapi:request UpdateRegistryConfigRequest
type UpdateRegistryConfigRequest struct {
	PrivateRegistryUrl string `json:"private_registry_url"`
	RegistryUsername   string `json:"registry_username"`
	RegistryPassword   string `json:"registry_password"`
	Insecure           bool   `json:"insecure"`
	CaBundle           string `json:"ca_bundle"`
}

// --- Endpoints ---

// List handles GET /api/v1/clusters/.
func (h *ClusterHandler) List(w http.ResponseWriter, r *http.Request) {
	// Clamp limit/offset via the shared helper: limit → [1,200] (default 20),
	// offset → >=0. Previously an int32(queryInt(...)) with no upper bound, so
	// e.g. limit=3e9 overflowed the int32 param to a negative value.
	limitInt, offsetInt := queryLimitOffset(r, 20)
	limit := int32(limitInt)
	offset := int32(offsetInt)
	filterStatus := strings.TrimSpace(r.URL.Query().Get("status"))
	filterProvider := strings.TrimSpace(r.URL.Query().Get("provider"))
	filterEnvironment := strings.TrimSpace(r.URL.Query().Get("environment"))
	filterSearch := strings.TrimSpace(r.URL.Query().Get("search"))
	for name, value := range map[string]string{
		"status": filterStatus, "provider": filterProvider,
		"environment": filterEnvironment, "search": filterSearch,
	} {
		if len(value) > 128 {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, name+" must be at most 128 bytes")
			return
		}
	}
	hasFilters := filterStatus != "" || filterProvider != "" || filterEnvironment != "" || filterSearch != ""

	// Scope filter. The collection gate (RequireCollectionPermission) admits a
	// caller whose only grant is a cluster_role_bindings row, so the page must
	// be narrowed to the clusters they may see. A platform-wide grant (and a
	// superuser) returns all==true and takes the original unfiltered path
	// below, byte-identical to before.
	all, clusterIDs, _, err := h.authz.authorizedScopeIDs(r.Context(), rbac.ResourceClusters, rbac.VerbList, rbac.NarrowedClustersWiden)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to retrieve user permissions")
		return
	}

	var clusters []sqlc.Cluster
	var total int64
	if all && hasFilters {
		filtered, ok := h.queries.(clusterFilteredQuerier)
		if !ok {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StoreUnavailable, "Filtered cluster listing is not available")
			return
		}
		clusters, err = filtered.ListClustersFiltered(r.Context(), sqlc.ListClustersFilteredParams{
			FilterStatus: filterStatus, FilterProvider: filterProvider,
			FilterEnvironment: filterEnvironment, FilterSearch: filterSearch,
			QueryLimit: limit, QueryOffset: offset,
		})
		if err == nil {
			total, err = filtered.CountClustersFiltered(r.Context(), sqlc.CountClustersFilteredParams{
				FilterStatus: filterStatus, FilterProvider: filterProvider,
				FilterEnvironment: filterEnvironment, FilterSearch: filterSearch,
			})
		}
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list filtered clusters")
			return
		}
	} else if all {
		clusters, err = h.queries.ListClusters(r.Context(), sqlc.ListClustersParams{
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list clusters")
			return
		}
		total, err = h.queries.CountClusters(r.Context())
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count clusters")
			return
		}
	} else if hasFilters {
		filtered, ok := h.queries.(clusterFilteredQuerier)
		if !ok {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StoreUnavailable, "Scoped filtered cluster listing is not available")
			return
		}
		clusters, err = filtered.ListClustersFilteredForScopes(r.Context(), sqlc.ListClustersFilteredForScopesParams{
			ClusterIds: clusterIDs, FilterStatus: filterStatus, FilterProvider: filterProvider,
			FilterEnvironment: filterEnvironment, FilterSearch: filterSearch,
			QueryLimit: limit, QueryOffset: offset,
		})
		if err == nil {
			total, err = filtered.CountClustersFilteredForScopes(r.Context(), sqlc.CountClustersFilteredForScopesParams{
				ClusterIds: clusterIDs, FilterStatus: filterStatus, FilterProvider: filterProvider,
				FilterEnvironment: filterEnvironment, FilterSearch: filterSearch,
			})
		}
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list scoped filtered clusters")
			return
		}
	} else {
		scoped, ok := h.queries.(clusterScopeQuerier)
		if !ok {
			// Authorization is wired but the query surface cannot filter. Fail
			// closed rather than serve the whole fleet to a scoped caller.
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Scoped cluster listing is not available")
			return
		}
		clusters, err = scoped.ListClustersForScopes(r.Context(), sqlc.ListClustersForScopesParams{
			ClusterIds:  clusterIDs,
			QueryLimit:  limit,
			QueryOffset: offset,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list clusters")
			return
		}
		// Count over the SAME predicate: an unfiltered total under a filtered
		// page would both leak the fleet size and break the pager.
		total, err = scoped.CountClustersForScopes(r.Context(), clusterIDs)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count clusters")
			return
		}
	}

	// One query for all in-flight decommissions, scoped to this page's cluster
	// IDs → mark the matching rows Decommissioning (avoids an N+1 per cluster).
	// Best-effort: on error we just don't flag anything.
	pageIDs := make([]uuid.UUID, len(clusters))
	for i, c := range clusters {
		pageIDs[i] = c.ID
	}
	// `total` is scope-dependent now, so it can no longer serve as the fetch
	// bound: ListPendingClusterDecommissions is a estate-wide query, and a
	// caller scoped to one cluster would fetch limit=1 and miss their own row
	// whenever another tenant's decommission sorts first. Bound it by the
	// unfiltered cluster count instead — that is the count the invariant in
	// inFlightDecommissionSet actually rests on. Best-effort like the rest of
	// this enrichment: on a count error we fall back to the scoped total.
	fleetTotal := total
	if !all {
		if n, err := h.queries.CountClusters(r.Context()); err == nil {
			fleetTotal = n
		}
	}
	decommissioning := h.inFlightDecommissionSet(r.Context(), pageIDs, fleetTotal)

	enriched := make([]ClusterResponse, 0, len(clusters))
	for _, c := range clusters {
		resp := h.enrichClusterFromCache(r.Context(), c)
		resp.Decommissioning = decommissioning[c.ID]
		enriched = append(enriched, resp)
	}
	RespondPaginated(w, r, enriched, total)
}

// inFlightDecommissionSet returns which of the given cluster IDs have a
// pending/running decommission. Best-effort — returns an empty set on any
// error. The fetch is bounded by fleetTotal, the UNFILTERED (non-tombstoned)
// cluster count, rather than a fixed cap: an in-flight decommission always
// belongs to a still-existing cluster, so the number of in-flight rows can
// never exceed it. The old fixed 500 cap silently rendered clusters past the
// cap as Decommissioning=false once the fleet had >500 concurrent
// decommissions. Do NOT pass a scope-filtered count here — the underlying
// query is estate-wide, so a smaller bound can truncate away this page's rows.
// The already-fetched rows are then filtered to this page's IDs in Go.
func (h *ClusterHandler) inFlightDecommissionSet(ctx context.Context, ids []uuid.UUID, fleetTotal int64) map[uuid.UUID]bool {
	set := map[uuid.UUID]bool{}
	if len(ids) == 0 {
		return set
	}
	want := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	limit := fleetTotal
	if limit > 1<<31-1 {
		limit = 1<<31 - 1
	}
	if limit < 1 {
		limit = 1
	}
	rows, err := h.queries.ListPendingClusterDecommissions(ctx, int32(limit))
	if err != nil {
		return set
	}
	for _, row := range rows {
		if _, ok := want[row.ClusterID]; ok {
			set[row.ClusterID] = true
		}
	}
	return set
}

// Create handles POST /api/v1/clusters/.
func (h *ClusterHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateClusterRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	// Estate-wide cap (migration 051). The 'global' quota plan's
	// max_total_clusters caps how many clusters the platform will
	// hold. Soft enforcement is logged + metric'd; hard returns a 429.
	if h.enforcer != nil {
		if err := h.enforcer.CheckGlobalClusterCreate(r.Context()); err != nil {
			if qe, ok := quota.IsQuotaExceeded(err); ok {
				WriteQuotaExceeded(w, qe)
				return
			}
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.QuotaCheckError, "Failed to evaluate cluster quota")
			return
		}
	}

	labels := req.Labels
	if labels == nil {
		labels = json.RawMessage(`{}`)
	}
	annotations := req.Annotations
	if annotations == nil {
		annotations = json.RawMessage(`{}`)
	}
	if err := validateDirectAccessConfig(req.ApiServerUrl, req.CaCertificate); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	// Downstream-impersonation mode: superuser-only, validated, default off.
	// uuid.Nil as the cluster id is correct here — the row does not exist yet,
	// so there is nothing stored to preserve and no agent that could have
	// advertised the capability, which makes `enforce` unreachable at create
	// time by construction.
	annotations, ok := guardDownstreamImpersonationAnnotation(w, r, h.queries, uuid.Nil, nil, annotations)
	if !ok {
		return
	}
	params := sqlc.CreateClusterParams{
		Name:          req.Name,
		DisplayName:   req.DisplayName,
		Description:   req.Description,
		Environment:   req.Environment,
		Region:        req.Region,
		Provider:      req.Provider,
		Distribution:  req.Distribution,
		Labels:        labels,
		Annotations:   annotations,
		ApiServerUrl:  strings.TrimSpace(req.ApiServerUrl),
		CaCertificate: strings.TrimSpace(req.CaCertificate),
		CreatedByID:   currentUserUUID(r),
	}
	cluster, err := executeClusterMutation(r, h,
		func(q ClusterMutationTx) (sqlc.Cluster, error) { return q.CreateCluster(r.Context(), params) },
		func() (sqlc.Cluster, error) { return h.queries.CreateCluster(r.Context(), params) },
		func(cluster sqlc.Cluster) clusterAuditEvent {
			return clusterAuditEvent{
				action: "cluster.create", resourceType: "cluster", resourceID: cluster.ID.String(), resourceName: cluster.Name,
				status: http.StatusCreated,
				detail: map[string]any{
					"environment": req.Environment, "region": req.Region,
					"provider": req.Provider, "distribution": req.Distribution,
				},
			}
		})
	if err != nil {
		if isUniqueViolation(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, fmt.Sprintf("A cluster named %q already exists", req.Name))
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create cluster")
		return
	}

	h.publishEvent("cluster.created", map[string]any{
		"cluster_id":   cluster.ID.String(),
		"name":         cluster.Name,
		"display_name": cluster.DisplayName,
		"status":       cluster.Status,
	})

	// Wizard step rows. Best-effort: when the registration service
	// isn't wired (legacy test harness), we just skip the timeline
	// rows — the API still returns the cluster body.
	if h.registration != nil {
		_, _ = h.registration.WriteStep(r.Context(), cluster.ID, registration.StepInput{
			StepName: "cluster_created",
			Status:   "success",
			Detail: map[string]any{
				"name":         cluster.Name,
				"display_name": cluster.DisplayName,
			},
		})
		_, _ = h.registration.WriteStep(r.Context(), cluster.ID, registration.StepInput{
			StepName: "manifest_generated",
			Status:   "success",
		})
	}

	h.triggerGrafanaFolders()

	w.Header().Set("Location", "/api/v1/clusters/"+cluster.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, clusterToResponse(cluster))
}

// Get handles GET /api/v1/clusters/{id}/.
func (h *ClusterHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}

	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	out := h.enrichClusterFresh(r.Context(), cluster)
	if latest, derr := h.queries.GetLatestClusterDecommissionByCluster(r.Context(), id); derr == nil {
		out.Decommissioning = latest.Status == "pending" || latest.Status == "running"
	}
	RespondJSON(w, http.StatusOK, out)
}

// Update handles PUT /api/v1/clusters/{id}/.
func (h *ClusterHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}

	var req UpdateClusterRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	if blocked, err := clusterUpdateBlockedByOwnership(r.Context(), h.queries, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to check cluster ownership")
		return
	} else if blocked != "" {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, blocked)
		return
	}

	cluster, err := executeClusterMutation(r, h,
		func(q ClusterMutationTx) (sqlc.Cluster, error) {
			// Lock before merging so two concurrent partial updates cannot restore
			// stale values for fields the second request omitted.
			existing, lockErr := q.GetClusterByIDForUpdate(r.Context(), id)
			if lockErr != nil {
				return sqlc.Cluster{}, lockErr
			}
			params, mergeErr := mergeClusterUpdate(id, existing, req)
			if mergeErr != nil {
				return sqlc.Cluster{}, mergeErr
			}
			annotations, ok := guardDownstreamImpersonationAnnotation(w, r, q, id, existing.Annotations, params.Annotations)
			if !ok {
				return sqlc.Cluster{}, errClusterUpdateResponseWritten
			}
			params.Annotations = annotations
			return q.UpdateCluster(r.Context(), params)
		},
		func() (sqlc.Cluster, error) {
			existing, loadErr := h.queries.GetClusterByID(r.Context(), id)
			if loadErr != nil {
				return sqlc.Cluster{}, loadErr
			}
			params, mergeErr := mergeClusterUpdate(id, existing, req)
			if mergeErr != nil {
				return sqlc.Cluster{}, mergeErr
			}
			annotations, ok := guardDownstreamImpersonationAnnotation(w, r, h.queries, id, existing.Annotations, params.Annotations)
			if !ok {
				return sqlc.Cluster{}, errClusterUpdateResponseWritten
			}
			params.Annotations = annotations
			return h.queries.UpdateCluster(r.Context(), params)
		},
		func(cluster sqlc.Cluster) clusterAuditEvent {
			return clusterAuditEvent{
				action: "cluster.update", resourceType: "cluster", resourceID: cluster.ID.String(), resourceName: cluster.Name,
				status: http.StatusOK,
				detail: map[string]any{
					"display_name": cluster.DisplayName, "description": cluster.Description,
					"environment": cluster.Environment, "region": cluster.Region,
				},
			}
		})
	if err != nil {
		if errors.Is(err, errClusterUpdateResponseWritten) {
			return
		}
		var validationErr *clusterUpdateValidationError
		if errors.As(err, &validationErr) {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, validationErr.Error())
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		} else if errors.Is(err, audit.ErrOutboxUnavailable) {
			respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "Failed to update cluster")
		} else {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to update cluster")
		}
		return
	}

	h.publishEvent("cluster.updated", map[string]any{
		"cluster_id":   cluster.ID.String(),
		"name":         cluster.Name,
		"display_name": cluster.DisplayName,
		"status":       cluster.Status,
	})

	h.triggerGrafanaFolders()
	RespondJSON(w, http.StatusOK, clusterToResponse(cluster))
}

var errClusterUpdateResponseWritten = errors.New("cluster update response already written")

type clusterUpdateValidationError struct{ err error }

func (e *clusterUpdateValidationError) Error() string { return e.err.Error() }
func (e *clusterUpdateValidationError) Unwrap() error { return e.err }

// mergeClusterUpdate implements lossless PATCH semantics over the generated
// request shape. Explicit empty values clear a field; omitted values preserve
// the row locked by the caller.
func mergeClusterUpdate(id uuid.UUID, existing sqlc.Cluster, req UpdateClusterRequest) (sqlc.UpdateClusterParams, error) {
	displayName, description := existing.DisplayName, existing.Description
	environment, region := existing.Environment, existing.Region
	labels, annotations := existing.Labels, existing.Annotations
	if req.DisplayName != nil {
		displayName = *req.DisplayName
	}
	if req.Description != nil {
		description = *req.Description
	}
	if req.Environment != nil {
		environment = *req.Environment
	}
	if req.Region != nil {
		region = *req.Region
	}
	if req.Labels != nil {
		labels = *req.Labels
	}
	if req.Annotations != nil {
		annotations = *req.Annotations
	}
	directURL, directCA := existing.ApiServerUrl, existing.CaCertificate
	if req.ApiServerUrl != nil {
		directURL = strings.TrimSpace(*req.ApiServerUrl)
	}
	if req.CaCertificate != nil {
		directCA = strings.TrimSpace(*req.CaCertificate)
	}
	if req.ApiServerUrl != nil || req.CaCertificate != nil {
		if err := validateDirectAccessConfig(directURL, directCA); err != nil {
			return sqlc.UpdateClusterParams{}, &clusterUpdateValidationError{err: err}
		}
	}
	return sqlc.UpdateClusterParams{
		ID: id, DisplayName: displayName, Description: description,
		Environment: environment, Region: region, Labels: labels, Annotations: annotations,
		ApiServerUrl:  pgtype.Text{String: directURL, Valid: true},
		CaCertificate: pgtype.Text{String: directCA, Valid: true},
	}, nil
}

func clusterUpdateBlockedByOwnership(ctx context.Context, q any, id uuid.UUID) (string, error) {
	ownership, ok, err := readClusterOwnership(ctx, q, id)
	if !ok {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if ownership.ManagedBy != "crd" {
		return "", nil
	}
	return fmt.Sprintf("Cluster is managed by CRD %s/%s %s/%s; edit the Kubernetes resource or transfer ownership before using this API.",
		ownership.ExternalRefApiVersion,
		ownership.ExternalRefKind,
		ownership.ExternalRefNamespace,
		ownership.ExternalRefName,
	), nil
}

func fleetOwnershipFromClusterGet(row sqlc.GetClusterOwnershipRow) sqlc.FleetOwnership {
	return sqlc.FleetOwnership(row)
}

func fleetOwnershipFromClusterSet(row sqlc.SetClusterOwnershipRow) sqlc.FleetOwnership {
	return sqlc.FleetOwnership(row)
}

func readClusterOwnership(ctx context.Context, q any, id uuid.UUID) (sqlc.FleetOwnership, bool, error) {
	if legacy, ok := q.(clusterOwnershipQuerier); ok {
		row, err := legacy.GetClusterOwnership(ctx, id)
		return row, true, err
	}
	if generated, ok := q.(clusterOwnershipSQLQuerier); ok {
		row, err := generated.GetClusterOwnership(ctx, id)
		return fleetOwnershipFromClusterGet(row), true, err
	}
	return sqlc.FleetOwnership{}, false, nil
}

func writeClusterOwnership(ctx context.Context, q any, arg sqlc.SetClusterOwnershipParams) (sqlc.FleetOwnership, bool, error) {
	if legacy, ok := q.(clusterOwnershipTransferQuerier); ok {
		row, err := legacy.SetClusterOwnership(ctx, arg)
		return row, true, err
	}
	if generated, ok := q.(clusterOwnershipSQLQuerier); ok {
		row, err := generated.SetClusterOwnership(ctx, arg)
		return fleetOwnershipFromClusterSet(row), true, err
	}
	return sqlc.FleetOwnership{}, false, nil
}

// TakeoverOwnership handles POST /api/v1/clusters/{id}/ownership/takeover/.
//
// Ordinary PUT/PATCH still rejects CRD-owned rows. This explicit endpoint is
// the operator escape hatch: it clears the CR external_ref metadata and moves
// the row back to API ownership so future UI/API edits are intentional.
func (h *ClusterHandler) TakeoverOwnership(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	type takeoverResult struct {
		previous    sqlc.FleetOwnership
		updated     sqlc.FleetOwnership
		transferred bool
	}
	takeover, err := executeClusterMutation(r, h,
		func(q ClusterMutationTx) (takeoverResult, error) {
			previous, updated, transferred, mutationErr := transferClusterOwnershipToAPI(r.Context(), q, id)
			return takeoverResult{previous: previous, updated: updated, transferred: transferred}, mutationErr
		},
		func() (takeoverResult, error) {
			previous, updated, transferred, mutationErr := transferClusterOwnershipToAPI(r.Context(), h.queries, id)
			return takeoverResult{previous: previous, updated: updated, transferred: transferred}, mutationErr
		},
		func(result takeoverResult) clusterAuditEvent {
			return clusterAuditEvent{
				action: "cluster.ownership.takeover", resourceType: "cluster", resourceID: id.String(), resourceName: cluster.Name,
				status: http.StatusOK,
				detail: map[string]any{
					"previous_managed_by": result.previous.ManagedBy,
					"previous_ref": map[string]string{
						"api_version": result.previous.ExternalRefApiVersion,
						"kind":        result.previous.ExternalRefKind, "namespace": result.previous.ExternalRefNamespace,
						"name": result.previous.ExternalRefName,
					},
					"transferred": result.transferred,
				},
			}
		})
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		case errors.Is(err, errClusterOwnershipTransferUnsupported):
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Only CRD-owned clusters can be transferred through this endpoint")
		default:
			respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "Failed to transfer cluster ownership")
		}
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"id":          takeover.updated.ID.String(),
		"managed_by":  takeover.updated.ManagedBy,
		"transferred": takeover.transferred,
	})
}

func transferClusterOwnershipToAPI(ctx context.Context, q any, id uuid.UUID) (sqlc.FleetOwnership, sqlc.FleetOwnership, bool, error) {
	previous, ok, err := readClusterOwnership(ctx, q, id)
	if !ok {
		return sqlc.FleetOwnership{}, sqlc.FleetOwnership{}, false, fmt.Errorf("cluster ownership transfer query support is not configured")
	}
	if err != nil {
		return sqlc.FleetOwnership{}, sqlc.FleetOwnership{}, false, err
	}
	switch previous.ManagedBy {
	case "crd":
		updated, supported, err := writeClusterOwnership(ctx, q, sqlc.SetClusterOwnershipParams{
			ID:        id,
			ManagedBy: "api",
		})
		if !supported {
			return previous, sqlc.FleetOwnership{}, false, fmt.Errorf("cluster ownership transfer query support is not configured")
		}
		return previous, updated, true, err
	case "api", "ui":
		return previous, previous, false, nil
	default:
		return previous, sqlc.FleetOwnership{}, false, errClusterOwnershipTransferUnsupported
	}
}

// DecommissionPhaseStatus is one entry in the decommission status response.
// Mirrors the worker.tasks.phaseRecord shape so the frontend can render a
// per-phase progress indicator (when it eventually picks up the API).
type DecommissionPhaseStatus struct {
	Name        string         `json:"name"`
	Status      string         `json:"status"`
	StartedAt   string         `json:"started_at,omitempty"`
	CompletedAt string         `json:"completed_at,omitempty"`
	Error       string         `json:"error,omitempty"`
	Detail      map[string]any `json:"detail,omitempty"`
}

// DecommissionStatusResponse is the JSON body returned from
// GET /api/v1/clusters/{id}/decommission/ and POST .../decommission/ (the
// 202-Accepted enqueue path).
type DecommissionStatusResponse struct {
	DecommissionID string                    `json:"decommission_id"`
	ClusterID      string                    `json:"cluster_id"`
	ClusterName    string                    `json:"cluster_name"`
	Status         string                    `json:"status"`
	Attempts       int32                     `json:"attempts"`
	StartedAt      string                    `json:"started_at,omitempty"`
	CompletedAt    string                    `json:"completed_at,omitempty"`
	LastError      string                    `json:"last_error,omitempty"`
	Phases         []DecommissionPhaseStatus `json:"phases"`
	StatusURL      string                    `json:"status_url"`
}

// phaseOrder is the canonical order phases are rendered in the API response.
// We keep this in lockstep with the reconciler's execution order so the UI
// can render a left-to-right progress bar.
var phaseOrder = []string{
	tasks.PhaseCleanupManagedSide,
	tasks.PhaseRevokeAgentToken,
	tasks.PhaseArchiveAudit,
	tasks.PhaseDeleteDependents,
	tasks.PhaseTombstoneCluster,
}

func formatPhases(raw json.RawMessage) []DecommissionPhaseStatus {
	if len(raw) == 0 {
		return formatEmptyPhases()
	}
	type phaseRecord struct {
		Status      string         `json:"status"`
		StartedAt   time.Time      `json:"started_at,omitempty"`
		CompletedAt time.Time      `json:"completed_at,omitempty"`
		Error       string         `json:"error,omitempty"`
		Detail      map[string]any `json:"detail,omitempty"`
	}
	parsed := map[string]phaseRecord{}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return formatEmptyPhases()
	}
	out := make([]DecommissionPhaseStatus, 0, len(phaseOrder))
	for _, name := range phaseOrder {
		rec, ok := parsed[name]
		entry := DecommissionPhaseStatus{Name: name, Status: tasks.PhaseStatusPending}
		if ok {
			entry.Status = rec.Status
			if !rec.StartedAt.IsZero() {
				entry.StartedAt = rec.StartedAt.UTC().Format(time.RFC3339)
			}
			if !rec.CompletedAt.IsZero() {
				entry.CompletedAt = rec.CompletedAt.UTC().Format(time.RFC3339)
			}
			entry.Error = rec.Error
			entry.Detail = rec.Detail
		}
		out = append(out, entry)
	}
	return out
}

func formatEmptyPhases() []DecommissionPhaseStatus {
	out := make([]DecommissionPhaseStatus, 0, len(phaseOrder))
	for _, name := range phaseOrder {
		out = append(out, DecommissionPhaseStatus{Name: name, Status: tasks.PhaseStatusPending})
	}
	return out
}

func renderDecommission(row sqlc.ClusterDecommission, statusURL string) DecommissionStatusResponse {
	out := DecommissionStatusResponse{
		DecommissionID: row.ID.String(),
		ClusterID:      row.ClusterID.String(),
		ClusterName:    row.ClusterName,
		Status:         row.Status,
		Attempts:       row.Attempts,
		LastError:      row.LastError,
		Phases:         formatPhases(row.Phases),
		StatusURL:      statusURL,
	}
	if row.StartedAt.Valid {
		out.StartedAt = row.StartedAt.Time.UTC().Format(time.RFC3339)
	}
	if row.CompletedAt.Valid {
		out.CompletedAt = row.CompletedAt.Time.UTC().Format(time.RFC3339)
	}
	return out
}

// Delete handles DELETE /api/v1/clusters/{id}/.
//
// Previously this hard-deleted the cluster row, leaving residue (agent WS
// tunnel still connected until timeout, managed-side resources still
// running, audit_log rows orphaned, registration tokens not revoked).
// Now the handler inserts a cluster_decommissions row and enqueues the
// reconciler — the worker walks the cleanup phases and tombstones the
// cluster row at the end. The endpoint returns 202 Accepted with the
// decommission ID + a poll URL.
//
// Idempotent: re-DELETE on a cluster with an in-flight decommission returns
// the existing row's status (202 again) rather than creating a duplicate.
func (h *ClusterHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}

	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	if cluster.IsLocal {
		// The local cluster represents the host this server itself runs in;
		// decommissioning it would tear down the management plane. Refuse.
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Cannot decommission the local cluster")
		return
	}

	// Migration 057: refuse or defer when an active maintenance window
	// applies to cluster.delete on this cluster's labels.
	if EnforceMaintenanceWindow(w, r, h.maintenanceGate, "cluster.delete",
		MaintenanceGateClusterLabels(cluster),
		pgtype.UUID{Bytes: id, Valid: true}, pgtype.UUID{}) {
		return
	}

	// force=true (query param) tells the reconciler to skip the managed-side
	// cleanup grace window and tombstone immediately — used when the operator
	// knows the agent is gone and wants the row removed now.
	force := queryBool(r, "force")

	// Idempotency: if there's already an in-flight or succeeded decommission
	// for this cluster, return its status rather than creating a duplicate.
	if existing, lookupErr := h.queries.GetLatestClusterDecommissionByCluster(r.Context(), id); lookupErr == nil {
		if existing.Status == tasks.PhaseStatusPending || existing.Status == tasks.PhaseStatusRunning || existing.Status == tasks.PhaseStatusSucceeded {
			// A force re-delete of an already in-flight decommission escalates it
			// (so a normal delete that's stuck waiting out the grace can be
			// forced through) and nudges the worker to re-run now.
			if force && !existing.Force && existing.Status != tasks.PhaseStatusSucceeded {
				escalated, forceErr := executeClusterMutation(r, h,
					func(q ClusterMutationTx) (sqlc.ClusterDecommission, error) {
						return q.SetClusterDecommissionForce(r.Context(), existing.ID)
					},
					func() (sqlc.ClusterDecommission, error) {
						return h.queries.SetClusterDecommissionForce(r.Context(), existing.ID)
					},
					func(row sqlc.ClusterDecommission) clusterAuditEvent {
						return clusterAuditEvent{
							action: "cluster.decommission.forced", resourceType: "cluster", resourceID: id.String(), resourceName: cluster.Name,
							status: http.StatusAccepted, detail: map[string]any{"decommission_id": row.ID.String()},
						}
					})
				if forceErr != nil {
					respondTransactionalMutationError(w, r, forceErr, http.StatusInternalServerError, apierror.UpdateError, "Failed to force cluster decommission")
					return
				}
				existing = escalated
				h.enqueueClusterDecommission(r.Context(), existing.ID)
			}
			statusURL := fmt.Sprintf("/api/v1/clusters/%s/decommission/", id.String())
			RespondJSON(w, http.StatusAccepted, renderDecommission(existing, statusURL))
			return
		}
		// `failed` → fall through and create a fresh decommission row; the
		// previous attempt remains in the DB for forensics.
	}

	requestedBy := pgtype.UUID{}
	if userID := currentUserUUID(r); userID.Valid {
		requestedBy = userID
	}

	params := sqlc.CreateClusterDecommissionParams{
		ClusterID:     id,
		RequestedByID: requestedBy,
		ClusterName:   cluster.Name,
		Force:         force,
	}
	created, err := executeClusterMutation(r, h,
		func(q ClusterMutationTx) (clusterDecommissionMutationResult, error) {
			row, enqueued, mutationErr := h.createClusterDecommission(r.Context(), q, params)
			return clusterDecommissionMutationResult{row: row, enqueued: enqueued}, mutationErr
		},
		func() (clusterDecommissionMutationResult, error) {
			row, enqueued, mutationErr := h.createClusterDecommission(r.Context(), h.queries, params)
			return clusterDecommissionMutationResult{row: row, enqueued: enqueued}, mutationErr
		},
		func(result clusterDecommissionMutationResult) clusterAuditEvent {
			return clusterAuditEvent{
				action: "cluster.decommission.requested", resourceType: "cluster", resourceID: id.String(), resourceName: cluster.Name,
				status: http.StatusAccepted, detail: map[string]any{"decommission_id": result.row.ID.String()},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateDecommissionFailed, "Failed to enqueue cluster decommission")
		return
	}
	row, enqueued := created.row, created.enqueued

	if !enqueued {
		h.enqueueClusterDecommission(r.Context(), row.ID)
	}

	h.publishEvent("cluster.decommission_enqueued", map[string]any{
		"cluster_id":      id.String(),
		"decommission_id": row.ID.String(),
	})

	h.triggerGrafanaFolders()
	statusURL := fmt.Sprintf("/api/v1/clusters/%s/decommission/", id.String())
	RespondJSON(w, http.StatusAccepted, renderDecommission(row, statusURL))
}

// tunnelQueueName is the asynq queue drained by the server pod's in-process
// worker (which holds the WS tunnel hub). Decommission tasks go here so the
// managed-side cleanup phase can reach a connected agent. Matches the literal
// used by the registration apply path.
const tunnelQueueName = "tunnel"

func (h *ClusterHandler) createClusterDecommission(ctx context.Context, q any, arg sqlc.CreateClusterDecommissionParams) (sqlc.ClusterDecommission, bool, error) {
	atomicQ, ok := q.(clusterDecommissionTaskOutboxQuerier)
	if ok && h.taskOutbox != nil {
		decommissionID := uuid.New()
		task, err := tasks.NewClusterDecommissionTask(decommissionID)
		if err != nil {
			return sqlc.ClusterDecommission{}, false, err
		}
		payload := observability.EnrichTaskPayload(ctx, task.Payload(), middleware.GetCorrelationID(ctx))
		row, err := atomicQ.CreateClusterDecommissionWithTaskOutbox(ctx, sqlc.CreateClusterDecommissionWithTaskOutboxParams{
			ID:            decommissionID,
			ClusterID:     arg.ClusterID,
			RequestedByID: arg.RequestedByID,
			ClusterName:   arg.ClusterName,
			Force:         arg.Force,
			DedupeKey:     pgtype.Text{String: fmt.Sprintf("cluster_decommission:%s", decommissionID.String()), Valid: true},
			TaskType:      task.Type(),
			Payload:       payload,
			// "tunnel" queue, not "default": the decommission's managed-side
			// cleanup phase needs the WS tunnel hub, which lives only in the
			// server pod's in-process asynq worker (it drains "tunnel").
			QueueName:           tunnelQueueName,
			MaxRetry:            3,
			MaxDeliveryAttempts: 20,
			NextAttemptAt:       pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		})
		return row, true, err
	}
	creator, ok := q.(interface {
		CreateClusterDecommission(context.Context, sqlc.CreateClusterDecommissionParams) (sqlc.ClusterDecommission, error)
	})
	if !ok {
		return sqlc.ClusterDecommission{}, false, fmt.Errorf("cluster decommission query support is not configured")
	}
	row, err := creator.CreateClusterDecommission(ctx, arg)
	return row, false, err
}

func (h *ClusterHandler) enqueueClusterDecommission(ctx context.Context, decommissionID uuid.UUID) {
	task, err := tasks.NewClusterDecommissionTask(decommissionID)
	if err != nil {
		return
	}
	payload := observability.EnrichTaskPayload(ctx, task.Payload(), middleware.GetCorrelationID(ctx))
	task = asynq.NewTask(task.Type(), payload)
	if h.taskOutbox != nil {
		if _, err := tasks.EnqueueTaskOutbox(ctx, h.taskOutbox, task, tasks.TaskOutboxOptions{
			DedupeKey:           fmt.Sprintf("cluster_decommission:%s", decommissionID.String()),
			QueueName:           tunnelQueueName, // needs the hub — see createClusterDecommission.
			MaxRetry:            3,
			MaxDeliveryAttempts: 20,
		}); err == nil {
			return
		}
	}
	if h.decommissionQueue != nil {
		_, _ = h.decommissionQueue.Enqueue(task, asynq.Queue(tunnelQueueName), asynq.MaxRetry(3))
	}
}

// GetDecommission handles GET /api/v1/clusters/{id}/decommission/.
// Returns the latest decommission row's status (idempotent — callers can
// poll). 404 when no decommission has ever been enqueued for the cluster.
func (h *ClusterHandler) GetDecommission(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	row, err := h.queries.GetLatestClusterDecommissionByCluster(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "No decommission for cluster")
		return
	}
	statusURL := fmt.Sprintf("/api/v1/clusters/%s/decommission/", id.String())
	RespondJSON(w, http.StatusOK, renderDecommission(row, statusURL))
}

// GetHealth handles GET /api/v1/clusters/{id}/health/.
func (h *ClusterHandler) GetHealth(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}

	health, err := h.queries.GetClusterHealthStatus(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Health status not found for cluster")
		return
	}

	RespondJSON(w, http.StatusOK, health)
}

// ClusterConditionResponse is the JSON shape returned from
// GET /api/v1/clusters/{id}/conditions/. Names mirror metav1.Condition so
// the frontend can render Kubernetes-style pills without translation.
type ClusterConditionResponse struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason"`
	Message            string `json:"message"`
	LastTransitionTime string `json:"last_transition_time"`
	LastProbeTime      string `json:"last_probe_time"`
}

// ListConditions handles GET /api/v1/clusters/{id}/conditions/. Returns
// one entry per condition type that the health-check worker has written
// (Connected, AgentReachable, GatewayAPISupported, ...). Returns an empty
// list (not 404) for a cluster that hasn't had a health-check tick yet —
// the UI then shows neutral pills rather than an error toast.
func (h *ClusterHandler) ListConditions(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	rows, err := h.queries.ListClusterConditions(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to list conditions")
		return
	}
	out := make([]ClusterConditionResponse, 0, len(rows))
	for _, c := range rows {
		out = append(out, ClusterConditionResponse{
			Type:               c.Type,
			Status:             c.Status,
			Reason:             c.Reason,
			Message:            c.Message,
			LastTransitionTime: c.LastTransitionTime.UTC().Format(time.RFC3339),
			LastProbeTime:      c.LastProbeTime.UTC().Format(time.RFC3339),
		})
	}
	// The query returns the complete condition set for this cluster.
	RespondList(w, out, NewPagination(len(out), len(out), 0, len(out)))
}

// GenerateRegistrationToken handles POST /api/v1/clusters/{id}/register/.
func (h *ClusterHandler) GenerateRegistrationToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}

	// Verify cluster exists.
	if _, err := h.queries.GetClusterByID(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	// Generate a random registration token.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TokenError, "Failed to generate registration token")
		return
	}
	tokenStr := base64.URLEncoding.EncodeToString(b)

	params := sqlc.CreateClusterRegistrationTokenParams{
		ClusterID: id,
		TokenHash: auth.HashOpaqueToken(tokenStr),
		ExpiresAt: time.Now().Add(h.registrationTokenTTL),
	}
	token, err := executeClusterMutation(r, h,
		func(q ClusterMutationTx) (sqlc.ClusterRegistrationToken, error) {
			return q.CreateClusterRegistrationToken(r.Context(), params)
		},
		func() (sqlc.ClusterRegistrationToken, error) {
			return h.queries.CreateClusterRegistrationToken(r.Context(), params)
		},
		func(token sqlc.ClusterRegistrationToken) clusterAuditEvent {
			return clusterAuditEvent{
				action: "cluster.register_token", resourceType: "cluster", resourceID: id.String(),
				status: http.StatusCreated,
				detail: map[string]any{"token_id": token.ID.String(), "expires_at": token.ExpiresAt.UTC().Format(time.RFC3339)},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create registration token")
		return
	}
	token.Token = tokenStr

	RespondJSON(w, http.StatusCreated, token)
}

// RotateAgentToken handles POST /api/v1/clusters/{id}/agent-token/rotate/.
// It sets rotation_pending_at on the cluster's durable agent token so the
// agent's NEXT CONNECT performs the grace rotation (mint fresh, demote old to
// previous, deliver fresh in the ACK). It does NOT change the live token, so
// there is no mid-rotation lockout: the agent keeps using its held token until
// it adopts the freshly-minted one.
func (h *ClusterHandler) RotateAgentToken(w http.ResponseWriter, r *http.Request) {
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "cluster mutation transaction runner is not configured")
		return
	}
	r = r.WithContext(withOperationIdempotency(r, "agent_token_rotation"))
	digest, err := canonicalOperationRequestDigest(struct {
		ClusterID string `json:"cluster_id"`
	}{ClusterID: id.String()})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode token rotation request")
		return
	}
	receipt := AgentTokenRotationReceipt{ClusterID: id.String(), RotationPending: true, Message: "rotation will complete on the agent's next connect"}
	receipt, err = executeClusterMutation(r, h, func(q ClusterMutationTx) (AgentTokenRotationReceipt, error) {
		idemQ, ok := q.(resourceOperationIdempotencyQuerier)
		if !ok {
			return AgentTokenRotationReceipt{}, errors.New("cluster token rotation idempotency store is not configured")
		}
		_, stored, replay, claimErr := claimOperationReceipt[AgentTokenRotationReceipt](r.Context(), idemQ, "cluster_agent_token_rotations", digest)
		if claimErr != nil {
			return AgentTokenRotationReceipt{}, claimErr
		}
		if replay {
			return stored, nil
		}
		rows, mutationErr := q.SetClusterAgentTokenRotationPending(r.Context(), id)
		if mutationErr != nil {
			return AgentTokenRotationReceipt{}, mutationErr
		}
		if rows == 0 {
			return AgentTokenRotationReceipt{}, errAgentTokenRotationIneligible
		}
		if auditErr := recordAuditOutbox(r, q, "agent.token.rotate.requested", "cluster", id.String(), cluster.Name, http.StatusAccepted, map[string]any{"cluster_id": id.String(), "trigger": "admin_api", "rows_affected": rows}); auditErr != nil {
			return AgentTokenRotationReceipt{}, auditErr
		}
		if attachErr := attachOperationReceipt(r.Context(), idemQ, "cluster_agent_token_rotations", id, digest, receipt); attachErr != nil {
			return AgentTokenRotationReceipt{}, attachErr
		}
		return receipt, nil
	}, func() (AgentTokenRotationReceipt, error) {
		return AgentTokenRotationReceipt{}, errors.New("cluster token rotation transaction runner is not configured")
	}, func(AgentTokenRotationReceipt) clusterAuditEvent { return clusterAuditEvent{} })
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different token rotation")
			return
		}
		if errors.Is(err, errAgentTokenRotationIneligible) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "No agent token eligible for rotation: none is active or a rotation is already in flight")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "Failed to request agent token rotation")
		return
	}
	RespondAcceptedOperation(w, "/api/v1/clusters/"+id.String()+"/", receipt)
}

type AgentTokenRotationReceipt struct {
	ClusterID       string `json:"cluster_id"`
	RotationPending bool   `json:"rotation_pending"`
	Message         string `json:"message"`
}

// RevokeAgentToken handles POST /api/v1/clusters/{id}/agent-token/revoke/.
// It hard-revokes the durable agent token (sets revoked_at, clears the grace
// previous_token_hash). After revoke, the agent's token fails validation, so
// its next CONNECT is denied (401/policy violation) and an operator must
// re-import the cluster to issue a fresh credential.
func (h *ClusterHandler) RevokeAgentToken(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	_, err = executeClusterMutation(r, h,
		func(q ClusterMutationTx) (int64, error) {
			rows, mutationErr := q.RevokeClusterAgentToken(r.Context(), id)
			if mutationErr == nil && rows == 0 {
				mutationErr = errAgentTokenNotActive
			}
			return rows, mutationErr
		},
		func() (int64, error) {
			rows, mutationErr := h.queries.RevokeClusterAgentToken(r.Context(), id)
			if mutationErr == nil && rows == 0 {
				mutationErr = errAgentTokenNotActive
			}
			return rows, mutationErr
		},
		func(rows int64) clusterAuditEvent {
			return clusterAuditEvent{
				action: "agent.token.revoked", resourceType: "cluster", resourceID: id.String(), resourceName: cluster.Name,
				status: http.StatusOK, detail: map[string]any{"cluster_id": id.String(), "trigger": "admin_api", "rows_affected": rows},
			}
		})
	if err != nil {
		if errors.Is(err, errAgentTokenNotActive) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster has no active agent token to revoke")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DBError, "Failed to revoke agent token")
		return
	}
	// Sever the live tunnel NOW so a compromised/rogue agent loses access
	// immediately rather than persisting on its already-authenticated session
	// until it happens to reconnect. The DB revoke above guarantees the
	// subsequent CONNECT is denied; this just collapses the window to ~0.
	disconnected := false
	if h.agentDisconnector != nil {
		disconnected = h.agentDisconnector.Disconnect(id.String())
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"cluster_id":      id.String(),
		"revoked":         true,
		"session_severed": disconnected,
		"message":         "agent token revoked; re-import the cluster to issue a new credential",
	})
}

// GetRegistryConfig handles GET /api/v1/clusters/{id}/registry/.
func (h *ClusterHandler) GetRegistryConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}

	config, err := h.queries.GetClusterRegistryConfig(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Registry config not found for cluster")
		return
	}

	// Never return the raw registry password or its ciphertext. Map through
	// the shared DTO so the secret is redacted to RegistryPasswordSentinel
	// (mirrors the newer /registries handler) while url, username, insecure,
	// and CA bundle remain intact.
	RespondJSON(w, http.StatusOK, clusterRegistryConfigToResponse(config))
}

// GetManifest handles GET /api/v1/clusters/{id}/manifest/.
// Returns the agent install manifest as raw YAML for curl-based installation.
func (h *ClusterHandler) GetManifest(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	// Generate a fresh registration token. T6.078 — short TTL: the
	// manifest is consumed by a single `kubectl apply` shortly after
	// download, so a 1-hour window is plenty in normal operation. A
	// stale token left in scrollback poses a smaller blast radius
	// than the historical 24h. Operators who need a longer window
	// can keep regenerating from the wizard.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TokenError, "Failed to generate registration token")
		return
	}
	tokenStr := base64.URLEncoding.EncodeToString(b)
	params := sqlc.CreateClusterRegistrationTokenParams{
		ClusterID: id,
		TokenHash: auth.HashOpaqueToken(tokenStr),
		ExpiresAt: time.Now().Add(h.registrationTokenTTL),
	}
	token, err := executeClusterMutation(r, h,
		func(q ClusterMutationTx) (sqlc.ClusterRegistrationToken, error) {
			return q.CreateClusterRegistrationToken(r.Context(), params)
		},
		func() (sqlc.ClusterRegistrationToken, error) {
			return h.queries.CreateClusterRegistrationToken(r.Context(), params)
		},
		func(token sqlc.ClusterRegistrationToken) clusterAuditEvent {
			return clusterAuditEvent{
				action: "cluster.register_token", resourceType: "cluster", resourceID: id.String(), resourceName: cluster.Name,
				status: http.StatusOK,
				detail: map[string]any{
					"token_id": token.ID.String(), "source": "manifest_download",
					"expires_at": token.ExpiresAt.UTC().Format(time.RFC3339),
				},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create registration token")
		return
	}
	token.Token = tokenStr

	manifest := h.renderAgentInstallManifest(cluster, tokenStr, agentServerURLFor(r.Context(), h.queries, r))

	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="astronomer-agent-%s.yaml"`, cluster.Name))
	// Expose the freshly-minted registration token via header so the
	// wizard can render the Rancher-style one-liner without having
	// to grep it back out of the YAML body. Token is short-lived (1h
	// per T6.078) and the manifest body already contains it in
	// plaintext, so this isn't widening the secret's exposure surface.
	w.Header().Set("X-Astronomer-Registration-Token", tokenStr)
	w.Header().Set("Access-Control-Expose-Headers", "X-Astronomer-Registration-Token")
	_, _ = w.Write([]byte(manifest))
}

// GetManifestByToken handles GET /api/v1/register/{token}.yaml.
//
// Public (unauthenticated) endpoint that returns the agent install
// manifest for the cluster the token belongs to. The token IS the
// credential — same trust model as the manifest itself, which embeds
// the token in plaintext. This exists so operators can run the
// Rancher-style one-liner:
//
//	curl -sfL https://<server>/api/v1/register/<token>.yaml | kubectl apply --server-side --field-manager=astronomer-bootstrap -f -
//
// rather than copy-paste a multi-kilobyte heredoc.
func (h *ClusterHandler) GetManifestByToken(w http.ResponseWriter, r *http.Request) {
	rawToken := chi.URLParam(r, "token")
	tokenStr := strings.TrimSuffix(rawToken, ".yaml")
	if tokenStr == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidToken, "Missing registration token")
		return
	}
	token, err := h.queries.GetRegistrationTokenByToken(r.Context(), tokenStr)
	if err != nil {
		// GetRegistrationTokenByToken already filters expired rows
		// (WHERE expires_at > now()), so any error here is "no such
		// token". 404 keeps the response opaque.
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Registration token not found or expired")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), token.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	manifest := h.renderAgentInstallManifest(cluster, tokenStr, agentServerURLFor(r.Context(), h.queries, r))

	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(manifest))
}

// ListConditionRemediation handles GET /api/v1/clusters/{id}/condition-remediation/.
// Returns the 50 most recent remediation attempts (success / failed /
// skipped) for the cluster, ordered newest-first. Read by the
// cluster-detail page to show on-call "what did the controller do
// when this condition went red?" — closes the loop the
// cluster_conditions table opens.
func (h *ClusterHandler) ListConditionRemediation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if _, err := h.queries.GetClusterByID(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	rows, err := h.queries.ListClusterConditionRemediationByCluster(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list remediation attempts")
		return
	}
	// The endpoint returns the complete bounded remediation history.
	RespondList(w, rows, NewPagination(len(rows), len(rows), 0, len(rows)))
}

// GetSignedManifest handles GET /api/v1/register/signed/{cluster_id}.
//
// Public (unauthenticated) endpoint guarded by a short-TTL HMAC
// signature over (cluster_id, expiry) instead of a registration token.
// The wizard mints the URL via SignManifestURL with a 15-minute window;
// a tampered or expired URL is rejected (404, opaque) before any DB
// work. On success it mints a fresh registration token and returns the
// install manifest, identically to GetManifestByToken.
func (h *ClusterHandler) GetSignedManifest(w http.ResponseWriter, r *http.Request) {
	if len(h.manifestSigningSecret) == 0 {
		http.Error(w, "signed manifest URLs not enabled", http.StatusServiceUnavailable)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	expiry, err := strconv.ParseInt(r.URL.Query().Get("expires"), 10, 64)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidToken, "Invalid or missing expiry")
		return
	}
	if err := h.verifyManifestSignature(id, expiry, r.URL.Query().Get("sig")); err != nil {
		// Opaque 404 — don't distinguish expired from tampered.
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Manifest link invalid or expired")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	// Mint a fresh, short-lived registration token for this download.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.TokenError, "Failed to generate registration token")
		return
	}
	tokenStr := base64.URLEncoding.EncodeToString(b)
	// Cap the minted token's lifetime to the remaining signature window
	// rather than a flat hour, so a replayed signed URL can't mint a
	// token that outlives the URL that authorized it.
	tokenExpiry := time.Unix(expiry, 0)
	if max := time.Now().Add(h.registrationTokenTTL); tokenExpiry.After(max) {
		tokenExpiry = max
	}
	if _, err := h.queries.CreateClusterRegistrationToken(r.Context(), sqlc.CreateClusterRegistrationTokenParams{
		ClusterID: id,
		TokenHash: auth.HashOpaqueToken(tokenStr),
		ExpiresAt: tokenExpiry,
	}); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create registration token")
		return
	}

	manifest := h.renderAgentInstallManifest(cluster, tokenStr, agentServerURLFor(r.Context(), h.queries, r))
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(manifest))
}

// GetCABundle handles GET /api/v1/register/ca.crt.
//
// Public (unauthenticated) endpoint that returns the operator-provided
// PEM bundle from platform_settings["registration.ca_bundle"], so the
// Rancher-style `curl --cacert /tmp/astronomer-ca.crt -sfL …` variant
// of the registration one-liner works end-to-end. Returns 404 when no
// bundle is configured (either because the platform runs on a public
// CA or because the operator hasn't pasted one yet) — the wizard
// guards the variant on `registration.tls_mode == "private_ca"` so a
// 404 from here is the consistent "nothing to download" signal.
func (h *ClusterHandler) GetCABundle(w http.ResponseWriter, r *http.Request) {
	pem := registrationCABundle(r.Context(), h.queries)
	if pem == "" {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "No CA bundle configured for cluster registration")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="astronomer-ca.crt"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(pem + "\n"))
}

// registrationCABundle returns the operator-provided CA PEM bundle from
// platform_settings[registration.ca_bundle], or "" when none is configured.
// This is the single source of truth for the tunnel CA pin and is shared by the
// HTTP GetCABundle endpoint and the agent install-manifest renderers.
func registrationCABundle(ctx context.Context, q registrationCAQuerier) string {
	if q == nil {
		return ""
	}
	row, err := q.GetPlatformSetting(ctx, "registration.ca_bundle")
	if err != nil || len(row.Value) == 0 {
		return ""
	}
	// platform_settings.value is JSONB carrying a JSON-encoded string; unwrap it.
	var pem string
	_ = json.Unmarshal(row.Value, &pem)
	return strings.TrimSpace(pem)
}

// registrationCAQuerier is the slice of the queries surface registrationCABundle
// needs, so the helper is callable from any caller holding GetPlatformSetting.
type registrationCAQuerier interface {
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
}

// GenerateKubeconfig handles POST /api/v1/clusters/{id}/generate-kubeconfig/.
// Returns a short-lived, read-only kubeconfig routed through Astronomer. This
// audited proxy path complements the separately supported hardened direct
// kubeconfig capability for clusters whose API endpoint is explicitly set.
func (h *ClusterHandler) GenerateKubeconfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	serverURL := agentServerURLFor(r.Context(), h.queries, r)
	userEmail := authenticatedEmail(r)
	token, expiresAt, err := h.mintKubeconfigToken(r, cluster)
	if err != nil {
		slog.Default().ErrorContext(r.Context(), "mint proxy kubeconfig credential", "cluster_id", cluster.ID.String(), "error", err)
		if errors.Is(err, audit.ErrOutboxUnavailable) {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable, "Mandatory audit storage is unavailable; the proxy credential was not issued")
		} else {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.StatusError, "A short-lived proxy credential could not be issued")
		}
		return
	}
	kubeconfig := buildProxyKubeconfig(cluster, userEmail, serverURL, token)
	yamlBytes, err := yaml.Marshal(kubeconfig)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.RenderError, "Failed to render kubeconfig")
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-proxy-kubeconfig.yaml"`, cluster.Name))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Astronomer-Kubeconfig-Mode", "proxy")
	w.Header().Set("X-Astronomer-Credential-Expires-At", expiresAt.Format(time.RFC3339))
	_, _ = w.Write(yamlBytes)
}

// PreviewKubeconfig handles GET /api/v1/clusters/{id}/kubeconfig-preview/.
// Returns the proxy kubeconfig as JSON for UI display.
func (h *ClusterHandler) PreviewKubeconfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	serverURL := agentServerURLFor(r.Context(), h.queries, r)
	userEmail := authenticatedEmail(r)
	// Preview is a UI display (JSON), not a download for kubectl — keep the
	// placeholder here rather than minting a live credential into a view that
	// may be rendered/logged. The download path (GenerateKubeconfig) mints.
	kubeconfig := buildProxyKubeconfig(cluster, userEmail, serverURL, "")
	RespondJSON(w, http.StatusOK, kubeconfig)
}

// GetMetrics handles GET /api/v1/clusters/{id}/metrics/.
// Returns CPU/memory/pod aggregate metrics derived from health snapshots.
func (h *ClusterHandler) GetMetrics(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	isConnected := cluster.LastHeartbeat.Valid && time.Since(cluster.LastHeartbeat.Time) < 5*time.Minute
	metrics := map[string]any{
		"cluster_id":         cluster.ID.String(),
		"cluster_name":       cluster.Name,
		"status":             cluster.Status,
		"is_connected":       isConnected,
		"kubernetes_version": cluster.KubernetesVersion,
		"node_count":         cluster.NodeCount,
		"agent_version":      cluster.AgentVersion,
	}
	if cluster.LastHeartbeat.Valid {
		metrics["last_heartbeat"] = cluster.LastHeartbeat.Time.UTC().Format(time.RFC3339)
	} else {
		metrics["last_heartbeat"] = nil
	}
	if health, err := h.queries.GetClusterHealthStatus(r.Context(), id); err == nil {
		metrics["cpu_usage_percent"] = health.CpuUsagePercent
		metrics["memory_usage_percent"] = health.MemoryUsagePercent
		metrics["pod_count"] = health.PodCount
		metrics["conditions"] = health.Conditions
		metrics["last_health_check"] = health.LastCheck.UTC().Format(time.RFC3339)
	}
	RespondJSON(w, http.StatusOK, metrics)
}

// GetMetricsSummary handles GET /api/v1/clusters/{id}/metrics/summary/.
// Returns a metrics summary using cached health data.
func (h *ClusterHandler) GetMetricsSummary(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	cluster, err := h.queries.GetClusterByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}
	nodeCount := int(cluster.NodeCount)
	cpuUsage := 0.0
	memUsage := 0.0
	podCount := 0
	if health, err := h.queries.GetClusterHealthStatus(r.Context(), id); err == nil {
		cpuUsage = health.CpuUsagePercent
		memUsage = health.MemoryUsagePercent
		podCount = int(health.PodCount)
	}
	podCapacity := 110 * nodeCount
	if podCapacity == 0 {
		podCapacity = 110
	}
	summary := map[string]any{
		"cpu_usage":         cpuUsage,
		"cpu_capacity":      100,
		"cpu_percentage":    cpuUsage,
		"memory_usage":      memUsage,
		"memory_capacity":   100,
		"memory_percentage": memUsage,
		"pod_count":         podCount,
		"pod_capacity":      podCapacity,
		"node_count":        nodeCount,
		"network_receive":   0,
		"network_transmit":  0,
		"disk_usage":        0,
		"disk_capacity":     0,
	}
	RespondJSON(w, http.StatusOK, summary)
}

func buildProxyKubeconfig(cluster sqlc.Cluster, userEmail, serverURL, token string) map[string]any {
	if userEmail == "" {
		userEmail = "user"
	}
	// When we couldn't mint a token (unauthenticated caller or store without
	// token support) fall back to the placeholder so the YAML still renders and
	// the user can paste a token by hand — the old always-broken behaviour, now
	// only the degraded path.
	if token == "" {
		token = "REPLACE_WITH_API_TOKEN"
	}
	proxyURL := fmt.Sprintf("%s/api/v1/clusters/%s/k8s", serverURL, cluster.ID.String())
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Config",
		"clusters": []map[string]any{
			{
				"cluster": map[string]any{
					"server":                   proxyURL,
					"insecure-skip-tls-verify": false,
				},
				"name": cluster.Name,
			},
		},
		"contexts": []map[string]any{
			{
				"context": map[string]any{
					"cluster": cluster.Name,
					"user":    userEmail,
				},
				"name": cluster.Name + "-context",
			},
		},
		"current-context": cluster.Name + "-context",
		"users": []map[string]any{
			{
				"name": userEmail,
				"user": map[string]any{
					"token": token,
				},
			},
		},
	}
}

const kubeconfigTokenTTL = time.Hour

// mintKubeconfigToken creates a short-lived, caller-owned, read-only API token.
// The token row and sanitized audit intent share one commit decision, so an
// auditable credential is the only credential that can become usable.
func (h *ClusterHandler) mintKubeconfigToken(r *http.Request, cluster sqlc.Cluster) (string, time.Time, error) {
	if h == nil || h.runTx == nil {
		return "", time.Time{}, audit.ErrOutboxUnavailable
	}
	userID := currentUserUUID(r)
	if !userID.Valid {
		return "", time.Time{}, errors.New("authenticated user identity is unavailable")
	}
	plaintext, hash, prefix, err := generateAPIToken()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("generate credential: %w", err)
	}
	expiresAt := time.Now().UTC().Add(kubeconfigTokenTTL)
	err = h.runTx(r.Context(), func(q ClusterMutationTx) error {
		if _, createErr := q.CreateAPIToken(r.Context(), sqlc.CreateAPITokenParams{
			UserID:       uuid.UUID(userID.Bytes),
			Name:         fmt.Sprintf("kubeconfig-%s", cluster.Name),
			TokenHash:    hash,
			Prefix:       prefix,
			ExpiresAt:    pgtype.Timestamptz{Time: expiresAt, Valid: true},
			Scopes:       json.RawMessage(`["read"]`),
			AllowedCidrs: "",
		}); createErr != nil {
			return fmt.Errorf("persist credential: %w", createErr)
		}
		return recordAuditOutbox(r, q, "cluster.proxy_kubeconfig.issued", "cluster", cluster.ID.String(), cluster.Name, http.StatusOK, map[string]any{
			"mode": "proxy", "access": "read_only", "expires_at": expiresAt.Format(time.RFC3339),
		})
	})
	if err != nil {
		return "", time.Time{}, err
	}
	return plaintext, expiresAt, nil
}

func (h *ClusterHandler) renderAgentInstallManifest(cluster sqlc.Cluster, token, serverURL string) string {
	annotations := clusterAnnotations(cluster.Annotations)
	agentImage := "ghcr.io/alphabravo-oss/astronomer-go-agent:latest"
	if h != nil && h.agentImage != "" {
		agentImage = h.agentImage
	}
	// Server-CA pin: populate the CA bundle + checksum from the operator-provided
	// registration.ca_bundle. Empty when no private CA is configured, in which
	// case the agent falls back to the OS trust store (no behavior change).
	caPEM := ""
	if h != nil {
		caPEM = registrationCABundle(context.Background(), h.queries)
	}
	return agenttemplate.RenderInstallYAML(agenttemplate.InstallTemplateData{
		ServerURL:            serverURL,
		ClusterID:            cluster.ID.String(),
		RegistrationToken:    token,
		CACert:               caPEM,
		CAChecksum:           agenttemplate.CAChecksumFromPEM(caPEM),
		AgentImage:           agentImage,
		PrivilegeProfile:     agenttemplate.NormalizePrivilegeProfile(annotations[agenttemplate.PrivilegeProfileAnnotation]),
		ServiceAccountName:   strings.TrimSpace(annotations[agenttemplate.AgentServiceAccountNameAnnotation]),
		PodLabels:            clusterAgentPodLabels(annotations),
		SystemArtifactURL:    h.systemArtifactURL,
		SystemArtifactDigest: h.systemArtifactDigest,
		SystemOIDCIssuer:     h.systemOIDCIssuer,
		SystemOIDCIdentity:   h.systemOIDCIdentity,
	})
}

func clusterAgentPrivilegeProfile(raw json.RawMessage) string {
	return agenttemplate.NormalizePrivilegeProfile(clusterAnnotations(raw)[agenttemplate.PrivilegeProfileAnnotation])
}

func clusterAnnotations(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return map[string]string{}
	}
	var annotations map[string]string
	if err := json.Unmarshal(raw, &annotations); err != nil {
		return map[string]string{}
	}
	return annotations
}

func clusterAgentPodLabels(annotations map[string]string) map[string]string {
	raw := strings.TrimSpace(annotations[agenttemplate.AgentPodLabelsAnnotation])
	if raw == "" {
		return nil
	}
	var labels map[string]string
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		return nil
	}
	return labels
}

func agentServerURL(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "" {
		scheme = "http"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = forwarded
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

// agentServerURLFor prefers platform_configuration.server_url (the
// operator-set authoritative public URL — includes any non-default port)
// over the request-derived value. The nginx → traefik → server chain
// strips the :8080 from the inbound Host header so the request-derived
// URL drops the port and the agent can't connect back. The platform
// config row is seeded at bootstrap from the Helm value so it always
// carries the port.
func agentServerURLFor(ctx context.Context, q interface {
	GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)
}, r *http.Request) string {
	if q != nil {
		if cfg, err := q.GetPlatformConfig(ctx); err == nil {
			if u := strings.TrimSpace(cfg.ServerUrl); u != "" {
				return strings.TrimRight(u, "/")
			}
		}
	}
	return agentServerURL(r)
}

func authenticatedEmail(r *http.Request) string {
	if user, ok := middleware.GetAuthenticatedUser(r.Context()); ok && user != nil {
		return user.Email
	}
	return ""
}

// UpdateRegistryConfig handles PUT /api/v1/clusters/{id}/registry/.
func (h *ClusterHandler) UpdateRegistryConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}

	var req UpdateRegistryConfigRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	registryPassword, registryPasswordEncrypted, err := h.encryptLegacyRegistryPassword(req.RegistryPassword)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CryptoError, "Failed to encrypt registry password")
		return
	}

	params := sqlc.UpsertClusterRegistryConfigParams{
		ClusterID:                 id,
		PrivateRegistryUrl:        req.PrivateRegistryUrl,
		RegistryUsername:          req.RegistryUsername,
		RegistryPassword:          registryPassword,
		RegistryPasswordEncrypted: registryPasswordEncrypted,
		Insecure:                  req.Insecure,
		CaBundle:                  req.CaBundle,
	}
	config, err := executeClusterMutation(r, h,
		func(q ClusterMutationTx) (sqlc.ClusterRegistryConfig, error) {
			return q.UpsertClusterRegistryConfig(r.Context(), params)
		},
		func() (sqlc.ClusterRegistryConfig, error) {
			return h.queries.UpsertClusterRegistryConfig(r.Context(), params)
		},
		func(config sqlc.ClusterRegistryConfig) clusterAuditEvent {
			return clusterAuditEvent{
				action: "cluster.registry.updated", resourceType: "cluster", resourceID: id.String(),
				status: http.StatusOK,
				detail: map[string]any{
					"registry_id": config.ID.String(), "private_registry_url": req.PrivateRegistryUrl,
					"registry_username": req.RegistryUsername, "insecure": req.Insecure,
				},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update registry config")
		return
	}

	// Redact the secret before echoing back — the raw sqlc row carries both
	// registry_password and registry_password_encrypted, which must never be
	// serialized (same reason GET uses the DTO mapper).
	RespondJSON(w, http.StatusOK, clusterRegistryConfigToResponse(config))
}

// DeleteRegistryConfig handles DELETE /api/v1/clusters/{id}/registry/.
func (h *ClusterHandler) DeleteRegistryConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}

	_, err = executeClusterMutation(r, h,
		func(q ClusterMutationTx) (struct{}, error) {
			return struct{}{}, q.DeleteClusterRegistryConfig(r.Context(), id)
		},
		func() (struct{}, error) {
			return struct{}{}, h.queries.DeleteClusterRegistryConfig(r.Context(), id)
		},
		func(struct{}) clusterAuditEvent {
			return clusterAuditEvent{
				action: "cluster.registry.deleted", resourceType: "cluster", resourceID: id.String(),
				status: http.StatusNoContent,
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete registry config")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

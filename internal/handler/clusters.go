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
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/handler/clustermetrics"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/quota"
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

// rfc1123ClusterName matches the same naming rules Rancher applies to imported
// cluster CRDs: lowercase letters/digits/hyphens, start+end alphanumeric,
// 1–63 chars. Mirrors k8s.io/apimachinery/pkg/util/validation.IsDNS1123Label.
var rfc1123ClusterName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// validClusterName enforces the RFC-1123 label rules.
func validClusterName(s string) bool {
	return rfc1123ClusterName.MatchString(s)
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
	// Sprint 074 — auto-attach the platform-default cluster_template on
	// Create. All three calls are best-effort: a failure in any one MUST
	// NOT fail the cluster create. The apply worker / drift sweep is the
	// durable retry path. GetPlatformConfig is read-only; the second two
	// are the writes the auto-attach makes on success.
	GetPlatformConfig(ctx context.Context) (sqlc.PlatformConfiguration, error)
	GetClusterTemplateByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterTemplate, error)
	UpsertClusterTemplateApplication(ctx context.Context, arg sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error)
	// Platform-TLS surface for the public CA-bundle endpoint
	// (GET /api/v1/register/ca.crt) used by the Rancher-style
	// `curl --cacert ca.crt -sfL …` registration variant.
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
	ListArgoCDManagedClustersByCluster(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ArgocdManagedCluster, error)
	ListArgoCDApplicationsByManagedClusterTargets(ctx context.Context, arg sqlc.ListArgoCDApplicationsByManagedClusterTargetsParams) ([]sqlc.ArgocdApplication, error)
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

type clusterDecommissionTaskOutboxQuerier interface {
	CreateClusterDecommissionWithTaskOutbox(ctx context.Context, arg sqlc.CreateClusterDecommissionWithTaskOutboxParams) (sqlc.ClusterDecommission, error)
}

var errClusterOwnershipTransferUnsupported = errors.New("cluster ownership can only be transferred from crd to api")

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
	// decommissionQueue is the asynq client used to enqueue
	// cluster_decommission tasks from the DELETE handler. Optional —
	// when nil, the row is still inserted but the worker doesn't pick it up
	// until the periodic sweep runs (slower path).
	decommissionQueue ClusterDecommissionEnqueuer
	// taskOutbox persists critical task intents before Redis delivery.
	// Optional; when nil the handler falls back to direct enqueue and
	// periodic sweeps.
	taskOutbox tasks.TaskOutboxWriter
	// argoCDRefreshQueue is the asynq client used to enqueue
	// argocd:refresh_managed_cluster_labels tasks after a cluster Update mutates
	// `labels`. Same interface as decommissionQueue (Enqueue only), reused on
	// purpose so wiring stays trivial. Optional and nil-safe.
	argoCDRefreshQueue   ClusterDecommissionEnqueuer
	agentImage           string
	pullReconcileEnabled bool
	// enforcer gates Create against the fleet-wide cluster cap
	// configured by the 'global' quota plan (migration 051).
	// Optional; nil disables the check (test fakes, pre-migration).
	enforcer *quota.Enforcer
	// maintenanceGate (sprint 057) gates destructive mutations.
	maintenanceGate *MaintenanceGate
	// templateApplyQueue (sprint 074) enqueues cluster_template:apply
	// after auto-attach inserts the application row.
	templateApplyQueue ClusterDecommissionEnqueuer
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
	h.agentImage = repository + ":" + tag
}

// SetPullReconcileEnabled records whether rendered agent manifests should carry
// the Fleet-pull flag, so the agent's Phase-2 self-apply preserves it.
func (h *ClusterHandler) SetPullReconcileEnabled(enabled bool) {
	if h != nil {
		h.pullReconcileEnabled = enabled
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
// against the fleet-wide cluster cap (migration 051). Optional; nil
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

// SetTemplateApplyQueue wires the asynq client used by Create's
// auto-attach (sprint 074) to schedule the cluster_template:apply task
// immediately after the auto-attached application row is written.
// Optional and nil-safe; without it, the drift_check sweep picks up the
// 'pending' row on its next pass.
func (h *ClusterHandler) SetTemplateApplyQueue(q ClusterDecommissionEnqueuer) {
	if h == nil {
		return
	}
	h.templateApplyQueue = q
}

// SetArgoCDRefreshQueue wires the asynq client used by the Update handler to
// schedule the argocd:refresh_managed_cluster_labels task after a labels
// mutation. Optional: nil means changes to clusters.labels won't propagate to
// the upstream ArgoCD cluster Secrets — operators would have to re-register
// the cluster manually. In a normal deployment this is wired alongside
// SetDecommissionQueue.
func (h *ClusterHandler) SetArgoCDRefreshQueue(q ClusterDecommissionEnqueuer) {
	if h == nil {
		return
	}
	h.argoCDRefreshQueue = q
}

// publishEvent is a nil-safe wrapper around the optional publisher.
func (h *ClusterHandler) publishEvent(eventType string, data any) {
	if h == nil || h.publisher == nil {
		return
	}
	h.publisher.Publish(eventType, data)
}

// enqueueArgoCDLabelRefresh schedules a refresh of the upstream ArgoCD cluster
// Secret labels for this cluster across every ArgoCD instance it's registered
// into. Best-effort: when the queue is unwired, when task construction fails,
// or when redis is briefly unavailable we silently skip — operators can
// re-issue the refresh by hitting PUT /api/v1/clusters/{id}/ again.
func (h *ClusterHandler) enqueueArgoCDLabelRefresh(r *http.Request, clusterID uuid.UUID) {
	if h == nil || h.argoCDRefreshQueue == nil {
		return
	}
	task, err := tasks.NewArgoCDRefreshManagedClusterLabelsTask(clusterID)
	if err != nil {
		return
	}
	// Stamp correlation_id + W3C traceparent into the payload so worker logs
	// tie back to the originating request. Mirrors the pattern in the
	// decommission enqueue.
	payload := observability.EnrichTaskPayload(r.Context(), task.Payload(), middleware.GetCorrelationID(r.Context()))
	task = asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3))
	_, _ = h.argoCDRefreshQueue.Enqueue(task)
}

// enqueueArgoCDAutoRegister schedules a lazy, label-based ArgoCD auto-register
// pass for this cluster after a labels mutation. This is what lets a cluster
// that newly matches the configured auto-register selector get registered into
// ArgoCD without a Git commit — the worker re-evaluates the selector and
// registers when it now matches. Best-effort and nil-safe, mirroring
// enqueueArgoCDLabelRefresh: it reuses the same queue so wiring stays trivial.
func (h *ClusterHandler) enqueueArgoCDAutoRegister(r *http.Request, clusterID uuid.UUID) {
	if h == nil || h.argoCDRefreshQueue == nil {
		return
	}
	task, err := tasks.NewArgoCDAutoRegisterClusterTask(clusterID)
	if err != nil {
		return
	}
	payload := observability.EnrichTaskPayload(r.Context(), task.Payload(), middleware.GetCorrelationID(r.Context()))
	// Dedup on a stable task id rather than asynq.Unique: the enriched payload
	// carries a per-request correlation_id/traceparent, so Unique (which hashes
	// type+payload) would treat every PUT as distinct and never collapse them.
	// A stable TaskID keyed on the cluster makes asynq reject re-enqueues while
	// an auto-register task for the same cluster is still active.
	task = asynq.NewTask(task.Type(), payload)
	_, _ = h.argoCDRefreshQueue.Enqueue(task, asynq.MaxRetry(5), asynq.TaskID("argocd-auto-register:"+clusterID.String()))
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
func (h *ClusterHandler) enrichClusterFromCache(ctx context.Context, c sqlc.Cluster) ClusterResponse {
	out := clusterToResponse(c)
	h.enrichClusterArgoCD(ctx, &out, c)
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
	h.enrichClusterArgoCD(ctx, &out, c)
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

func (h *ClusterHandler) enrichClusterArgoCD(ctx context.Context, out *ClusterResponse, c sqlc.Cluster) {
	if h == nil || h.queries == nil || out == nil {
		return
	}
	manageBaseline := true
	if row, err := h.queries.GetPlatformSetting(ctx, "argocd.manage_platform_baseline"); err == nil && len(row.Value) > 0 {
		var b bool
		if err := json.Unmarshal(row.Value, &b); err == nil {
			manageBaseline = b
		}
	}
	out.ArgoCD.BaselineManagedBy = "helm"
	if c.IsLocal {
		out.ArgoCD.BaselineManagedBy = "local"
	} else if manageBaseline {
		out.ArgoCD.BaselineManagedBy = "argocd_pending"
	}
	out.ArgoCD.BaselineComponents = baselineComponentOwnership(out.ArgoCD.BaselineManagedBy)
	rows, err := h.queries.ListArgoCDManagedClustersByCluster(ctx, c.ID)
	if err != nil {
		return
	}
	out.ArgoCD.Registered = len(rows) > 0
	out.ArgoCD.InstanceCount = len(rows)
	out.ArgoCD.ClusterSecretNames = make([]string, 0, len(rows))
	for _, row := range rows {
		if row.ClusterSecretName != "" {
			out.ArgoCD.ClusterSecretNames = append(out.ArgoCD.ClusterSecretNames, row.ClusterSecretName)
		}
	}
	if out.ArgoCD.Registered && manageBaseline && !c.IsLocal {
		out.ArgoCD.BaselineManagedBy = "argocd"
	}
	out.ArgoCD.BaselineComponents = baselineComponentOwnership(out.ArgoCD.BaselineManagedBy)

	instanceIDs, targets := argoCDManagedClusterApplicationTargets(c, rows)
	if len(instanceIDs) == 0 || len(targets) == 0 {
		return
	}
	apps, err := h.queries.ListArgoCDApplicationsByManagedClusterTargets(ctx, sqlc.ListArgoCDApplicationsByManagedClusterTargetsParams{
		ArgocdInstanceIds:   instanceIDs,
		DestinationClusters: targets,
	})
	if err != nil {
		out.ArgoCD.Drift.LastError = "cached ArgoCD application drift unavailable"
		return
	}
	out.ArgoCD.Drift = summarizeArgoCDDrift(apps)
}

func argoCDManagedClusterApplicationTargets(c sqlc.Cluster, rows []sqlc.ArgocdManagedCluster) ([]uuid.UUID, []string) {
	seenIDs := map[uuid.UUID]struct{}{}
	seenTargets := map[string]struct{}{}
	instanceIDs := make([]uuid.UUID, 0, len(rows))
	targets := make([]string, 0, len(rows)*2+1)

	addID := func(id uuid.UUID) {
		if id == uuid.Nil {
			return
		}
		if _, ok := seenIDs[id]; ok {
			return
		}
		seenIDs[id] = struct{}{}
		instanceIDs = append(instanceIDs, id)
	}
	addTarget := func(target string) {
		target = strings.TrimSpace(target)
		if target == "" {
			return
		}
		if _, ok := seenTargets[target]; ok {
			return
		}
		seenTargets[target] = struct{}{}
		targets = append(targets, target)
	}

	for _, row := range rows {
		addID(row.ArgocdInstanceID)
		addTarget(row.ServerUrl)
		addTarget(row.ClusterSecretName)
	}
	addTarget(c.Name)

	return instanceIDs, targets
}

func summarizeArgoCDDrift(apps []sqlc.ArgocdApplication) ClusterArgoCDDriftSummary {
	out := ClusterArgoCDDriftSummary{AppCount: len(apps)}
	var latest time.Time
	for _, app := range apps {
		switch normalizeArgoStatus(app.SyncStatus) {
		case "synced":
			out.SyncedCount++
		case "outofsync":
			out.OutOfSyncCount++
		default:
			out.UnknownSyncCount++
		}

		switch normalizeArgoStatus(app.HealthStatus) {
		case "healthy":
			out.HealthyCount++
		case "progressing":
			out.ProgressingCount++
		case "degraded":
			out.DegradedCount++
		default:
			out.UnknownHealthCount++
		}

		if app.LastSynced.Valid && app.LastSynced.Time.After(latest) {
			latest = app.LastSynced.Time
		}
		out.ResourceCreatedCount += int(app.ResourceCreatedCount)
		out.ResourceChangedCount += int(app.ResourceChangedCount)
		out.ResourcePrunedCount += int(app.ResourcePrunedCount)
	}
	if !latest.IsZero() {
		s := latest.UTC().Format(time.RFC3339Nano)
		out.LastSynced = &s
	}
	if out.DegradedCount > 0 {
		out.LastError = fmt.Sprintf("%d degraded ArgoCD application%s", out.DegradedCount, pluralSuffix(out.DegradedCount))
	} else if out.OutOfSyncCount > 0 {
		out.LastError = fmt.Sprintf("%d out-of-sync ArgoCD application%s", out.OutOfSyncCount, pluralSuffix(out.OutOfSyncCount))
	}
	return out
}

func normalizeArgoStatus(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer("_", "", "-", "", " ", "")
	return replacer.Replace(s)
}

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// --- Request / Response types ---

// CreateClusterRequest represents the request body for creating a cluster.
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
}

// UpdateClusterRequest represents the request body for updating a cluster.
type UpdateClusterRequest struct {
	DisplayName string          `json:"display_name"`
	Description string          `json:"description"`
	Environment string          `json:"environment"`
	Region      string          `json:"region"`
	Labels      json.RawMessage `json:"labels"`
	Annotations json.RawMessage `json:"annotations"`
}

// UpdateRegistryConfigRequest represents the request body for upserting registry config.
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
	limit := int32(queryInt(r, "limit", 20))
	offset := int32(queryInt(r, "offset", 0))

	clusters, err := h.queries.ListClusters(r.Context(), sqlc.ListClustersParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list clusters")
		return
	}

	total, err := h.queries.CountClusters(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count clusters")
		return
	}

	// One query for all in-flight decommissions → mark the matching rows
	// Decommissioning (avoids an N+1 per cluster). Best-effort: on error we
	// just don't flag anything.
	decommissioning := h.inFlightDecommissionSet(r.Context())

	enriched := make([]ClusterResponse, 0, len(clusters))
	for _, c := range clusters {
		resp := h.enrichClusterFromCache(r.Context(), c)
		resp.Decommissioning = decommissioning[c.ID]
		enriched = append(enriched, resp)
	}
	RespondPaginated(w, r, enriched, total)
}

// inFlightDecommissionSet returns the set of cluster IDs with a pending/running
// decommission. Best-effort — returns an empty set on any error.
func (h *ClusterHandler) inFlightDecommissionSet(ctx context.Context) map[uuid.UUID]bool {
	set := map[uuid.UUID]bool{}
	rows, err := h.queries.ListPendingClusterDecommissions(ctx, 500)
	if err != nil {
		return set
	}
	for _, row := range rows {
		set[row.ClusterID] = true
	}
	return set
}

// Create handles POST /api/v1/clusters/.
func (h *ClusterHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateClusterRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	// Fleet-wide cap (migration 051). The 'global' quota plan's
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
	cluster, err := h.queries.CreateCluster(r.Context(), sqlc.CreateClusterParams{
		Name:         req.Name,
		DisplayName:  req.DisplayName,
		Description:  req.Description,
		Environment:  req.Environment,
		Region:       req.Region,
		Provider:     req.Provider,
		Distribution: req.Distribution,
		Labels:       labels,
		Annotations:  annotations,
		CreatedByID:  currentUserUUID(r),
	})
	if err != nil {
		if isUniqueViolation(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, fmt.Sprintf("A cluster named %q already exists", req.Name))
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create cluster")
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

	recordAudit(r, h.queries, "cluster.create", "cluster", cluster.ID.String(), cluster.Name, map[string]any{
		"environment":  req.Environment,
		"region":       req.Region,
		"provider":     req.Provider,
		"distribution": req.Distribution,
	})

	// Sprint 074 — auto-attach the platform-default cluster template
	// (typically the seeded "Platform baseline" — trivy-operator,
	// kube-state-metrics, node-exporter, fluent-bit, ingress-nginx,
	// cert-manager, gatekeeper).
	// Best-effort: a failure here MUST NOT fail the cluster create.
	// The apply worker (sprint 049) is the durable retry path; the
	// drift_check sweep picks up any 'pending' row left behind.
	h.autoAttachDefaultTemplate(r, cluster.ID)

	w.Header().Set("Location", "/api/v1/clusters/"+cluster.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, clusterToResponse(cluster))
}

// autoAttachDefaultTemplate records a cluster_template_applications row
// pointing at the platform's configured default template (sprint 074).
// All steps are best-effort — every error path is a warn log + return,
// never a failed cluster create.
//
// Why three separate query calls instead of a single transaction? The
// auto-attach is logically optional and the existing sqlc Queries
// surface doesn't expose a tx-bound batch. A partial state
// (cluster exists, application row missing) is exactly the case the
// reapply endpoint (this same sprint) handles — the operator can opt
// the cluster in later without re-registering it.
func (h *ClusterHandler) autoAttachDefaultTemplate(r *http.Request, clusterID uuid.UUID) {
	if h == nil || h.queries == nil {
		return
	}
	ctx := r.Context()
	cfg, err := h.queries.GetPlatformConfig(ctx)
	if err != nil {
		// Singleton row missing or DB blip — fall through; the
		// drift_check sweep won't help here (there's nothing to drift
		// from yet) but the operator can still hit the reapply
		// endpoint after the platform_configuration row materializes.
		return
	}
	if !cfg.DefaultClusterTemplateID.Valid {
		// Operator hasn't enabled the auto-attach default. Legacy
		// behavior: clusters come up bare and the operator wires
		// tools click-ops.
		return
	}
	templateID := uuid.UUID(cfg.DefaultClusterTemplateID.Bytes)
	tmpl, err := h.queries.GetClusterTemplateByID(ctx, templateID)
	if err != nil {
		// Stale default — the operator deleted the template after
		// pointing platform_configuration at it (and the FK's
		// ON DELETE SET NULL hasn't run yet because the row hasn't
		// been deleted, or the FK trigger races with this read).
		// Either way, log and move on.
		return
	}
	if _, err := h.queries.UpsertClusterTemplateApplication(ctx, sqlc.UpsertClusterTemplateApplicationParams{
		ClusterID:    clusterID,
		TemplateID:   tmpl.ID,
		SpecSnapshot: tmpl.Spec,
	}); err != nil {
		return
	}
	recordAudit(r, h.queries, "cluster.template.auto_attached", "cluster", clusterID.String(), "", map[string]any{
		"template_id":   tmpl.ID.String(),
		"template_name": tmpl.Name,
		"source":        "platform_default",
	})
	h.enqueueTemplateApply(r, clusterID)
}

// enqueueTemplateApply schedules a cluster_template:apply task for the
// freshly auto-attached row. Mirrors the pattern in
// ClusterTemplateHandler.enqueueApply but lives here so the cluster
// Create path doesn't need a circular dependency on the template
// handler. Nil-safe: when templateApplyQueue is unwired, the periodic
// sweep is the fallback.
func (h *ClusterHandler) enqueueTemplateApply(r *http.Request, clusterID uuid.UUID) {
	if h == nil || (h.templateApplyQueue == nil && h.taskOutbox == nil) {
		return
	}
	task, err := tasks.NewClusterTemplateApplyTask(clusterID)
	if err != nil {
		return
	}
	payload := observability.EnrichTaskPayload(r.Context(), task.Payload(), middleware.GetCorrelationID(r.Context()))
	task = asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3))
	if enqueueClusterTemplateApplyOutbox(r.Context(), h.taskOutbox, task, clusterID) {
		return
	}
	if h.templateApplyQueue != nil {
		_, _ = h.templateApplyQueue.Enqueue(task, asynq.Queue(tasks.ClusterTemplateApplyQueueName))
	}
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

	labels := req.Labels
	if labels == nil {
		labels = json.RawMessage(`{}`)
	}
	annotations := req.Annotations
	if annotations == nil {
		annotations = json.RawMessage(`{}`)
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

	cluster, err := h.queries.UpdateCluster(r.Context(), sqlc.UpdateClusterParams{
		ID:          id,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Environment: req.Environment,
		Region:      req.Region,
		Labels:      labels,
		Annotations: annotations,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		return
	}

	h.publishEvent("cluster.updated", map[string]any{
		"cluster_id":   cluster.ID.String(),
		"name":         cluster.Name,
		"display_name": cluster.DisplayName,
		"status":       cluster.Status,
	})

	// Propagate the (possibly mutated) cluster.labels onto every upstream
	// ArgoCD cluster Secret this cluster is registered into. The task is
	// idempotent — it diffs the current Secret labels against the desired set
	// and skips the PATCH when they match — so we enqueue on every Update
	// rather than diffing JSONB here.
	h.enqueueArgoCDLabelRefresh(r, cluster.ID)
	// Labels may have changed such that the cluster now matches the
	// auto-register selector; trigger a lazy re-evaluation so it gets
	// registered into ArgoCD without requiring a Git commit.
	h.enqueueArgoCDAutoRegister(r, cluster.ID)

	recordAudit(r, h.queries, "cluster.update", "cluster", cluster.ID.String(), cluster.Name, map[string]any{
		"display_name": req.DisplayName,
		"description":  req.Description,
		"environment":  req.Environment,
		"region":       req.Region,
	})

	RespondJSON(w, http.StatusOK, clusterToResponse(cluster))
}

func clusterUpdateBlockedByOwnership(ctx context.Context, q any, id uuid.UUID) (string, error) {
	ownershipQ, ok := q.(clusterOwnershipQuerier)
	if !ok {
		return "", nil
	}
	ownership, err := ownershipQ.GetClusterOwnership(ctx, id)
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
	previous, updated, transferred, err := transferClusterOwnershipToAPI(r.Context(), h.queries, id)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster not found")
		case errors.Is(err, errClusterOwnershipTransferUnsupported):
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Only CRD-owned clusters can be transferred through this endpoint")
		default:
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to transfer cluster ownership")
		}
		return
	}
	recordAudit(r, h.queries, "cluster.ownership.takeover", "cluster", id.String(), cluster.Name, map[string]any{
		"previous_managed_by": previous.ManagedBy,
		"previous_ref": map[string]string{
			"api_version": previous.ExternalRefApiVersion,
			"kind":        previous.ExternalRefKind,
			"namespace":   previous.ExternalRefNamespace,
			"name":        previous.ExternalRefName,
		},
		"transferred": transferred,
	})
	RespondJSON(w, http.StatusOK, map[string]any{
		"id":          updated.ID.String(),
		"managed_by":  updated.ManagedBy,
		"transferred": transferred,
	})
}

func transferClusterOwnershipToAPI(ctx context.Context, q any, id uuid.UUID) (sqlc.FleetOwnership, sqlc.FleetOwnership, bool, error) {
	ownershipQ, ok := q.(clusterOwnershipTransferQuerier)
	if !ok {
		return sqlc.FleetOwnership{}, sqlc.FleetOwnership{}, false, fmt.Errorf("cluster ownership transfer query support is not configured")
	}
	previous, err := ownershipQ.GetClusterOwnership(ctx, id)
	if err != nil {
		return sqlc.FleetOwnership{}, sqlc.FleetOwnership{}, false, err
	}
	switch previous.ManagedBy {
	case "crd":
		updated, err := ownershipQ.SetClusterOwnership(ctx, sqlc.SetClusterOwnershipParams{
			ID:        id,
			ManagedBy: "api",
		})
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

	// Idempotency: if there's already an in-flight or succeeded decommission
	// for this cluster, return its status rather than creating a duplicate.
	if existing, lookupErr := h.queries.GetLatestClusterDecommissionByCluster(r.Context(), id); lookupErr == nil {
		if existing.Status == tasks.PhaseStatusPending || existing.Status == tasks.PhaseStatusRunning || existing.Status == tasks.PhaseStatusSucceeded {
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

	row, enqueued, err := h.createClusterDecommission(r.Context(), sqlc.CreateClusterDecommissionParams{
		ClusterID:     id,
		RequestedByID: requestedBy,
		ClusterName:   cluster.Name,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateDecommissionFailed, "Failed to enqueue cluster decommission")
		return
	}

	if !enqueued {
		h.enqueueClusterDecommission(r.Context(), row.ID)
	}

	h.publishEvent("cluster.decommission_enqueued", map[string]any{
		"cluster_id":      id.String(),
		"decommission_id": row.ID.String(),
	})

	recordAudit(r, h.queries, "cluster.decommission.requested", "cluster", id.String(), cluster.Name, map[string]any{
		"decommission_id": row.ID.String(),
	})

	statusURL := fmt.Sprintf("/api/v1/clusters/%s/decommission/", id.String())
	RespondJSON(w, http.StatusAccepted, renderDecommission(row, statusURL))
}

// tunnelQueueName is the asynq queue drained by the server pod's in-process
// worker (which holds the WS tunnel hub). Decommission tasks go here so the
// managed-side cleanup phase can reach a connected agent. Matches the literal
// used by the registration apply path.
const tunnelQueueName = "tunnel"

func (h *ClusterHandler) createClusterDecommission(ctx context.Context, arg sqlc.CreateClusterDecommissionParams) (sqlc.ClusterDecommission, bool, error) {
	atomicQ, ok := any(h.queries).(clusterDecommissionTaskOutboxQuerier)
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
	row, err := h.queries.CreateClusterDecommission(ctx, arg)
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
	// TODO(total): no COUNT query for cluster conditions; use page length.
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

	token, err := h.queries.CreateClusterRegistrationToken(r.Context(), sqlc.CreateClusterRegistrationTokenParams{
		ClusterID: id,
		TokenHash: auth.HashOpaqueToken(tokenStr),
		ExpiresAt: time.Now().Add(h.registrationTokenTTL),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create registration token")
		return
	}
	token.Token = tokenStr

	recordAudit(r, h.queries, "cluster.register_token", "cluster", id.String(), "", map[string]any{
		"token_id":   token.ID.String(),
		"expires_at": token.ExpiresAt.UTC().Format(time.RFC3339),
	})

	RespondJSON(w, http.StatusCreated, token)
}

// RotateAgentToken handles POST /api/v1/clusters/{id}/agent-token/rotate/.
// It sets rotation_pending_at on the cluster's durable agent token so the
// agent's NEXT CONNECT performs the grace rotation (mint fresh, demote old to
// previous, deliver fresh in the ACK). It does NOT change the live token, so
// there is no mid-rotation lockout: the agent keeps using its held token until
// it adopts the freshly-minted one.
func (h *ClusterHandler) RotateAgentToken(w http.ResponseWriter, r *http.Request) {
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
	rows, err := h.queries.SetClusterAgentTokenRotationPending(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to request agent token rotation")
		return
	}
	if rows == 0 {
		// Gated by SetClusterAgentTokenRotationPending: 0 rows means either no
		// active token exists, or a rotation is already in flight (pending, or
		// the agent hasn't yet adopted the last new token). Conflict either way —
		// re-triggering would risk demoting the in-use previous hash and locking
		// the agent out.
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "No agent token eligible for rotation: none is active or a rotation is already in flight")
		return
	}
	recordAudit(r, h.queries, "agent.token.rotate.requested", "cluster", id.String(), cluster.Name, map[string]any{
		"cluster_id": id.String(),
		"trigger":    "admin_api",
	})
	RespondJSON(w, http.StatusAccepted, map[string]any{
		"cluster_id":       id.String(),
		"rotation_pending": true,
		"message":          "rotation will complete on the agent's next connect",
	})
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
	rows, err := h.queries.RevokeClusterAgentToken(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DBError, "Failed to revoke agent token")
		return
	}
	if rows == 0 {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster has no active agent token to revoke")
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
	recordAudit(r, h.queries, "agent.token.revoked", "cluster", id.String(), cluster.Name, map[string]any{
		"cluster_id":      id.String(),
		"trigger":         "admin_api",
		"session_severed": disconnected,
	})
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

	RespondJSON(w, http.StatusOK, config)
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
	token, err := h.queries.CreateClusterRegistrationToken(r.Context(), sqlc.CreateClusterRegistrationTokenParams{
		ClusterID: id,
		TokenHash: auth.HashOpaqueToken(tokenStr),
		ExpiresAt: time.Now().Add(h.registrationTokenTTL),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create registration token")
		return
	}
	token.Token = tokenStr

	recordAudit(r, h.queries, "cluster.register_token", "cluster", id.String(), cluster.Name, map[string]any{
		"token_id":   token.ID.String(),
		"source":     "manifest_download",
		"expires_at": token.ExpiresAt.UTC().Format(time.RFC3339),
	})

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
//	curl -sfL https://<server>/api/v1/register/<token>.yaml | kubectl apply -f -
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
	// TODO(total): no COUNT query for remediation attempts; use page length.
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

// GetKubeconfig handles GET /api/v1/clusters/{id}/kubeconfig/.
// Generates a kubeconfig snippet for direct API access using stored CA + URL.
func (h *ClusterHandler) GetKubeconfig(w http.ResponseWriter, r *http.Request) {
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
	if cluster.ApiServerUrl == "" {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Cluster API server URL is not yet available")
		return
	}
	userEmail := authenticatedEmail(r)
	kubeconfig := buildDirectKubeconfig(cluster, userEmail)
	RespondJSON(w, http.StatusOK, kubeconfig)
}

// GenerateKubeconfig handles POST /api/v1/clusters/{id}/generate-kubeconfig/.
// Returns a kubeconfig that routes through the Astronomer proxy.
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
	kubeconfig := buildProxyKubeconfig(cluster, userEmail, serverURL)
	yamlBytes, err := yaml.Marshal(kubeconfig)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.RenderError, "Failed to render kubeconfig")
		return
	}
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="kubeconfig-%s.yaml"`, cluster.Name))
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
	kubeconfig := buildProxyKubeconfig(cluster, userEmail, serverURL)
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

func buildDirectKubeconfig(cluster sqlc.Cluster, userEmail string) map[string]any {
	if userEmail == "" {
		userEmail = "user"
	}
	return map[string]any{
		"apiVersion": "v1",
		"kind":       "Config",
		"clusters": []map[string]any{
			{
				"cluster": map[string]any{
					"server":                     cluster.ApiServerUrl,
					"certificate-authority-data": cluster.CaCertificate,
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
					"token": "REPLACE_WITH_TOKEN",
				},
			},
		},
	}
}

func buildProxyKubeconfig(cluster sqlc.Cluster, userEmail, serverURL string) map[string]any {
	if userEmail == "" {
		userEmail = "user"
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
					"token": "REPLACE_WITH_API_TOKEN",
				},
			},
		},
	}
}

func (h *ClusterHandler) renderAgentInstallManifest(cluster sqlc.Cluster, token, serverURL string) string {
	annotations := clusterAnnotations(cluster.Annotations)
	agentImage := "ghcr.io/alphabravo-oss/astronomer-go-agent:latest"
	if h != nil && h.agentImage != "" {
		agentImage = h.agentImage
	}
	if image := strings.TrimSpace(annotations[agenttemplate.AgentImageAnnotation]); image != "" {
		agentImage = image
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
		PullReconcileEnabled: h != nil && h.pullReconcileEnabled,
	})
}

// RenderAgentManifestForCluster renders the agent's own install manifest
// (Deployment + RBAC + config) for a cluster at the management plane's central
// agent image — without an HTTP request. It is the (a) source of the Fleet-style
// PULL desired state: the server-side DesiredState adapter (internal/server)
// combines this with the enabled baseline components.
//
// The server URL is taken from platform configuration. The registration token
// is intentionally left as the template placeholder: the pull path targets an
// already-connected, already-credentialed agent (its durable token Secret is
// mounted and self-managed), so re-rendering does NOT mint or rotate a token.
// The manifest's purpose here is the Deployment/RBAC/config shape, which the
// agent server-side-applies over its own footprint.
func (h *ClusterHandler) RenderAgentManifestForCluster(ctx context.Context, clusterID uuid.UUID) (string, error) {
	if h == nil {
		return "", fmt.Errorf("nil cluster handler")
	}
	cluster, err := h.queries.GetClusterByID(ctx, clusterID)
	if err != nil {
		return "", fmt.Errorf("get cluster %s: %w", clusterID, err)
	}
	serverURL := ""
	if cfg, cerr := h.queries.GetPlatformConfig(ctx); cerr == nil {
		serverURL = strings.TrimRight(strings.TrimSpace(cfg.ServerUrl), "/")
	}
	return h.renderAgentInstallManifest(cluster, "REPLACE_WITH_REGISTRATION_TOKEN", serverURL), nil
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

	config, err := h.queries.UpsertClusterRegistryConfig(r.Context(), sqlc.UpsertClusterRegistryConfigParams{
		ClusterID:                 id,
		PrivateRegistryUrl:        req.PrivateRegistryUrl,
		RegistryUsername:          req.RegistryUsername,
		RegistryPassword:          registryPassword,
		RegistryPasswordEncrypted: registryPasswordEncrypted,
		Insecure:                  req.Insecure,
		CaBundle:                  req.CaBundle,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update registry config")
		return
	}

	// Don't surface the password / CA in the audit detail; recordAudit will
	// redact the well-known keys but we keep the explicit map narrow anyway.
	recordAudit(r, h.queries, "cluster.registry.updated", "cluster", id.String(), "", map[string]any{
		"private_registry_url": req.PrivateRegistryUrl,
		"registry_username":    req.RegistryUsername,
		"insecure":             req.Insecure,
	})

	RespondJSON(w, http.StatusOK, config)
}

// DeleteRegistryConfig handles DELETE /api/v1/clusters/{id}/registry/.
func (h *ClusterHandler) DeleteRegistryConfig(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}

	if err := h.queries.DeleteClusterRegistryConfig(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete registry config")
		return
	}

	recordAudit(r, h.queries, "cluster.registry.deleted", "cluster", id.String(), "", nil)
	w.WriteHeader(http.StatusNoContent)
}

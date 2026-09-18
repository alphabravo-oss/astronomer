package handler

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/google/uuid"
)

// SecurityClusterQuerier exposes the single cluster lookup we need to default
// the CIS profile from cluster.distribution. Kept as its own interface so
// SecurityQuerier doesn't have to grow a Cluster dependency.
type SecurityClusterQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
}

// SecurityQuerier abstracts the security-related database queries needed by SecurityHandler.
type SecurityQuerier interface {
	// Templates
	GetPodSecurityTemplateByID(ctx context.Context, id uuid.UUID) (sqlc.PodSecurityTemplate, error)
	ListPodSecurityTemplates(ctx context.Context, arg sqlc.ListPodSecurityTemplatesParams) ([]sqlc.PodSecurityTemplate, error)
	CountPodSecurityTemplates(ctx context.Context) (int64, error)
	// Policies
	GetClusterSecurityPolicyByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterSecurityPolicy, error)
	GetPolicyByCluster(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterSecurityPolicy, error)
	ListClusterSecurityPolicies(ctx context.Context, arg sqlc.ListClusterSecurityPoliciesParams) ([]sqlc.ClusterSecurityPolicy, error)
	CountClusterSecurityPolicies(ctx context.Context) (int64, error)
	// Scans
	ListSecurityScanResults(ctx context.Context, arg sqlc.ListSecurityScanResultsParams) ([]sqlc.SecurityScanResult, error)
	ListScansByCluster(ctx context.Context, arg sqlc.ListScansByClusterParams) ([]sqlc.SecurityScanResult, error)
	GetSecurityScanResultByID(ctx context.Context, id uuid.UUID) (sqlc.SecurityScanResult, error)
	CreateSecurityScanResult(ctx context.Context, arg sqlc.CreateSecurityScanResultParams) (sqlc.SecurityScanResult, error)
	CreateCISScan(ctx context.Context, arg sqlc.CreateCISScanParams) (sqlc.SecurityScanResult, error)
	CountSecurityScanResults(ctx context.Context) (int64, error)
}

// SecurityMutationTx is the transaction-bound surface for security state that
// must commit with durable audit intent. Production passes sqlc.New(tx); narrow
// tests can model the same all-or-nothing contract in memory.
type SecurityMutationTx interface {
	audit.OutboxQuerier
	CreatePodSecurityTemplate(context.Context, sqlc.CreatePodSecurityTemplateParams) (sqlc.PodSecurityTemplate, error)
	UpdatePodSecurityTemplate(context.Context, sqlc.UpdatePodSecurityTemplateParams) (sqlc.PodSecurityTemplate, error)
	DeletePodSecurityTemplate(context.Context, uuid.UUID) error
	CreateClusterSecurityPolicy(context.Context, sqlc.CreateClusterSecurityPolicyParams) (sqlc.ClusterSecurityPolicy, error)
	UpdateClusterSecurityPolicyApplied(context.Context, uuid.UUID) error
	DeleteClusterSecurityPolicy(context.Context, uuid.UUID) error
	CancelSecurityScan(context.Context, sqlc.CancelSecurityScanParams) (sqlc.SecurityScanResult, error)
}

type securityRunTxFunc func(context.Context, func(SecurityMutationTx) error) error

// SecurityHandler handles security endpoints.
type SecurityHandler struct {
	queries  SecurityQuerier
	clusters SecurityClusterQuerier
	authz    authorizationSupport
	k8s      K8sRequester
	log      *slog.Logger
	bus      *events.Bus
	runTx    securityRunTxFunc
}

// SetEventBus wires the SSE bus for cis_scan.changed liveness events (P4.5).
// Optional: publishers are fire-and-forget and nil-safe.
func (h *SecurityHandler) SetEventBus(bus *events.Bus) {
	if h == nil {
		return
	}
	h.bus = bus
}

// NewSecurityHandler creates a new security handler.
func NewSecurityHandler(queries SecurityQuerier) *SecurityHandler {
	return &SecurityHandler{queries: queries, log: slog.Default()}
}

// SetRunTx enables production's atomic security-mutation + audit-outbox path.
func (h *SecurityHandler) SetRunTx(runTx securityRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *SecurityHandler) SetAuthorization(engine *rbac.Engine, querier rbac.BindingQuerier) {
	if h != nil {
		h.authz.SetAuthorization(engine, querier)
	}
}

func (h *SecurityHandler) TransactionalAuditWired() bool {
	return h != nil && h.runTx != nil
}

// SetK8sRequester wires the tunnel-backed Kubernetes API client. Optional:
// when nil the CIS scan trigger short-circuits with a 503 and tests can run
// the rest of the handlers without standing up a tunnel hub.
func (h *SecurityHandler) SetK8sRequester(req K8sRequester) {
	if h != nil {
		h.k8s = req
	}
}

// SetClusterQuerier supplies the GetClusterByID dependency used to default
// the CIS profile from cluster.distribution.
func (h *SecurityHandler) SetClusterQuerier(q SecurityClusterQuerier) {
	if h != nil {
		h.clusters = q
	}
}

// SetLogger sets the structured logger used for handler-side diagnostics.
func (h *SecurityHandler) SetLogger(log *slog.Logger) {
	if h != nil && log != nil {
		h.log = log
	}
}

func recordSecurityAuditOutbox(r *http.Request, q audit.OutboxQuerier, action, resourceType, resourceID, resourceName string, status int, detail map[string]any) error {
	return recordAuditOutbox(r, q, action, resourceType, resourceID, resourceName, status, detail)
}

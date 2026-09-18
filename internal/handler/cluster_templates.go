// Cluster templates (migration 049).
//
// Templates package the manual cluster onboarding flow (environment +
// labels + tool installs + a default project + a token rotation policy)
// into operator-defined "Production Web App" style presets. Applying a
// template to a cluster is async + idempotent: the handler upserts a
// cluster_template_applications row with status='pending' and enqueues
// a cluster_template:apply task; the worker walks the spec and converges
// the cluster to match.
//
// The handler validates the spec JSONB at create/update time:
//   - top-level keys are restricted to a known set (unknown keys -> 400)
//   - environment is one of "production"|"staging"|"development"
//   - default_project.pod_security_profile is one of
//     "privileged"|"baseline"|"restricted"
//   - registration_policy.token_rotation_days is non-negative
//
// Everything else (label k/v shapes, tools[].slug existence, project
// quota strings) is validated at apply time by the worker — the spec
// stays expressive enough that future fields don't need a handler
// change to flow through.

package handler

import (
	"context"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	avault "github.com/alphabravocompany/astronomer-go/internal/vault"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
)

// ClusterTemplateQuerier is the database surface the handler needs. The
// production *sqlc.Queries satisfies it; tests stand up a narrow fake.
type ClusterTemplateQuerier interface {
	// Template CRUD.
	ListClusterTemplates(ctx context.Context, arg sqlc.ListClusterTemplatesParams) ([]sqlc.ClusterTemplate, error)
	CountClusterTemplates(ctx context.Context) (int64, error)
	GetClusterTemplateByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterTemplate, error)
	GetClusterTemplateByName(ctx context.Context, name string) (sqlc.ClusterTemplate, error)
	CreateClusterTemplate(ctx context.Context, arg sqlc.CreateClusterTemplateParams) (sqlc.ClusterTemplate, error)
	UpdateClusterTemplate(ctx context.Context, arg sqlc.UpdateClusterTemplateParams) (sqlc.ClusterTemplate, error)
	DeleteClusterTemplate(ctx context.Context, id uuid.UUID) error
	CountClusterTemplateApplicationsByTemplate(ctx context.Context, templateID uuid.UUID) (int64, error)
	ListClusterTemplateBoundClusters(ctx context.Context, templateID uuid.UUID) ([]sqlc.ListClusterTemplateBoundClustersRow, error)

	// Application + status surface.
	GetClusterTemplateApplication(ctx context.Context, clusterID uuid.UUID) (sqlc.ClusterTemplateApplication, error)
	UpsertClusterTemplateApplication(ctx context.Context, arg sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error)
	MarkClusterTemplateApplicationStatus(ctx context.Context, arg sqlc.MarkClusterTemplateApplicationStatusParams) (sqlc.ClusterTemplateApplication, error)
	DeleteClusterTemplateApplication(ctx context.Context, clusterID uuid.UUID) error

	// Cluster existence check for the bind endpoints.
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)

	// Registration policy detach when the operator unbinds a template
	// that stamped one.
	DeleteClusterRegistrationPolicy(ctx context.Context, clusterID uuid.UUID) error
}

type ClusterTemplateMutationTx interface {
	ClusterTemplateQuerier
	clusterTemplateApplicationTaskOutboxQuerier
	audit.OutboxQuerier
	tasks.TaskOutboxWriter
}

type clusterTemplateRunTxFunc func(context.Context, func(ClusterTemplateMutationTx) error) error

// ClusterTemplateHandler owns /api/v1/cluster-templates/* and the per-
// cluster /api/v1/clusters/{cluster_id}/template/* endpoints.
type ClusterTemplateHandler struct {
	queries ClusterTemplateQuerier
	authz   authorizationSupport
	runTx   clusterTemplateRunTxFunc
	bus     *events.Bus
	// maintenanceGate is the migration-057 hook on cluster_template.apply.
	// Optional + nil-safe.
	maintenanceGate *MaintenanceGate
	// vaultResolver pre-flights ${vault://...} references in the
	// template spec at Apply time. The spec snapshot is NEVER mutated
	// with resolved values — that would persist cleartext secrets in
	// the DB; the worker re-resolves at install time. Migration 067.
	vaultResolver *avault.Resolver
}

func (h *ClusterTemplateHandler) SetAuthorization(engine *rbac.Engine, querier rbac.BindingQuerier) {
	if h != nil {
		h.authz.SetAuthorization(engine, querier)
	}
}

// SetVaultResolver wires the Vault resolver used to pre-flight
// ${vault://...} references in template specs at Apply time.
func (h *ClusterTemplateHandler) SetVaultResolver(r *avault.Resolver) {
	if h == nil {
		return
	}
	h.vaultResolver = r
}

// NewClusterTemplateHandler constructs the handler.
func NewClusterTemplateHandler(queries ClusterTemplateQuerier) *ClusterTemplateHandler {
	return &ClusterTemplateHandler{queries: queries}
}

func (h *ClusterTemplateHandler) SetRunTx(runTx clusterTemplateRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *ClusterTemplateHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

// SetEventBus wires the SSE bus for template_binding.changed liveness
// events (P4.5). Optional: fire-and-forget and nil-safe.
func (h *ClusterTemplateHandler) SetEventBus(bus *events.Bus) {
	if h == nil {
		return
	}
	h.bus = bus
}

// SetMaintenanceGate wires the migration-057 gate that refuses or
// defers cluster_template.apply during an active maintenance window.
func (h *ClusterTemplateHandler) SetMaintenanceGate(g *MaintenanceGate) {
	if h == nil {
		return
	}
	h.maintenanceGate = g
}

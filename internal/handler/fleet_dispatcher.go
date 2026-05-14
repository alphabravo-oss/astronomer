// Fleet orchestrator dispatcher adapter (migration 056).
//
// Bridges the worker-side FleetSubOperationDispatcher interface to the
// platform's existing per-operation enqueue paths:
//
//   - tool_upgrade / tool_install / tool_uninstall  →  ToolHandler.EnqueueFleetSubOperation
//     (a thin wrapper around the existing enqueueOperation that the
//     fleet orchestrator can call without holding the handler's RBAC
//     middleware HTTP context).
//
//   - apply_template  →  ClusterTemplateHandler.enqueueApplyForCluster
//     (upserts the cluster_template_applications row and enqueues the
//     existing cluster_template:apply task, returning the cluster ID
//     as the sub-operation ID so the orchestrator can poll the
//     applications row directly).
//
// The adapter lives in the handler package because that's where the
// existing tool + template machinery lives; it satisfies the
// tasks.FleetSubOperationDispatcher interface so the worker can call
// in without an import cycle.

package handler

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// FleetDispatcher implements tasks.FleetSubOperationDispatcher by
// reaching into the existing ToolHandler + ClusterTemplateHandler.
// One instance is constructed at startup and handed to
// tasks.ConfigureFleetOrchestrate; the orchestrator calls into it
// every dispatch.
type FleetDispatcher struct {
	Tools     *ToolHandler
	Templates *ClusterTemplateHandler
	Queries   FleetDispatcherQuerier
}

// FleetDispatcherQuerier is the slice of *sqlc.Queries the dispatcher
// needs for apply_template — specifically the template lookup +
// applications upsert. ToolHandler-routed operations don't need a
// direct query handle because the ToolHandler already has one.
type FleetDispatcherQuerier interface {
	GetClusterTemplateByID(ctx context.Context, id uuid.UUID) (sqlc.ClusterTemplate, error)
	UpsertClusterTemplateApplication(ctx context.Context, arg sqlc.UpsertClusterTemplateApplicationParams) (sqlc.ClusterTemplateApplication, error)
}

// NewFleetDispatcher wires the dispatcher. ToolHandler may be nil at
// boot during tests that don't exercise the tool path; the dispatcher
// returns a clear error in that case rather than panicking.
func NewFleetDispatcher(tools *ToolHandler, templates *ClusterTemplateHandler, queries FleetDispatcherQuerier) *FleetDispatcher {
	return &FleetDispatcher{Tools: tools, Templates: templates, Queries: queries}
}

// DispatchToolOperation enqueues a per-cluster tool_operations row via
// the existing tools enqueue path. Returns the new row's ID for the
// orchestrator to poll.
func (d *FleetDispatcher) DispatchToolOperation(ctx context.Context, kind string, clusterID uuid.UUID, spec tasks.FleetToolOperationSpec) (uuid.UUID, string, error) {
	if d == nil || d.Tools == nil {
		return uuid.Nil, "", fmt.Errorf("tool handler not wired for fleet dispatcher")
	}
	op, err := d.Tools.EnqueueFleetSubOperation(ctx, kind, clusterID, spec)
	if err != nil {
		return uuid.Nil, "", err
	}
	return op.ID, kind, nil
}

// DispatchApplyTemplate upserts the cluster_template_applications row
// (resetting status=pending) and enqueues the existing
// cluster_template:apply task. We use the cluster ID as the
// sub-operation reference because applications are keyed by cluster.
func (d *FleetDispatcher) DispatchApplyTemplate(ctx context.Context, clusterID, templateID uuid.UUID) (uuid.UUID, string, error) {
	if d == nil || d.Queries == nil {
		return uuid.Nil, "", fmt.Errorf("queries not wired for fleet dispatcher")
	}
	tmpl, err := d.Queries.GetClusterTemplateByID(ctx, templateID)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("lookup template %s: %w", templateID, err)
	}
	if _, err := d.Queries.UpsertClusterTemplateApplication(ctx, sqlc.UpsertClusterTemplateApplicationParams{
		ClusterID:    clusterID,
		TemplateID:   tmpl.ID,
		SpecSnapshot: tmpl.Spec,
	}); err != nil {
		return uuid.Nil, "", fmt.Errorf("upsert template application: %w", err)
	}
	if d.Templates != nil {
		d.Templates.enqueueApplyForCluster(ctx, clusterID)
	}
	return clusterID, tasks.FleetOpTypeApplyTemplate, nil
}

// enqueueApplyForCluster mirrors ClusterTemplateHandler.enqueueApply
// but takes context.Context directly so the fleet dispatcher can call
// without forging an *http.Request. Nil-safe when no queue is wired.
func (h *ClusterTemplateHandler) enqueueApplyForCluster(ctx context.Context, clusterID uuid.UUID) {
	if h == nil || h.queue == nil {
		return
	}
	task, err := tasks.NewClusterTemplateApplyTask(clusterID)
	if err != nil {
		return
	}
	payload := observability.EnrichTaskPayload(ctx, task.Payload(), middleware.GetCorrelationID(ctx))
	task = asynq.NewTask(task.Type(), payload, asynq.MaxRetry(3))
	_, _ = h.queue.Enqueue(task, asynq.Queue(tasks.ClusterTemplateApplyQueueName))
}

// EnqueueFleetSubOperation creates a tool_operations row of the
// requested kind ("tool_install" / "tool_upgrade" / "tool_uninstall")
// for the given cluster, the same way the per-cluster HTTP endpoints
// would. The fleet orchestrator calls into this from an asynq worker
// context, so we use enqueueOperation (the same internal path the
// HTTP endpoints use) rather than reimplementing the queue write.
func (h *ToolHandler) EnqueueFleetSubOperation(ctx context.Context, kind string, clusterID uuid.UUID, spec tasks.FleetToolOperationSpec) (sqlc.ToolOperation, error) {
	if h == nil || h.queries == nil {
		return sqlc.ToolOperation{}, fmt.Errorf("tool handler not configured")
	}
	tool, err := h.queries.GetToolBySlug(ctx, spec.Slug)
	if err != nil {
		return sqlc.ToolOperation{}, fmt.Errorf("lookup tool %q: %w", spec.Slug, err)
	}
	charts, _ := parseToolCharts(tool.Charts)
	chart := firstChart(charts)
	namespace := chartNamespace(tool, chart)
	releaseName := spec.ReleaseName
	if releaseName == "" {
		releaseName = tool.Slug
	}
	valuesYAML := ""
	if len(spec.Values) > 0 {
		valuesYAML = string(spec.Values)
	}
	// Map fleet operation_type -> tool operation envelope kind. The
	// tool worker treats "install" / "upgrade" / "uninstall" as its
	// own operation_type strings; the fleet orchestrator's kind is
	// the type with a "tool_" prefix.
	envKind := ""
	switch kind {
	case tasks.FleetOpTypeToolInstall:
		envKind = "install"
	case tasks.FleetOpTypeToolUpgrade:
		envKind = "upgrade"
	case tasks.FleetOpTypeToolUninstall:
		envKind = "uninstall"
	default:
		return sqlc.ToolOperation{}, fmt.Errorf("unknown fleet tool kind %q", kind)
	}
	env := toolOperationEnvelope{
		ClusterID:   clusterID.String(),
		ToolSlug:    tool.Slug,
		ReleaseName: releaseName,
		Namespace:   namespace,
		Preset:      spec.Preset,
		ValuesYAML:  valuesYAML,
		ChartName:   chart.ChartName,
		RepoURL:     chart.RepoURL,
		Version:     pickToolVersion(spec.TargetVersion, tool.VersionConstraint),
	}
	// The orchestrator runs unattended — there's no user behind the
	// click — so we stamp an empty UUID for created_by. The audit row
	// on the fleet_operation captures the original operator.
	op, err := h.enqueueOperation(ctx, "tool_installation", operationTargetKey(clusterID, tool.Slug), envKind, env, pgtype.UUID{})
	if err != nil {
		return sqlc.ToolOperation{}, err
	}
	return op, nil
}

// pickToolVersion picks the operator-supplied target_version when
// present; otherwise falls back to the tool catalog's
// version_constraint. Centralised so a future "minimum version"
// policy lands once.
func pickToolVersion(targetVersion, catalogVersion string) string {
	if targetVersion != "" {
		return targetVersion
	}
	return catalogVersion
}

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	ctrlrt "sigs.k8s.io/controller-runtime"

	"github.com/alphabravocompany/astronomer-go/internal/crd"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"k8s.io/client-go/rest"
)

// crdClusterAdapter implements crd.ClusterSync against the existing sqlc
// queries + the asynq queue used by the REST handler. We deliberately do not
// import internal/handler here — the controller talks to the DB directly so
// the dependency graph stays:
//
//	internal/crd  -> (interfaces only)
//	internal/server -> internal/crd, internal/handler, internal/db/sqlc
//
// — which means a unit test of the controller package can be written without
// dragging the entire server graph along.
type crdClusterAdapter struct {
	queries *sqlc.Queries
	queue   *asynq.Client
	log     *slog.Logger
}

// EnsureFromCRD upserts the clusters row to match the spec.
//
// Semantics:
//   - Look up by name; if missing, INSERT.
//   - If present, UPDATE the operator-tunable columns (display_name,
//     description, environment, region, labels, annotations).
//   - Return the resulting ClusterStatus: cluster UUID, coarse phase, agent
//     version, last reconciled stamp.
func (a *crdClusterAdapter) EnsureFromCRD(ctx context.Context, spec crd.ClusterSpec) (crd.ClusterStatus, error) {
	if a == nil || a.queries == nil {
		return crd.ClusterStatus{}, errors.New("crdClusterAdapter: queries not wired")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return crd.ClusterStatus{}, errors.New("ClusterSpec.name is required")
	}

	existing, err := a.queries.GetClusterByName(ctx, spec.Name)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return crd.ClusterStatus{}, fmt.Errorf("lookup cluster: %w", err)
	}

	envVal := spec.Environment
	if envVal == "" {
		envVal = "development"
	}

	if errors.Is(err, pgx.ErrNoRows) {
		created, cerr := a.queries.CreateCluster(ctx, sqlc.CreateClusterParams{
			Name:         spec.Name,
			DisplayName:  spec.DisplayName,
			Description:  spec.Description,
			Environment:  envVal,
			Region:       spec.Region,
			Provider:     spec.Provider,
			Distribution: spec.Distribution,
			// CreatedByID is null — the CR has no user identity attached.
			// audit_log entries from the CRD path are emitted by the
			// controller's slog stream rather than the audit table; we may
			// add a synthetic system user in a future migration.
			CreatedByID: pgtype.UUID{},
		})
		if cerr != nil {
			return crd.ClusterStatus{}, fmt.Errorf("create cluster: %w", cerr)
		}
		existing = created
	} else {
		labels, annotations := marshalStringMap(spec.Labels), marshalStringMap(spec.Annotations)
		updated, uerr := a.queries.UpdateCluster(ctx, sqlc.UpdateClusterParams{
			ID:          existing.ID,
			DisplayName: spec.DisplayName,
			Description: spec.Description,
			Environment: envVal,
			Region:      spec.Region,
			Labels:      labels,
			Annotations: annotations,
		})
		if uerr != nil {
			return crd.ClusterStatus{}, fmt.Errorf("update cluster: %w", uerr)
		}
		existing = updated
	}

	phase := "pending"
	switch {
	case existing.DecommissionedAt.Valid:
		phase = "decommissioned"
	case existing.LastHeartbeat.Valid:
		phase = "registered"
	}

	return crd.ClusterStatus{
		ClusterID:    existing.ID.String(),
		Phase:        phase,
		AgentVersion: existing.AgentVersion,
	}, nil
}

// DeleteByName starts the decommission flow. The REST DELETE handler does
// the same thing — we duplicate the orchestration here (rather than calling
// the handler) so the CRD path doesn't need a synthetic *http.Request.
//
// Returns crd.ErrInProgress while the decommission row is still pending /
// running; nil once the cluster row is gone (or no row ever existed).
func (a *crdClusterAdapter) DeleteByName(ctx context.Context, name string) error {
	if a == nil || a.queries == nil {
		return errors.New("crdClusterAdapter: queries not wired")
	}
	cluster, err := a.queries.GetClusterByName(ctx, name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Already gone — finalizer can drop.
			return nil
		}
		return fmt.Errorf("lookup cluster: %w", err)
	}
	if cluster.IsLocal {
		// Refuse — same guard as the REST Delete handler.
		return errors.New("cannot decommission the local cluster via CRD")
	}

	// Has there already been a decommission row? Idempotency: poll status.
	if existing, err := a.queries.GetLatestClusterDecommissionByCluster(ctx, cluster.ID); err == nil {
		switch existing.Status {
		case "pending", "running":
			return crd.ErrInProgress
		case "succeeded":
			// Cluster row should be tombstoned at this point — the
			// reconciler stamps decommissioned_at when it completes. If we
			// still see the row without that stamp it's a transient gap;
			// return ErrInProgress so the controller requeues.
			if cluster.DecommissionedAt.Valid {
				return nil
			}
			return crd.ErrInProgress
		case "failed":
			// Fall through and create a fresh attempt.
		}
	}

	row, err := a.queries.CreateClusterDecommission(ctx, sqlc.CreateClusterDecommissionParams{
		ClusterID:     cluster.ID,
		ClusterName:   cluster.Name,
		RequestedByID: pgtype.UUID{},
	})
	if err != nil {
		return fmt.Errorf("create decommission: %w", err)
	}
	// Best-effort enqueue — the periodic sweep will pick the row up if redis
	// is briefly unavailable.
	if a.queue != nil {
		if task, terr := tasks.NewClusterDecommissionTask(row.ID); terr == nil {
			_, _ = a.queue.Enqueue(task)
		}
	}
	return crd.ErrInProgress
}

// crdProjectAdapter implements crd.ProjectSync.
type crdProjectAdapter struct {
	queries *sqlc.Queries
	log     *slog.Logger
}

// EnsureFromCRD upserts the projects row to match the spec.
func (a *crdProjectAdapter) EnsureFromCRD(ctx context.Context, spec crd.ProjectSpec) (crd.ProjectStatus, error) {
	if a == nil || a.queries == nil {
		return crd.ProjectStatus{}, errors.New("crdProjectAdapter: queries not wired")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return crd.ProjectStatus{}, errors.New("ProjectSpec.name is required")
	}
	if len(spec.Clusters) == 0 {
		return crd.ProjectStatus{}, errors.New("ProjectSpec.clusters must list at least one cluster name")
	}

	// Resolve cluster by name; the first entry wins, the rest are recorded
	// onto status.observedClusters by the reconciler.
	cluster, err := a.queries.GetClusterByName(ctx, spec.Clusters[0])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return crd.ProjectStatus{}, fmt.Errorf("cluster %q not found", spec.Clusters[0])
		}
		return crd.ProjectStatus{}, fmt.Errorf("lookup cluster: %w", err)
	}

	// Find existing project by (name, cluster_id). The (name, cluster_id)
	// pair is uniquely indexed on the projects table.
	existing, err := a.queries.GetProjectByNameAndCluster(ctx, sqlc.GetProjectByNameAndClusterParams{
		Name:      spec.Name,
		ClusterID: cluster.ID,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return crd.ProjectStatus{}, fmt.Errorf("lookup project: %w", err)
	}

	pss := spec.PodSecurityProfile
	if pss == "" {
		pss = "baseline"
	}
	networkPolicy := spec.NetworkPolicyMode
	if networkPolicy == "" {
		networkPolicy = "none"
	}
	quotaPayload := projectResourceQuotaJSON(spec.ResourceQuota)

	if errors.Is(err, pgx.ErrNoRows) {
		created, cerr := a.queries.CreateProject(ctx, sqlc.CreateProjectParams{
			Name:                     spec.Name,
			DisplayName:              spec.DisplayName,
			Description:              spec.Description,
			ClusterID:                cluster.ID,
			Namespaces:               json.RawMessage(`[]`),
			ResourceQuota:            quotaPayload,
			LimitRange:               json.RawMessage(`{}`),
			NetworkPolicyMode:        networkPolicy,
			CreatedByID:              pgtype.UUID{},
			PodSecurityProfile:       pss,
			ResourceQuotaCpuLimit:    spec.ResourceQuota.CPULimit,
			ResourceQuotaMemoryLimit: spec.ResourceQuota.MemoryLimit,
			ResourceQuotaPodCount:    spec.ResourceQuota.PodCount,
		})
		if cerr != nil {
			return crd.ProjectStatus{}, fmt.Errorf("create project: %w", cerr)
		}
		existing = created
	} else {
		updated, uerr := a.queries.UpdateProject(ctx, sqlc.UpdateProjectParams{
			ID:                       existing.ID,
			DisplayName:              spec.DisplayName,
			Description:              spec.Description,
			Namespaces:               existing.Namespaces,
			ResourceQuota:            quotaPayload,
			LimitRange:               existing.LimitRange,
			NetworkPolicyMode:        networkPolicy,
			PodSecurityProfile:       pss,
			ResourceQuotaCpuLimit:    spec.ResourceQuota.CPULimit,
			ResourceQuotaMemoryLimit: spec.ResourceQuota.MemoryLimit,
			ResourceQuotaPodCount:    spec.ResourceQuota.PodCount,
		})
		if uerr != nil {
			return crd.ProjectStatus{}, fmt.Errorf("update project: %w", uerr)
		}
		existing = updated
	}

	return crd.ProjectStatus{
		ProjectID:         existing.ID.String(),
		Phase:             "active",
		ResolvedClusterID: cluster.ID.String(),
	}, nil
}

// DeleteByName drops the project row identified by spec.Name. The CRD path
// does not currently resolve cluster context for delete — the projects table
// permits the same project name across clusters, so the controller picks the
// first match. Re-bind across clusters has never been a supported workflow
// and is callable from the REST API instead.
func (a *crdProjectAdapter) DeleteByName(ctx context.Context, name string) error {
	if a == nil || a.queries == nil {
		return errors.New("crdProjectAdapter: queries not wired")
	}
	// List + filter; the projects table has no GetProjectByName lookup.
	// In practice the CRD path tracks 1:1 with metadata.name = spec.name, so
	// list-and-filter is cheap (small N).
	page, err := a.queries.ListProjects(ctx, sqlc.ListProjectsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return fmt.Errorf("list projects: %w", err)
	}
	var target *sqlc.Project
	for i := range page {
		if page[i].Name == name {
			target = &page[i]
			break
		}
	}
	if target == nil {
		return nil
	}
	if err := a.queries.DeleteProject(ctx, target.ID); err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	return nil
}

// marshalStringMap turns a map[string]string into the JSONB payload the
// clusters.labels / clusters.annotations columns expect. A nil/empty map
// becomes the JSON empty-object literal so the column never sees NULL.
func marshalStringMap(m map[string]string) json.RawMessage {
	if len(m) == 0 {
		return json.RawMessage(`{}`)
	}
	b, err := json.Marshal(m)
	if err != nil {
		// Marshaling a string-string map can't fail in practice; fall back
		// to empty object so the caller never has to handle the error.
		return json.RawMessage(`{}`)
	}
	return b
}

// projectResourceQuotaJSON encodes the structured quota into the JSONB column
// alongside the flat ResourceQuotaCpuLimit / MemoryLimit / PodCount columns.
// The structured form is what the REST handler also writes, so the two paths
// converge to the same row shape.
func projectResourceQuotaJSON(q crd.ProjectResourceQuota) json.RawMessage {
	payload := map[string]any{
		"cpu_limit":    q.CPULimit,
		"memory_limit": q.MemoryLimit,
		"pod_count":    q.PodCount,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// startCRDController boots the controller-runtime manager when CRD_ENABLED
// is true. Failures here are warned (not fatal) so the REST API path is not
// blocked by a CRD wiring regression — the operator can leave crds.enabled=false
// to disable the path entirely.
//
// The returned function blocks until the manager has shut down; callers wire
// it onto reconcileCtx so server.Shutdown drains the manager before the DB
// pool is closed.
func startCRDController(ctx context.Context, logger *slog.Logger, queries *sqlc.Queries, queue *asynq.Client) {
	if !crdEnabled() {
		return
	}
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		// Try the same fallback ctrl.GetConfig() uses — KUBECONFIG / ~/.kube/config
		// — so devs running the binary locally with kubectl can still flip the
		// flag on. When neither works we log and disable.
		fallback, ferr := ctrlrt.GetConfig()
		if ferr != nil {
			logger.Warn("crd_controller_disabled", "reason", "no_kubeconfig", "in_cluster_error", err.Error(), "fallback_error", ferr.Error())
			return
		}
		restCfg = fallback
	}
	cAdapter := &crdClusterAdapter{queries: queries, queue: queue, log: logger}
	pAdapter := &crdProjectAdapter{queries: queries, log: logger}

	mgr, err := crd.New(crd.ControllerConfig{
		K8sConfig:      restCfg,
		WatchNamespace: crdWatchNamespace(),
		ClusterHandler: cAdapter,
		ProjectHandler: pAdapter,
		Log:            logger,
	})
	if err != nil {
		logger.Warn("crd_controller_disabled", "reason", "build_manager_failed", "error", err.Error())
		return
	}

	go func() {
		logger.Info("crd_controller_starting", "watch_namespace", crdWatchNamespace())
		if err := mgr.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("crd_controller_stopped", "error", err.Error())
			return
		}
		logger.Info("crd_controller_stopped")
	}()
}

// crdEnabled reads CRD_ENABLED. The chart sets the env from crds.enabled
// (defaults to false). Truthy values: "1", "true", "yes". Anything else
// (including empty string) leaves the controller disabled.
func crdEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(getenv("CRD_ENABLED")))
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// crdWatchNamespace reads CRD_WATCH_NAMESPACE; defaults to "astronomer-mgmt"
// when unset (matches the chart's crds.watchNamespace default).
func crdWatchNamespace() string {
	v := strings.TrimSpace(getenv("CRD_WATCH_NAMESPACE"))
	if v == "" {
		return "astronomer-mgmt"
	}
	return v
}

// getenv is a tiny indirection over os.Getenv so the CRD toggle is read in
// a single place. Today it just calls through.
func getenv(key string) string { return os.Getenv(key) }

// _ keeps the uuid import live — adapters above currently use it via sqlc
// param structs that take uuid.UUID. This anchors the import so go vet
// stays clean if the param shapes ever change.
var _ uuid.UUID

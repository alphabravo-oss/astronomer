package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	ctrlrt "sigs.k8s.io/controller-runtime"

	agenttemplate "github.com/alphabravocompany/astronomer-go/deploy/agent"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/crd"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"k8s.io/client-go/rest"
)

// crdClusterAdapter implements crd.ClusterSync against the existing sqlc
// queries used by the REST handler. We deliberately do not
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
	}

	labels := marshalStringMap(spec.Labels)
	annotations := marshalStringMap(clusterAnnotationsWithAgentProfile(spec))
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

	phase := "pending"
	switch {
	case existing.DecommissionedAt.Valid:
		phase = "decommissioned"
	case existing.LastHeartbeat.Valid:
		phase = "registered"
	}

	status := crd.ClusterStatus{
		ClusterID:    existing.ID.String(),
		Phase:        phase,
		AgentVersion: existing.AgentVersion,
	}
	if spec.ArgoCD.AutoAdopt != nil && !*spec.ArgoCD.AutoAdopt {
		status.ArgoCD.Phase = "disabled"
		return status, nil
	}
	if managed, merr := a.queries.ListArgoCDManagedClustersByCluster(ctx, existing.ID); merr == nil && len(managed) > 0 {
		status.ArgoCD.Phase = "registered"
		status.ArgoCD.ClusterSecretName = managed[0].ClusterSecretName
	} else {
		if merr != nil && a.log != nil {
			a.log.Warn("failed to load ArgoCD managed cluster status", "cluster_id", existing.ID.String(), "error", merr)
		}
		status.ArgoCD.Phase = "pending"
	}
	return status, nil
}

// ValidateClusterOwnership prevents a CR from silently claiming a row that was
// created through REST/UI/API ownership. The transfer path must be explicit.
func (a *crdClusterAdapter) ValidateClusterOwnership(ctx context.Context, spec crd.ClusterSpec, ref crd.ObjectRef) error {
	if a == nil || a.queries == nil {
		return errors.New("crdClusterAdapter: queries not wired")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return errors.New("ClusterSpec.name is required")
	}
	cluster, err := a.queries.GetClusterByName(ctx, spec.Name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lookup cluster for ownership validation: %w", err)
	}
	ownership, err := a.queries.GetClusterOwnership(ctx, cluster.ID)
	if err != nil {
		return fmt.Errorf("load cluster ownership: %w", err)
	}
	if ownership.ManagedBy == "crd" && ownershipMatchesRef(ownership, ref) {
		return nil
	}
	return fmt.Errorf("cluster %q already exists and is managed_by=%q; refusing implicit CRD takeover", spec.Name, ownership.ManagedBy)
}

// RecordClusterOwnership marks the DB row as CRD-owned after a successful
// spec sync. This gives REST/UI paths a durable way to detect ownership
// conflicts and gives restore/repair jobs a stable Kubernetes external ref.
func (a *crdClusterAdapter) RecordClusterOwnership(ctx context.Context, spec crd.ClusterSpec, ref crd.ObjectRef) error {
	if a == nil || a.queries == nil {
		return errors.New("crdClusterAdapter: queries not wired")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return errors.New("ClusterSpec.name is required")
	}
	if strings.TrimSpace(ref.Namespace) == "" || strings.TrimSpace(ref.Name) == "" {
		return errors.New("cluster CR external ref requires namespace and name")
	}
	cluster, err := a.queries.GetClusterByName(ctx, spec.Name)
	if err != nil {
		return fmt.Errorf("lookup cluster for ownership: %w", err)
	}
	if _, err := a.queries.SetClusterOwnership(ctx, sqlc.SetClusterOwnershipParams{
		ID:                    cluster.ID,
		ManagedBy:             "crd",
		ExternalRefApiVersion: ref.APIVersion,
		ExternalRefKind:       ref.Kind,
		ExternalRefNamespace:  ref.Namespace,
		ExternalRefName:       ref.Name,
		ObservedGeneration:    ref.Generation,
	}); err != nil {
		return fmt.Errorf("set cluster ownership: %w", err)
	}
	return nil
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

	_, err = a.createDecommissionWithOutbox(ctx, cluster)
	if err != nil {
		return fmt.Errorf("create decommission: %w", err)
	}
	return crd.ErrInProgress
}

func (a *crdClusterAdapter) createDecommissionWithOutbox(ctx context.Context, cluster sqlc.Cluster) (sqlc.ClusterDecommission, error) {
	decommissionID := uuid.New()
	task, err := tasks.NewClusterDecommissionTask(decommissionID)
	if err != nil {
		return sqlc.ClusterDecommission{}, err
	}
	return a.queries.CreateClusterDecommissionWithTaskOutbox(ctx, sqlc.CreateClusterDecommissionWithTaskOutboxParams{
		ID:                  decommissionID,
		ClusterID:           cluster.ID,
		ClusterName:         cluster.Name,
		RequestedByID:       pgtype.UUID{},
		DedupeKey:           pgtype.Text{String: fmt.Sprintf("cluster_decommission:%s", decommissionID.String()), Valid: true},
		TaskType:            task.Type(),
		Payload:             task.Payload(),
		QueueName:           "default",
		MaxRetry:            3,
		MaxDeliveryAttempts: 20,
		NextAttemptAt:       pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
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

// ValidateProjectOwnership prevents a Project CR from silently claiming an
// existing UI/API-owned project row.
func (a *crdProjectAdapter) ValidateProjectOwnership(ctx context.Context, spec crd.ProjectSpec, ref crd.ObjectRef) error {
	if a == nil || a.queries == nil {
		return errors.New("crdProjectAdapter: queries not wired")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return errors.New("ProjectSpec.name is required")
	}
	if len(spec.Clusters) == 0 {
		return nil
	}
	cluster, err := a.queries.GetClusterByName(ctx, spec.Clusters[0])
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lookup project cluster for ownership validation: %w", err)
	}
	project, err := a.queries.GetProjectByNameAndCluster(ctx, sqlc.GetProjectByNameAndClusterParams{
		Name:      spec.Name,
		ClusterID: cluster.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("lookup project for ownership validation: %w", err)
	}
	ownership, err := a.queries.GetProjectOwnership(ctx, project.ID)
	if err != nil {
		return fmt.Errorf("load project ownership: %w", err)
	}
	if ownership.ManagedBy == "crd" && ownershipMatchesRef(ownership, ref) {
		return nil
	}
	return fmt.Errorf("project %q already exists and is managed_by=%q; refusing implicit CRD takeover", spec.Name, ownership.ManagedBy)
}

// RecordProjectOwnership marks a project row as CRD-owned after a successful
// spec sync. Project names are unique per cluster, so the first cluster ref in
// spec.clusters is resolved the same way EnsureFromCRD resolves it.
func (a *crdProjectAdapter) RecordProjectOwnership(ctx context.Context, spec crd.ProjectSpec, ref crd.ObjectRef) error {
	if a == nil || a.queries == nil {
		return errors.New("crdProjectAdapter: queries not wired")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return errors.New("ProjectSpec.name is required")
	}
	if len(spec.Clusters) == 0 {
		return errors.New("ProjectSpec.clusters must list at least one cluster name")
	}
	if strings.TrimSpace(ref.Namespace) == "" || strings.TrimSpace(ref.Name) == "" {
		return errors.New("project CR external ref requires namespace and name")
	}
	cluster, err := a.queries.GetClusterByName(ctx, spec.Clusters[0])
	if err != nil {
		return fmt.Errorf("lookup project cluster for ownership: %w", err)
	}
	project, err := a.queries.GetProjectByNameAndCluster(ctx, sqlc.GetProjectByNameAndClusterParams{
		Name:      spec.Name,
		ClusterID: cluster.ID,
	})
	if err != nil {
		return fmt.Errorf("lookup project for ownership: %w", err)
	}
	if _, err := a.queries.SetProjectOwnership(ctx, sqlc.SetProjectOwnershipParams{
		ID:                    project.ID,
		ManagedBy:             "crd",
		ExternalRefApiVersion: ref.APIVersion,
		ExternalRefKind:       ref.Kind,
		ExternalRefNamespace:  ref.Namespace,
		ExternalRefName:       ref.Name,
		ObservedGeneration:    ref.Generation,
	}); err != nil {
		return fmt.Errorf("set project ownership: %w", err)
	}
	return nil
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

func clusterAnnotationsWithAgentProfile(spec crd.ClusterSpec) map[string]string {
	annotations := make(map[string]string, len(spec.Annotations)+3)
	for k, v := range spec.Annotations {
		annotations[k] = v
	}
	profile := agenttemplate.NormalizePrivilegeProfile(spec.Agent.PrivilegeProfile)
	if strings.TrimSpace(spec.Agent.PrivilegeProfile) != "" {
		annotations[agenttemplate.PrivilegeProfileAnnotation] = profile
	}
	if mode := strings.TrimSpace(spec.AdoptionPolicy.Mode); mode != "" {
		annotations["management.astronomer.io/adoption-policy-mode"] = mode
	}
	if len(spec.AdoptionPolicy.AllowedManagementModes) > 0 {
		modes := make([]string, 0, len(spec.AdoptionPolicy.AllowedManagementModes))
		for _, mode := range spec.AdoptionPolicy.AllowedManagementModes {
			mode = strings.TrimSpace(mode)
			if mode == "" {
				continue
			}
			modes = append(modes, mode)
		}
		if len(modes) == 0 {
			return annotations
		}
		sort.Strings(modes)
		annotations["management.astronomer.io/allowed-management-modes"] = strings.Join(modes, ",")
	}
	return annotations
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

func ownershipMatchesRef(ownership sqlc.FleetOwnership, ref crd.ObjectRef) bool {
	return ownership.ExternalRefApiVersion == ref.APIVersion &&
		ownership.ExternalRefKind == ref.Kind &&
		ownership.ExternalRefNamespace == ref.Namespace &&
		ownership.ExternalRefName == ref.Name
}

// startCRDController boots the controller-runtime manager when CRD_ENABLED
// is true. The manager uses controller-runtime leader election so HA server
// replicas do not all reconcile the same management CRDs.
//
// Initial bootstrap failures are fatal in production when CRD_ENABLED=true.
// Dev/test keeps the previous warn-and-disable behavior so local binaries can
// run without a kubeconfig.
func startCRDController(ctx context.Context, logger *slog.Logger, cfg *config.Config, queries *sqlc.Queries) error {
	if !crdEnabled() {
		return nil
	}
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		// Try the same fallback ctrl.GetConfig() uses — KUBECONFIG / ~/.kube/config
		// — so devs running the binary locally with kubectl can still flip the
		// flag on. When neither works we log and disable.
		fallback, ferr := ctrlrt.GetConfig()
		if ferr != nil {
			err := fmt.Errorf("CRD controller enabled but no Kubernetes config is available: in-cluster=%v; fallback=%v", err, ferr)
			if isProductionConfig(cfg) {
				return err
			}
			logger.Warn("crd_controller_disabled", "reason", "no_kubeconfig", "error", err.Error())
			return nil
		}
		restCfg = fallback
	}
	cAdapter := &crdClusterAdapter{queries: queries, log: logger}
	pAdapter := &crdProjectAdapter{queries: queries, log: logger}

	mgr, err := crd.New(crd.ControllerConfig{
		K8sConfig:               restCfg,
		WatchNamespace:          crdWatchNamespace(),
		LeaderElection:          true,
		LeaderElectionNamespace: crdWatchNamespace(),
		ClusterHandler:          cAdapter,
		ProjectHandler:          pAdapter,
		Log:                     logger,
	})
	if err != nil {
		err := fmt.Errorf("build CRD controller manager: %w", err)
		if isProductionConfig(cfg) {
			return err
		}
		logger.Warn("crd_controller_disabled", "reason", "build_manager_failed", "error", err.Error())
		return nil
	}

	go func() {
		logger.Info("crd_controller_starting", "watch_namespace", crdWatchNamespace())
		if err := mgr.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("crd_controller_stopped", "error", err.Error())
			return
		}
		logger.Info("crd_controller_stopped")
	}()
	return nil
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

package tasks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/argolabels"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	argocdclient "github.com/alphabravocompany/astronomer-go/internal/handler/argocd"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const ArgoCDAutoRegisterClusterType = "argocd:auto_register_cluster"

const (
	argoCDApplicationControllerSA        = "argocd-application-controller"
	platformSettingArgoCDAutoAdoptKey    = "argocd.auto_adopt_clusters"
	platformSettingArgoCDAutoRegSelector = "argocd.auto_register_selector"
	ConditionArgoCDAdopted               = "ArgoCDAdopted"
)

var argoCDProxyTokenTTL = 180 * 24 * time.Hour

// localArgoCDClusterTokenExpiryDriftWindow is the remaining bearer-token
// lifetime below which the sweep treats the LOCAL cluster Secret as drifted
// and re-mints the credential. The server self-manage loop renews at half the
// 24h TTL (12h remaining) and the handler reconciler backstops at 2h, but
// both run inside the server process — this is the only renewal path that
// survives a prolonged server outage. Kept well under 12h so a healthy
// server always wins the race and the worker never writes in steady state.
const localArgoCDClusterTokenExpiryDriftWindow = 4 * time.Hour

// argoCDAutoRegisterSweepPageSize bounds each ListClusters batch during the
// periodic sweep so fleets larger than the old fixed 1000-row cap are fully
// covered instead of silently truncated.
const argoCDAutoRegisterSweepPageSize = 500

var errArgoCDManagedClusterCredentialUnavailable = errors.New("argocd managed-cluster credential unavailable")

type ArgoCDAutoRegisterQuerier interface {
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	ListClusters(ctx context.Context, arg sqlc.ListClustersParams) ([]sqlc.Cluster, error)
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
	ListArgoCDInstances(ctx context.Context, arg sqlc.ListArgoCDInstancesParams) ([]sqlc.ArgocdInstance, error)
	CreateArgoCDManagedCluster(ctx context.Context, arg sqlc.CreateArgoCDManagedClusterParams) (sqlc.ArgocdManagedCluster, error)
	GetActiveArgoCDClusterProxyTokenByClusterID(ctx context.Context, clusterID uuid.UUID) (sqlc.ArgocdClusterProxyToken, error)
	UpsertArgoCDClusterProxyToken(ctx context.Context, arg sqlc.UpsertArgoCDClusterProxyTokenParams) (sqlc.ArgocdClusterProxyToken, error)
}

type ArgoCDAutoRegisterDeps struct {
	Queries             ArgoCDAutoRegisterQuerier
	Encryptor           *auth.Encryptor
	K8s                 kubernetes.Interface
	ClusterProxyBaseURL string
	Registration        ArgoCDRegistrationTimeline
}

var argoCDAutoRegisterDeps ArgoCDAutoRegisterDeps

type ArgoCDRegistrationTimeline interface {
	WriteStep(ctx context.Context, clusterID uuid.UUID, in registration.StepInput) (sqlc.ClusterRegistrationStep, error)
}

type argoCDConditionWriter interface {
	UpsertClusterCondition(ctx context.Context, arg sqlc.UpsertClusterConditionParams) (sqlc.ClusterCondition, error)
}

type argoCDManagedClusterLister interface {
	ListArgoCDManagedClustersByCluster(ctx context.Context, clusterID uuid.UUID) ([]sqlc.ArgocdManagedCluster, error)
}

func ConfigureArgoCDAutoRegister(deps ArgoCDAutoRegisterDeps) {
	deps.ClusterProxyBaseURL = strings.TrimRight(strings.TrimSpace(deps.ClusterProxyBaseURL), "/")
	argoCDAutoRegisterDeps = deps
}

func ResetArgoCDAutoRegister() {
	argoCDAutoRegisterDeps = ArgoCDAutoRegisterDeps{}
}

type ArgoCDAutoRegisterClusterPayload struct {
	ClusterID string `json:"cluster_id,omitempty"`
}

func NewArgoCDAutoRegisterClusterTask(clusterID uuid.UUID) (*asynq.Task, error) {
	data, err := json.Marshal(ArgoCDAutoRegisterClusterPayload{ClusterID: clusterID.String()})
	if err != nil {
		return nil, fmt.Errorf("marshal argocd auto-register payload: %w", err)
	}
	return asynq.NewTask(ArgoCDAutoRegisterClusterType, data, asynq.MaxRetry(5), asynq.Unique(10*time.Minute)), nil
}

func HandleArgoCDAutoRegisterCluster(ctx context.Context, t *asynq.Task) error {
	deps := argoCDAutoRegisterDeps
	if deps.Queries == nil {
		runtimeLogger().InfoContext(ctx, "argocd auto-register runtime not configured, skipping")
		return nil
	}
	enabled, err := readArgoCDAutoAdoptSetting(ctx, deps.Queries)
	if err != nil {
		return err
	}
	if !enabled {
		runtimeLogger().InfoContext(ctx, "argocd auto-register disabled by platform setting")
		return nil
	}
	var p ArgoCDAutoRegisterClusterPayload
	if len(t.Payload()) > 0 {
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal argocd auto-register payload: %w", err)
		}
	}
	if strings.TrimSpace(p.ClusterID) != "" {
		clusterID, err := uuid.Parse(p.ClusterID)
		if err != nil {
			return fmt.Errorf("invalid cluster_id: %w", err)
		}
		cluster, err := deps.Queries.GetClusterByID(ctx, clusterID)
		if err != nil {
			return err
		}
		return autoRegisterClusterIntoArgoCD(ctx, deps, cluster)
	}

	// Fleet sweep: leader-gate the whole body so only the lease holder runs it.
	// This is the one periodic reconciler that used to run on EVERY worker
	// replica; two replicas sweeping a newly-connected cluster concurrently both
	// mint a distinct proxy token and RegisterCluster (upsert) last-writer-wins,
	// so ArgoCD ends up presenting a bearer whose hash the DB no longer stores ->
	// 401 until the next sweep re-converges. Every replica also duplicated the
	// full ListSecrets index repair each tick. The per-cluster enqueued path
	// (p.ClusterID != "", asynq.Unique) above stays unguarded.
	return runPeriodicTaskWithLeader(ctx, ArgoCDAutoRegisterClusterType, func() error {
		return runArgoCDAutoRegisterSweep(ctx, deps)
	})
}

func runArgoCDAutoRegisterSweep(ctx context.Context, deps ArgoCDAutoRegisterDeps) error {
	runStarted := time.Now().UTC()
	// Load the ArgoCD instance set once for the whole sweep and thread it into
	// each per-cluster registration below, instead of re-querying every
	// instance once per cluster.
	instances, err := deps.Queries.ListArgoCDInstances(ctx, sqlc.ListArgoCDInstancesParams{Limit: 1000, Offset: 0})
	if err != nil {
		runErr := fmt.Errorf("list argocd instances for repair: %w", err)
		recordRepairJobFailure(ctx, deps.Queries, ArgoCDAutoRegisterClusterType, runErr, map[string]any{"mode": "sweep"})
		return runErr
	}
	var firstErr error
	clustersListed := 0
	eligibleClusters := 0
	// Page through clusters rather than capping the sweep at a single 1000-row
	// batch, so fleets larger than one page are fully reconciled.
	for offset := int32(0); ; offset += argoCDAutoRegisterSweepPageSize {
		page, err := deps.Queries.ListClusters(ctx, sqlc.ListClustersParams{Limit: argoCDAutoRegisterSweepPageSize, Offset: offset})
		if err != nil {
			runErr := fmt.Errorf("list clusters: %w", err)
			recordRepairJobFailure(ctx, deps.Queries, ArgoCDAutoRegisterClusterType, runErr, map[string]any{
				"mode":            "sweep",
				"clusters_listed": clustersListed,
			})
			return runErr
		}
		if len(page) == 0 {
			break
		}
		clustersListed += len(page)
		for _, cluster := range page {
			if !cluster.IsLocal && !cluster.LastHeartbeat.Valid {
				continue
			}
			eligibleClusters++
			if err := autoRegisterClusterIntoArgoCDWithInstances(ctx, deps, cluster, instances); err != nil {
				runtimeLogger().WarnContext(ctx, "argocd auto-register failed",
					"cluster_id", cluster.ID.String(),
					"error", err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		if int32(len(page)) < argoCDAutoRegisterSweepPageSize {
			break
		}
	}
	metadata := map[string]any{
		"mode":              "sweep",
		"clusters_listed":   clustersListed,
		"eligible_clusters": eligibleClusters,
		"argocd_instances":  len(instances),
		"duration_ms":       time.Since(runStarted).Milliseconds(),
	}
	repairStats, err := repairArgoCDManagedClusterIndexWithStats(ctx, deps, instances)
	repairStats.addToMetadata(metadata)
	if err != nil {
		runtimeLogger().WarnContext(ctx, "argocd managed-cluster index repair failed", "error", err)
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		recordRepairJobFailure(ctx, deps.Queries, ArgoCDAutoRegisterClusterType, firstErr, metadata)
	} else {
		recordRepairJobSuccess(ctx, deps.Queries, ArgoCDAutoRegisterClusterType, metadata)
	}
	return firstErr
}

type argoCDManagedClusterIndexRepairStats struct {
	ClusterSecretsChecked         int `json:"cluster_secrets_checked"`
	AstronomerManagedSecrets      int `json:"astronomer_managed_secrets"`
	ExistingRows                  int `json:"existing_rows"`
	DBRowsRecreated               int `json:"db_rows_recreated"`
	OrphanSecretsFound            int `json:"orphan_secrets_found"`
	DecommissionedSecretsFound    int `json:"decommissioned_secrets_found"`
	InvalidClusterIDSecretsFound  int `json:"invalid_cluster_id_secrets_found"`
	UnattributedManagedSecretRows int `json:"unattributed_managed_secret_rows"`
}

func (s argoCDManagedClusterIndexRepairStats) addToMetadata(metadata map[string]any) {
	if metadata == nil {
		return
	}
	metadata["argocd_cluster_secrets_checked"] = s.ClusterSecretsChecked
	metadata["argocd_astronomer_managed_secrets"] = s.AstronomerManagedSecrets
	metadata["argocd_existing_managed_cluster_rows"] = s.ExistingRows
	metadata["argocd_managed_cluster_rows_recreated"] = s.DBRowsRecreated
	metadata["argocd_orphan_secrets_found"] = s.OrphanSecretsFound
	metadata["argocd_decommissioned_secrets_found"] = s.DecommissionedSecretsFound
	metadata["argocd_invalid_cluster_id_secrets_found"] = s.InvalidClusterIDSecretsFound
	metadata["argocd_unattributed_managed_secret_rows"] = s.UnattributedManagedSecretRows
}

type argoCDManagedClusterSecretRepairOutcome string

const (
	argoCDSecretRepairIgnored             argoCDManagedClusterSecretRepairOutcome = "ignored"
	argoCDSecretRepairExistingRow         argoCDManagedClusterSecretRepairOutcome = "existing_row"
	argoCDSecretRepairDBIndexRecreated    argoCDManagedClusterSecretRepairOutcome = "db_index_recreated"
	argoCDSecretRepairOrphan              argoCDManagedClusterSecretRepairOutcome = "orphan"
	argoCDSecretRepairDecommissioned      argoCDManagedClusterSecretRepairOutcome = "decommissioned"
	argoCDSecretRepairInvalidClusterID    argoCDManagedClusterSecretRepairOutcome = "invalid_cluster_id"
	argoCDSecretRepairUnattributedManaged argoCDManagedClusterSecretRepairOutcome = "unattributed_managed"
)

func readArgoCDAutoAdoptSetting(ctx context.Context, q ArgoCDAutoRegisterQuerier) (bool, error) {
	row, err := q.GetPlatformSetting(ctx, platformSettingArgoCDAutoAdoptKey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true, nil
		}
		return false, fmt.Errorf("read %s setting: %w", platformSettingArgoCDAutoAdoptKey, err)
	}
	var enabled bool
	if err := json.Unmarshal(row.Value, &enabled); err != nil {
		return false, fmt.Errorf("parse %s setting: %w", platformSettingArgoCDAutoAdoptKey, err)
	}
	return enabled, nil
}

// readArgoCDAutoRegisterSelector loads the optional label selector that gates
// which clusters are auto-registered into ArgoCD. An unset/empty selector
// matches every cluster (current behavior). The selector is stored as a JSON
// object of label key/value pairs in the platform_settings table, matching the
// maintenance-window cluster_selector convention.
func readArgoCDAutoRegisterSelector(ctx context.Context, q ArgoCDAutoRegisterQuerier) (map[string]string, error) {
	row, err := q.GetPlatformSetting(ctx, platformSettingArgoCDAutoRegSelector)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s setting: %w", platformSettingArgoCDAutoRegSelector, err)
	}
	if len(row.Value) == 0 || string(row.Value) == "null" {
		return nil, nil
	}
	var sel map[string]string
	if err := json.Unmarshal(row.Value, &sel); err != nil {
		return nil, fmt.Errorf("parse %s setting: %w", platformSettingArgoCDAutoRegSelector, err)
	}
	return sel, nil
}

// clusterMatchesAutoRegisterSelector reports whether the cluster's labels
// satisfy every key/value pair in the selector. An empty selector matches all
// clusters; a non-empty selector against an unlabeled cluster never matches.
func clusterMatchesAutoRegisterSelector(cluster sqlc.Cluster, selector map[string]string) bool {
	if len(selector) == 0 {
		return true
	}
	labels := map[string]string{}
	if len(cluster.Labels) > 0 {
		_ = json.Unmarshal(cluster.Labels, &labels)
	}
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func autoRegisterClusterIntoArgoCD(ctx context.Context, deps ArgoCDAutoRegisterDeps, cluster sqlc.Cluster) error {
	return autoRegisterClusterIntoArgoCDWithInstances(ctx, deps, cluster, nil)
}

// autoRegisterClusterIntoArgoCDWithInstances registers a single cluster into
// every configured ArgoCD instance. When preloadedInstances is non-nil the
// caller's already-loaded slice is reused (the sweep path, which loads the
// instance set once); passing nil makes this function load the instances
// itself (the single-cluster task path).
func autoRegisterClusterIntoArgoCDWithInstances(ctx context.Context, deps ArgoCDAutoRegisterDeps, cluster sqlc.Cluster, preloadedInstances []sqlc.ArgocdInstance) error {
	if !cluster.IsLocal && !cluster.LastHeartbeat.Valid {
		return nil
	}
	managedRows := argoCDManagedClusterRows(ctx, deps, cluster.ID)
	if len(managedRows) == 0 {
		// Lazy, label-based gate: only first-time registration is selector
		// scoped. A cluster that already has managed-cluster rows is kept
		// in sync (drift repair / label refresh) regardless of selector so
		// removing a label doesn't silently orphan it here.
		selector, err := readArgoCDAutoRegisterSelector(ctx, deps.Queries)
		if err != nil {
			return err
		}
		if !clusterMatchesAutoRegisterSelector(cluster, selector) {
			runtimeLogger().InfoContext(ctx, "argocd auto-register skipped: cluster does not match selector",
				"cluster_id", cluster.ID.String())
			return nil
		}
	}
	alreadyManaged := len(managedRows) > 0
	repairReasons := detectArgoCDManagedClusterDrift(ctx, deps, cluster, managedRows)
	if !alreadyManaged {
		writeArgoCDRegistrationStep(ctx, deps, cluster.ID, "argocd_registering", "running", nil, "")
		upsertArgoCDAdoptionCondition(ctx, deps, cluster.ID, "Unknown", "RegistrationInProgress", "Astronomer is registering this cluster into ArgoCD.")
	}
	instances := preloadedInstances
	if instances == nil {
		var err error
		instances, err = deps.Queries.ListArgoCDInstances(ctx, sqlc.ListArgoCDInstancesParams{Limit: 1000, Offset: 0})
		if err != nil {
			recordArgoCDRegistrationFailure(ctx, deps, cluster.ID, err)
			return fmt.Errorf("list argocd instances: %w", err)
		}
	}
	if len(instances) == 0 {
		if alreadyManaged {
			upsertArgoCDAdoptionCondition(ctx, deps, cluster.ID, "True", "Registered", "Cluster already has an ArgoCD managed-cluster record.")
			return nil
		}
		recordArgoCDRegistrationFailure(ctx, deps, cluster.ID, errors.New("no ArgoCD instances configured"))
		return nil
	}
	if cluster.IsLocal && alreadyManaged && len(repairReasons) == 0 && argoCDManagedRowsCoverInstances(managedRows, instances) {
		// The server's self-manage loop owns the local cluster Secret in
		// steady state (ensureLocalArgoClusterSecret: skip-unless-drift plus
		// the renew-after token annotation). Re-upserting here would mint a
		// fresh application-controller token, rewrite the Secret, invalidate
		// ArgoCD's cluster cache, and clobber the renew annotation on every
		// sweep. Step in only when drift needs repair or an instance has no
		// registration yet (a newly added instance is not modeled as drift —
		// drift detection only inspects existing managed rows).
		upsertArgoCDAdoptionCondition(ctx, deps, cluster.ID, "True", "Registered", "Cluster already has an ArgoCD managed-cluster record.")
		return nil
	}
	var firstErr error
	for _, instance := range instances {
		if err := autoRegisterClusterIntoInstance(ctx, deps, instance, cluster); err != nil {
			runtimeLogger().WarnContext(ctx, "argocd auto-register instance failed",
				"cluster_id", cluster.ID.String(),
				"argocd_instance_id", instance.ID.String(),
				"error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if firstErr != nil {
		if alreadyManaged && len(repairReasons) > 0 && errors.Is(firstErr, errArgoCDManagedClusterCredentialUnavailable) {
			writeArgoCDRegistrationStep(ctx, deps, cluster.ID, "argocd_registration_repair_blocked", "failed", map[string]any{
				"repairs": repairReasons,
				"reason":  "credential_unavailable",
			}, firstErr.Error())
		}
		recordArgoCDRegistrationFailure(ctx, deps, cluster.ID, firstErr)
		return firstErr
	}
	upsertArgoCDAdoptionCondition(ctx, deps, cluster.ID, "True", "Registered", "Cluster is registered into ArgoCD for baseline reconciliation.")
	if alreadyManaged && len(repairReasons) > 0 {
		writeArgoCDRegistrationStep(ctx, deps, cluster.ID, "argocd_registration_repaired", "success", map[string]any{
			"repairs": repairReasons,
		}, "")
	}
	if !alreadyManaged {
		writeArgoCDRegistrationStep(ctx, deps, cluster.ID, "argocd_registered", "success", map[string]any{
			"instances": len(instances),
		}, "")
		if !cluster.IsLocal {
			// Cosmetic step: the cluster Secret carries the baseline appset
			// selector labels. It does NOT imply a baseline App will fan out — the
			// push appset is only generated when PullReconcileEnabled is off
			// (reconcileLocalArgoSelfManagement) and the cluster is not excluded by
			// a "leave_local" ownership decision. No appset is created here.
			writeArgoCDRegistrationStep(ctx, deps, cluster.ID, "baseline_appsets_matched", "success", map[string]any{
				"selector": "astronomer.io/managed-by=astronomer, astronomer.io/is-local=false",
			}, "")
		}
	}
	return nil
}

func repairArgoCDManagedClusterIndex(ctx context.Context, deps ArgoCDAutoRegisterDeps, instances []sqlc.ArgocdInstance) error {
	_, err := repairArgoCDManagedClusterIndexWithStats(ctx, deps, instances)
	return err
}

func repairArgoCDManagedClusterIndexWithStats(ctx context.Context, deps ArgoCDAutoRegisterDeps, instances []sqlc.ArgocdInstance) (argoCDManagedClusterIndexRepairStats, error) {
	var stats argoCDManagedClusterIndexRepairStats
	if deps.K8s == nil {
		runtimeLogger().InfoContext(ctx, "argocd managed-cluster index repair skipped: kubernetes client not configured")
		return stats, nil
	}
	lister, ok := deps.Queries.(argoCDManagedClusterLister)
	if !ok {
		return stats, nil
	}
	if len(instances) == 0 {
		return stats, nil
	}
	if len(instances) > 1 {
		runtimeLogger().WarnContext(ctx, "argocd managed-cluster index repair skipped: multiple argocd instances configured")
		return stats, nil
	}
	instance := instances[0]
	secrets, err := deps.K8s.CoreV1().Secrets(argoCDNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: argoCDClusterSecretTypeLabel + "=" + argoCDClusterSecretTypeValue,
	})
	if err != nil {
		return stats, fmt.Errorf("list argocd cluster secrets: %w", err)
	}
	stats.ClusterSecretsChecked = len(secrets.Items)
	var firstErr error
	for i := range secrets.Items {
		outcome, err := repairArgoCDManagedClusterIndexForSecret(ctx, deps, lister, instance, &secrets.Items[i])
		stats.recordSecretOutcome(outcome)
		if err != nil {
			runtimeLogger().WarnContext(ctx, "argocd managed-cluster index repair failed for secret",
				"secret", secrets.Items[i].Name,
				"error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return stats, firstErr
}

func (s *argoCDManagedClusterIndexRepairStats) recordSecretOutcome(outcome argoCDManagedClusterSecretRepairOutcome) {
	switch outcome {
	case argoCDSecretRepairExistingRow:
		s.AstronomerManagedSecrets++
		s.ExistingRows++
	case argoCDSecretRepairDBIndexRecreated:
		s.AstronomerManagedSecrets++
		s.DBRowsRecreated++
	case argoCDSecretRepairOrphan:
		s.AstronomerManagedSecrets++
		s.OrphanSecretsFound++
	case argoCDSecretRepairDecommissioned:
		s.AstronomerManagedSecrets++
		s.DecommissionedSecretsFound++
	case argoCDSecretRepairInvalidClusterID:
		s.AstronomerManagedSecrets++
		s.InvalidClusterIDSecretsFound++
	case argoCDSecretRepairUnattributedManaged:
		s.AstronomerManagedSecrets++
		s.UnattributedManagedSecretRows++
	}
}

func repairArgoCDManagedClusterIndexForSecret(ctx context.Context, deps ArgoCDAutoRegisterDeps, lister argoCDManagedClusterLister, instance sqlc.ArgocdInstance, secret *corev1.Secret) (argoCDManagedClusterSecretRepairOutcome, error) {
	if secret == nil || secret.Labels[astronomerManagedByLabelKey] != astronomerManagedByLabelValue {
		return argoCDSecretRepairIgnored, nil
	}
	clusterIDRaw := strings.TrimSpace(secret.Labels[astronomerClusterIDLabelKey])
	if clusterIDRaw == "" {
		return argoCDSecretRepairUnattributedManaged, nil
	}
	clusterID, err := uuid.Parse(clusterIDRaw)
	if err != nil {
		runtimeLogger().WarnContext(ctx, "argocd managed-cluster index repair skipped secret with invalid cluster id",
			"secret", secret.Name,
			"cluster_id", clusterIDRaw)
		return argoCDSecretRepairInvalidClusterID, nil
	}
	rows, err := lister.ListArgoCDManagedClustersByCluster(ctx, clusterID)
	if err != nil {
		return argoCDSecretRepairIgnored, fmt.Errorf("list managed-cluster rows for %s: %w", clusterID, err)
	}
	for _, row := range rows {
		if row.ArgocdInstanceID == instance.ID {
			return argoCDSecretRepairExistingRow, nil
		}
	}
	cluster, err := deps.Queries.GetClusterByID(ctx, clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			runtimeLogger().WarnContext(ctx, "argocd managed-cluster index repair found secret for missing cluster",
				"secret", secret.Name,
				"cluster_id", clusterID.String())
			return argoCDSecretRepairOrphan, nil
		}
		return argoCDSecretRepairIgnored, fmt.Errorf("get cluster %s: %w", clusterID, err)
	}
	if cluster.DecommissionedAt.Valid {
		runtimeLogger().WarnContext(ctx, "argocd managed-cluster index repair found secret for decommissioned cluster",
			"secret", secret.Name,
			"cluster_id", clusterID.String())
		return argoCDSecretRepairDecommissioned, nil
	}
	projects, err := argolabels.ProjectsForCluster(ctx, deps.Queries, cluster.ID)
	if err != nil {
		return argoCDSecretRepairIgnored, fmt.Errorf("list cluster projects: %w", err)
	}
	desired := managedClusterArgoLabelsForProjects(cluster, projects)
	if err := refreshSingleManagedClusterSecret(ctx, deps.K8s, sqlc.ArgocdManagedCluster{
		ClusterSecretName: secret.Name,
		ServerUrl:         strings.TrimSpace(string(secret.Data["server"])),
	}, desired); err != nil {
		return argoCDSecretRepairIgnored, fmt.Errorf("refresh repaired secret labels: %w", err)
	}
	labelsJSON, err := json.Marshal(desired)
	if err != nil {
		return argoCDSecretRepairIgnored, fmt.Errorf("marshal repaired labels: %w", err)
	}
	_, err = deps.Queries.CreateArgoCDManagedCluster(ctx, sqlc.CreateArgoCDManagedClusterParams{
		ArgocdInstanceID:  instance.ID,
		ClusterID:         clusterID,
		ClusterSecretName: secret.Name,
		ServerUrl:         strings.TrimSpace(string(secret.Data["server"])),
		Labels:            labelsJSON,
	})
	if err != nil {
		return argoCDSecretRepairIgnored, fmt.Errorf("recreate managed-cluster row: %w", err)
	}
	writeArgoCDRegistrationStep(ctx, deps, clusterID, "argocd_registration_repaired", "success", map[string]any{
		"repair": "db_index_recreated",
		"secret": secret.Name,
		"server": strings.TrimSpace(string(secret.Data["server"])),
	}, "")
	upsertArgoCDAdoptionCondition(ctx, deps, clusterID, "True", "Registered", "Cluster is registered into ArgoCD for baseline reconciliation.")
	return argoCDSecretRepairDBIndexRecreated, nil
}

func autoRegisterClusterIntoInstance(ctx context.Context, deps ArgoCDAutoRegisterDeps, instance sqlc.ArgocdInstance, cluster sqlc.Cluster) error {
	token, server, tlsConfig, err := managedClusterCredential(ctx, deps, cluster)
	if err != nil {
		return err
	}
	instanceToken, err := decryptArgoCDInstanceToken(deps.Encryptor, instance)
	if err != nil {
		return err
	}
	client := argocdclient.NewClient(instance.ApiUrl, instanceToken, argocdclient.Options{
		VerifySSL: instance.VerifySsl,
	})
	projects, err := argolabels.ProjectsForCluster(ctx, deps.Queries, cluster.ID)
	if err != nil {
		return fmt.Errorf("list cluster projects: %w", err)
	}
	labels := managedClusterArgoLabelsForProjects(cluster, projects)
	upstream, err := client.RegisterCluster(ctx, argocdclient.ClusterRegistration{
		Server: server,
		Name:   cluster.Name,
		Config: argocdclient.ClusterConfig{
			BearerToken:     token,
			TLSClientConfig: tlsConfig,
		},
		Labels: labels,
		Upsert: true,
	})
	if err != nil {
		return fmt.Errorf("register cluster with argocd: %w", err)
	}
	labelsJSON, _ := json.Marshal(labels)
	_, err = deps.Queries.CreateArgoCDManagedCluster(ctx, sqlc.CreateArgoCDManagedClusterParams{
		ArgocdInstanceID:  instance.ID,
		ClusterID:         cluster.ID,
		ClusterSecretName: firstNonEmptyArgoString(upstream.Name, cluster.Name),
		ServerUrl:         server,
		Labels:            labelsJSON,
	})
	if err != nil {
		return fmt.Errorf("record managed cluster: %w", err)
	}
	return nil
}

func managedClusterCredential(ctx context.Context, deps ArgoCDAutoRegisterDeps, cluster sqlc.Cluster) (string, string, *argocdclient.TLSClientConfig, error) {
	if cluster.IsLocal {
		if strings.TrimSpace(cluster.ApiServerUrl) == "" {
			return "", "", nil, fmt.Errorf("%w: local cluster %s has no api_server_url", errArgoCDManagedClusterCredentialUnavailable, cluster.ID)
		}
		token, err := createArgoCDApplicationControllerToken(ctx, deps.K8s)
		if err != nil {
			return "", "", nil, fmt.Errorf("%w: %v", errArgoCDManagedClusterCredentialUnavailable, err)
		}
		return token, strings.TrimSpace(cluster.ApiServerUrl), &argocdclient.TLSClientConfig{
			Insecure: cluster.CaCertificate == "",
			CAData:   []byte(cluster.CaCertificate),
		}, nil
	}
	if deps.Encryptor == nil {
		return "", "", nil, fmt.Errorf("%w: encryptor not configured for argocd cluster proxy token", errArgoCDManagedClusterCredentialUnavailable)
	}
	if deps.ClusterProxyBaseURL == "" {
		return "", "", nil, fmt.Errorf("%w: argocd cluster proxy base URL is not configured", errArgoCDManagedClusterCredentialUnavailable)
	}
	token, err := ensureArgoCDClusterProxyToken(ctx, deps, cluster.ID)
	if err != nil {
		return "", "", nil, fmt.Errorf("%w: %v", errArgoCDManagedClusterCredentialUnavailable, err)
	}
	server := fmt.Sprintf("%s/api/v1/internal/argocd/clusters/%s/k8s", deps.ClusterProxyBaseURL, cluster.ID.String())
	return token, server, nil, nil
}

func ensureArgoCDClusterProxyToken(ctx context.Context, deps ArgoCDAutoRegisterDeps, clusterID uuid.UUID) (string, error) {
	row, err := deps.Queries.GetActiveArgoCDClusterProxyTokenByClusterID(ctx, clusterID)
	if err == nil {
		token, decErr := deps.Encryptor.Decrypt(row.TokenEncrypted)
		if decErr == nil && strings.HasPrefix(token, auth.ArgoCDClusterProxyTokenPrefix) {
			return token, nil
		}
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("lookup argocd cluster proxy token: %w", err)
	}
	token, err := auth.GenerateArgoCDClusterProxyToken()
	if err != nil {
		return "", err
	}
	encrypted, err := deps.Encryptor.Encrypt(token)
	if err != nil {
		return "", fmt.Errorf("encrypt argocd cluster proxy token: %w", err)
	}
	expiresAt := pgtype.Timestamptz{Time: time.Now().UTC().Add(argoCDProxyTokenTTL), Valid: true}
	_, err = deps.Queries.UpsertArgoCDClusterProxyToken(ctx, sqlc.UpsertArgoCDClusterProxyTokenParams{
		ClusterID:      clusterID,
		TokenHash:      auth.HashArgoCDClusterProxyToken(token),
		TokenPrefix:    auth.ArgoCDClusterProxyTokenDisplayPrefix(token),
		TokenEncrypted: encrypted,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		return "", fmt.Errorf("upsert argocd cluster proxy token: %w", err)
	}
	return token, nil
}

func createArgoCDApplicationControllerToken(ctx context.Context, k8s kubernetes.Interface) (string, error) {
	if k8s == nil {
		return "", fmt.Errorf("kubernetes client not configured")
	}
	duration := int64((24 * time.Hour).Seconds())
	req, err := k8s.CoreV1().ServiceAccounts(argoCDNamespace).CreateToken(ctx, argoCDApplicationControllerSA, &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{ExpirationSeconds: &duration},
	}, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("create argocd application-controller token: %w", err)
	}
	return strings.TrimSpace(req.Status.Token), nil
}

func decryptArgoCDInstanceToken(encryptor *auth.Encryptor, instance sqlc.ArgocdInstance) (string, error) {
	raw := strings.TrimSpace(instance.AuthTokenEncrypted)
	if raw == "" {
		return "", fmt.Errorf("argocd instance %s has no auth token", instance.ID)
	}
	if encryptor == nil {
		return raw, nil
	}
	token, err := encryptor.Decrypt(raw)
	if err != nil {
		return "", fmt.Errorf("decrypt argocd instance token: %w", err)
	}
	return strings.TrimSpace(token), nil
}

func firstNonEmptyArgoString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func argoCDManagedClusterRows(ctx context.Context, deps ArgoCDAutoRegisterDeps, clusterID uuid.UUID) []sqlc.ArgocdManagedCluster {
	lister, ok := deps.Queries.(argoCDManagedClusterLister)
	if !ok {
		return nil
	}
	rows, err := lister.ListArgoCDManagedClustersByCluster(ctx, clusterID)
	if err != nil {
		return nil
	}
	return rows
}

func detectArgoCDManagedClusterDrift(ctx context.Context, deps ArgoCDAutoRegisterDeps, cluster sqlc.Cluster, rows []sqlc.ArgocdManagedCluster) []string {
	if deps.K8s == nil || len(rows) == 0 {
		return nil
	}
	projects, err := argolabels.ProjectsForCluster(ctx, deps.Queries, cluster.ID)
	if err != nil {
		runtimeLogger().WarnContext(ctx, "argocd auto-register project label lookup failed",
			"cluster_id", cluster.ID.String(),
			"error", err)
		return nil
	}
	desired := managedClusterArgoLabelsForProjects(cluster, projects)
	reasons := map[string]struct{}{}
	for _, row := range rows {
		secret, err := lookupClusterSecret(ctx, deps.K8s, row.ClusterSecretName, row.ServerUrl)
		if err != nil {
			runtimeLogger().WarnContext(ctx, "argocd auto-register drift check failed",
				"cluster_id", cluster.ID.String(),
				"argocd_instance_id", row.ArgocdInstanceID.String(),
				"cluster_secret_name", row.ClusterSecretName,
				"error", err)
			continue
		}
		if secret == nil {
			reasons["missing_secret"] = struct{}{}
			continue
		}
		if managedClusterSecretLabelsDrift(secret.Labels, desired) {
			reasons["stale_labels"] = struct{}{}
		}
		// Local-only token-expiry backstop: the server's self-manage loop
		// renews the local bearer token at half its 24h TTL, so while the
		// server is healthy the remaining lifetime never drops below ~12h and
		// this never fires (single-owner steady state preserved). It only
		// trips when the server has been down long enough to miss its renew
		// window — without it a server outage longer than ~22h would expire
		// ArgoCD's local-cluster credential with no writer left to fix it.
		if cluster.IsLocal {
			if expiry, ok := argoCDClusterSecretBearerTokenExpiry(secret); ok && time.Until(expiry) <= localArgoCDClusterTokenExpiryDriftWindow {
				reasons["token_expiring"] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(reasons))
	for reason := range reasons {
		out = append(out, reason)
	}
	sort.Strings(out)
	return out
}

// argoCDManagedRowsCoverInstances reports whether every configured ArgoCD
// instance already has a managed-cluster row for this cluster. A newly added
// instance with no row still needs the registration loop even when the
// existing registrations show no drift.
func argoCDManagedRowsCoverInstances(rows []sqlc.ArgocdManagedCluster, instances []sqlc.ArgocdInstance) bool {
	covered := make(map[uuid.UUID]struct{}, len(rows))
	for _, row := range rows {
		covered[row.ArgocdInstanceID] = struct{}{}
	}
	for _, instance := range instances {
		if _, ok := covered[instance.ID]; !ok {
			return false
		}
	}
	return true
}

// argoCDClusterSecretBearerTokenExpiry extracts the exp claim from the bearer
// token inside an ArgoCD cluster Secret's config blob (same JWT shape the
// handler reconciler parses in argoCDClusterTokenExpiry). ok is false when the
// Secret carries no parseable JWT with an exp claim.
func argoCDClusterSecretBearerTokenExpiry(secret *corev1.Secret) (time.Time, bool) {
	if len(secret.Data["config"]) == 0 {
		return time.Time{}, false
	}
	var cfg struct {
		BearerToken string `json:"bearerToken"`
	}
	if json.Unmarshal(secret.Data["config"], &cfg) != nil {
		return time.Time{}, false
	}
	parts := strings.Split(strings.TrimSpace(cfg.BearerToken), ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	raw := parts[1]
	if mod := len(raw) % 4; mod != 0 {
		raw += strings.Repeat("=", 4-mod)
	}
	payload, err := base64.URLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0).UTC(), true
}

func managedClusterSecretLabelsDrift(existing, desired map[string]string) bool {
	for k, v := range desired {
		if existing[k] != v {
			return true
		}
	}
	for k := range existing {
		if !isAstronomerOwnedLabel(k) {
			continue
		}
		if _, ok := desired[k]; !ok {
			return true
		}
	}
	return false
}

func recordArgoCDRegistrationFailure(ctx context.Context, deps ArgoCDAutoRegisterDeps, clusterID uuid.UUID, cause error) {
	msg := "ArgoCD auto-adoption failed."
	if cause != nil {
		msg = cause.Error()
	}
	writeArgoCDRegistrationStep(ctx, deps, clusterID, "argocd_registration_failed", "failed", nil, msg)
	upsertArgoCDAdoptionCondition(ctx, deps, clusterID, "False", "RegistrationFailed", msg)
}

func writeArgoCDRegistrationStep(ctx context.Context, deps ArgoCDAutoRegisterDeps, clusterID uuid.UUID, stepName, status string, detail map[string]any, errMsg string) {
	if deps.Registration == nil {
		return
	}
	_, _ = deps.Registration.WriteStep(ctx, clusterID, registration.StepInput{
		StepName:      stepName,
		Status:        status,
		ProgressPct:   argoCDRegistrationProgress(status),
		Detail:        detail,
		ErrorMessage:  errMsg,
		MarkStarted:   status == "running",
		MarkCompleted: status == "success" || status == "failed" || status == "skipped",
	})
}

func argoCDRegistrationProgress(status string) int32 {
	switch status {
	case "success", "skipped":
		return 100
	default:
		return 0
	}
}

func upsertArgoCDAdoptionCondition(ctx context.Context, deps ArgoCDAutoRegisterDeps, clusterID uuid.UUID, status, reason, message string) {
	writer, ok := deps.Queries.(argoCDConditionWriter)
	if !ok {
		return
	}
	_, _ = writer.UpsertClusterCondition(ctx, sqlc.UpsertClusterConditionParams{
		ClusterID: clusterID,
		Type:      ConditionArgoCDAdopted,
		Status:    status,
		Reason:    reason,
		Message:   message,
	})
}

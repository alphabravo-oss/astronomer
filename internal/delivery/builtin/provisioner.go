// Package builtin provisions the reviewed platform bundle catalog through the
// same immutable delivery targets and rollouts used by every user workload.
// It is intentionally triggered only after an enrolled agent reports a Ready,
// compatible Flux inventory; registration never reaches into the downstream
// cluster or installs Helm releases directly.
package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/alphabravocompany/astronomer-go/internal/registration"
)

const (
	systemProjectName = "astronomer-system"
	systemSourceName  = "astronomer-builtins-prometheus-community"
	systemActor       = "system:cluster-registration"
)

var identityNamespace = uuid.MustParse("2b079af7-4a47-4dbe-984b-a67d78ab6909")

type Previewer interface {
	Preview(context.Context, uuid.UUID) (rollout.PlanningSnapshot, placement.Result, error)
}

type Planner interface {
	Create(context.Context, rollout.CreateRequest) (rollout.FrozenRollout, error)
}

type Registration interface {
	OnDeliveryApplyStart(context.Context, uuid.UUID) error
	OnDeliveryApplySuccess(context.Context, uuid.UUID) error
	OnDeliveryApplyFailure(context.Context, uuid.UUID, string) error
}

// Provisioner is a retry-safe bridge from registration readiness to normal
// delivery resources. Deterministic IDs and planner idempotency keys allow any
// server replica to reconcile the same cluster without duplicate resources.
type Provisioner struct {
	pool         *pgxpool.Pool
	previewer    Previewer
	planner      Planner
	registration Registration
}

func NewProvisioner(pool *pgxpool.Pool, previewer Previewer, planner Planner, registration Registration) (*Provisioner, error) {
	if pool == nil || previewer == nil || planner == nil || registration == nil {
		return nil, errors.New("built-in delivery provisioner dependencies are required")
	}
	return &Provisioner{pool: pool, previewer: previewer, planner: planner, registration: registration}, nil
}

type registrationState struct {
	phase           registration.Phase
	install         bool
	inventoryReady  bool
	latestRetryTime *time.Time
}

type targetIdentity struct {
	id         uuid.UUID
	generation uint64
	slug       string
	release    string
}

// Reconcile is called after an authenticated delivery status commit. Errors do
// not invalidate that status; the next agent observation retries this work.
func (p *Provisioner) Reconcile(ctx context.Context, clusterID uuid.UUID) error {
	if p == nil || p.pool == nil || clusterID == uuid.Nil {
		return errors.New("built-in delivery provisioner is not configured")
	}
	state, err := p.loadRegistrationState(ctx, clusterID)
	if err != nil {
		return err
	}
	if !state.install || !state.inventoryReady || state.phase == registration.PhaseReady || state.phase == registration.PhaseFailed {
		return nil
	}
	catalog, err := builtinbundles.Load()
	if err != nil {
		return err
	}
	targets, err := p.ensureAssets(ctx, clusterID, catalog)
	if err != nil {
		return err
	}
	complete, failedCode, failed, err := p.observeTargets(ctx, clusterID, targets, state.latestRetryTime)
	if err != nil {
		return err
	}
	if complete {
		return p.registration.OnDeliveryApplySuccess(ctx, clusterID)
	}
	if failed {
		return p.registration.OnDeliveryApplyFailure(ctx, clusterID, failedCode)
	}
	if err := p.registration.OnDeliveryApplyStart(ctx, clusterID); err != nil {
		return err
	}
	for _, target := range targets {
		if err := p.ensureRollout(ctx, target, state.latestRetryTime); err != nil {
			return fmt.Errorf("ensure built-in rollout %s: %w", target.slug, err)
		}
	}
	return nil
}

func (p *Provisioner) loadRegistrationState(ctx context.Context, clusterID uuid.UUID) (registrationState, error) {
	var state registrationState
	err := p.pool.QueryRow(ctx, `
		SELECT c.registration_phase,
		       COALESCE(c.install_baseline, false),
		       EXISTS (
		           SELECT 1 FROM delivery_controller_inventory i
		           WHERE i.cluster_id=c.id AND i.ready=true AND i.compatibility_status='compatible'
		       ),
		       (SELECT s.created_at FROM cluster_registration_steps s
		        WHERE s.cluster_id=c.id AND s.step_name='delivery_retry_requested'
		        ORDER BY s.created_at DESC,s.step_order DESC LIMIT 1)
		FROM clusters c WHERE c.id=$1 AND c.decommissioned_at IS NULL`, clusterID).
		Scan(&state.phase, &state.install, &state.inventoryReady, &state.latestRetryTime)
	if err != nil {
		return registrationState{}, fmt.Errorf("load built-in registration state: %w", err)
	}
	return state, nil
}

func (p *Provisioner) ensureAssets(ctx context.Context, clusterID uuid.UUID, catalog builtinbundles.Catalog) ([]targetIdentity, error) {
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, fmt.Errorf("begin built-in delivery transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('astronomer-builtins:' || $1::text, 0))`, clusterID); err != nil {
		return nil, fmt.Errorf("lock built-in delivery: %w", err)
	}

	projectID := stableID("project", clusterID.String())
	if _, err := tx.Exec(ctx, `
		INSERT INTO projects (id,name,display_name,description,cluster_id,namespaces,resource_quota,managed_by)
		VALUES ($1,$2,'Astronomer System','Flux-managed platform components',$3,'[]','{}','system')
		ON CONFLICT (id) DO NOTHING`, projectID, systemProjectName, clusterID); err != nil {
		return nil, fmt.Errorf("ensure built-in project: %w", err)
	}
	var actualCluster uuid.UUID
	var actualProjectName, managedBy string
	if err := tx.QueryRow(ctx, `SELECT cluster_id,name,managed_by FROM projects WHERE id=$1`, projectID).
		Scan(&actualCluster, &actualProjectName, &managedBy); err != nil || actualCluster != clusterID || actualProjectName != systemProjectName || managedBy != "system" {
		return nil, errors.New("built-in project identity conflict")
	}

	sourceID := stableID("source", projectID.String(), systemSourceName)
	trust := model.TrustPolicy{AllowUnsigned: true}
	trustJSON, _ := json.Marshal(trust)
	if _, err := tx.Exec(ctx, `
		INSERT INTO delivery_sources (
			id,project_id,name,description,source_type,url,auth_mode,trust_policy,status,last_resolved_at
		) VALUES ($1,$2,$3,'Release-pinned built-in Helm charts','helm_http',$4,'none',$5,'ready',now())
		ON CONFLICT (id) DO NOTHING`, sourceID, projectID, systemSourceName,
		"https://prometheus-community.github.io/helm-charts", trustJSON); err != nil {
		return nil, fmt.Errorf("ensure built-in source: %w", err)
	}
	var sourceProject uuid.UUID
	var sourceType, sourceURL, authMode string
	if err := tx.QueryRow(ctx, `SELECT project_id,source_type,url,auth_mode FROM delivery_sources WHERE id=$1`, sourceID).
		Scan(&sourceProject, &sourceType, &sourceURL, &authMode); err != nil || sourceProject != projectID || sourceType != "helm_http" || sourceURL != "https://prometheus-community.github.io/helm-charts" || authMode != "none" {
		return nil, errors.New("built-in source identity conflict")
	}

	targets := make([]targetIdentity, 0, len(catalog.Components))
	for _, component := range catalog.Components {
		if !component.DefaultEnabled {
			continue
		}
		target, err := ensureComponent(ctx, tx, projectID, clusterID, sourceID, catalog.Release, component, trust)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return nil, errors.New("built-in catalog has no enabled components")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit built-in delivery assets: %w", err)
	}
	return targets, nil
}

func ensureComponent(ctx context.Context, tx pgx.Tx, projectID, clusterID, sourceID uuid.UUID, release string, component builtinbundles.Component, trust model.TrustPolicy) (targetIdentity, error) {
	bundleID := stableID("bundle", projectID.String(), component.Slug)
	if _, err := tx.Exec(ctx, `
		INSERT INTO component_bundles (id,project_id,name,description)
		VALUES ($1,$2,$3,$4) ON CONFLICT (id) DO NOTHING`,
		bundleID, projectID, "astronomer-builtins-"+component.Slug, component.Description); err != nil {
		return targetIdentity{}, fmt.Errorf("ensure built-in bundle %s: %w", component.Slug, err)
	}
	var bundleProject uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT project_id FROM component_bundles WHERE id=$1`, bundleID).Scan(&bundleProject); err != nil || bundleProject != projectID {
		return targetIdentity{}, fmt.Errorf("built-in bundle %s identity conflict", component.Slug)
	}

	valuesJSON, err := json.Marshal(component.Values)
	if err != nil {
		return targetIdentity{}, fmt.Errorf("encode built-in values %s: %w", component.Slug, err)
	}
	reconciliation := model.ReconciliationPolicy{
		Interval: model.Duration(5 * time.Minute), RetryInterval: model.Duration(30 * time.Second),
		Timeout: model.Duration(10 * time.Minute), Prune: true, Wait: true, Drift: model.DriftRepair,
	}
	requirements := make([]model.CapabilityRequirement, 0, len(component.Requirements.Capabilities))
	for _, capability := range component.Requirements.Capabilities {
		requirements = append(requirements, model.CapabilityRequirement{Name: capability})
	}
	draft := model.BundleVersionDraft{
		SourceID: sourceID, RequestedRevision: component.Source.Version, Scope: model.ScopePlatform,
		Renderer: model.RendererSpec{Kind: model.RendererHelm, Helm: &model.HelmSpec{
			Chart: component.Source.Chart, ChartVersion: component.Source.Version,
			ReleaseName: component.ReleaseName, TargetNamespace: component.TargetNamespace,
			Values: valuesJSON, InstallRetries: 3, UpgradeRetries: 3,
		}},
		Reconciliation: reconciliation, RequiredCapabilities: requirements,
	}
	artifactDigest, err := model.ParseDigest(component.Source.ChartDigest)
	if err != nil {
		return targetIdentity{}, err
	}
	revision := model.ImmutableRevision{Kind: model.RevisionHelmChart, Value: component.Source.Version, ArtifactDigest: artifactDigest}
	resolved, err := draft.Resolve(revision)
	if err != nil {
		return targetIdentity{}, fmt.Errorf("validate built-in component %s: %w", component.Slug, err)
	}
	specDigest, err := model.CanonicalDigest(struct {
		Spec         model.BundleVersionSpec `json:"spec"`
		Dependencies []uuid.UUID             `json:"dependency_bundle_ids"`
	}{resolved, []uuid.UUID{}})
	if err != nil {
		return targetIdentity{}, err
	}
	resolvedSource := model.ResolvedSourceSpec{
		SourceID: sourceID, Type: model.SourceHelmHTTP,
		URL: "https://prometheus-community.github.io/helm-charts", AuthMode: model.AuthNone,
		Trust: trust, Revision: revision,
	}
	sourceJSON, _ := json.Marshal(resolvedSource)
	rendererJSON, _ := json.Marshal(draft.Renderer)
	reconciliationJSON, _ := json.Marshal(reconciliation)
	requirementsJSON, _ := json.Marshal(requirements)
	versionID := stableID("version", bundleID.String(), release, component.Source.Version, component.Source.ChartDigest)
	versionLabel := strings.TrimPrefix(release, "v") + "+" + component.Source.Version
	if _, err := tx.Exec(ctx, `
		INSERT INTO component_bundle_versions (
			id,bundle_id,source_id,version,renderer,scope,requested_revision,resolved_revision,
			artifact_digest,source_spec,renderer_spec,reconciliation_policy,health_policy,
			requirements,dependency_bundle_ids,spec_digest,verification_status,
			verification_identity,state
		) VALUES ($1,$2,$3,$4,'helm','platform',$5,$5,$6,$7,$8,$9,'{}',$10,'[]',$11,'verified',$12,'ready')
		ON CONFLICT (id) DO NOTHING`, versionID, bundleID, sourceID, versionLabel,
		component.Source.Version, component.Source.ChartDigest, sourceJSON, rendererJSON,
		reconciliationJSON, requirementsJSON, specDigest.String(), "astronomer-release-catalog:"+release); err != nil {
		return targetIdentity{}, fmt.Errorf("ensure built-in version %s: %w", component.Slug, err)
	}
	var actualBundle uuid.UUID
	var actualDigest, actualState string
	if err := tx.QueryRow(ctx, `SELECT bundle_id,spec_digest,state FROM component_bundle_versions WHERE id=$1`, versionID).
		Scan(&actualBundle, &actualDigest, &actualState); err != nil || actualBundle != bundleID || actualDigest != specDigest.String() || actualState != "ready" {
		return targetIdentity{}, fmt.Errorf("built-in version %s identity conflict", component.Slug)
	}

	placementJSON, _ := json.Marshal(model.Placement{ProjectIDs: []uuid.UUID{projectID}, ClusterIDs: []uuid.UUID{clusterID}})
	rolloutPolicyJSON := json.RawMessage(`{"approval_required":false}`)
	targetID := stableID("target", clusterID.String(), component.Slug)
	var generation int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO delivery_targets (
			id,project_id,name,description,bundle_version_id,placement,rollout_policy,
			reconciliation_policy,maintenance_window_policy,suspended
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'{}',false)
		ON CONFLICT (id) DO UPDATE SET
			bundle_version_id=EXCLUDED.bundle_version_id,
			placement=EXCLUDED.placement,
			rollout_policy=EXCLUDED.rollout_policy,
			reconciliation_policy=EXCLUDED.reconciliation_policy,
			generation=delivery_targets.generation + CASE
				WHEN delivery_targets.bundle_version_id<>EXCLUDED.bundle_version_id THEN 1 ELSE 0 END,
			resource_version=delivery_targets.resource_version + CASE
				WHEN delivery_targets.bundle_version_id<>EXCLUDED.bundle_version_id THEN 1 ELSE 0 END,
			updated_at=CASE WHEN delivery_targets.bundle_version_id<>EXCLUDED.bundle_version_id THEN now() ELSE delivery_targets.updated_at END
		RETURNING generation`, targetID, projectID, "astronomer-builtins-"+component.Slug,
		component.Description, versionID, placementJSON, rolloutPolicyJSON, reconciliationJSON).Scan(&generation); err != nil {
		return targetIdentity{}, fmt.Errorf("ensure built-in target %s: %w", component.Slug, err)
	}
	return targetIdentity{id: targetID, generation: uint64(generation), slug: component.Slug, release: release}, nil
}

func (p *Provisioner) observeTargets(ctx context.Context, clusterID uuid.UUID, targets []targetIdentity, retryRequestedAt *time.Time) (complete bool, code string, failed bool, err error) {
	ready := 0
	for _, target := range targets {
		var rolloutState string
		var rolloutCreatedAt time.Time
		rolloutErr := p.pool.QueryRow(ctx, `
			SELECT state,created_at FROM delivery_rollouts
			WHERE target_id=$1 ORDER BY created_at DESC,id DESC LIMIT 1`, target.id).
			Scan(&rolloutState, &rolloutCreatedAt)
		if rolloutErr != nil && !errors.Is(rolloutErr, pgx.ErrNoRows) {
			return false, "", false, rolloutErr
		}
		if rolloutErr == nil {
			switch rolloutState {
			case "failed", "rejected", "aborted", "rolled_back", "rollback_failed":
				// A retry request newer than this terminal rollout deliberately
				// suppresses its stale failure while ensureRollout creates the
				// next immutable attempt.
				if retryAfter(retryRequestedAt, rolloutCreatedAt) {
					continue
				}
				return false, "built_in_rollout_" + rolloutState, true, nil
			case "draft", "resolving", "awaiting_approval", "queued", "progressing", "paused", "rolling_back":
				continue
			}
		}
		var phase, errorCode string
		err := p.pool.QueryRow(ctx, `
			SELECT phase,last_error_code FROM cluster_deployments
			WHERE target_id=$1 AND cluster_id=$2`, target.id, clusterID).Scan(&phase, &errorCode)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, "", false, err
		}
		if phase == "ready" {
			ready++
		}
		if phase == "failed" || phase == "timed_out" || phase == "rollback_failed" {
			if errorCode == "" {
				errorCode = "built_in_delivery_failed"
			}
			return false, errorCode, true, nil
		}
	}
	return ready == len(targets), "", false, nil
}

func (p *Provisioner) ensureRollout(ctx context.Context, target targetIdentity, retryRequestedAt *time.Time) error {
	var state string
	var count int
	var createdAt time.Time
	err := p.pool.QueryRow(ctx, `
		SELECT state, count(*) OVER ()::integer, created_at
		FROM delivery_rollouts WHERE target_id=$1
		ORDER BY created_at DESC,id DESC LIMIT 1`, target.id).Scan(&state, &count, &createdAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err == nil {
		switch state {
		case "resolving", "awaiting_approval", "queued", "progressing", "paused", "rolling_back", "succeeded":
			return nil
		}
		if !retryAfter(retryRequestedAt, createdAt) {
			return nil
		}
	}
	snapshot, result, err := p.previewer.Preview(ctx, target.id)
	if err != nil {
		return err
	}
	if result.SelectedCount != 1 {
		return fmt.Errorf("built-in target selected %d clusters", result.SelectedCount)
	}
	key := "builtins:" + target.release + ":" + target.slug
	if count > 0 {
		key = fmt.Sprintf("%s:retry-%d", key, count)
	}
	strategy := model.RolloutStrategy{
		Type: model.StrategyAllAtOnce, MaxConcurrent: 1,
		MaxUnavailable:   model.Amount{Type: model.AmountCount, Value: 1},
		ProgressDeadline: model.Duration(30 * time.Minute),
		FailureThreshold: model.Amount{Type: model.AmountCount, Value: 1},
		OnFailure:        model.FailurePause,
	}
	_, err = p.planner.Create(ctx, rollout.CreateRequest{
		TargetID: target.id, ExpectedTargetGeneration: snapshot.TargetGeneration,
		PreviewDigest: result.PreviewDigest, Strategy: strategy,
		Actor: systemActor, IdempotencyKey: key,
	})
	return err
}

func retryAfter(requestedAt *time.Time, rolloutCreatedAt time.Time) bool {
	return requestedAt != nil && requestedAt.After(rolloutCreatedAt)
}

func stableID(parts ...string) uuid.UUID {
	return uuid.NewSHA1(identityNamespace, []byte(strings.Join(parts, "\x00")))
}

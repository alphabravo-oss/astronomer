// Package catalogapp bridges the curated Helm catalog to Astronomer's
// immutable Delivery model. The bridge deliberately creates the same source,
// bundle, target, rollout, and cluster-deployment records used by every other
// Flux workload; it never invokes Helm through the agent tunnel.
package catalogapp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
)

var identityNamespace = uuid.MustParse("852128cf-31d7-4ead-852d-a4905887d4fd")

type Previewer interface {
	Preview(context.Context, uuid.UUID) (rollout.PlanningSnapshot, placement.Result, error)
}

type Planner interface {
	Create(context.Context, rollout.CreateRequest) (rollout.FrozenRollout, error)
}

// InstallRequest contains resolved catalog metadata. Values remain bounded by
// the Delivery model and are embedded only in the immutable bundle spec.
type InstallRequest struct {
	InstallationID uuid.UUID
	ProjectID      uuid.UUID
	ClusterID      uuid.UUID
	ChartVersionID uuid.UUID
	ChartName      string
	ChartVersion   string
	ChartDigest    string
	RepositoryURL  string
	ReleaseName    string
	Namespace      string
	ValuesYAML     string
	Description    string
	ActorID        pgtype.UUID
	IdempotencyKey string
}

type InstallResult struct {
	SourceID        uuid.UUID `json:"source_id"`
	BundleID        uuid.UUID `json:"bundle_id"`
	BundleVersionID uuid.UUID `json:"bundle_version_id"`
	TargetID        uuid.UUID `json:"target_id"`
	RolloutID       uuid.UUID `json:"rollout_id"`
}

type Status struct {
	Phase         string
	LastErrorCode string
}

type Service struct {
	pool      *pgxpool.Pool
	previewer Previewer
	planner   Planner
}

func New(pool *pgxpool.Pool, previewer Previewer, planner Planner) (*Service, error) {
	if pool == nil || previewer == nil || planner == nil {
		return nil, errors.New("catalog application delivery dependencies are required")
	}
	return &Service{pool: pool, previewer: previewer, planner: planner}, nil
}

// Install stages immutable Delivery assets and starts an all-at-once rollout.
// Every identity is deterministic, making retries safe even when a worker dies
// between asset creation and rollout planning.
func (s *Service) Install(ctx context.Context, request InstallRequest) (InstallResult, error) {
	return s.apply(ctx, request, "installing")
}

// Upgrade resolves the existing catalog installation's Delivery ownership and
// advances its target to a new immutable bundle version.
func (s *Service) Upgrade(ctx context.Context, request InstallRequest) (InstallResult, error) {
	if request.ProjectID == uuid.Nil {
		if err := s.pool.QueryRow(ctx, `
			SELECT dt.project_id FROM installed_charts ic
			JOIN delivery_targets dt ON dt.id=ic.request_id
			WHERE ic.id=$1`, request.InstallationID).Scan(&request.ProjectID); err != nil {
			return InstallResult{}, fmt.Errorf("resolve catalog application ownership: %w", err)
		}
	}
	return s.apply(ctx, request, "upgrading")
}

func (s *Service) apply(ctx context.Context, request InstallRequest, pendingStatus string) (InstallResult, error) {
	if err := validateInstall(request); err != nil {
		return InstallResult{}, err
	}
	result, generation, err := s.ensureAssets(ctx, request)
	if err != nil {
		return InstallResult{}, err
	}
	snapshot, preview, err := s.previewer.Preview(ctx, result.TargetID)
	if err != nil {
		return InstallResult{}, fmt.Errorf("preview catalog application placement: %w", err)
	}
	if snapshot.TargetGeneration != generation || preview.SelectedCount != 1 {
		return InstallResult{}, fmt.Errorf("catalog application placement selected %d clusters at generation %d", preview.SelectedCount, snapshot.TargetGeneration)
	}
	strategy := model.RolloutStrategy{
		Type: model.StrategyAllAtOnce, MaxConcurrent: 1,
		MaxUnavailable:   model.Amount{Type: model.AmountCount, Value: 1},
		ProgressDeadline: model.Duration(30 * time.Minute),
		FailureThreshold: model.Amount{Type: model.AmountCount, Value: 1},
		OnFailure:        model.FailurePause,
	}
	plan, err := s.planner.Create(ctx, rollout.CreateRequest{
		TargetID: result.TargetID, ExpectedTargetGeneration: generation,
		PreviewDigest: preview.PreviewDigest, Strategy: strategy,
		Actor: actor(request.ActorID), IdempotencyKey: "catalog:" + request.IdempotencyKey,
		Audit: catalogRolloutAuditIntent(request, result.TargetID),
	})
	if err != nil {
		return InstallResult{}, fmt.Errorf("start catalog application rollout: %w", err)
	}
	result.RolloutID = plan.ID
	if _, err := s.pool.Exec(ctx, `
		UPDATE installed_charts
		SET request_id=$2,status=$3,updated_at=now()
		WHERE id=$1`, request.InstallationID, result.TargetID, pendingStatus); err != nil {
		return InstallResult{}, fmt.Errorf("link catalog installation to delivery target: %w", err)
	}
	return result, nil
}

// Status projects the authoritative per-cluster Flux deployment state back to
// the catalog compatibility record used by the Apps UI.
func (s *Service) Status(ctx context.Context, targetID uuid.UUID) (Status, error) {
	if s == nil || s.pool == nil || targetID == uuid.Nil {
		return Status{}, errors.New("catalog application target is required")
	}
	var status Status
	err := s.pool.QueryRow(ctx, `
		SELECT phase,last_error_code FROM cluster_deployments
		WHERE target_id=$1 ORDER BY updated_at DESC,id DESC LIMIT 1`, targetID).
		Scan(&status.Phase, &status.LastErrorCode)
	if errors.Is(err, pgx.ErrNoRows) {
		return Status{Phase: "pending"}, nil
	}
	return status, err
}

// Uninstall requests the Delivery target's fenced deletion. The agent removes
// only objects matching the accepted deployment identity and spec digest.
func (s *Service) Uninstall(ctx context.Context, installationID uuid.UUID, actorID pgtype.UUID) error {
	if s == nil || s.pool == nil || installationID == uuid.Nil {
		return errors.New("catalog installation is required")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var targetID, projectID uuid.UUID
	var resourceVersion int64
	if err := tx.QueryRow(ctx, `
		SELECT dt.id,dt.project_id,dt.resource_version
		FROM installed_charts ic JOIN delivery_targets dt ON dt.id=ic.request_id
		WHERE ic.id=$1 FOR UPDATE OF dt`, installationID).
		Scan(&targetID, &projectID, &resourceVersion); err != nil {
		return fmt.Errorf("resolve catalog application target: %w", err)
	}
	if _, err := sqlc.New(tx).RequestDeliveryTargetDeletionCAS(ctx, sqlc.RequestDeliveryTargetDeletionCASParams{
		UpdatedBy: actorID, ID: targetID, ProjectID: projectID, ExpectedResourceVersion: resourceVersion,
	}); err != nil {
		return fmt.Errorf("request catalog application deletion: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE installed_charts SET status='uninstalling',updated_at=now() WHERE id=$1`, installationID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Rollback advances the target to the immutable bundle version recorded before
// its latest rollout. This is a new fenced rollout, not an in-place Helm
// history mutation.
func (s *Service) Rollback(ctx context.Context, installationID uuid.UUID, actorID pgtype.UUID, idempotencyKey string) (InstallResult, error) {
	if s == nil || s.pool == nil || installationID == uuid.Nil || strings.TrimSpace(idempotencyKey) == "" {
		return InstallResult{}, errors.New("catalog installation and idempotency key are required")
	}
	var result InstallResult
	var projectID uuid.UUID
	var previousVersion pgtype.UUID
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.QueryRow(ctx, `
		SELECT dt.id,dt.project_id,r.from_bundle_version_id
		FROM installed_charts ic
		JOIN delivery_targets dt ON dt.id=ic.request_id
		JOIN LATERAL (
			SELECT from_bundle_version_id FROM delivery_rollouts
			WHERE target_id=dt.id ORDER BY created_at DESC,id DESC LIMIT 1
		) r ON true WHERE ic.id=$1 FOR UPDATE OF dt`, installationID).
		Scan(&result.TargetID, &projectID, &previousVersion); err != nil {
		return result, fmt.Errorf("resolve catalog rollback target: %w", err)
	}
	if !previousVersion.Valid || uuid.UUID(previousVersion.Bytes) == uuid.Nil {
		return result, errors.New("catalog application has no previous immutable version")
	}
	result.BundleVersionID = uuid.UUID(previousVersion.Bytes)
	var generation int64
	if err := tx.QueryRow(ctx, `
		UPDATE delivery_targets SET bundle_version_id=$2,generation=generation+1,
			resource_version=resource_version+1,updated_by=$3,updated_at=now()
		WHERE id=$1 AND deletion_state='active' RETURNING generation`,
		result.TargetID, result.BundleVersionID, actorID).Scan(&generation); err != nil {
		return result, fmt.Errorf("stage catalog rollback target: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE installed_charts SET status='rolling_back',revision=GREATEST(revision-1,1),
			values_override=COALESCE((SELECT (renderer_spec #> '{helm,values}')::text
				FROM component_bundle_versions WHERE id=$2),values_override),updated_at=now()
		WHERE id=$1`, installationID, result.BundleVersionID); err != nil {
		return result, err
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	snapshot, preview, err := s.previewer.Preview(ctx, result.TargetID)
	if err != nil {
		return result, err
	}
	if snapshot.ProjectID != projectID || snapshot.TargetGeneration != uint64(generation) || preview.SelectedCount != 1 {
		return result, errors.New("catalog rollback placement changed unexpectedly")
	}
	strategy := model.RolloutStrategy{
		Type: model.StrategyAllAtOnce, MaxConcurrent: 1,
		MaxUnavailable: model.Amount{Type: model.AmountCount, Value: 1}, ProgressDeadline: model.Duration(30 * time.Minute),
		FailureThreshold: model.Amount{Type: model.AmountCount, Value: 1}, OnFailure: model.FailurePause,
	}
	plan, err := s.planner.Create(ctx, rollout.CreateRequest{
		TargetID: result.TargetID, ExpectedTargetGeneration: uint64(generation), PreviewDigest: preview.PreviewDigest,
		Strategy: strategy, Actor: actor(actorID), IdempotencyKey: "catalog-rollback:" + idempotencyKey,
	})
	if err != nil {
		return result, err
	}
	result.RolloutID = plan.ID
	return result, nil
}

func (s *Service) ensureAssets(ctx context.Context, request InstallRequest) (InstallResult, uint64, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return InstallResult{}, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result := InstallResult{
		SourceID: stableID("source", request.ProjectID.String(), request.RepositoryURL),
		BundleID: stableID("bundle", request.ProjectID.String(), request.InstallationID.String()),
		TargetID: stableID("target", request.ProjectID.String(), request.InstallationID.String()),
	}
	trust := model.TrustPolicy{AllowUnsigned: true}
	trustJSON, _ := json.Marshal(trust)
	sourceName := boundedName("catalog-" + request.ChartName + "-" + result.SourceID.String()[:8])
	if _, err := tx.Exec(ctx, `
		INSERT INTO delivery_sources
			(id,project_id,name,description,source_type,url,auth_mode,trust_policy,status,last_resolved_at,created_by,updated_by)
		VALUES ($1,$2,$3,$4,'helm_http',$5,'none',$6,'ready',now(),$7,$7)
		ON CONFLICT (id) DO UPDATE SET last_resolved_at=now(),status='ready',last_error_code=''`,
		result.SourceID, request.ProjectID, sourceName, "Upstream Helm source for catalog applications",
		request.RepositoryURL, trustJSON, request.ActorID); err != nil {
		return InstallResult{}, 0, fmt.Errorf("ensure catalog delivery source: %w", err)
	}
	bundleName := boundedName("app-" + request.ReleaseName + "-" + request.InstallationID.String()[:8])
	if _, err := tx.Exec(ctx, `
		INSERT INTO component_bundles (id,project_id,name,description,created_by,updated_by)
		VALUES ($1,$2,$3,$4,$5,$5) ON CONFLICT (id) DO NOTHING`,
		result.BundleID, request.ProjectID, bundleName, request.Description, request.ActorID); err != nil {
		return InstallResult{}, 0, fmt.Errorf("ensure catalog application: %w", err)
	}

	values, err := valuesJSON(request.ValuesYAML)
	if err != nil {
		return InstallResult{}, 0, err
	}
	reconciliation := model.ReconciliationPolicy{
		Interval: model.Duration(5 * time.Minute), RetryInterval: model.Duration(30 * time.Second),
		Timeout: model.Duration(15 * time.Minute), Prune: true, Wait: true, Drift: model.DriftRepair,
	}
	draft := model.BundleVersionDraft{
		SourceID: result.SourceID, RequestedRevision: request.ChartVersion, Scope: model.ScopePlatform,
		Renderer: model.RendererSpec{Kind: model.RendererHelm, Helm: &model.HelmSpec{
			Chart: request.ChartName, ChartVersion: request.ChartVersion,
			ReleaseName: request.ReleaseName, TargetNamespace: request.Namespace,
			Values: values, InstallRetries: 3, UpgradeRetries: 3,
		}},
		Reconciliation: reconciliation,
		RequiredCapabilities: []model.CapabilityRequirement{
			{Name: "delivery.source.helm_http"}, {Name: "delivery.renderer.helm"}, {Name: "delivery.scope.platform"},
		},
	}
	digest, err := model.ParseDigest(normalizeDigest(request.ChartDigest))
	if err != nil {
		return InstallResult{}, 0, fmt.Errorf("catalog chart digest: %w", err)
	}
	revision := model.ImmutableRevision{Kind: model.RevisionHelmChart, Value: request.ChartVersion, ArtifactDigest: digest}
	resolved, err := draft.Resolve(revision)
	if err != nil {
		return InstallResult{}, 0, fmt.Errorf("validate catalog application bundle: %w", err)
	}
	specDigest, err := model.CanonicalDigest(struct {
		Spec model.BundleVersionSpec `json:"spec"`
	}{Spec: resolved})
	if err != nil {
		return InstallResult{}, 0, err
	}
	result.BundleVersionID = stableID("version", result.BundleID.String(), request.ChartVersionID.String(), specDigest.String())
	sourceSpec := model.ResolvedSourceSpec{
		SourceID: result.SourceID, Type: model.SourceHelmHTTP, URL: request.RepositoryURL,
		AuthMode: model.AuthNone, Trust: trust, Revision: revision,
	}
	sourceJSON, _ := json.Marshal(sourceSpec)
	rendererJSON, _ := json.Marshal(draft.Renderer)
	reconciliationJSON, _ := json.Marshal(reconciliation)
	requirementsJSON, _ := json.Marshal(draft.RequiredCapabilities)
	versionLabel := boundedVersion(request.ChartVersion + "+" + specDigest.String()[7:19])
	if _, err := tx.Exec(ctx, `
		INSERT INTO component_bundle_versions (
			id,bundle_id,source_id,version,renderer,scope,requested_revision,resolved_revision,
			artifact_digest,source_spec,renderer_spec,reconciliation_policy,health_policy,
			requirements,dependency_bundle_ids,spec_digest,verification_status,verification_identity,state,created_by
		) VALUES ($1,$2,$3,$4,'helm','platform',$5,$5,$6,$7,$8,$9,'{}',$10,'[]',$11,'unsigned','upstream-catalog','ready',$12)
		ON CONFLICT (id) DO NOTHING`, result.BundleVersionID, result.BundleID, result.SourceID,
		versionLabel, request.ChartVersion, normalizeDigest(request.ChartDigest), sourceJSON, rendererJSON,
		reconciliationJSON, requirementsJSON, specDigest.String(), request.ActorID); err != nil {
		return InstallResult{}, 0, fmt.Errorf("ensure catalog application version: %w", err)
	}
	valuesDigest := sha256.Sum256(values)
	if _, err := tx.Exec(ctx, `
		INSERT INTO delivery_application_versions (
			installation_id,chart_version_id,bundle_version_id,catalog_slug,
			version,artifact_digest,values_digest,verification_status
		)
		SELECT $1,$2,$3,b.slug,$4,$5,$6,b.verification_status
		FROM helm_chart_versions v
		JOIN helm_charts c ON c.id=v.chart_id
		JOIN helm_repositories r ON r.id=c.repository_id
		JOIN catalog_blessed_charts b
		  ON b.repo_url=r.url AND b.chart_name=c.name AND b.source='catalog-v1'
		WHERE v.id=$2
		ON CONFLICT (installation_id,chart_version_id,values_digest) DO NOTHING`,
		request.InstallationID, request.ChartVersionID, result.BundleVersionID,
		request.ChartVersion, normalizeDigest(request.ChartDigest),
		fmt.Sprintf("sha256:%x", valuesDigest)); err != nil {
		return InstallResult{}, 0, fmt.Errorf("record immutable catalog application version: %w", err)
	}

	placementJSON, _ := json.Marshal(model.Placement{ProjectIDs: []uuid.UUID{request.ProjectID}, ClusterIDs: []uuid.UUID{request.ClusterID}})
	rolloutPolicyJSON := json.RawMessage(`{"approval_required":false}`)
	var generation int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO delivery_targets (
			id,project_id,name,description,bundle_version_id,placement,rollout_policy,
			reconciliation_policy,maintenance_window_policy,suspended,created_by,updated_by
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'{}',false,$9,$9)
		ON CONFLICT (id) DO UPDATE SET
			bundle_version_id=EXCLUDED.bundle_version_id,
			generation=delivery_targets.generation + CASE WHEN delivery_targets.bundle_version_id<>EXCLUDED.bundle_version_id THEN 1 ELSE 0 END,
			resource_version=delivery_targets.resource_version + 1,updated_at=now(),updated_by=EXCLUDED.updated_by
		RETURNING generation`, result.TargetID, request.ProjectID, bundleName, request.Description,
		result.BundleVersionID, placementJSON, rolloutPolicyJSON, reconciliationJSON, request.ActorID).Scan(&generation); err != nil {
		return InstallResult{}, 0, fmt.Errorf("ensure catalog application target: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return InstallResult{}, 0, err
	}
	return result, uint64(generation), nil
}

func validateInstall(request InstallRequest) error {
	if request.InstallationID == uuid.Nil || request.ProjectID == uuid.Nil || request.ClusterID == uuid.Nil || request.ChartVersionID == uuid.Nil {
		return errors.New("catalog installation, project, cluster, and chart version identities are required")
	}
	for name, value := range map[string]string{
		"chart": request.ChartName, "chart version": request.ChartVersion, "repository": request.RepositoryURL,
		"release": request.ReleaseName, "namespace": request.Namespace, "idempotency key": request.IdempotencyKey,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("catalog %s is required", name)
		}
	}
	if strings.Contains(request.ValuesYAML, "${vault://") {
		return errors.New("Flux catalog applications require Kubernetes Secret references; vault placeholders are not persisted into immutable delivery bundles")
	}
	return nil
}

func valuesJSON(raw string) (json.RawMessage, error) {
	if strings.TrimSpace(raw) == "" {
		return json.RawMessage(`{}`), nil
	}
	var values map[string]any
	if err := yaml.Unmarshal([]byte(raw), &values); err != nil {
		return nil, fmt.Errorf("decode catalog values: %w", err)
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("encode catalog values: %w", err)
	}
	if len(encoded) > model.MaxValuesBytes {
		return nil, errors.New("catalog values exceed the delivery payload limit")
	}
	return encoded, nil
}

func stableID(parts ...string) uuid.UUID {
	return uuid.NewSHA1(identityNamespace, []byte(strings.Join(parts, "\x00")))
}

func normalizeDigest(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "sha256:") {
		return "sha256:" + value
	}
	return value
}

func boundedName(value string) string {
	value = strings.ToLower(strings.Trim(value, "-"))
	if len(value) <= 128 {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return value[:111] + fmt.Sprintf("-%x", sum[:8])
}

func boundedVersion(value string) string {
	if len(value) <= 128 {
		return value
	}
	return value[:128]
}

func actor(value pgtype.UUID) string {
	if value.Valid && uuid.UUID(value.Bytes) != uuid.Nil {
		return "user:" + uuid.UUID(value.Bytes).String()
	}
	return "system:catalog"
}

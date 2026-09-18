package rollout

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	deliveryconfig "github.com/alphabravocompany/astronomer-go/internal/delivery/configuration"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

const (
	TaskType              = "delivery:rollout_reconcile"
	defaultTaskTimeout    = 2 * time.Minute
	defaultTaskMaxRetries = 12
)

// PostgresPlanningStore is the production transaction adapter for Planner.
// All reads, immutable writes, the creation event, and the durable task intent
// use one serializable PostgreSQL transaction.
type PostgresPlanningStore struct {
	pool *pgxpool.Pool
}

func NewPostgresPlanningStore(pool *pgxpool.Pool) (*PostgresPlanningStore, error) {
	if pool == nil {
		return nil, fail(CodeInvalidInput, "pool", "is required")
	}
	return &PostgresPlanningStore{pool: pool}, nil
}

// Preview evaluates the exact authoritative planning snapshot used by Create.
// It deliberately uses the same serializable transaction and target-row lock,
// so the returned digest is a meaningful optimistic-concurrency token rather
// than a best-effort cache result.
func (store *PostgresPlanningStore) Preview(ctx context.Context, targetID uuid.UUID) (PlanningSnapshot, placement.Result, error) {
	var snapshot PlanningSnapshot
	var result placement.Result
	err := store.InTransaction(ctx, func(tx PlanningTransaction) error {
		loaded, err := tx.LoadSnapshotForUpdate(ctx, targetID)
		if err != nil {
			return err
		}
		evaluated, err := placement.Evaluate(loaded.PlacementRequest)
		if err != nil {
			return err
		}
		snapshot, result = loaded, evaluated
		return nil
	})
	if err != nil {
		return PlanningSnapshot{}, placement.Result{}, err
	}
	return snapshot, result, nil
}

func (store *PostgresPlanningStore) InTransaction(ctx context.Context, work func(PlanningTransaction) error) error {
	if store == nil || store.pool == nil {
		return fail(CodeInvalidInput, "store", "is not configured")
	}
	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return fmt.Errorf("begin delivery planning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	adapter := &postgresPlanningTransaction{queries: sqlc.New(tx)}
	if err := work(adapter); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delivery planning transaction: %w", err)
	}
	return nil
}

type postgresPlanningTransaction struct {
	queries *sqlc.Queries
}

func (tx *postgresPlanningTransaction) RecordAuditIntent(ctx context.Context, intent audit.Intent) error {
	return audit.RecordIntent(ctx, tx.queries, intent)
}

func (tx *postgresPlanningTransaction) FindByIdempotency(ctx context.Context, targetID uuid.UUID, key string) (FrozenRollout, bool, error) {
	row, err := tx.queries.GetDeliveryRolloutByIdempotency(ctx, sqlc.GetDeliveryRolloutByIdempotencyParams{TargetID: targetID, IdempotencyKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return FrozenRollout{}, false, nil
	}
	if err != nil {
		return FrozenRollout{}, false, fmt.Errorf("find delivery rollout by idempotency: %w", err)
	}
	plan, err := decodeFrozenPlan(row)
	if err != nil {
		return FrozenRollout{}, false, err
	}
	return plan, true, nil
}

func (tx *postgresPlanningTransaction) LoadSnapshotForUpdate(ctx context.Context, targetID uuid.UUID) (PlanningSnapshot, error) {
	row, err := tx.queries.GetDeliveryPlanningSnapshot(ctx, targetID)
	if err != nil {
		return PlanningSnapshot{}, fmt.Errorf("lock delivery target planning snapshot: %w", err)
	}
	if row.Suspended || row.DeletionState != "active" {
		return PlanningSnapshot{}, fail(CodeInvalidInput, "target", "is suspended or deleting")
	}
	if row.BundleState != "ready" {
		return PlanningSnapshot{}, fail(CodeInvalidInput, "bundle_version", "is not ready")
	}
	if row.Generation < 1 {
		return PlanningSnapshot{}, fail(CodeInvariant, "target.generation", "must be positive")
	}
	var selector model.Placement
	if err := decodeStrict(row.Placement, &selector); err != nil {
		return PlanningSnapshot{}, fail(CodeInvariant, "target.placement", "stored selector is invalid")
	}
	var source model.ResolvedSourceSpec
	if err := decodeStrict(row.SourceSpec, &source); err != nil {
		return PlanningSnapshot{}, fail(CodeInvariant, "bundle.source_spec", "stored source snapshot is invalid")
	}
	bundleSpecDigest, err := model.ParseDigest(row.SpecDigest)
	if err != nil {
		return PlanningSnapshot{}, &Error{Code: CodeInvariant, Field: "bundle.spec_digest", Cause: err}
	}
	var targetOverrides model.TargetOverrides
	if err := decodeStrict(row.Overrides, &targetOverrides); err != nil {
		return PlanningSnapshot{}, fail(CodeInvariant, "target.overrides", "stored overrides are invalid")
	}
	template, configurationOverrides, err := tx.loadConfigurationRows(ctx, row)
	if err != nil {
		return PlanningSnapshot{}, err
	}
	renderer, configurationDigest, effectiveSpecDigest, err := resolveEffectiveRenderer(row, bundleSpecDigest, targetOverrides, template, configurationOverrides, nil)
	if err != nil {
		return PlanningSnapshot{}, err
	}
	desired := VersionIdentity{BundleVersionID: row.BundleVersionID, SpecDigest: effectiveSpecDigest, Source: source, Overrides: targetOverrides, Renderer: &renderer, ConfigurationDigest: configurationDigest}
	if err := desired.Validate(); err != nil {
		return PlanningSnapshot{}, &Error{Code: CodeInvariant, Field: "bundle", Cause: err}
	}
	var requirements []model.CapabilityRequirement
	if len(row.Requirements) != 0 && string(row.Requirements) != "{}" && string(row.Requirements) != "[]" {
		if err := decodeStrict(row.Requirements, &requirements); err != nil {
			return PlanningSnapshot{}, fail(CodeInvariant, "bundle.requirements", "stored requirements are invalid")
		}
	}
	allowedProjects := append([]uuid.UUID{row.ProjectID}, selector.ProjectIDs...)
	allowedProjects = uniqueUUIDs(allowedProjects)
	candidateRows, err := tx.queries.ListDeliveryPlanningCandidates(ctx, sqlc.ListDeliveryPlanningCandidatesParams{
		TargetID: targetID, ProjectIds: allowedProjects, OwnerProjectID: row.ProjectID,
		QueryLimit: MaxRolloutClusters + 1,
	})
	if err != nil {
		return PlanningSnapshot{}, fmt.Errorf("load delivery planning candidates: %w", err)
	}
	if len(candidateRows) > MaxRolloutClusters {
		return PlanningSnapshot{}, fail(CodeInvalidInput, "placement", fmt.Sprintf("selects more than %d clusters", MaxRolloutClusters))
	}
	candidates := make([]placement.Candidate, 0, len(candidateRows))
	previous := make(map[uuid.UUID]PreviousDeployment)
	desiredByCluster := make(map[uuid.UUID]VersionIdentity, len(candidateRows))
	for _, candidateRow := range candidateRows {
		labels := make(map[string]string)
		if err := json.Unmarshal(candidateRow.Labels, &labels); err != nil {
			return PlanningSnapshot{}, fail(CodeInvariant, "cluster.labels", "stored labels are invalid")
		}
		groups := []uuid.UUID(nil)
		if candidateRow.GroupID.Valid {
			groups = []uuid.UUID{candidateRow.GroupID.Bytes}
		}
		capabilities, err := deliveryCapabilities(candidateRow.FluxVersion, candidateRow.Components)
		if err != nil {
			return PlanningSnapshot{}, fail(CodeInvariant, "controller_inventory.components", "stored component versions are invalid")
		}
		candidate := placement.Candidate{
			ID: candidateRow.ClusterID, ProjectID: candidateRow.ProjectID, Name: candidateRow.ClusterName,
			Labels: labels, GroupIDs: groups, Connected: candidateRow.Connected,
			Compatibility:       placement.CompatibilityStatus(candidateRow.CompatibilityStatus),
			CompatibilityReason: candidateRow.CompatibilityReason,
			Decommissioning:     candidateRow.DecommissionedAt.Valid,
			Capabilities:        capabilities,
		}
		candidates = append(candidates, candidate)
		clusterRenderer, clusterConfigurationDigest, clusterSpecDigest, resolveErr := resolveEffectiveRenderer(row, bundleSpecDigest, targetOverrides, template, configurationOverrides, &candidate)
		if resolveErr != nil {
			return PlanningSnapshot{}, resolveErr
		}
		desiredByCluster[candidate.ID] = VersionIdentity{
			BundleVersionID: row.BundleVersionID, SpecDigest: clusterSpecDigest, Source: source,
			Overrides: targetOverrides, Renderer: &clusterRenderer, ConfigurationDigest: clusterConfigurationDigest,
		}
		if candidateRow.PreviousBundleVersionID.Valid && candidateRow.PreviousGeneration.Valid &&
			candidateRow.PreviousSpecDigest.Valid && len(candidateRow.PreviousSourceSpec) != 0 {
			previousDigest, digestErr := model.ParseDigest(candidateRow.PreviousSpecDigest.String)
			if digestErr != nil {
				return PlanningSnapshot{}, fail(CodeInvariant, "previous.spec_digest", "stored digest is invalid")
			}
			var previousSource model.ResolvedSourceSpec
			if decodeErr := decodeStrict(candidateRow.PreviousSourceSpec, &previousSource); decodeErr != nil {
				return PlanningSnapshot{}, fail(CodeInvariant, "previous.source_spec", "stored source snapshot is invalid")
			}
			var previousOverrides model.TargetOverrides
			if len(candidateRow.PreviousOverrides) != 0 {
				if decodeErr := decodeStrict(candidateRow.PreviousOverrides, &previousOverrides); decodeErr != nil {
					return PlanningSnapshot{}, fail(CodeInvariant, "previous.overrides", "stored overrides are invalid")
				}
			}
			previousVersion := VersionIdentity{BundleVersionID: candidateRow.PreviousBundleVersionID.Bytes, SpecDigest: previousDigest, Source: previousSource, Overrides: previousOverrides}
			if len(candidateRow.PreviousRendererSpec) != 0 && candidateRow.PreviousConfigurationDigest.Valid {
				var previousRenderer model.RendererSpec
				if decodeErr := decodeStrict(candidateRow.PreviousRendererSpec, &previousRenderer); decodeErr != nil {
					return PlanningSnapshot{}, fail(CodeInvariant, "previous.renderer", "stored renderer snapshot is invalid")
				}
				previousConfigurationDigest, digestErr := model.ParseDigest(candidateRow.PreviousConfigurationDigest.String)
				if digestErr != nil {
					return PlanningSnapshot{}, fail(CodeInvariant, "previous.configuration_digest", "stored digest is invalid")
				}
				previousVersion.Renderer = &previousRenderer
				previousVersion.ConfigurationDigest = previousConfigurationDigest
			}
			previous[candidateRow.ClusterID] = PreviousDeployment{
				Version:    previousVersion,
				Generation: candidateRow.PreviousGeneration.Int64,
			}
		}
	}
	type clusterConfigurationIdentity struct {
		ClusterID  uuid.UUID    `json:"cluster_id"`
		SpecDigest model.Digest `json:"spec_digest"`
	}
	configurationIdentities := make([]clusterConfigurationIdentity, 0, len(candidates))
	for _, candidate := range candidates {
		configurationIdentities = append(configurationIdentities, clusterConfigurationIdentity{
			ClusterID: candidate.ID, SpecDigest: desiredByCluster[candidate.ID].SpecDigest,
		})
	}
	placementConfigurationDigest, err := model.CanonicalDigest(struct {
		Base     model.Digest                   `json:"base"`
		Clusters []clusterConfigurationIdentity `json:"clusters"`
	}{desired.SpecDigest, configurationIdentities})
	if err != nil {
		return PlanningSnapshot{}, fail(CodeInvariant, "configuration_digest", "cannot bind cluster configurations")
	}
	var policy struct {
		ApprovalRequired bool `json:"approval_required"`
	}
	if len(row.RolloutPolicy) != 0 {
		if err := decodeStrict(row.RolloutPolicy, &policy); err != nil {
			return PlanningSnapshot{}, fail(CodeInvariant, "target.rollout_policy", "stored policy is invalid")
		}
	}
	return PlanningSnapshot{
		TargetID: row.TargetID, ProjectID: row.ProjectID, TargetGeneration: uint64(row.Generation), Desired: desired,
		DesiredByCluster: desiredByCluster, ConfigurationSetDigest: placementConfigurationDigest,
		PlacementRequest: placement.Request{
			Placement: selector, AllowedProjectIDs: allowedProjects, Candidates: candidates,
			RequiredCapabilities: requirements,
			Identity: placement.SnapshotIdentity{
				TargetGeneration: uint64(row.Generation), BundleVersionID: desired.BundleVersionID,
				BundleSpecDigest: placementConfigurationDigest, ResolvedRevision: desired.Source.Revision,
			},
		},
		InitialApproval: policy.ApprovalRequired, PreviousByCluster: previous,
	}, nil
}

func (tx *postgresPlanningTransaction) loadConfigurationRows(ctx context.Context, row sqlc.GetDeliveryPlanningSnapshotRow) (*sqlc.DeliveryConfigurationTemplate, []sqlc.DeliveryOverrideSet, error) {
	var template *sqlc.DeliveryConfigurationTemplate
	if row.ConfigurationTemplateID.Valid {
		value, err := tx.queries.GetDeliveryConfigurationTemplate(ctx, sqlc.GetDeliveryConfigurationTemplateParams{ProjectID: row.ProjectID, ID: row.ConfigurationTemplateID.Bytes})
		if err != nil {
			return nil, nil, fail(CodeInvalidInput, "configuration_template_id", "template is unavailable")
		}
		template = &value
	}
	var overrides []sqlc.DeliveryOverrideSet
	if len(row.OverrideSetIds) != 0 {
		var err error
		overrides, err = tx.queries.ListDeliveryOverrideSetsByIDs(ctx, sqlc.ListDeliveryOverrideSetsByIDsParams{ProjectID: row.ProjectID, Column2: row.OverrideSetIds})
		if err != nil || len(overrides) != len(row.OverrideSetIds) {
			return nil, nil, fail(CodeInvalidInput, "override_set_ids", "an override is missing, disabled, or outside the project")
		}
	}
	return template, overrides, nil
}

func resolveEffectiveRenderer(row sqlc.GetDeliveryPlanningSnapshotRow, bundleDigest model.Digest, targetOverrides model.TargetOverrides, template *sqlc.DeliveryConfigurationTemplate, overrides []sqlc.DeliveryOverrideSet, candidate *placement.Candidate) (model.RendererSpec, model.Digest, model.Digest, error) {
	var renderer model.RendererSpec
	if err := decodeStrict(row.RendererSpec, &renderer); err != nil || renderer.Validate() != nil {
		return renderer, "", "", fail(CodeInvariant, "bundle.renderer_spec", "stored renderer is invalid")
	}
	baseValues := json.RawMessage(`{}`)
	basePatches := []string(nil)
	valueSecretRefs := []model.HelmValueSecretRef(nil)
	if renderer.Kind == model.RendererHelm {
		baseValues = renderer.Helm.Values
	} else {
		basePatches = append(basePatches, renderer.Kustomize.Patches...)
	}
	layers := make([]deliveryconfig.Layer, 0, len(row.OverrideSetIds)+1)
	if template != nil {
		if template.Renderer != string(renderer.Kind) {
			return renderer, "", "", fail(CodeInvalidInput, "configuration_template_id", "renderer does not match the bundle")
		}
		var patches []string
		if json.Unmarshal(template.Patches, &patches) != nil {
			return renderer, "", "", fail(CodeInvariant, "configuration_template", "stored patches are invalid")
		}
		var secretRefs []struct {
			Name      string `json:"name"`
			Key       string `json:"key"`
			ValuePath string `json:"value_path"`
		}
		if json.Unmarshal(template.SecretRefs, &secretRefs) != nil {
			return renderer, "", "", fail(CodeInvariant, "configuration_template", "stored Secret references are invalid")
		}
		if renderer.Kind != model.RendererHelm && len(secretRefs) != 0 {
			return renderer, "", "", fail(CodeInvalidInput, "configuration_template_id", "Kustomize templates cannot use Helm value Secret references")
		}
		for _, ref := range secretRefs {
			valueSecretRefs = append(valueSecretRefs, model.HelmValueSecretRef{Name: ref.Name, Key: ref.Key, TargetPath: ref.ValuePath})
		}
		layers = append(layers, deliveryconfig.Layer{ID: template.ID, Name: template.Name, Scope: deliveryconfig.ScopeProject, Precedence: -1 << 30, Values: template.ValuesDocument, Patches: patches})
	}
	for _, override := range overrides {
		if overrideApplies(row.TargetID, override, candidate) {
			if override.TemplateID.Valid && (template == nil || override.TemplateID.Bytes != template.ID) {
				return renderer, "", "", fail(CodeInvalidInput, "override_set_ids", "an override is bound to a different configuration template")
			}
			var patches []string
			if json.Unmarshal(override.Patches, &patches) != nil {
				return renderer, "", "", fail(CodeInvariant, "override_set", "stored patches are invalid")
			}
			layers = append(layers, deliveryconfig.Layer{ID: override.ID, Name: override.Name, Scope: deliveryconfig.Scope(override.ScopeType), Precedence: int(override.Precedence), Values: override.ValuesDocument, Patches: patches})
		}
	}
	result, err := deliveryconfig.Merge(baseValues, layers)
	if err != nil {
		return renderer, "", "", &Error{Code: CodeInvalidInput, Field: "override_set_ids", Cause: err}
	}
	configurationDigest, err := model.CanonicalDigest(struct {
		Values          json.RawMessage            `json:"values"`
		Patches         []string                   `json:"patches"`
		ValueSecretRefs []model.HelmValueSecretRef `json:"value_secret_refs,omitempty"`
	}{result.Values, result.Patches, valueSecretRefs})
	if err != nil {
		return renderer, "", "", &Error{Code: CodeInvariant, Field: "configuration_digest", Cause: err}
	}
	if renderer.Kind == model.RendererHelm {
		if len(result.Patches) != 0 {
			return renderer, "", "", fail(CodeInvalidInput, "override_set_ids", "Helm configuration cannot contain Kustomize patches")
		}
		renderer.Helm.Values = result.Values
		renderer.Helm.ValueSecretRefs = append([]model.HelmValueSecretRef(nil), valueSecretRefs...)
	} else {
		renderer.Kustomize.Patches = append(basePatches, result.Patches...)
	}
	effectiveDigest, err := model.CanonicalDigest(struct {
		Bundle        model.Digest          `json:"bundle"`
		Configuration model.Digest          `json:"configuration"`
		Renderer      model.RendererSpec    `json:"renderer"`
		Overrides     model.TargetOverrides `json:"overrides"`
	}{bundleDigest, configurationDigest, renderer, targetOverrides})
	if err != nil {
		return renderer, "", "", &Error{Code: CodeInvariant, Field: "effective_spec", Cause: err}
	}
	return renderer, configurationDigest, effectiveDigest, nil
}

func overrideApplies(targetID uuid.UUID, override sqlc.DeliveryOverrideSet, candidate *placement.Candidate) bool {
	switch deliveryconfig.Scope(override.ScopeType) {
	case deliveryconfig.ScopeOrganization, deliveryconfig.ScopeProject:
		return true
	case deliveryconfig.ScopeRollout:
		return override.ScopeID.Valid && override.ScopeID.Bytes == targetID
	case deliveryconfig.ScopeCluster:
		return candidate != nil && override.ScopeID.Valid && override.ScopeID.Bytes == candidate.ID
	case deliveryconfig.ScopeEnvironment, deliveryconfig.ScopeGroup:
		if candidate == nil || !override.ScopeID.Valid {
			return false
		}
		for _, groupID := range candidate.GroupIDs {
			if groupID == override.ScopeID.Bytes {
				return true
			}
		}
	}
	return false
}

func (tx *postgresPlanningTransaction) InsertRollout(ctx context.Context, plan FrozenRollout) error {
	frozenJSON, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("marshal frozen rollout: %w", err)
	}
	strategyJSON, _ := json.Marshal(plan.Strategy)
	approvalJSON, _ := json.Marshal(plan.Approval)
	overrideDigest, err := plan.Desired.Overrides.CanonicalDigest()
	if err != nil {
		return fmt.Errorf("digest frozen target overrides: %w", err)
	}
	placementJSON, _ := json.Marshal(struct {
		Digest         model.Digest          `json:"digest"`
		OverrideDigest model.Digest          `json:"override_digest"`
		Overrides      model.TargetOverrides `json:"overrides"`
		Cohorts        []Cohort              `json:"cohorts"`
		Clusters       []PlannedCluster      `json:"clusters"`
	}{plan.PlacementDigest, overrideDigest, plan.Desired.Overrides, plan.Cohorts, plan.Clusters})
	fromVersion := commonPreviousVersion(plan.Clusters)
	initiatedBy := pgtype.UUID{}
	if actorID, parseErr := uuid.Parse(plan.Actor); parseErr == nil {
		initiatedBy = pgtype.UUID{Bytes: actorID, Valid: true}
	}
	if _, err := tx.queries.CreateDeliveryRollout(ctx, sqlc.CreateDeliveryRolloutParams{
		ID: plan.ID, TargetID: plan.TargetID, TargetGeneration: int64(plan.TargetGeneration),
		FromBundleVersionID: fromVersion, ToBundleVersionID: plan.Desired.BundleVersionID,
		PlacementDigest: plan.PlacementDigest.String(), PlacementSnapshot: placementJSON,
		Strategy: strategyJSON, StrategyDigest: plan.StrategyDigest.String(), ApprovalPolicy: approvalJSON,
		RequestDigest: plan.RequestDigest.String(), PlanDigest: plan.PlanDigest.String(), FrozenPlan: frozenJSON,
		State: string(model.RolloutResolving), IdempotencyKey: plan.IdempotencyKey,
		ProgressDeadline: timestamptz(plan.Deadline), InitiatedBy: initiatedBy,
	}); err != nil {
		return fmt.Errorf("insert frozen delivery rollout: %w", err)
	}
	for _, cluster := range plan.Clusters {
		desired := plan.Desired
		if cluster.Desired != nil {
			desired = *cluster.Desired
		}
		previousVersion := pgtype.UUID{}
		if cluster.Previous != nil {
			previousVersion = pgtype.UUID{Bytes: cluster.Previous.Version.BundleVersionID, Valid: true}
		}
		if _, err := tx.queries.CreateDeliveryRolloutCluster(ctx, sqlc.CreateDeliveryRolloutClusterParams{
			RolloutID: plan.ID, ClusterID: cluster.ClusterID, Cohort: int32(cluster.Cohort), ReleaseOrder: int32(cluster.Order),
			PreviousBundleVersionID: previousVersion, DesiredBundleVersionID: desired.BundleVersionID,
			DesiredSpecDigest: desired.SpecDigest.String(), Deadline: timestamptz(plan.Deadline),
		}); err != nil {
			return fmt.Errorf("insert rollout cluster %s: %w", cluster.ClusterID, err)
		}
	}
	if _, err := tx.queries.RecomputeDeliveryRolloutCounters(ctx, plan.ID); err != nil {
		return fmt.Errorf("initialize delivery rollout counters: %w", err)
	}
	return nil
}

func (tx *postgresPlanningTransaction) AppendRolloutCreated(ctx context.Context, plan FrozenRollout) error {
	_, err := tx.queries.CreateDeliveryRolloutEvent(ctx, sqlc.CreateDeliveryRolloutEventParams{
		RolloutID: plan.ID, DecisionDigest: plan.PlanDigest.String(), EventType: "rollout_created",
		FromState: "", ToState: string(model.RolloutResolving), ReasonCode: "plan_frozen", Fence: 1,
		OccurredAt: plan.CreatedAt,
	})
	if err != nil {
		return fmt.Errorf("append rollout creation event: %w", err)
	}
	return nil
}

func (tx *postgresPlanningTransaction) EnqueueRollout(ctx context.Context, rolloutID uuid.UUID) error {
	payload, _ := json.Marshal(struct {
		RolloutID uuid.UUID `json:"rollout_id"`
	}{rolloutID})
	_, err := tx.queries.UpsertTaskOutbox(ctx, sqlc.UpsertTaskOutboxParams{
		DedupeKey: pgtype.Text{String: "delivery-rollout-initial:" + rolloutID.String(), Valid: true},
		TaskType:  TaskType, Payload: payload, QueueName: "default", MaxRetry: defaultTaskMaxRetries,
		TimeoutSeconds: int32(defaultTaskTimeout / time.Second), UniqueSeconds: 1,
		MaxDeliveryAttempts: 20, NextAttemptAt: timestamptz(time.Now().UTC()),
	})
	if err != nil {
		return fmt.Errorf("enqueue delivery rollout task outbox: %w", err)
	}
	return nil
}

func decodeFrozenPlan(row sqlc.DeliveryRollout) (FrozenRollout, error) {
	var plan FrozenRollout
	if err := decodeStrict(row.FrozenPlan, &plan); err != nil {
		return FrozenRollout{}, fail(CodeInvariant, "frozen_plan", "stored plan is invalid")
	}
	if plan.ID != row.ID || plan.TargetID != row.TargetID || int64(plan.TargetGeneration) != row.TargetGeneration ||
		plan.Desired.BundleVersionID != row.ToBundleVersionID || plan.PlacementDigest.String() != row.PlacementDigest ||
		plan.StrategyDigest.String() != row.StrategyDigest || plan.PlanDigest.String() != row.PlanDigest ||
		plan.RequestDigest.String() != row.RequestDigest || plan.IdempotencyKey != row.IdempotencyKey {
		return FrozenRollout{}, fail(CodeInvariant, "frozen_plan", "does not match indexed rollout identity")
	}
	if err := plan.Validate(); err != nil {
		return FrozenRollout{}, &Error{Code: CodeInvariant, Field: "frozen_plan", Cause: err}
	}
	return plan, nil
}

func decodeStrict(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing data")
	}
	return nil
}

func uniqueUUIDs(input []uuid.UUID) []uuid.UUID {
	set := make(map[uuid.UUID]struct{}, len(input))
	for _, id := range input {
		if id != uuid.Nil {
			set[id] = struct{}{}
		}
	}
	result := make([]uuid.UUID, 0, len(set))
	for id := range set {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}

func deliveryCapabilities(fluxVersion string, raw json.RawMessage) (map[string]string, error) {
	components := make(map[string]string)
	if len(raw) != 0 {
		if err := decodeStrict(raw, &components); err != nil {
			return nil, err
		}
	}
	// Capability requirements are the protocol feature bits placed in bundle
	// specs. Keep this projection in that same namespace: the previous
	// delivery.astronomer.io/* keys could never satisfy a bundle requiring
	// delivery.source.*, delivery.renderer.*, or delivery.scope.* and therefore
	// made every clean Flux placement appear incompatible.
	result := map[string]string{
		protocol.FeatureDeliveryAssignmentsV2:  "",
		protocol.FeatureDeliveryStatusV2:       "",
		protocol.FeatureDeliveryNamespaceScope: "",
		protocol.FeatureDeliveryPlatformScope:  "",
	}
	if fluxVersion != "" {
		result[protocol.FeatureDeliverySourceGit] = fluxVersion
		result[protocol.FeatureDeliverySourceOCIArtifact] = fluxVersion
		result[protocol.FeatureDeliverySourceHelmHTTP] = fluxVersion
		result[protocol.FeatureDeliverySourceHelmOCI] = fluxVersion
	}
	for name, version := range components {
		switch name {
		case "source-controller":
			result[protocol.FeatureDeliverySourceGit] = version
			result[protocol.FeatureDeliverySourceOCIArtifact] = version
			result[protocol.FeatureDeliverySourceHelmHTTP] = version
			result[protocol.FeatureDeliverySourceHelmOCI] = version
		case "kustomize-controller":
			result[protocol.FeatureDeliveryRendererKustomize] = version
		case "helm-controller":
			result[protocol.FeatureDeliveryRendererHelm] = version
		}
	}
	return result, nil
}

func commonPreviousVersion(clusters []PlannedCluster) pgtype.UUID {
	var common uuid.UUID
	for _, cluster := range clusters {
		if cluster.Previous == nil {
			return pgtype.UUID{}
		}
		if common == uuid.Nil {
			common = cluster.Previous.Version.BundleVersionID
		} else if common != cluster.Previous.Version.BundleVersionID {
			return pgtype.UUID{}
		}
	}
	if common == uuid.Nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: common, Valid: true}
}

func timestamptz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: !value.IsZero()}
}

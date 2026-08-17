package charlie

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	deliverydeployment "github.com/alphabravocompany/astronomer-go/internal/delivery/deployment"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
	deliveryrollout "github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
)

// deliveryCapabilityQueries is deliberately limited to credential-free public
// projections and rollout/deployment metadata. The adapter cannot load source
// secrets, rendered manifests, raw inventory, or downstream Kubernetes data.
type deliveryCapabilityQueries interface {
	ListDeliverySources(context.Context, sqlc.ListDeliverySourcesParams) ([]sqlc.ListDeliverySourcesRow, error)
	CountDeliverySources(context.Context, sqlc.CountDeliverySourcesParams) (int64, error)
	GetDeliverySource(context.Context, sqlc.GetDeliverySourceParams) (sqlc.GetDeliverySourceRow, error)
	ListComponentBundles(context.Context, sqlc.ListComponentBundlesParams) ([]sqlc.ComponentBundle, error)
	CountComponentBundles(context.Context, uuid.UUID) (int64, error)
	GetComponentBundle(context.Context, sqlc.GetComponentBundleParams) (sqlc.ComponentBundle, error)
	ListComponentBundleVersions(context.Context, sqlc.ListComponentBundleVersionsParams) ([]sqlc.ComponentBundleVersion, error)
	ListDeliveryTargets(context.Context, sqlc.ListDeliveryTargetsParams) ([]sqlc.DeliveryTarget, error)
	CountDeliveryTargets(context.Context, uuid.UUID) (int64, error)
	GetDeliveryTarget(context.Context, sqlc.GetDeliveryTargetParams) (sqlc.DeliveryTarget, error)
	ListDeliveryRollouts(context.Context, sqlc.ListDeliveryRolloutsParams) ([]sqlc.DeliveryRollout, error)
	CountDeliveryRollouts(context.Context, sqlc.CountDeliveryRolloutsParams) (int64, error)
	GetDeliveryRollout(context.Context, sqlc.GetDeliveryRolloutParams) (sqlc.DeliveryRollout, error)
	ListDeliveryRolloutApprovals(context.Context, uuid.UUID) ([]sqlc.DeliveryRolloutApproval, error)
	ListDeliveryRolloutEvents(context.Context, sqlc.ListDeliveryRolloutEventsParams) ([]sqlc.DeliveryRolloutEvent, error)
	ListClusterDeployments(context.Context, sqlc.ListClusterDeploymentsParams) ([]sqlc.ClusterDeployment, error)
	CountClusterDeployments(context.Context, sqlc.CountClusterDeploymentsParams) (int64, error)
	GetClusterDeployment(context.Context, sqlc.GetClusterDeploymentParams) (sqlc.ClusterDeployment, error)
	ListClusterDeploymentEvents(context.Context, sqlc.ListClusterDeploymentEventsParams) ([]sqlc.ClusterDeploymentEvent, error)
	CountDeliveryControllerCompatibility(context.Context) ([]sqlc.CountDeliveryControllerCompatibilityRow, error)
}

type deliveryTargetPreviewer interface {
	Preview(context.Context, uuid.UUID) (deliveryrollout.PlanningSnapshot, placement.Result, error)
}

// DeliveryCapabilityAdapter exposes the same first-party delivery persistence
// and control services as the HTTP API. It never reimplements reconciliation,
// constructs Flux objects, or crosses the managed-cluster boundary.
type DeliveryCapabilityAdapter struct {
	queries           deliveryCapabilityQueries
	previewer         deliveryTargetPreviewer
	rolloutControl    deliveryrollout.Controller
	deploymentControl deliverydeployment.Controller
	now               func() time.Time
}

func NewDeliveryCapabilityAdapter(queries deliveryCapabilityQueries, previewer deliveryTargetPreviewer, rolloutControl deliveryrollout.Controller, deploymentControl deliverydeployment.Controller) (*DeliveryCapabilityAdapter, error) {
	if queries == nil || previewer == nil || rolloutControl == nil || deploymentControl == nil {
		return nil, fmt.Errorf("Charlie delivery capability services are unavailable")
	}
	return &DeliveryCapabilityAdapter{
		queries: queries, previewer: previewer, rolloutControl: rolloutControl,
		deploymentControl: deploymentControl, now: time.Now,
	}, nil
}

func DeliveryCapabilityAdapters(adapter CapabilityExecutor) map[string]CapabilityExecutor {
	registrations := map[string]CapabilityExecutor{}
	for _, name := range []string{
		"astronomer.delivery.overview", "astronomer.delivery.sources", "astronomer.delivery.source_get",
		"astronomer.delivery.bundles", "astronomer.delivery.bundle_get", "astronomer.delivery.targets",
		"astronomer.delivery.target_preview", "astronomer.delivery.rollouts", "astronomer.delivery.rollout_get",
		"astronomer.delivery.deployments", "astronomer.delivery.deployment_get", "astronomer.delivery.system_health",
		"astronomer.delivery.rollout_pause", "astronomer.delivery.rollout_resume",
		"astronomer.delivery.rollout_approve", "astronomer.delivery.rollout_retry_failed", "astronomer.delivery.rollout_rollback",
		"astronomer.delivery.deployment_reconcile",
	} {
		registrations[name] = adapter
	}
	return registrations
}

func (a *DeliveryCapabilityAdapter) Execute(ctx context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage) (json.RawMessage, error) {
	projectID, projectErr := optionalCapabilityUUID(arguments, "project_id")
	switch capability.Name {
	case "astronomer.delivery.system_health":
		return a.systemHealth(ctx, capability.MaxResponseBytes)
	case "astronomer.delivery.overview":
		if projectErr != nil {
			return nil, projectErr
		}
		return a.overview(ctx, projectID, capability.MaxResponseBytes)
	case "astronomer.delivery.sources":
		return a.sources(ctx, projectID, projectErr, arguments, capability.MaxResponseBytes)
	case "astronomer.delivery.source_get":
		return a.sourceGet(ctx, projectID, projectErr, arguments, capability.MaxResponseBytes)
	case "astronomer.delivery.bundles":
		return a.bundles(ctx, projectID, projectErr, arguments, capability.MaxResponseBytes)
	case "astronomer.delivery.bundle_get":
		return a.bundleGet(ctx, projectID, projectErr, arguments, capability.MaxResponseBytes)
	case "astronomer.delivery.targets":
		return a.targets(ctx, projectID, projectErr, arguments, capability.MaxResponseBytes)
	case "astronomer.delivery.target_preview":
		return a.targetPreview(ctx, projectID, projectErr, arguments, capability.MaxResponseBytes)
	case "astronomer.delivery.rollouts":
		return a.rollouts(ctx, projectID, projectErr, arguments, capability.MaxResponseBytes)
	case "astronomer.delivery.rollout_get":
		return a.rolloutGet(ctx, projectID, projectErr, arguments, capability.MaxResponseBytes)
	case "astronomer.delivery.deployments":
		return a.deployments(ctx, projectID, projectErr, arguments, capability.MaxResponseBytes)
	case "astronomer.delivery.deployment_get":
		return a.deploymentGet(ctx, projectID, projectErr, arguments, capability.MaxResponseBytes)
	case "astronomer.delivery.rollout_pause", "astronomer.delivery.rollout_resume",
		"astronomer.delivery.rollout_retry_failed", "astronomer.delivery.rollout_rollback":
		return a.rolloutAction(ctx, capability, projectID, projectErr, arguments)
	case "astronomer.delivery.rollout_approve":
		return a.rolloutApprove(ctx, capability, projectID, projectErr, arguments)
	case "astronomer.delivery.deployment_reconcile":
		return a.deploymentAction(ctx, capability, projectID, projectErr, arguments)
	default:
		return nil, fmt.Errorf("unsupported delivery capability")
	}
}

func (a *DeliveryCapabilityAdapter) Verify(ctx context.Context, capability CapabilityDescriptor, arguments map[string]json.RawMessage, result json.RawMessage) (bool, error) {
	if capability.Effect == EffectRead {
		return true, nil
	}
	projectID, err := optionalCapabilityUUID(arguments, "project_id")
	if err != nil {
		return false, err
	}
	var expected struct {
		State             string `json:"state"`
		Fence             int64  `json:"fencing_generation"`
		Phase             string `json:"phase"`
		DesiredGeneration int64  `json:"desired_generation"`
	}
	if json.Unmarshal(result, &expected) != nil {
		return false, fmt.Errorf("delivery operation result is invalid")
	}
	if capability.Name == "astronomer.delivery.deployment_reconcile" {
		id, parseErr := requiredCapabilityUUID(arguments, "deployment_id")
		if parseErr != nil {
			return false, parseErr
		}
		row, readErr := a.queries.GetClusterDeployment(ctx, sqlc.GetClusterDeploymentParams{ID: id, ProjectID: projectID})
		return readErr == nil && row.Phase == expected.Phase && row.DesiredGeneration == expected.DesiredGeneration, readErr
	}
	id, err := requiredCapabilityUUID(arguments, "rollout_id")
	if err != nil {
		return false, err
	}
	row, err := a.queries.GetDeliveryRollout(ctx, sqlc.GetDeliveryRolloutParams{ID: id, ProjectID: projectID})
	return err == nil && row.State == expected.State && row.FencingGeneration == expected.Fence, err
}

func (a *DeliveryCapabilityAdapter) overview(ctx context.Context, projectID uuid.UUID, max int) (json.RawMessage, error) {
	sources, err := a.queries.CountDeliverySources(ctx, sqlc.CountDeliverySourcesParams{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	bundles, err := a.queries.CountComponentBundles(ctx, projectID)
	if err != nil {
		return nil, err
	}
	targets, err := a.queries.CountDeliveryTargets(ctx, projectID)
	if err != nil {
		return nil, err
	}
	rollouts, err := a.queries.CountDeliveryRollouts(ctx, sqlc.CountDeliveryRolloutsParams{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	deployments, err := a.queries.CountClusterDeployments(ctx, sqlc.CountClusterDeploymentsParams{ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	return marshalBounded(map[string]any{"project_id": projectID, "sources": sources, "bundles": bundles, "targets": targets, "rollouts": rollouts, "deployments": deployments}, max)
}

func (a *DeliveryCapabilityAdapter) systemHealth(ctx context.Context, max int) (json.RawMessage, error) {
	rows, err := a.queries.CountDeliveryControllerCompatibility(ctx)
	if err != nil {
		return nil, err
	}
	states := map[string]int64{}
	for _, row := range rows {
		states[row.CompatibilityStatus] = row.ClusterCount
	}
	return marshalBounded(map[string]any{"controller_compatibility": states}, max)
}

func (a *DeliveryCapabilityAdapter) sources(ctx context.Context, projectID uuid.UUID, projectErr error, args map[string]json.RawMessage, max int) (json.RawMessage, error) {
	if projectErr != nil {
		return nil, projectErr
	}
	page, size := pagination(args, 50)
	status := optionalPGText(args, "status")
	rows, err := a.queries.ListDeliverySources(ctx, sqlc.ListDeliverySourcesParams{ProjectID: projectID, Status: status, QueryOffset: int32((page - 1) * size), QueryLimit: int32(size)})
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"source_id": row.ID, "project_id": row.ProjectID, "name": row.Name, "source_type": row.SourceType, "auth_mode": row.AuthMode, "credential_configured": row.AuthMode != "none", "credential_epoch": row.CredentialEpoch, "status": row.Status, "last_resolved_at": nullableTime(row.LastResolvedAt), "last_error_code": row.LastErrorCode, "created_at": row.CreatedAt, "updated_at": row.UpdatedAt})
	}
	total, err := a.queries.CountDeliverySources(ctx, sqlc.CountDeliverySourcesParams{ProjectID: projectID, Status: status})
	if err != nil {
		return nil, err
	}
	return marshalBounded(map[string]any{"items": items, "total": total, "page": page, "page_size": size}, max)
}

func (a *DeliveryCapabilityAdapter) sourceGet(ctx context.Context, projectID uuid.UUID, projectErr error, args map[string]json.RawMessage, max int) (json.RawMessage, error) {
	if projectErr != nil {
		return nil, projectErr
	}
	id, err := requiredCapabilityUUID(args, "source_id")
	if err != nil {
		return nil, err
	}
	row, err := a.queries.GetDeliverySource(ctx, sqlc.GetDeliverySourceParams{ID: id, ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	return marshalBounded(map[string]any{"source_id": row.ID, "project_id": row.ProjectID, "name": row.Name, "description": row.Description, "source_type": row.SourceType, "auth_mode": row.AuthMode, "credential_configured": row.AuthMode != "none", "credential_epoch": row.CredentialEpoch, "proxy_configured": row.ProxyRef != "", "status": row.Status, "last_resolved_at": nullableTime(row.LastResolvedAt), "last_error_code": row.LastErrorCode, "created_at": row.CreatedAt, "updated_at": row.UpdatedAt}, max)
}

func (a *DeliveryCapabilityAdapter) bundles(ctx context.Context, projectID uuid.UUID, projectErr error, args map[string]json.RawMessage, max int) (json.RawMessage, error) {
	if projectErr != nil {
		return nil, projectErr
	}
	page, size := pagination(args, 50)
	rows, err := a.queries.ListComponentBundles(ctx, sqlc.ListComponentBundlesParams{ProjectID: projectID, QueryOffset: int32((page - 1) * size), QueryLimit: int32(size)})
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, safeBundle(row))
	}
	total, err := a.queries.CountComponentBundles(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return marshalBounded(map[string]any{"items": items, "total": total, "page": page, "page_size": size}, max)
}

func (a *DeliveryCapabilityAdapter) bundleGet(ctx context.Context, projectID uuid.UUID, projectErr error, args map[string]json.RawMessage, max int) (json.RawMessage, error) {
	if projectErr != nil {
		return nil, projectErr
	}
	id, err := requiredCapabilityUUID(args, "bundle_id")
	if err != nil {
		return nil, err
	}
	bundle, err := a.queries.GetComponentBundle(ctx, sqlc.GetComponentBundleParams{ID: id, ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	page, size := pagination(args, 25)
	rows, err := a.queries.ListComponentBundleVersions(ctx, sqlc.ListComponentBundleVersionsParams{BundleID: id, QueryOffset: int32((page - 1) * size), QueryLimit: int32(size)})
	if err != nil {
		return nil, err
	}
	versions := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		versions = append(versions, safeBundleVersion(row))
	}
	return marshalBounded(map[string]any{"bundle": safeBundle(bundle), "versions": versions, "page": page, "page_size": size}, max)
}

func (a *DeliveryCapabilityAdapter) targets(ctx context.Context, projectID uuid.UUID, projectErr error, args map[string]json.RawMessage, max int) (json.RawMessage, error) {
	if projectErr != nil {
		return nil, projectErr
	}
	page, size := pagination(args, 50)
	rows, err := a.queries.ListDeliveryTargets(ctx, sqlc.ListDeliveryTargetsParams{ProjectID: projectID, QueryOffset: int32((page - 1) * size), QueryLimit: int32(size)})
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, safeTarget(row))
	}
	total, err := a.queries.CountDeliveryTargets(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return marshalBounded(map[string]any{"items": items, "total": total, "page": page, "page_size": size}, max)
}

func (a *DeliveryCapabilityAdapter) targetPreview(ctx context.Context, projectID uuid.UUID, projectErr error, args map[string]json.RawMessage, max int) (json.RawMessage, error) {
	if projectErr != nil {
		return nil, projectErr
	}
	id, err := requiredCapabilityUUID(args, "target_id")
	if err != nil {
		return nil, err
	}
	snapshot, result, err := a.previewer.Preview(ctx, id)
	if err != nil {
		return nil, err
	}
	if snapshot.ProjectID != projectID {
		return nil, fmt.Errorf("delivery target was not found in project")
	}
	return marshalBounded(map[string]any{"target_id": id, "target_generation": snapshot.TargetGeneration, "bundle_version_id": snapshot.Desired.BundleVersionID, "preview_digest": result.PreviewDigest, "selected_count": result.SelectedCount, "excluded_count": result.ExcludedCount, "requires_all_confirmation": result.RequiresAllConfirmation, "decisions": result.Decisions}, max)
}

func (a *DeliveryCapabilityAdapter) rollouts(ctx context.Context, projectID uuid.UUID, projectErr error, args map[string]json.RawMessage, max int) (json.RawMessage, error) {
	if projectErr != nil {
		return nil, projectErr
	}
	page, size := pagination(args, 50)
	state := optionalPGText(args, "state")
	rows, err := a.queries.ListDeliveryRollouts(ctx, sqlc.ListDeliveryRolloutsParams{ProjectID: projectID, State: state, QueryOffset: int32((page - 1) * size), QueryLimit: int32(size)})
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, safeRollout(row))
	}
	total, err := a.queries.CountDeliveryRollouts(ctx, sqlc.CountDeliveryRolloutsParams{ProjectID: projectID, State: state})
	if err != nil {
		return nil, err
	}
	return marshalBounded(map[string]any{"items": items, "total": total, "page": page, "page_size": size}, max)
}

func (a *DeliveryCapabilityAdapter) rolloutGet(ctx context.Context, projectID uuid.UUID, projectErr error, args map[string]json.RawMessage, max int) (json.RawMessage, error) {
	if projectErr != nil {
		return nil, projectErr
	}
	id, err := requiredCapabilityUUID(args, "rollout_id")
	if err != nil {
		return nil, err
	}
	row, err := a.queries.GetDeliveryRollout(ctx, sqlc.GetDeliveryRolloutParams{ID: id, ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	approvals, err := a.queries.ListDeliveryRolloutApprovals(ctx, id)
	if err != nil {
		return nil, err
	}
	events, err := a.queries.ListDeliveryRolloutEvents(ctx, sqlc.ListDeliveryRolloutEventsParams{RolloutID: id, ProjectID: projectID, QueryLimit: 50})
	if err != nil {
		return nil, err
	}
	return marshalBounded(map[string]any{"rollout": safeRollout(row), "approvals": safeRolloutApprovals(approvals), "timeline": safeRolloutEvents(events)}, max)
}

func (a *DeliveryCapabilityAdapter) deployments(ctx context.Context, projectID uuid.UUID, projectErr error, args map[string]json.RawMessage, max int) (json.RawMessage, error) {
	if projectErr != nil {
		return nil, projectErr
	}
	page, size := pagination(args, 50)
	clusterID, err := optionalCapabilityUUID(args, "cluster_id")
	if err != nil {
		return nil, err
	}
	cluster := pgtype.UUID{}
	if clusterID != uuid.Nil {
		cluster = pgtype.UUID{Bytes: clusterID, Valid: true}
	}
	phase := optionalPGText(args, "phase")
	rows, err := a.queries.ListClusterDeployments(ctx, sqlc.ListClusterDeploymentsParams{ProjectID: projectID, ClusterID: cluster, Phase: phase, QueryOffset: int32((page - 1) * size), QueryLimit: int32(size)})
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, safeDeployment(row))
	}
	total, err := a.queries.CountClusterDeployments(ctx, sqlc.CountClusterDeploymentsParams{ProjectID: projectID, ClusterID: cluster, Phase: phase})
	if err != nil {
		return nil, err
	}
	return marshalBounded(map[string]any{"items": items, "total": total, "page": page, "page_size": size}, max)
}

func (a *DeliveryCapabilityAdapter) deploymentGet(ctx context.Context, projectID uuid.UUID, projectErr error, args map[string]json.RawMessage, max int) (json.RawMessage, error) {
	if projectErr != nil {
		return nil, projectErr
	}
	id, err := requiredCapabilityUUID(args, "deployment_id")
	if err != nil {
		return nil, err
	}
	row, err := a.queries.GetClusterDeployment(ctx, sqlc.GetClusterDeploymentParams{ID: id, ProjectID: projectID})
	if err != nil {
		return nil, err
	}
	events, err := a.queries.ListClusterDeploymentEvents(ctx, sqlc.ListClusterDeploymentEventsParams{DeploymentID: id, ProjectID: projectID, QueryLimit: 50})
	if err != nil {
		return nil, err
	}
	return marshalBounded(map[string]any{"deployment": safeDeployment(row), "events": safeDeploymentEvents(events)}, max)
}

func (a *DeliveryCapabilityAdapter) rolloutAction(ctx context.Context, capability CapabilityDescriptor, projectID uuid.UUID, projectErr error, args map[string]json.RawMessage) (json.RawMessage, error) {
	if projectErr != nil {
		return nil, projectErr
	}
	id, err := requiredCapabilityUUID(args, "rollout_id")
	if err != nil {
		return nil, err
	}
	action := map[string]deliveryrollout.Action{
		"astronomer.delivery.rollout_pause":        deliveryrollout.ActionPause,
		"astronomer.delivery.rollout_resume":       deliveryrollout.ActionResume,
		"astronomer.delivery.rollout_retry_failed": deliveryrollout.ActionRetry,
		"astronomer.delivery.rollout_rollback":     deliveryrollout.ActionRollback,
	}[capability.Name]
	result, err := a.rolloutControl.Act(ctx, deliveryrollout.ActionRequest{ProjectID: projectID, RolloutID: id, ExpectedFence: int64Argument(args, "expected_fence", 0), Action: action, ReasonCode: stringArgument(args, "reason_code")})
	if err != nil {
		return nil, err
	}
	return marshalBounded(map[string]any{"status": "accepted", "rollout_id": id, "state": result.Rollout.State, "fencing_generation": result.Rollout.FencingGeneration}, capability.MaxResponseBytes)
}

func (a *DeliveryCapabilityAdapter) rolloutApprove(ctx context.Context, capability CapabilityDescriptor, projectID uuid.UUID, projectErr error, args map[string]json.RawMessage) (json.RawMessage, error) {
	if projectErr != nil {
		return nil, projectErr
	}
	id, err := requiredCapabilityUUID(args, "rollout_id")
	if err != nil {
		return nil, err
	}
	digest, err := model.ParseDigest(stringArgument(args, "binding_digest"))
	if err != nil {
		return nil, fmt.Errorf("approval binding digest is invalid")
	}
	result, err := a.rolloutControl.Approve(ctx, deliveryrollout.ApprovalRequest{ProjectID: projectID, RolloutID: id, ExpectedFence: int64Argument(args, "expected_fence", 0), Cohort: int32(int64Argument(args, "cohort", -2)), BindingDigest: digest, Decision: "approved", ExpiresAt: a.now().UTC().Add(time.Duration(int64Argument(args, "expires_in_seconds", 900)) * time.Second)})
	if err != nil {
		return nil, err
	}
	return marshalBounded(map[string]any{"status": "accepted", "rollout_id": id, "state": result.Rollout.State, "fencing_generation": result.Rollout.FencingGeneration, "cohort": result.Approval.Cohort, "binding_digest": result.Approval.BindingDigest}, capability.MaxResponseBytes)
}

func (a *DeliveryCapabilityAdapter) deploymentAction(ctx context.Context, capability CapabilityDescriptor, projectID uuid.UUID, projectErr error, args map[string]json.RawMessage) (json.RawMessage, error) {
	if projectErr != nil {
		return nil, projectErr
	}
	id, err := requiredCapabilityUUID(args, "deployment_id")
	if err != nil {
		return nil, err
	}
	result, err := a.deploymentControl.Act(ctx, deliverydeployment.Request{ProjectID: projectID, DeploymentID: id, ExpectedGeneration: int64Argument(args, "expected_generation", -1), Action: deliverydeployment.ActionReconcile, ReasonCode: stringArgument(args, "reason_code")})
	if err != nil {
		return nil, err
	}
	return marshalBounded(map[string]any{"status": "accepted", "deployment_id": id, "phase": result.Deployment.Phase, "desired_generation": result.Deployment.DesiredGeneration}, capability.MaxResponseBytes)
}

func safeBundle(row sqlc.ComponentBundle) map[string]any {
	return map[string]any{"bundle_id": row.ID, "project_id": row.ProjectID, "name": row.Name, "description": row.Description, "created_at": row.CreatedAt, "updated_at": row.UpdatedAt}
}

func safeBundleVersion(row sqlc.ComponentBundleVersion) map[string]any {
	return map[string]any{"version_id": row.ID, "bundle_id": row.BundleID, "source_id": row.SourceID, "version": row.Version, "renderer": row.Renderer, "scope": row.Scope, "requested_revision": row.RequestedRevision, "resolved_revision": row.ResolvedRevision, "artifact_digest": row.ArtifactDigest, "spec_digest": row.SpecDigest, "verification_status": row.VerificationStatus, "verification_identity": row.VerificationIdentity, "state": row.State, "last_error_code": row.LastErrorCode, "created_at": row.CreatedAt}
}

func safeTarget(row sqlc.DeliveryTarget) map[string]any {
	return map[string]any{"target_id": row.ID, "project_id": row.ProjectID, "name": row.Name, "description": row.Description, "bundle_version_id": row.BundleVersionID, "suspended": row.Suspended, "generation": row.Generation, "resource_version": row.ResourceVersion, "deletion_state": row.DeletionState, "created_at": row.CreatedAt, "updated_at": row.UpdatedAt}
}

func safeRollout(row sqlc.DeliveryRollout) map[string]any {
	return map[string]any{"rollout_id": row.ID, "target_id": row.TargetID, "target_generation": row.TargetGeneration, "from_bundle_version_id": nullableUUID(row.FromBundleVersionID), "to_bundle_version_id": row.ToBundleVersionID, "placement_digest": row.PlacementDigest, "strategy_digest": row.StrategyDigest, "plan_digest": row.PlanDigest, "state": row.State, "fencing_generation": row.FencingGeneration, "total_clusters": row.TotalClusters, "ready_clusters": row.ReadyClusters, "failed_clusters": row.FailedClusters, "blocked_clusters": row.BlockedClusters, "released_clusters": row.ReleasedClusters, "progress_deadline": nullableTime(row.ProgressDeadline), "started_at": nullableTime(row.StartedAt), "completed_at": nullableTime(row.CompletedAt), "last_error_code": row.LastErrorCode, "created_at": row.CreatedAt, "updated_at": row.UpdatedAt}
}

func safeRolloutApprovals(rows []sqlc.DeliveryRolloutApproval) []map[string]any {
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"approval_id": row.ID, "cohort": row.Cohort, "binding_digest": row.BindingDigest, "decision": row.Decision, "decided_at": row.DecidedAt, "expires_at": row.ExpiresAt})
	}
	return items
}

func safeRolloutEvents(rows []sqlc.DeliveryRolloutEvent) []map[string]any {
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"event_id": row.ID, "cluster_id": nullableUUID(row.ClusterID), "event_type": row.EventType, "from_state": row.FromState, "to_state": row.ToState, "reason_code": row.ReasonCode, "fence": row.Fence, "occurred_at": row.OccurredAt})
	}
	return items
}

func safeDeployment(row sqlc.ClusterDeployment) map[string]any {
	return map[string]any{"deployment_id": row.ID, "target_id": row.TargetID, "cluster_id": row.ClusterID, "current_rollout_id": nullableUUID(row.CurrentRolloutID), "desired_bundle_version_id": nullableUUID(row.DesiredBundleVersionID), "previous_bundle_version_id": nullableUUID(row.PreviousBundleVersionID), "desired_generation": row.DesiredGeneration, "observed_generation": row.ObservedGeneration, "desired_spec_digest": row.DesiredSpecDigest, "observed_spec_digest": row.ObservedSpecDigest, "desired_revision": row.DesiredRevision, "observed_revision": row.ObservedRevision, "action": row.Action, "phase": row.Phase, "source_kind": row.SourceKind, "source_name": row.SourceName, "reconciler_kind": row.ReconcilerKind, "reconciler_name": row.ReconcilerName, "last_error_code": row.LastErrorCode, "last_observed_at": nullableTime(row.LastObservedAt), "created_at": row.CreatedAt, "updated_at": row.UpdatedAt}
}

func safeDeploymentEvents(rows []sqlc.ClusterDeploymentEvent) []map[string]any {
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, map[string]any{"event_id": row.ID, "rollout_id": nullableUUID(row.RolloutID), "event_type": row.EventType, "from_phase": row.FromPhase, "to_phase": row.ToPhase, "generation": row.Generation, "spec_digest": row.SpecDigest, "reason_code": row.ReasonCode, "observed_at": row.ObservedAt})
	}
	return items
}

func requiredCapabilityUUID(arguments map[string]json.RawMessage, name string) (uuid.UUID, error) {
	id, err := optionalCapabilityUUID(arguments, name)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%s is invalid", name)
	}
	return id, nil
}

func optionalCapabilityUUID(arguments map[string]json.RawMessage, name string) (uuid.UUID, error) {
	raw, ok := arguments[name]
	if !ok {
		return uuid.Nil, nil
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return uuid.Nil, fmt.Errorf("%s is invalid", name)
	}
	id, err := uuid.Parse(value)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("%s is invalid", name)
	}
	return id, nil
}

func nullableUUID(value pgtype.UUID) any {
	if !value.Valid {
		return nil
	}
	return uuid.UUID(value.Bytes)
}

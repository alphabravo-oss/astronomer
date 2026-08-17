package rollout

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/placement"
)

type Planner struct {
	store PlanningStore
	now   func() time.Time
	newID IDGenerator
}

func NewPlanner(store PlanningStore, now func() time.Time, newID IDGenerator) (*Planner, error) {
	if store == nil {
		return nil, fail(CodeInvalidInput, "store", "is required")
	}
	if now == nil {
		now = time.Now
	}
	if newID == nil {
		newID = uuid.New
	}
	return &Planner{store: store, now: now, newID: newID}, nil
}

// Create freezes placement, source, version, strategy, approvals, previous
// known-good versions, cluster order, and deadlines in one storage transaction.
// A committed retry with the same key and request returns the original plan.
func (p *Planner) Create(ctx context.Context, request CreateRequest) (FrozenRollout, error) {
	if err := validateCreateRequest(request); err != nil {
		return FrozenRollout{}, err
	}
	strategyDigest, err := request.Strategy.CanonicalDigest()
	if err != nil {
		return FrozenRollout{}, &Error{Code: CodeInvalidInput, Field: "strategy", Cause: err}
	}
	requestDigest, err := model.CanonicalDigest(struct {
		TargetID                 uuid.UUID    `json:"target_id"`
		ExpectedTargetGeneration uint64       `json:"expected_target_generation"`
		PreviewDigest            model.Digest `json:"preview_digest"`
		ConfirmAllClusters       bool         `json:"confirm_all_clusters"`
		StrategyDigest           model.Digest `json:"strategy_digest"`
		Actor                    string       `json:"actor"`
	}{request.TargetID, request.ExpectedTargetGeneration, request.PreviewDigest, request.ConfirmAllClusters, strategyDigest, request.Actor})
	if err != nil {
		return FrozenRollout{}, &Error{Code: CodeInvalidInput, Field: "request", Cause: err}
	}

	var result FrozenRollout
	err = p.store.InTransaction(ctx, func(tx PlanningTransaction) error {
		existing, found, findErr := tx.FindByIdempotency(ctx, request.TargetID, request.IdempotencyKey)
		if findErr != nil {
			return findErr
		}
		if found {
			if existing.RequestDigest != requestDigest {
				return fail(CodeIdempotencyConflict, "idempotency_key", "key is already bound to a different request")
			}
			if err := existing.Validate(); err != nil {
				return &Error{Code: CodeInvariant, Field: "existing_rollout", Cause: err}
			}
			result = existing
			return nil
		}

		snapshot, loadErr := tx.LoadSnapshotForUpdate(ctx, request.TargetID)
		if loadErr != nil {
			return loadErr
		}
		// Recheck after locking the target. Two transactions may both miss the
		// fast-path lookup, but the target-row lock serializes this second read
		// and guarantees that identical concurrent submissions observe the one
		// committed immutable plan rather than surfacing a unique-key race.
		existing, found, findErr = tx.FindByIdempotency(ctx, request.TargetID, request.IdempotencyKey)
		if findErr != nil {
			return findErr
		}
		if found {
			if existing.RequestDigest != requestDigest {
				return fail(CodeIdempotencyConflict, "idempotency_key", "key is already bound to a different request")
			}
			if err := existing.Validate(); err != nil {
				return &Error{Code: CodeInvariant, Field: "existing_rollout", Cause: err}
			}
			result = existing
			return nil
		}
		if snapshot.TargetID != request.TargetID || snapshot.TargetGeneration == 0 {
			return fail(CodeInvariant, "snapshot", "target identity is inconsistent")
		}
		if snapshot.TargetGeneration != request.ExpectedTargetGeneration {
			return fail(CodeTargetChanged, "target_generation", "target changed after preview")
		}
		if err := validateSnapshot(snapshot); err != nil {
			return err
		}
		placementResult, evaluateErr := placement.Evaluate(snapshot.PlacementRequest)
		if evaluateErr != nil {
			return evaluateErr
		}
		if launchErr := placementResult.ValidateLaunch(request.PreviewDigest, request.ConfirmAllClusters); launchErr != nil {
			if placement.HasCode(launchErr, placement.CodePreviewStale) {
				return &Error{Code: CodePreviewStale, Field: "preview_digest", Cause: launchErr}
			}
			return launchErr
		}
		if placementResult.SelectedCount == 0 {
			return fail(CodeNoClusters, "placement", "selects no eligible clusters")
		}

		cohorts, clusters, cohortErr := BuildCohorts(request.Strategy, placementResult.Selected)
		if cohortErr != nil {
			return cohortErr
		}
		for index := range clusters {
			if previous, exists := snapshot.PreviousByCluster[clusters[index].ClusterID]; exists {
				copy := previous
				clusters[index].Previous = &copy
			}
		}

		createdAt := p.now().UTC()
		if createdAt.IsZero() {
			return fail(CodeInvariant, "clock", "returned zero time")
		}
		rolloutID := p.newID()
		if rolloutID == uuid.Nil {
			return fail(CodeInvariant, "id_generator", "returned zero UUID")
		}
		result = FrozenRollout{
			ID: rolloutID, TargetID: snapshot.TargetID, ProjectID: snapshot.ProjectID,
			TargetGeneration: snapshot.TargetGeneration, Desired: snapshot.Desired,
			PlacementDigest: placementResult.PreviewDigest, Strategy: request.Strategy,
			StrategyDigest: strategyDigest, Actor: request.Actor,
			IdempotencyKey: request.IdempotencyKey, RequestDigest: requestDigest,
			CreatedAt: createdAt, Deadline: createdAt.Add(time.Duration(request.Strategy.ProgressDeadline)),
			Cohorts: cohorts, Clusters: clusters,
		}
		// All-cluster confirmation was already bound and checked by the exact
		// placement preview. A human approval is added only by the target/action
		// policy so confirmation and approval remain distinct audit concepts.
		result.Approval.Required = snapshot.InitialApproval
		result.Approval.Digest, err = approvalDigest(result, -1)
		if err != nil {
			return err
		}
		for index := range result.Cohorts {
			if result.Cohorts[index].ApprovalRequired {
				result.Cohorts[index].ApprovalDigest, err = approvalDigest(result, result.Cohorts[index].Index)
				if err != nil {
					return err
				}
			}
		}
		result.PlanDigest, err = frozenDigest(result)
		if err != nil {
			return err
		}
		if err := result.Validate(); err != nil {
			return &Error{Code: CodeInvariant, Field: "plan", Cause: err}
		}
		if err := tx.InsertRollout(ctx, result); err != nil {
			return err
		}
		if err := tx.AppendRolloutCreated(ctx, result); err != nil {
			return err
		}
		return tx.EnqueueRollout(ctx, result.ID)
	})
	if err != nil {
		return FrozenRollout{}, err
	}
	return result, nil
}

func validateSnapshot(snapshot PlanningSnapshot) error {
	if snapshot.ProjectID == uuid.Nil {
		return fail(CodeInvariant, "snapshot.project_id", "must be a non-zero UUID")
	}
	if err := snapshot.Desired.Validate(); err != nil {
		return &Error{Code: CodeInvariant, Field: "snapshot.desired", Cause: err}
	}
	identity := snapshot.PlacementRequest.Identity
	if identity.TargetGeneration != snapshot.TargetGeneration || identity.BundleVersionID != snapshot.Desired.BundleVersionID ||
		identity.BundleSpecDigest != snapshot.Desired.SpecDigest || identity.ResolvedRevision != snapshot.Desired.Source.Revision {
		return fail(CodeInvariant, "snapshot.placement_identity", "does not match the frozen target and desired version")
	}
	for clusterID, previous := range snapshot.PreviousByCluster {
		if clusterID == uuid.Nil {
			return fail(CodeInvariant, "snapshot.previous", "contains a zero cluster UUID")
		}
		if err := previous.Validate(); err != nil {
			return &Error{Code: CodeInvariant, Field: "snapshot.previous", Cause: err}
		}
	}
	return nil
}

func approvalDigest(plan FrozenRollout, cohort int) (model.Digest, error) {
	clusterIDs := []uuid.UUID(nil)
	if cohort >= 0 {
		if cohort >= len(plan.Cohorts) || plan.Cohorts[cohort].Index != cohort {
			return "", fail(CodeInvariant, "cohort", "approval cohort index is inconsistent")
		}
		clusterIDs = plan.Cohorts[cohort].ClusterIDs
	}
	return model.CanonicalDigest(struct {
		RolloutID        uuid.UUID    `json:"rollout_id"`
		TargetID         uuid.UUID    `json:"target_id"`
		TargetGeneration uint64       `json:"target_generation"`
		PlacementDigest  model.Digest `json:"placement_digest"`
		BundleVersionID  uuid.UUID    `json:"bundle_version_id"`
		SpecDigest       model.Digest `json:"spec_digest"`
		StrategyDigest   model.Digest `json:"strategy_digest"`
		Actor            string       `json:"actor"`
		Cohort           int          `json:"cohort"`
		ClusterIDs       []uuid.UUID  `json:"cluster_ids,omitempty"`
	}{plan.ID, plan.TargetID, plan.TargetGeneration, plan.PlacementDigest, plan.Desired.BundleVersionID, plan.Desired.SpecDigest, plan.StrategyDigest, plan.Actor, cohort, clusterIDs})
}

func frozenDigest(plan FrozenRollout) (model.Digest, error) {
	copy := plan
	copy.PlanDigest = ""
	return model.CanonicalDigest(copy)
}

func (plan FrozenRollout) Validate() error {
	if plan.ID == uuid.Nil || plan.TargetID == uuid.Nil || plan.ProjectID == uuid.Nil || plan.TargetGeneration == 0 {
		return fail(CodeInvalidInput, "identity", "rollout, target, project, and generation are required")
	}
	if err := plan.Desired.Validate(); err != nil {
		return err
	}
	for field, digest := range map[string]model.Digest{
		"placement_digest": plan.PlacementDigest, "strategy_digest": plan.StrategyDigest,
		"request_digest": plan.RequestDigest, "approval.digest": plan.Approval.Digest, "plan_digest": plan.PlanDigest,
	} {
		if err := digest.Validate(); err != nil {
			return &Error{Code: CodeInvalidInput, Field: field, Cause: err}
		}
	}
	if err := plan.Strategy.Validate(); err != nil {
		return err
	}
	strategyDigest, err := plan.Strategy.CanonicalDigest()
	if err != nil || strategyDigest != plan.StrategyDigest {
		return fail(CodeInvariant, "strategy_digest", "does not match strategy")
	}
	if plan.CreatedAt.IsZero() || !plan.Deadline.After(plan.CreatedAt) || plan.Deadline != plan.CreatedAt.Add(time.Duration(plan.Strategy.ProgressDeadline)) {
		return fail(CodeInvalidInput, "deadline", "must equal creation time plus progress deadline")
	}
	if len(plan.Cohorts) == 0 || len(plan.Clusters) == 0 {
		return fail(CodeInvalidInput, "cohorts", "must not be empty")
	}
	seen := make(map[uuid.UUID]struct{}, len(plan.Clusters))
	expectedOrder := 0
	for index, cohort := range plan.Cohorts {
		if cohort.Index != index || cohort.Name == "" || len(cohort.ClusterIDs) == 0 || cohort.SoakAfter < 0 {
			return fail(CodeInvariant, "cohorts", "indices, names, membership, and soak must be valid")
		}
		if cohort.ApprovalRequired {
			digest, digestErr := approvalDigest(plan, index)
			if digestErr != nil || digest != cohort.ApprovalDigest {
				return fail(CodeInvariant, "cohort.approval_digest", "does not match frozen cohort")
			}
		} else if cohort.ApprovalDigest != "" {
			return fail(CodeInvariant, "cohort.approval_digest", "must be empty when approval is not required")
		}
		for _, clusterID := range cohort.ClusterIDs {
			if expectedOrder >= len(plan.Clusters) {
				return fail(CodeInvariant, "clusters", "cohort membership exceeds cluster rows")
			}
			cluster := plan.Clusters[expectedOrder]
			if cluster.ClusterID != clusterID || cluster.Cohort != index || cluster.Order != expectedOrder {
				return fail(CodeInvariant, "clusters", "rows do not match frozen cohort order")
			}
			if _, exists := seen[clusterID]; exists {
				return fail(CodeInvariant, "clusters", "contains duplicate membership")
			}
			seen[clusterID] = struct{}{}
			if cluster.Previous != nil {
				if previousErr := cluster.Previous.Validate(); previousErr != nil {
					return previousErr
				}
			}
			expectedOrder++
		}
	}
	if expectedOrder != len(plan.Clusters) {
		return fail(CodeInvariant, "clusters", "contains rows absent from cohorts")
	}
	initialDigest, err := approvalDigest(plan, -1)
	if err != nil || initialDigest != plan.Approval.Digest {
		return fail(CodeInvariant, "approval.digest", "does not match frozen rollout")
	}
	digest, err := frozenDigest(plan)
	if err != nil {
		return err
	}
	if digest != plan.PlanDigest {
		return fail(CodeInvariant, "plan_digest", fmt.Sprintf("does not match immutable plan: got %s", plan.PlanDigest))
	}
	return nil
}

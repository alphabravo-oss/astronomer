package provider

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// This callback type is unexported and constructed only at the owner's fixed
// transitions. No request/plugin/report can select a writer or supply grants.
type databaseScanTransition struct {
	owner    *DatabaseScanOwner
	claim    sqlc.VulnerabilityDatabaseScanClaim
	update   *distribution.AuthenticatedDatabaseUpdate
	workload resolver.WorkloadImageMetadata
	apply    func(context.Context, *sqlc.Queries, sqlc.VulnerabilityDatabaseScanClaim) error
}

func (transition *databaseScanTransition) revalidateOwnedSnapshot(ctx context.Context) error {
	if transition == nil || transition.owner == nil || transition.update == nil || transition.apply == nil {
		return errors.New("owned database transition unavailable")
	}
	if err := transition.owner.revalidateScanMaterial(ctx); err != nil {
		return err
	}
	if err := transition.owner.projection.revalidateOwnedSnapshot(ctx); err != nil {
		return err
	}
	return transition.update.ValidateCurrentMetadata(time.Now().UTC())
}
func (transition *databaseScanTransition) prepareOwnedSnapshot(ctx context.Context, queries *sqlc.Queries, cluster uuid.UUID) (ownedSnapshotRefresh, error) {
	if cluster != transition.claim.ClusterID {
		return nil, errors.New("owned database transition cluster differs")
	}
	refresh, err := transition.owner.projection.prepareOwnedSnapshot(ctx, queries, cluster)
	if err != nil {
		return nil, err
	}
	current, err := queries.GetVulnerabilityDatabaseScanClaimForUpdate(ctx, transition.claim.ID)
	if err != nil || !reflect.DeepEqual(current, transition.claim) {
		return nil, errors.New("owned database transition current claim changed")
	}
	state, err := transition.owner.lockCurrentDatabaseScanState(ctx, queries, cluster)
	if err != nil || !databaseScanClaimMatchesState(current, state) {
		return nil, errors.New("owned database transition current accepted state differs")
	}
	digest, _, valid := transition.update.Snapshot()
	if !valid || digest != current.FeedDigest {
		return nil, errors.New("owned database transition signed feed differs")
	}
	if _, err := queries.LockVulnerabilityDatabaseCurrentUpdate(ctx, sqlc.LockVulnerabilityDatabaseCurrentUpdateParams{UpdateID: state.UpdateID, BaseID: state.BaseID, BaseEpoch: state.BaseEpoch, UpdateGeneration: state.UpdateGeneration}); err != nil {
		return nil, err
	}
	return func(ctx context.Context, queries *sqlc.Queries, receipt sqlc.DeliveryAssignmentReceipt, response protocol.DeliveryStateResponseV2) error {
		if err := refresh(ctx, queries, receipt, response); err != nil {
			return err
		}
		if current.AssignmentGeneration != receipt.DesiredSnapshotGeneration || current.AssignmentDigest != receipt.DesiredContentDigest || current.AssignmentEtag != receipt.DesiredSnapshotEtag || current.AssignmentCredentialEpoch != receipt.CredentialEpoch || current.AssignmentCredentialDigest != receipt.CredentialContentDigest {
			return errors.New("owned database transition canonical receipt changed")
		}
		if err := transition.owner.recheckCurrentWorkload(ctx, current, transition.workload); err != nil {
			return err
		}
		if err := transition.revalidateOwnedSnapshot(ctx); err != nil {
			return err
		}
		if err := transition.apply(ctx, queries, current); err != nil {
			return err
		}
		return transition.revalidateOwnedSnapshot(ctx)
	}, nil
}
func (owner *DatabaseScanOwner) transitionClaim(ctx context.Context, claim sqlc.VulnerabilityDatabaseScanClaim, apply func(context.Context, *sqlc.Queries, sqlc.VulnerabilityDatabaseScanClaim) error) error {
	if err := owner.revalidateScanMaterial(ctx); err != nil {
		return err
	}
	update, err := owner.projection.authority.LoadUpdate(ctx, owner.projection.proof, owner.repository, claim.FeedDigest, time.Now().UTC())
	if err != nil {
		return err
	}
	metadata, err := owner.projection.authority.ReadWorkloadImageMetadata(ctx, owner.projection.proof, claim.WorkloadRepository, claim.WorkloadManifestDigest, claim.Architecture)
	if err != nil {
		return err
	}
	defer clear(metadata.Config)
	defer clear(metadata.Manifest)
	participant := &databaseScanTransition{owner: owner, claim: claim, update: update, workload: metadata, apply: apply}
	provider, err := newOwnedSnapshotCoordinator(owner.pool, owner.prototype, participant)
	if err != nil {
		return err
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err = provider.ownedSnapshots.snapshot(bounded, claim.ClusterID, protocol.DeliveryStateRequestV2{})
	return err
}
func requireDatabaseScanWrite(count int64, err error) error {
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("owned database transition lost current lease")
	}
	return nil
}

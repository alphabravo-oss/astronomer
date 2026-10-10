package provider

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DecisionDigest describes the exact signed document DATA the producer reserved
// before publication. It never replaces a signature, payload proof, current
// physical authority or the actual published-attempt foreign key.
func databaseUpdateDecisionDigest(document distribution.DatabaseUpdateDocument) (string, error) {
	digest, err := model.CanonicalDigest(struct {
		Domain   string                              `json:"domain"`
		Document distribution.DatabaseUpdateDocument `json:"document"`
	}{"astronomer-database-publication-decision-v1", document})
	return digest.String(), err
}

func (participant *databaseAcceptanceParticipant) acceptLockedUpdate(ctx context.Context, queries *sqlc.Queries, base sqlc.VulnerabilityDatabaseBasis) (sqlc.VulnerabilityDatabaseUpdate, error) {
	captured, snapshot := participant.captured, participant.projection.snapshot
	decision, err := databaseUpdateDecisionDigest(captured.document)
	if err != nil {
		return sqlc.VulnerabilityDatabaseUpdate{}, err
	}
	attempt, err := queries.GetVulnerabilityDatabaseUpdateAttemptForUpdate(ctx, participant.attempt)
	if err != nil || attempt.BaseID != base.ID || attempt.BaseEpoch != base.Epoch || attempt.Generation != captured.document.Generation || attempt.DecisionDigest != decision || !databaseCompleteAttemptPhase(attempt.Phase) || !attempt.PublishedFeedDigest.Valid || attempt.PublishedFeedDigest.String != captured.feed {
		return sqlc.VulnerabilityDatabaseUpdate{}, errors.New("database acceptance completed reservation differs")
	}
	if attempt.Phase == "imported" && attempt.Generation < base.NextGeneration {
		return sqlc.VulnerabilityDatabaseUpdate{}, errors.New("database imported decision superseded by newer reservation")
	}
	existing, err := queries.GetVulnerabilityDatabaseUpdateByFeedForUpdate(ctx, captured.feed)
	if err == nil {
		if existing.AttemptPhase != attempt.Phase || !databaseAcceptedUpdateMatches(existing, base, participant.attempt, decision, snapshot, captured) {
			return sqlc.VulnerabilityDatabaseUpdate{}, errors.New("database acceptance replay differs, is old or revoked")
		}
		assets, err := queries.GetVulnerabilityDatabaseUpdateAssets(ctx, existing.ID)
		if err != nil || !databaseAcceptanceAssetsMatch(existing.ID, assets, captured) {
			return sqlc.VulnerabilityDatabaseUpdate{}, errors.New("database acceptance immutable payload records differ")
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return sqlc.VulnerabilityDatabaseUpdate{}, err
	}
	update, err := queries.CreateVulnerabilityDatabaseUpdate(ctx, sqlc.CreateVulnerabilityDatabaseUpdateParams{
		ID: uuid.New(), AttemptID: participant.attempt, DecisionDigest: decision,
		BaseID: base.ID, BaseEpoch: base.Epoch, Generation: captured.document.Generation, FeedDigest: captured.feed,
		PolicyFingerprint: snapshot.Base.PolicyFingerprint, TrustMaterialFingerprint: snapshot.TrustMaterialFingerprint,
		DistributionIdentity:    snapshot.Base.DistributionIdentity,
		MirrorEndorsementDigest: databaseProjectionOptional(snapshot.Base.Binding.MirrorEndorsementDigest),
		MirrorTrustFingerprint:  databaseProjectionOptional(snapshot.MirrorTrustFingerprint), MirrorTrustOriginFingerprint: databaseProjectionOptional(snapshot.MirrorTrustOriginFingerprint),
		PublishedAt: captured.document.PublishedAt, ExpiresAt: captured.document.ExpiresAt,
	})
	if err != nil {
		return sqlc.VulnerabilityDatabaseUpdate{}, err
	}
	for _, asset := range []distribution.DatabaseUpdateSnapshot{captured.main, captured.java} {
		parameters, err := databaseAcceptanceAssetParameters(update.ID, asset)
		if err != nil {
			return sqlc.VulnerabilityDatabaseUpdate{}, err
		}
		count, err := queries.CreateVulnerabilityDatabaseUpdateAsset(ctx, parameters)
		if err != nil {
			return sqlc.VulnerabilityDatabaseUpdate{}, err
		}
		if count != 1 {
			return sqlc.VulnerabilityDatabaseUpdate{}, errors.New("database acceptance payload metadata is no longer current")
		}
	}
	return update, nil
}

func databaseAcceptedUpdateMatches(row sqlc.VulnerabilityDatabaseUpdate, base sqlc.VulnerabilityDatabaseBasis, attempt uuid.UUID, decision string, snapshot distribution.DatabaseAuthoritySnapshot, captured databaseAcceptanceCapture) bool {
	return !row.RevokedAt.Valid && row.BaseID == base.ID && row.BaseEpoch == base.Epoch && row.Generation == base.AcceptedGeneration && row.Generation == captured.document.Generation && row.AttemptID == attempt && databaseCompleteAttemptPhase(row.AttemptPhase) && row.DecisionDigest == decision && row.FeedDigest == captured.feed && row.PolicyFingerprint == snapshot.Base.PolicyFingerprint && row.TrustMaterialFingerprint == snapshot.TrustMaterialFingerprint && row.PublishedAt.Equal(captured.document.PublishedAt) && row.ExpiresAt.Equal(captured.document.ExpiresAt) && time.Now().UTC().Before(row.ExpiresAt)
}

func databaseAcceptanceAssetParameters(update uuid.UUID, snapshot distribution.DatabaseUpdateSnapshot) (sqlc.CreateVulnerabilityDatabaseUpdateAssetParams, error) {
	var schema int32
	switch snapshot.Role {
	case "trivy-database":
		if snapshot.Metadata.Schema != 2 {
			return sqlc.CreateVulnerabilityDatabaseUpdateAssetParams{}, errors.New("database acceptance main schema differs")
		}
		schema = 2
	case "trivy-java-database":
		if snapshot.Metadata.Schema != 1 {
			return sqlc.CreateVulnerabilityDatabaseUpdateAssetParams{}, errors.New("database acceptance Java schema differs")
		}
		schema = 1
	default:
		return sqlc.CreateVulnerabilityDatabaseUpdateAssetParams{}, errors.New("database acceptance role differs")
	}
	return sqlc.CreateVulnerabilityDatabaseUpdateAssetParams{
		UpdateID: update, Role: snapshot.Role, SupplierSubject: snapshot.SupplierSubject, PublishedSubject: snapshot.PublishedSubject,
		ManifestDigest: snapshot.Manifest.Digest, ManifestSize: snapshot.Manifest.Size, ManifestMedia: snapshot.Manifest.MediaType,
		ConfigDigest: snapshot.Config.Digest, ConfigSize: snapshot.Config.Size, ConfigMedia: snapshot.Config.MediaType,
		LayerDigest: snapshot.Layer.Digest, LayerSize: snapshot.Layer.Size, LayerMedia: snapshot.Layer.MediaType,
		MetadataDigest: snapshot.Metadata.Digest, MetadataSize: snapshot.Metadata.Size, MetadataSchema: schema,
		UpdatedAt: snapshot.Metadata.UpdatedAt, NextUpdate: snapshot.Metadata.NextUpdate,
	}, nil
}

func databaseAcceptanceAssetsMatch(update uuid.UUID, rows []sqlc.VulnerabilityDatabaseUpdateAsset, captured databaseAcceptanceCapture) bool {
	if len(rows) != 2 {
		return false
	}
	seen := map[string]bool{}
	for _, row := range rows {
		snapshot := captured.main
		if row.Role == "trivy-java-database" {
			snapshot = captured.java
		}
		if seen[row.Role] || row.Role != snapshot.Role {
			return false
		}
		seen[row.Role] = true
		parameters, err := databaseAcceptanceAssetParameters(update, snapshot)
		if err != nil {
			return false
		}
		expected := sqlc.VulnerabilityDatabaseUpdateAsset(parameters)
		if !row.UpdatedAt.Equal(expected.UpdatedAt) || !row.NextUpdate.Equal(expected.NextUpdate) {
			return false
		}
		row.UpdatedAt, row.NextUpdate = time.Time{}, time.Time{}
		expected.UpdatedAt, expected.NextUpdate = time.Time{}, time.Time{}
		if !reflect.DeepEqual(row, expected) {
			return false
		}
	}
	return seen["trivy-database"] && seen["trivy-java-database"]
}

func (participant *databaseAcceptanceParticipant) assignmentAcceptance(cluster uuid.UUID, base sqlc.VulnerabilityDatabaseBasis, update sqlc.VulnerabilityDatabaseUpdate, intent databaseScannerIntent, assignment protocol.DeliveryAssignmentV2) ownedSnapshotRefresh {
	return func(ctx context.Context, queries *sqlc.Queries, receipt sqlc.DeliveryAssignmentReceipt, response protocol.DeliveryStateResponseV2) error {
		if receipt.ClusterID != cluster || receipt.DesiredSnapshotGeneration != response.SnapshotGeneration || receipt.DesiredSnapshotEtag != response.ETag || receipt.CredentialEpoch != response.CredentialEpoch || response.NotModified || !databaseProjectionAssignmentMatches(response, assignment) {
			return errors.New("database acceptance actual canonical assignment differs")
		}
		state, err := queries.GetVulnerabilityDatabaseClusterStateForUpdate(ctx, cluster)
		if err == nil && state.UpdateID == update.ID {
			if !databaseAcceptanceStateMatches(state, base, update, intent) {
				return errors.New("database acceptance same generation cannot change desired scanner intent")
			}
			return participant.projection.assignmentRefresh(cluster, state, intent.expected, intent.generation, intent.deployment.ID, intent.deployment.DesiredGeneration, intent.effective, assignment)(ctx, queries, receipt, response)
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		_, err = queries.UpsertVulnerabilityDatabaseClusterState(ctx, databaseAcceptanceStateParameters(cluster, base, update, intent, receipt))
		if err != nil {
			return err
		}
		count, err := queries.CreateVulnerabilityDatabaseRescanIntent(ctx, sqlc.CreateVulnerabilityDatabaseRescanIntentParams{ID: uuid.New(), ClusterID: cluster, UpdateID: update.ID, TargetID: intent.expected.TargetID, TargetGeneration: intent.generation, Reason: "database-updated"})
		// A durable intent is an invalidation/rescan obligation, never evidence a
		// job ran or the scanner consumed these bytes. Its worker remains separate.
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("database acceptance rescan obligation differs")
		}
		return nil
	}
}

func databaseAcceptanceStateMatches(state sqlc.VulnerabilityDatabaseClusterState, base sqlc.VulnerabilityDatabaseBasis, update sqlc.VulnerabilityDatabaseUpdate, intent databaseScannerIntent) bool {
	expected, effective := intent.expected, intent.effective
	return state.BaseID == base.ID && state.BaseEpoch == base.Epoch && state.UpdateID == update.ID && state.UpdateGeneration == update.Generation && state.ProjectID == expected.ProjectID && state.SourceID == expected.SourceID && state.VersionID == expected.VersionID && state.TargetID == expected.TargetID && state.TargetGeneration == intent.generation && state.BundleSpecDigest == expected.Version.SpecDigest && state.DeploymentID == intent.deployment.ID && state.DeploymentGeneration == intent.deployment.DesiredGeneration && state.ConfigurationDigest == effective.ConfigurationDigest.String() && state.DesiredSpecDigest == effective.SpecDigest.String()
}

func databaseAcceptanceStateParameters(cluster uuid.UUID, base sqlc.VulnerabilityDatabaseBasis, update sqlc.VulnerabilityDatabaseUpdate, intent databaseScannerIntent, receipt sqlc.DeliveryAssignmentReceipt) sqlc.UpsertVulnerabilityDatabaseClusterStateParams {
	expected, effective := intent.expected, intent.effective
	return sqlc.UpsertVulnerabilityDatabaseClusterStateParams{
		ClusterID: cluster, BaseID: base.ID, BaseEpoch: base.Epoch, UpdateID: update.ID,
		ProjectID: expected.ProjectID, SourceID: expected.SourceID, VersionID: expected.VersionID,
		TargetID: expected.TargetID, TargetGeneration: intent.generation, BundleSpecDigest: expected.Version.SpecDigest,
		DeploymentID: intent.deployment.ID, DeploymentGeneration: intent.deployment.DesiredGeneration,
		ConfigurationDigest: effective.ConfigurationDigest.String(), DesiredSpecDigest: effective.SpecDigest.String(),
		AssignmentGeneration: receipt.DesiredSnapshotGeneration, AssignmentDigest: receipt.DesiredContentDigest, AssignmentEtag: receipt.DesiredSnapshotEtag,
		AssignmentCredentialEpoch: receipt.CredentialEpoch, AssignmentCredentialDigest: receipt.CredentialContentDigest,
	}
}

// Imported is an accurate separate local disposition; it never means the local
// operator signed a child/feed. The acceptance participant independently locks
// and compares the exact current attempt/feed/decision before this DATA check.
func databaseCompleteAttemptPhase(phase string) bool {
	return phase == "published" || phase == "imported"
}

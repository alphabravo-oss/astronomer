package provider

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/builtin"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/rollout"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// NewWithDatabaseAuthority is optional constructor composition. The private
// participant obtains its proof from the actual physical owner; callers cannot
// supply a catalog, repository callback, ETag, snapshot or authority boolean.
// It does not initialize/promote a durable base or accept a database update.
// An independent owned initialization must establish the current durable base
// before this provider is used. Missing base authority fails closed.
func NewWithDatabaseAuthority(ctx context.Context, pool *pgxpool.Pool, original *Provider, authority *distribution.DatabaseAuthority) (*Provider, error) {
	if pool == nil || original == nil || original.queries == nil || original.ownedSnapshots != nil {
		return nil, errors.New("database snapshot provider constructor unavailable")
	}
	projection, err := newDatabaseSnapshotProjection(ctx, authority)
	if err != nil {
		return nil, err
	}
	if roots := projection.snapshot.RegistryRootsFingerprint; roots != "" && (original.builtinCAIdentity != roots || original.authorizeBuiltinCA == nil) {
		return nil, errors.New("database snapshot provider TLS trust differs from captured registry authority")
	}
	return newOwnedSnapshotCoordinator(pool, original, projection)
}

type databaseSnapshotProjection struct {
	authority          *distribution.DatabaseAuthority
	proof              *distribution.VerifiedDatabaseAuthorityBase
	snapshot           distribution.DatabaseAuthoritySnapshot
	bindingFingerprint string
}

func newDatabaseSnapshotProjection(ctx context.Context, authority *distribution.DatabaseAuthority) (*databaseSnapshotProjection, error) {
	if ctx == nil || authority == nil {
		return nil, errors.New("database snapshot physical constructor unavailable")
	}
	proof, err := authority.Base(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, valid := proof.Snapshot()
	if !valid {
		return nil, errors.New("database snapshot physical base unavailable")
	}
	if _, err := authority.Catalog(ctx, proof); err != nil {
		return nil, err
	}
	return &databaseSnapshotProjection{authority: authority, proof: proof, snapshot: snapshot, bindingFingerprint: databaseSnapshotBindingFingerprint(snapshot)}, nil
}

func (projection *databaseSnapshotProjection) revalidateOwnedSnapshot(ctx context.Context) error {
	if projection == nil || projection.authority == nil || projection.proof == nil {
		return errors.New("database snapshot physical owner unavailable")
	}
	return projection.authority.Revalidate(ctx, projection.proof)
}

func (projection *databaseSnapshotProjection) prepareOwnedSnapshot(ctx context.Context, queries *sqlc.Queries, cluster uuid.UUID) (ownedSnapshotRefresh, error) {
	catalog, err := projection.authority.Catalog(ctx, projection.proof)
	if err != nil {
		return nil, err
	}
	if err := queries.LockVulnerabilityDatabaseAuthority(ctx); err != nil {
		return nil, err
	}
	base, err := queries.GetCurrentVulnerabilityDatabaseBaseForUpdate(ctx)
	if err != nil || !databaseProjectionBaseMatches(base, projection.snapshot, projection.bindingFingerprint) {
		return nil, errors.New("database snapshot durable base differs or is revoked")
	}
	state, err := queries.GetVulnerabilityDatabaseClusterStateForUpdate(ctx, cluster)
	if errors.Is(err, pgx.ErrNoRows) {
		// Only the cluster projection may be absent. The independently initialized
		// current durable base was mandatory above; no base bootstrap is inferred.
		// No rows are minted, refreshed, or reported as clean by this branch.
		return noDatabaseProjectionRefresh, nil
	}
	if err != nil {
		return nil, err
	}
	if !projection.snapshot.Base.ScanningEnabled {
		return noDatabaseProjectionRefresh, nil
	}
	intent, err := lockDatabaseScannerIntent(ctx, queries, cluster, catalog)
	if err != nil {
		return nil, err
	}
	expected, effective, generation, deployment := intent.expected, intent.effective, intent.generation, intent.deployment
	if state.BaseID != base.ID || state.BaseEpoch != base.Epoch || state.ProjectID != expected.ProjectID || state.SourceID != expected.SourceID || state.VersionID != expected.VersionID || state.TargetID != expected.TargetID || state.TargetGeneration != generation || state.BundleSpecDigest != expected.Version.SpecDigest || state.DeploymentID != deployment.ID || state.DeploymentGeneration != deployment.DesiredGeneration || state.ConfigurationDigest != effective.ConfigurationDigest.String() || state.DesiredSpecDigest != effective.SpecDigest.String() {
		// Relevant intent changed: leave historical state intact and unrenewed.
		// A separate owned acceptance/rescan transition must replace it. No witness.
		return noDatabaseProjectionRefresh, nil
	}
	_, err = queries.LockVulnerabilityDatabaseCurrentUpdate(ctx, sqlc.LockVulnerabilityDatabaseCurrentUpdateParams{UpdateID: state.UpdateID, BaseID: base.ID, BaseEpoch: base.Epoch, UpdateGeneration: state.UpdateGeneration})
	if errors.Is(err, pgx.ErrNoRows) {
		return noDatabaseProjectionRefresh, nil
	}
	if err != nil {
		return nil, err
	}
	assignment, err := databaseProjectionExpectedAssignment(expected, deployment.ID, deployment.DesiredGeneration, effective)
	if err != nil {
		return nil, err
	}
	if projection.snapshot.RegistryRootsFingerprint != "" {
		// The constructor binds the provider's actual CA bytes to this physical
		// root identity. Its canonical authorizer must also approve this source.
		assignment.Source.CredentialSecret = protocol.ObjectNames(expected.ProjectID.String(), deployment.ID.String()).AuthSecret
	}
	return projection.assignmentRefresh(cluster, state, expected, generation, deployment.ID, deployment.DesiredGeneration, effective, assignment), nil
}

// This decision is created only after actual locked current-row validation in
// the owned transaction. It renews the same feed from the actual finalizer row;
// it cannot accept an older feed, advance DB generation, or enqueue a scan.
func (projection *databaseSnapshotProjection) assignmentRefresh(cluster uuid.UUID, state sqlc.VulnerabilityDatabaseClusterState, expected builtin.CatalogComponentExpectation, targetGeneration int64, deploymentID uuid.UUID, deploymentGeneration int64, effective rollout.UnconfiguredDeploymentIntent, assignment protocol.DeliveryAssignmentV2) ownedSnapshotRefresh {
	return func(ctx context.Context, queries *sqlc.Queries, receipt sqlc.DeliveryAssignmentReceipt, response protocol.DeliveryStateResponseV2) error {
		if receipt.ClusterID != cluster || receipt.DesiredSnapshotGeneration != response.SnapshotGeneration || receipt.DesiredSnapshotEtag != response.ETag || receipt.CredentialEpoch != response.CredentialEpoch {
			return errors.New("database projection finalizer identity differs")
		}
		if !response.NotModified && !databaseProjectionAssignmentMatches(response, assignment) {
			return errors.New("database projection actual assignment differs")
		}
		snapshot := projection.snapshot
		_, err := queries.RefreshVulnerabilityDatabaseAssignmentProjection(ctx, sqlc.RefreshVulnerabilityDatabaseAssignmentProjectionParams{
			ClusterID: cluster, ExpectedUpdateID: state.UpdateID, ExpectedBindingFingerprint: projection.bindingFingerprint,
			ExpectedPolicyFingerprint: snapshot.Base.PolicyFingerprint, ExpectedTrustMaterialFingerprint: snapshot.TrustMaterialFingerprint,
			ExpectedDistributionIdentity:    snapshot.Base.DistributionIdentity,
			ExpectedMirrorEndorsementDigest: databaseProjectionOptional(snapshot.Base.Binding.MirrorEndorsementDigest),
			ExpectedMirrorTrustFingerprint:  databaseProjectionOptional(snapshot.MirrorTrustFingerprint), ExpectedMirrorTrustOriginFingerprint: databaseProjectionOptional(snapshot.MirrorTrustOriginFingerprint),
			ExpectedSourceID: expected.SourceID, ExpectedVersionID: expected.VersionID, ExpectedTargetID: expected.TargetID, ExpectedTargetGeneration: targetGeneration,
			ExpectedBundleSpecDigest: expected.Version.SpecDigest, ExpectedDeploymentID: deploymentID, ExpectedDeploymentGeneration: deploymentGeneration,
			ExpectedConfigurationDigest: effective.ConfigurationDigest.String(), ExpectedSpecDigest: effective.SpecDigest.String(),
			FinalizedAssignmentGeneration: receipt.DesiredSnapshotGeneration, FinalizedAssignmentDigest: receipt.DesiredContentDigest, FinalizedAssignmentEtag: receipt.DesiredSnapshotEtag,
			FinalizedCredentialEpoch: receipt.CredentialEpoch, FinalizedCredentialDigest: receipt.CredentialContentDigest,
		})
		return err
	}
}

func noDatabaseProjectionRefresh(context.Context, *sqlc.Queries, sqlc.DeliveryAssignmentReceipt, protocol.DeliveryStateResponseV2) error {
	return nil
}

func databaseProjectionOptional(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

// The expected wire assignment is DATA from the SAME ordinary provider compiler,
// with the already locked no-override prerequisites and effective rollout data.
func databaseProjectionExpectedAssignment(expected builtin.CatalogComponentExpectation, deployment uuid.UUID, generation int64, effective rollout.UnconfiguredDeploymentIntent) (protocol.DeliveryAssignmentV2, error) {
	trust, err := json.Marshal(expected.Source.Trust)
	if err != nil {
		return protocol.DeliveryAssignmentV2{}, err
	}
	return compileAssignmentMetadata(sqlc.ListClusterDeliveryAssignmentsRow{
		ID: deployment, TargetID: expected.TargetID, ProjectID: expected.ProjectID,
		DesiredGeneration: generation, DesiredSpecDigest: effective.SpecDigest.String(),
		DesiredConfigurationDigest: pgtype.Text{String: effective.ConfigurationDigest.String(), Valid: true},
		Action:                     string(protocol.DeliveryActionApply), Scope: string(model.ScopePlatform),
		SourceType: string(expected.Source.Type), Url: expected.Source.URL, AuthMode: string(model.AuthNone),
		ResolvedRevision: expected.Version.RequestedRevision, ArtifactDigest: expected.Version.ArtifactDigest,
		Renderer: string(model.RendererHelm), RendererSpec: effective.Renderer, DesiredOverrides: json.RawMessage(`{}`),
		ReconciliationPolicy: expected.Version.ReconciliationPolicy, TrustPolicy: trust,
	})
}

func databaseProjectionAssignmentMatches(response protocol.DeliveryStateResponseV2, expected protocol.DeliveryAssignmentV2) bool {
	count := 0
	for _, assignment := range response.Assignments {
		if assignment.TargetID != expected.TargetID {
			continue
		}
		count++
		// Credential payload is owned by canonical hydration, never DB witness.
		// All metadata, including exact source chart/kind, platform scope, verify,
		// CA/trust secret references and complete Helm projection must match.
		assignment.Credential = nil
		if !reflect.DeepEqual(assignment, expected) {
			return false
		}
	}
	return count == 1
}

// Snapshot fields came from the same opaque physical constructor. Length
// framing binds the relational base without a second JSON trust reader.
func databaseSnapshotBindingFingerprint(snapshot distribution.DatabaseAuthoritySnapshot) string {
	hash := sha256.New()
	b := snapshot.Base
	binding := b.Binding
	for _, field := range []string{"astronomer-database-durable-base-v2", binding.BaseRelease, binding.BaseDistributionDigest, b.DistributionIdentity, binding.CatalogDigest, binding.Profile, binding.CandidateSourceCommit, binding.Scanner.ImageDigest, binding.Scanner.Version, strconv.Itoa(binding.Scanner.MainSchema), strconv.Itoa(binding.Scanner.JavaSchema), b.PolicyFingerprint, snapshot.TrustMaterialFingerprint, snapshot.VendorTrustFingerprint, snapshot.VendorTrustOriginFingerprint, binding.MirrorEndorsementDigest, snapshot.MirrorPolicyFingerprint, snapshot.MirrorTrustFingerprint, snapshot.MirrorTrustOriginFingerprint, snapshot.RegistryRootsFingerprint, snapshot.RegistryPolicyFingerprint, snapshot.RegistryRootOriginFingerprint, b.ChartSourceURL, b.Chart, b.ChartVersion, b.ChartDigest, strconv.FormatBool(b.ScanningEnabled)} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(field))
	}
	return fmt.Sprintf("sha256:%x", hash.Sum(nil))
}

// Compare actual immutable columns as well as the stored binding hash. A hash
// field alone cannot stand in for current source/scanner/base row validation.
func databaseProjectionBaseMatches(base sqlc.VulnerabilityDatabaseBasis, snapshot distribution.DatabaseAuthoritySnapshot, fingerprint string) bool {
	b := snapshot.Base
	binding := b.Binding
	return base.Epoch > 0 && !base.RevokedAt.Valid && base.BindingFingerprint == fingerprint && base.BaseRelease == binding.BaseRelease && base.DistributionDigest == binding.BaseDistributionDigest && base.DistributionIdentity == b.DistributionIdentity && base.CatalogDigest == binding.CatalogDigest && base.Profile == binding.Profile && base.CandidateSourceCommit == binding.CandidateSourceCommit && base.ScannerDigest == binding.Scanner.ImageDigest && base.ScannerVersion == binding.Scanner.Version && int(base.MainSchema) == binding.Scanner.MainSchema && int(base.JavaSchema) == binding.Scanner.JavaSchema && base.PolicyFingerprint == b.PolicyFingerprint && base.TrustMaterialFingerprint == snapshot.TrustMaterialFingerprint && databaseProjectionRetainedTrustMatches(base, snapshot) && base.MirrorEndorsementDigest == databaseProjectionOptional(binding.MirrorEndorsementDigest) && base.MirrorTrustFingerprint == databaseProjectionOptional(snapshot.MirrorTrustFingerprint) && base.MirrorTrustOriginFingerprint == databaseProjectionOptional(snapshot.MirrorTrustOriginFingerprint) && base.ChartSourceUrl == b.ChartSourceURL && base.ChartName == b.Chart && base.ChartVersion == b.ChartVersion && base.ChartDigest == b.ChartDigest
}

// Scalar retained public fingerprints are historical comparison data, not
// signing material or a grant. Missing/version-mismatched retention fails closed.
func databaseProjectionRetainedTrustMatches(base sqlc.VulnerabilityDatabaseBasis, snapshot distribution.DatabaseAuthoritySnapshot) bool {
	return base.PhysicalSnapshotVersion == 1 && base.VendorTrustFingerprint == snapshot.VendorTrustFingerprint && base.VendorTrustOriginFingerprint == snapshot.VendorTrustOriginFingerprint && base.RegistryRootsFingerprint == databaseProjectionOptional(snapshot.RegistryRootsFingerprint) && base.RegistryPolicyFingerprint == databaseProjectionOptional(snapshot.RegistryPolicyFingerprint) && base.RegistryRootOriginFingerprint == databaseProjectionOptional(snapshot.RegistryRootOriginFingerprint) && base.MirrorPolicyFingerprint == databaseProjectionOptional(snapshot.MirrorPolicyFingerprint) && base.ScanningEnabled == snapshot.Base.ScanningEnabled
}

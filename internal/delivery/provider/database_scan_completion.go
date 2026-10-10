package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/scanner"
	"github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Constructor-owned internal worker. There is no HTTP report/witness writer,
// report-supplied UUID authority, authorization boolean or verifier callback.
// A selected worker identifier can only cause reads of its actual owned claim,
// Kubernetes objects and locked current canonical assignment.
type DatabaseScanOwner struct {
	pool                   *pgxpool.Pool
	prototype              *Provider
	projection             *databaseSnapshotProjection
	requester              databaseScanRequester
	repository             string
	preparationEnvironment []corev1.EnvVar
	material               *distribution.DatabaseJobMaterial
	kubeletPull            *resolver.DatabaseJobPullCredential
	scanCursors            databaseScanCursors
}

func NewDatabaseScanOwner(ctx context.Context, pool *pgxpool.Pool, original *Provider, authority *distribution.DatabaseAuthority, requester databaseScanRequester, repository string, environment []corev1.EnvVar, material *distribution.DatabaseJobMaterial, kubeletPull *resolver.DatabaseJobPullCredential) (*DatabaseScanOwner, error) {
	if ctx == nil || pool == nil || material == nil || original == nil || original.queries == nil || original.ownedSnapshots != nil || requester == nil || repository == "" {
		return nil, errors.New("owned database scan constructor unavailable")
	}
	projection, err := newDatabaseSnapshotProjection(ctx, authority)
	if err != nil {
		return nil, err
	}
	if !projection.snapshot.Base.ScanningEnabled || projection.snapshot.RegistryRootsFingerprint == "" || original.builtinCAIdentity != projection.snapshot.RegistryRootsFingerprint || original.authorizeBuiltinCA == nil {
		return nil, errors.New("owned database scan provider or pinned TLS differs")
	}
	// Legacy profiles may keep ordinary scans with UNKNOWN provenance. Enabling
	// this measured owner requires its actual additive authenticated helper role.
	if _, err := authority.DatabaseScanImages(ctx, projection.proof); err != nil {
		return nil, err
	}
	if err := material.ValidateAuthority(ctx, authority); err != nil {
		return nil, err
	}
	copy := *original
	copy.builtinCA = append([]byte(nil), original.builtinCA...)
	copy.builtinSigningKey = append([]byte(nil), original.builtinSigningKey...)
	return &DatabaseScanOwner{pool: pool, prototype: &copy, projection: projection, requester: requester, repository: repository, preparationEnvironment: append([]corev1.EnvVar(nil), environment...), material: material, kubeletPull: kubeletPull}, nil
}

// completeOwnedScan receives only an identifier from the owner's durable query.
// It retrieves the report itself over the existing authenticated cluster tunnel.
// All remote observations happen before the current Serializable transaction;
// the current workload and node are checked again inside it before writing.
func (owner *DatabaseScanOwner) completeOwnedScan(ctx context.Context, cluster, identifier uuid.UUID) error {
	if ctx == nil || owner == nil || owner.pool == nil || owner.requester == nil || cluster == uuid.Nil || identifier == uuid.Nil {
		return errors.New("owned database scan completion owner unavailable")
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	claim, err := sqlc.New(owner.pool).GetVulnerabilityDatabaseScanClaim(bounded, identifier)
	if err != nil || claim.ClusterID != cluster || claim.Phase != "scanning" || !claim.ScanJobUid.Valid || !claim.ScanPodUid.Valid || !claim.LeaseToken.Valid || uuid.UUID(claim.ScanJobUid.Bytes) == uuid.Nil || uuid.UUID(claim.ScanPodUid.Bytes) == uuid.Nil || uuid.UUID(claim.LeaseToken.Bytes) == uuid.Nil {
		return errors.New("owned database scan current claim unavailable")
	}
	if err := owner.projection.revalidateOwnedSnapshot(bounded); err != nil {
		return err
	}
	update, err := owner.projection.authority.LoadUpdate(bounded, owner.projection.proof, owner.repository, claim.FeedDigest, time.Now().UTC())
	if err != nil {
		return err
	}
	feed, document, valid := update.Snapshot()
	if !valid || feed != claim.FeedDigest || document.Binding.Scanner.ImageDigest != owner.projection.snapshot.Base.Binding.Scanner.ImageDigest {
		return errors.New("owned database scan current signed feed differs")
	}
	var prepared databasejob.Preparation
	if len(claim.PreparedRecord) == 0 || len(claim.PreparedRecord) > databasejob.MaximumRecordBytes || jsoncontract.Decode(claim.PreparedRecord, &prepared) != nil {
		return errors.New("owned database scan prepared record unavailable")
	}
	// The canonical decoder enforces complete comparison shape. These fields do
	// not make a witness: actual prep/runtime ownership was recorded separately.
	decoded, err := databasejob.DecodePreparation(claim.PreparedRecord)
	if err != nil || !reflect.DeepEqual(decoded, prepared) || prepared.ClaimID != claim.ID || prepared.Cache.FeedDigest != feed || prepared.ScannerDigest != document.Binding.Scanner.ImageDigest || prepared.Workload == nil || prepared.Workload.ManifestDigest != claim.WorkloadManifestDigest || prepared.Workload.Architecture != claim.Architecture {
		return errors.New("owned database scan prepared identities differ")
	}
	for _, snapshot := range document.Databases {
		metadata := prepared.MainMetadata
		if snapshot.Role == "trivy-java-database" {
			metadata = prepared.JavaMetadata
		}
		if metadata != snapshot.Metadata {
			return errors.New("owned database scan prepared metadata differs from signed current feed")
		}
	}
	jobs, err := owner.buildClaimJobs(bounded, claim)
	if err != nil {
		return err
	}
	scoped, err := owner.claimReadRequester(claim, jobs.Scan)
	if err != nil {
		return err
	}
	observation, err := observeDatabaseScan(bounded, scoped, databaseScanObservationRequest{cluster: cluster, job: jobs.Scan, jobUID: types.UID(uuid.UUID(claim.ScanJobUid.Bytes).String()), podUID: types.UID(uuid.UUID(claim.ScanPodUid.Bytes).String()), podName: claim.ScanPodName, prepared: prepared})
	if err != nil {
		return err
	}
	defer observation.close()
	runtime, err := owner.captureRuntimeImageIdentities(bounded, claim, observation)
	if err != nil {
		return err
	}
	defer runtime.close()
	report, payloadDigest, err := normalizeDatabaseNativeReport(bounded, observation.report, databaseNativeReportTarget{podUID: claim.WorkloadPodUid, namespace: claim.WorkloadNamespace, pod: claim.WorkloadPodName, container: claim.WorkloadContainerName, repository: claim.WorkloadRepository, manifestDigest: claim.WorkloadManifestDigest, configDigest: runtime.workload.ConfigDescriptor.Digest.String(), scannerVersion: document.Binding.Scanner.Version}, observation)
	if err != nil {
		return err
	}
	participant := &databaseScanCompletionParticipant{owner: owner, claim: claim, prepared: prepared, observation: observation, report: report, payloadDigest: payloadDigest, update: update, runtime: runtime}
	provider, err := newOwnedSnapshotCoordinator(owner.pool, owner.prototype, participant)
	if err != nil {
		return err
	}
	transaction, stop := context.WithTimeout(bounded, 30*time.Second)
	defer stop()
	_, err = provider.ownedSnapshots.snapshot(transaction, cluster, protocol.DeliveryStateRequestV2{})
	return err
}

type databaseScanRuntimeImages struct {
	helper, scanner, workload resolver.WorkloadImageMetadata
}

func (databaseScanRuntimeImages) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "<private owned database runtime image metadata>")
}
func (images *databaseScanRuntimeImages) close() {
	if images != nil {
		for _, metadata := range []*resolver.WorkloadImageMetadata{&images.helper, &images.scanner, &images.workload} {
			clear(metadata.Config)
			clear(metadata.Manifest)
			metadata.Config = nil
			metadata.Manifest = nil
		}
	}
}

func (owner *DatabaseScanOwner) captureRuntimeImageIdentities(ctx context.Context, claim sqlc.VulnerabilityDatabaseScanClaim, observation databaseScanObservation) (databaseScanRuntimeImages, error) {
	var result databaseScanRuntimeImages
	retained := false
	defer func() {
		if !retained {
			result.close()
		}
	}()
	images, err := owner.projection.authority.DatabaseScanImages(ctx, owner.projection.proof)
	if err != nil {
		return result, err
	}
	for index, image := range []distribution.DatabaseScanImage{images.Helper, images.Scanner} {
		repository, indexDigest, found := strings.Cut(image.Reference, "@")
		if !found {
			return result, errors.New("owned database runtime image reference invalid")
		}
		child := ""
		for _, platform := range image.Platforms {
			if platform.Architecture == claim.Architecture {
				child = platform.Digest
			}
		}
		expectedChild, expectedIndex, actualID := claim.HelperChildDigest, claim.HelperIndexDigest, observation.helperImageID
		if index == 1 {
			expectedChild, expectedIndex, actualID = claim.ScannerChildDigest, claim.ScannerIndexDigest, observation.scannerImageID
		}
		if indexDigest != expectedIndex || child == "" || child != expectedChild {
			return result, errors.New("owned database selected runtime platform changed")
		}
		metadata, err := owner.projection.authority.ReadWorkloadImageMetadata(ctx, owner.projection.proof, repository, child, claim.Architecture)
		if err != nil {
			return result, err
		}
		if !databaseRuntimeImageIDMatches(actualID, repository, metadata) {
			clear(metadata.Config)
			clear(metadata.Manifest)
			return result, errors.New("owned database actual runtime imageID differs")
		}
		if index == 0 {
			for _, actual := range observation.helperImageIDs {
				if !databaseRuntimeImageIDMatches(actual, repository, metadata) {
					clear(metadata.Config)
					clear(metadata.Manifest)
					return result, errors.New("owned database initializer actual runtime imageID differs")
				}
			}
			result.helper = metadata
		} else {
			result.scanner = metadata
		}
	}
	result.workload, err = owner.projection.authority.ReadWorkloadImageMetadata(ctx, owner.projection.proof, claim.WorkloadRepository, claim.WorkloadManifestDigest, claim.Architecture)
	if err != nil {
		return result, err
	}
	if !databaseRuntimeImageIDMatches(claim.WorkloadImageID, claim.WorkloadRepository, result.workload) {
		return result, errors.New("owned database current workload runtime imageID differs")
	}
	retained = true
	return result, nil
}

// A CRI value is compared only after the exact selected manifest/config were
// authenticated by hash through the same guarded authority. Unknown CRI forms
// cannot be guessed from configured locators and remain unavailable.
func databaseRuntimeImageIDMatches(actual, repository string, metadata resolver.WorkloadImageMetadata) bool {
	config, child := metadata.ConfigDescriptor.Digest.String(), metadata.ManifestDescriptor.Digest.String()
	return actual == repository+"@"+child || actual == "docker-pullable://"+repository+"@"+child || actual == config || actual == "containerd://"+config || actual == "docker://"+config
}

type databaseScanCompletionParticipant struct {
	owner         *DatabaseScanOwner
	claim         sqlc.VulnerabilityDatabaseScanClaim
	prepared      databasejob.Preparation
	observation   databaseScanObservation
	report        scanner.TrivyVulnerabilityReport
	payloadDigest string
	update        *distribution.AuthenticatedDatabaseUpdate
	runtime       databaseScanRuntimeImages
}

func (participant *databaseScanCompletionParticipant) revalidateOwnedSnapshot(ctx context.Context) error {
	if participant == nil || participant.owner == nil || participant.update == nil {
		return errors.New("owned database report participant unavailable")
	}
	if err := participant.owner.revalidateScanMaterial(ctx); err != nil {
		return err
	}
	if err := participant.owner.projection.revalidateOwnedSnapshot(ctx); err != nil {
		return err
	}
	return participant.update.ValidateCurrentMetadata(time.Now().UTC())
}

func (participant *databaseScanCompletionParticipant) prepareOwnedSnapshot(ctx context.Context, queries *sqlc.Queries, cluster uuid.UUID) (ownedSnapshotRefresh, error) {
	if queries == nil || participant.claim.ClusterID != cluster {
		return nil, errors.New("owned database report cluster differs")
	}
	projection := participant.owner.projection
	// SAME canonical locks/compiler/refresh: no reconstructed source/renderer or
	// fabricated assignment ETag and no caller-selected transaction authority.
	refresh, err := projection.prepareOwnedSnapshot(ctx, queries, cluster)
	if err != nil {
		return nil, err
	}
	current, err := queries.GetVulnerabilityDatabaseScanClaimForUpdate(ctx, participant.claim.ID)
	if err != nil || !reflect.DeepEqual(current, participant.claim) || current.Phase != "scanning" || !current.LeaseExpiresAt.Valid || !time.Now().UTC().Before(current.LeaseExpiresAt.Time) {
		return nil, errors.New("owned database report claim changed or expired")
	}
	state, err := participant.owner.lockCurrentDatabaseScanState(ctx, queries, cluster)
	if err != nil || !databaseScanClaimMatchesState(current, state) {
		return nil, errors.New("owned database report accepted intent differs")
	}
	if _, err := queries.LockVulnerabilityDatabaseCurrentUpdate(ctx, sqlc.LockVulnerabilityDatabaseCurrentUpdateParams{UpdateID: state.UpdateID, BaseID: state.BaseID, BaseEpoch: state.BaseEpoch, UpdateGeneration: state.UpdateGeneration}); err != nil {
		return nil, err
	}
	return func(ctx context.Context, queries *sqlc.Queries, receipt sqlc.DeliveryAssignmentReceipt, response protocol.DeliveryStateResponseV2) error {
		if err := refresh(ctx, queries, receipt, response); err != nil {
			return err
		}
		if current.AssignmentGeneration != receipt.DesiredSnapshotGeneration || current.AssignmentDigest != receipt.DesiredContentDigest || current.AssignmentEtag != receipt.DesiredSnapshotEtag || current.AssignmentCredentialEpoch != receipt.CredentialEpoch || current.AssignmentCredentialDigest != receipt.CredentialContentDigest {
			return errors.New("owned database report actual canonical receipt changed")
		}
		if err := participant.owner.recheckCurrentWorkload(ctx, current, participant.runtime.workload); err != nil {
			return err
		}
		if err := participant.owner.recheckOwnedScanObjects(ctx, current); err != nil {
			return err
		}
		if err := participant.recheckFinalRuntime(ctx, current); err != nil {
			return err
		}
		if err := participant.revalidateOwnedSnapshot(ctx); err != nil {
			return err
		}
		// Concrete q.WithTx, no inner transaction, bus or precommit metric/audit.
		// Any writer/witness/CAS failure rolls back report, observations and CVEs.
		if err := scanner.NewIngester(queries, nil, nil, nil).Ingest(ctx, cluster, participant.report); err != nil {
			return err
		}
		report, err := queries.GetOwnedImageVulnerabilityReportForUpdate(ctx, sqlc.GetOwnedImageVulnerabilityReportForUpdateParams{ClusterID: cluster, ReportName: participant.report.Metadata.Name})
		if err != nil {
			return err
		}
		observed, err := queries.GetImageVulnerabilityObservation(ctx, report.ID)
		if err != nil || observed.Revision <= 0 || observed.TimeState != "valid" || !observed.UpstreamTime.Valid || !observed.UpstreamTime.Time.Equal(participant.observation.scannerFinished) || observed.UpstreamReportUid != "" {
			return errors.New("owned database report observation revision differs")
		}
		witness := sqlc.UpsertImageVulnerabilityDatabaseWitnessParams{ReportID: report.ID, ClaimID: current.ID, ClusterID: cluster, UpdateID: current.UpdateID, ScanJobUid: uuid.UUID(current.ScanJobUid.Bytes), ScanPodUid: uuid.UUID(current.ScanPodUid.Bytes), ScannerImageID: participant.observation.scannerImageID, HelperImageID: participant.observation.helperImageID, ScannerStartedAt: participant.observation.scannerStarted, ScannerFinishedAt: participant.observation.scannerFinished, InitialLogAt: participant.observation.measurements.InitialLogTimestamp, FinalLogAt: participant.observation.measurements.FinalLogTimestamp, MainDatabaseDigest: participant.prepared.Cache.Main.Digest, MainDatabaseSize: participant.prepared.Cache.Main.Size, JavaDatabaseDigest: participant.prepared.Cache.Java.Digest, JavaDatabaseSize: participant.prepared.Cache.Java.Size, WorkloadManifestDigest: current.WorkloadManifestDigest, WorkloadLayoutDigest: participant.prepared.Workload.IndexDigest, ReportPayloadDigest: participant.payloadDigest, ReportObservedAt: observed.ReceivedAt, ReportObservationRevision: observed.Revision, JavaApplicability: "unknown"}
		if _, err := queries.UpsertImageVulnerabilityDatabaseWitness(ctx, witness); err != nil {
			return err
		}
		count, err := queries.CompleteVulnerabilityDatabaseScanClaim(ctx, sqlc.CompleteVulnerabilityDatabaseScanClaimParams{ID: current.ID, LeaseToken: uuid.UUID(current.LeaseToken.Bytes), ReportID: report.ID})
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("owned database report claim completion lost lease")
		}
		// A final current workload/physical check runs after ALL writes, before the
		// coordinator's own physical revalidation and actual transaction commit.
		if err := participant.owner.recheckCurrentWorkload(ctx, current, participant.runtime.workload); err != nil {
			return err
		}
		if err := participant.owner.recheckOwnedScanObjects(ctx, current); err != nil {
			return err
		}
		if err := participant.recheckFinalRuntime(ctx, current); err != nil {
			return err
		}
		return participant.revalidateOwnedSnapshot(ctx)
	}, nil
}

func databaseScanClaimMatchesState(claim sqlc.VulnerabilityDatabaseScanClaim, state sqlc.VulnerabilityDatabaseClusterState) bool {
	return claim.ClusterID == state.ClusterID && claim.BaseID == state.BaseID && claim.BaseEpoch == state.BaseEpoch && claim.UpdateID == state.UpdateID && claim.UpdateGeneration == state.UpdateGeneration && claim.ProjectID == state.ProjectID && claim.SourceID == state.SourceID && claim.VersionID == state.VersionID && claim.TargetID == state.TargetID && claim.TargetGeneration == state.TargetGeneration && claim.BundleSpecDigest == state.BundleSpecDigest && claim.DeploymentID == state.DeploymentID && claim.DeploymentGeneration == state.DeploymentGeneration && claim.ConfigurationDigest == state.ConfigurationDigest && claim.DesiredSpecDigest == state.DesiredSpecDigest && claim.AssignmentGeneration == state.AssignmentGeneration && claim.AssignmentDigest == state.AssignmentDigest && claim.AssignmentEtag == state.AssignmentEtag && claim.AssignmentCredentialEpoch == state.AssignmentCredentialEpoch && claim.AssignmentCredentialDigest == state.AssignmentCredentialDigest
}

func (participant *databaseScanCompletionParticipant) recheckFinalRuntime(ctx context.Context, claim sqlc.VulnerabilityDatabaseScanClaim) error {
	jobs, err := participant.owner.buildClaimJobs(ctx, claim)
	if err != nil {
		return err
	}
	scoped, err := participant.owner.claimReadRequester(claim, jobs.Scan)
	if err != nil {
		return err
	}
	job, pod, err := readDatabaseScanObjects(ctx, scoped, databaseScanObservationRequest{cluster: claim.ClusterID, job: jobs.Scan, jobUID: types.UID(uuid.UUID(claim.ScanJobUid.Bytes).String()), podUID: types.UID(uuid.UUID(claim.ScanPodUid.Bytes).String()), podName: claim.ScanPodName, prepared: participant.prepared})
	if err != nil {
		return err
	}
	actual, err := databaseScanCompletedStatuses(job, pod)
	if err != nil || actual.runtimeDigest != participant.observation.runtimeDigest || actual.scannerImageID != participant.observation.scannerImageID || actual.helperImageID != participant.observation.helperImageID || !actual.scannerStarted.Equal(participant.observation.scannerStarted) || !actual.scannerFinished.Equal(participant.observation.scannerFinished) || !actual.helperFinished.Equal(participant.observation.helperFinished) {
		return errors.New("owned database final runtime changed before report commit")
	}
	return nil
}

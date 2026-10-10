package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	digest "github.com/opencontainers/go-digest"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
)

func (owner *DatabaseScanOwner) dispatchClaim(ctx context.Context, identifier uuid.UUID) error {
	claim, err := sqlc.New(owner.pool).GetVulnerabilityDatabaseScanClaim(ctx, identifier)
	if err != nil {
		return err
	}
	if claim.LeaseExpiresAt.Valid && !time.Now().UTC().Before(claim.LeaseExpiresAt.Time) {
		// Reduce-only timeout. No acceptance, witness or current source is granted.
		_, err := sqlc.New(owner.pool).FailExpiredOwnedDatabaseScanClaim(ctx, claim.ID)
		return err
	}
	switch claim.Phase {
	case "pending-preparation", "prepared":
		previous := claim.Phase
		next := "preparing"
		if previous == "prepared" {
			next = "scanning"
		}
		err = owner.transitionClaim(ctx, claim, func(ctx context.Context, queries *sqlc.Queries, current sqlc.VulnerabilityDatabaseScanClaim) error {
			claim, err = queries.ClaimOwnedVulnerabilityDatabaseScan(ctx, sqlc.ClaimOwnedVulnerabilityDatabaseScanParams{ID: current.ID, PreviousPhase: previous, NextPhase: next, LeaseToken: uuid.New()})
			return err
		})
		if err != nil {
			return err
		}
	case "preparing", "scanning":
	default:
		return nil
	}
	if err := owner.revalidateScanMaterial(ctx); err != nil {
		return err
	}
	if claim.Phase == "preparing" {
		return owner.dispatchPreparation(ctx, claim)
	}
	if err := owner.dispatchScan(ctx, claim); err != nil {
		if errors.Is(err, errDatabaseScanPending) {
			return nil
		}
		return err
	}
	return nil
}
func (owner *DatabaseScanOwner) dispatchPreparation(ctx context.Context, claim sqlc.VulnerabilityDatabaseScanClaim) error {
	jobs, err := owner.buildClaimJobs(ctx, claim)
	if err != nil {
		return err
	}
	if !claim.PreparationJobUid.Valid {
		if err := owner.ensureScanNamespace(ctx, claim); err != nil {
			return err
		}
		secret, err := owner.ensureScanMaterial(ctx, claim, false)
		if err != nil {
			return err
		}
		pull, err := owner.ensureScanImagePullSecret(ctx, claim, false)
		if err != nil {
			return err
		}
		volume, err := owner.ensureScanVolume(ctx, claim, jobs.Volume, false)
		if err != nil {
			return err
		}
		job, err := owner.ensureClaimJob(ctx, claim, jobs.Preparation)
		if err != nil {
			return err
		}
		spec, err := databaseScanJobDigest(jobs.Preparation)
		if err != nil {
			return err
		}
		if err := owner.transitionClaim(ctx, claim, func(ctx context.Context, queries *sqlc.Queries, current sqlc.VulnerabilityDatabaseScanClaim) error {
			pullID := pgtype.UUID{}
			if pull != "" {
				pullID = pgtype.UUID{Bytes: uuid.MustParse(string(pull)), Valid: true}
			}
			return requireDatabaseScanWrite(queries.RecordOwnedDatabasePreparationJob(ctx, sqlc.RecordOwnedDatabasePreparationJobParams{ID: current.ID, LeaseToken: uuid.UUID(current.LeaseToken.Bytes), JobUid: uuid.MustParse(string(job.UID)), SpecDigest: spec, SecretUid: uuid.MustParse(string(secret)), ImagePullSecretUid: pullID, VolumeClaimUid: uuid.MustParse(string(volume))}))
		}); err != nil {
			return err
		}
		claim, err = sqlc.New(owner.pool).GetVulnerabilityDatabaseScanClaim(ctx, claim.ID)
		if err != nil {
			return err
		}
	}
	pod, err := owner.lookupClaimPod(ctx, claim, jobs.Preparation, types.UID(uuid.UUID(claim.PreparationJobUid.Bytes).String()))
	if errors.Is(err, errDatabaseScanPending) {
		return nil
	}
	if err != nil {
		return err
	}
	if !claim.PreparationPodUid.Valid {
		if err := owner.transitionClaim(ctx, claim, func(ctx context.Context, queries *sqlc.Queries, current sqlc.VulnerabilityDatabaseScanClaim) error {
			return requireDatabaseScanWrite(queries.RecordOwnedDatabasePreparationPod(ctx, sqlc.RecordOwnedDatabasePreparationPodParams{ID: current.ID, LeaseToken: uuid.UUID(current.LeaseToken.Bytes), JobUid: uuid.UUID(current.PreparationJobUid.Bytes), PodUid: uuid.MustParse(string(pod.UID)), PodName: pod.Name}))
		}); err != nil {
			return err
		}
		claim, err = sqlc.New(owner.pool).GetVulnerabilityDatabaseScanClaim(ctx, claim.ID)
		if err != nil {
			return err
		}
	}
	record, err := owner.collectPreparation(ctx, claim, jobs.Preparation)
	if errors.Is(err, errDatabaseScanPending) {
		return nil
	}
	if err != nil {
		return err
	}
	defer clear(record)
	return owner.transitionClaim(ctx, claim, func(ctx context.Context, queries *sqlc.Queries, current sqlc.VulnerabilityDatabaseScanClaim) error {
		return requireDatabaseScanWrite(queries.RecordOwnedDatabasePreparation(ctx, sqlc.RecordOwnedDatabasePreparationParams{ID: current.ID, LeaseToken: uuid.UUID(current.LeaseToken.Bytes), JobUid: uuid.UUID(current.PreparationJobUid.Bytes), PodUid: uuid.UUID(current.PreparationPodUid.Bytes), PreparedRecord: record}))
	})
}
func (owner *DatabaseScanOwner) dispatchScan(ctx context.Context, claim sqlc.VulnerabilityDatabaseScanClaim) error {
	jobs, err := owner.buildClaimJobs(ctx, claim)
	if err != nil {
		return err
	}
	if !claim.ScanJobUid.Valid {
		record, err := owner.collectPreparation(ctx, claim, jobs.Preparation)
		if err != nil {
			return err
		}
		clear(record)
		if _, err := owner.ensureScanMaterial(ctx, claim, false); err != nil {
			return err
		}
		if _, err := owner.ensureScanImagePullSecret(ctx, claim, false); err != nil {
			return err
		}
		if _, err := owner.ensureScanVolume(ctx, claim, jobs.Volume, false); err != nil {
			return err
		}
		policy, err := owner.ensureDenyPolicy(ctx, claim, jobs.DenyEgress, jobs.Scan.Spec.Template.Labels, false)
		if err != nil {
			return err
		}
		job, err := owner.ensureClaimJob(ctx, claim, jobs.Scan)
		if err != nil {
			return err
		}
		spec, err := databaseScanJobDigest(jobs.Scan)
		if err != nil {
			return err
		}
		if err := owner.transitionClaim(ctx, claim, func(ctx context.Context, queries *sqlc.Queries, current sqlc.VulnerabilityDatabaseScanClaim) error {
			return requireDatabaseScanWrite(queries.RecordOwnedDatabaseScanJob(ctx, sqlc.RecordOwnedDatabaseScanJobParams{ID: current.ID, LeaseToken: uuid.UUID(current.LeaseToken.Bytes), JobUid: uuid.MustParse(string(job.UID)), SpecDigest: spec, PolicyUid: uuid.MustParse(string(policy))}))
		}); err != nil {
			return err
		}
		claim, err = sqlc.New(owner.pool).GetVulnerabilityDatabaseScanClaim(ctx, claim.ID)
		if err != nil {
			return err
		}
	}
	pod, err := owner.lookupClaimPod(ctx, claim, jobs.Scan, types.UID(uuid.UUID(claim.ScanJobUid.Bytes).String()))
	if err != nil {
		return err
	}
	if !claim.ScanPodUid.Valid {
		if err := owner.transitionClaim(ctx, claim, func(ctx context.Context, queries *sqlc.Queries, current sqlc.VulnerabilityDatabaseScanClaim) error {
			return requireDatabaseScanWrite(queries.RecordOwnedDatabaseScanPod(ctx, sqlc.RecordOwnedDatabaseScanPodParams{ID: current.ID, LeaseToken: uuid.UUID(current.LeaseToken.Bytes), JobUid: uuid.UUID(current.ScanJobUid.Bytes), PodUid: uuid.MustParse(string(pod.UID)), PodName: pod.Name}))
		}); err != nil {
			return err
		}
	}
	return owner.completeOwnedScan(ctx, claim.ClusterID, claim.ID)
}
func (owner *DatabaseScanOwner) ensureClaimJob(ctx context.Context, claim sqlc.VulnerabilityDatabaseScanClaim, expected *batchv1.Job) (*batchv1.Job, error) {
	value := expected.DeepCopy()
	value.Kind = "Job"
	value.APIVersion = "batch/v1"
	collection := "/apis/batch/v1/namespaces/" + databasejob.ScanNamespace + "/jobs"
	payload, err := owner.ensureOwnedObject(ctx, claim, collection, collection+"/"+url.PathEscape(value.Name), value, false)
	if err != nil {
		return nil, err
	}
	defer clear(payload)
	var current batchv1.Job
	if jsoncontract.Decode(payload, &current) != nil || current.APIVersion != "batch/v1" || current.Kind != "Job" || !ownedDatabaseObjectMetadata(current.ObjectMeta, value.ObjectMeta) || !databaseScanJobSpecMatches(value, &current) || !databaseScanExpectedLabels(current.Spec.Template.Labels, value.Spec.Template.Labels) {
		return nil, errors.New("owned database created Job differs")
	}
	if err := owner.recordScanResourceUID(ctx, claim, collection+"/"+value.Name, current.UID, false); err != nil {
		return nil, err
	}
	return &current, nil
}
func (owner *DatabaseScanOwner) lookupClaimPod(ctx context.Context, claim sqlc.VulnerabilityDatabaseScanClaim, expected *batchv1.Job, jobUID types.UID) (*corev1.Pod, error) {
	selector := "batch.kubernetes.io/controller-uid=" + string(jobUID)
	jobPath := "/apis/batch/v1/namespaces/" + databasejob.ScanNamespace + "/jobs/" + url.PathEscape(expected.Name)
	podPath := "/api/v1/namespaces/" + databasejob.ScanNamespace + "/pods?labelSelector=" + url.QueryEscape(selector) + "&limit=2"
	scoped, err := newDatabaseScanScopedRequester(owner.requester, claim.ClusterID, []string{jobPath, podPath}, nil)
	if err != nil {
		return nil, err
	}
	jobBytes, err := databaseScanGetBytes(ctx, scoped, claim.ClusterID, jobPath, databaseScanMaximumAPIBytes)
	if err != nil {
		return nil, err
	}
	defer clear(jobBytes)
	var job batchv1.Job
	if jsoncontract.Decode(jobBytes, &job) != nil || job.UID != jobUID || !ownedDatabaseObjectMetadata(job.ObjectMeta, expected.ObjectMeta) || !databaseScanJobSpecMatches(expected, &job) {
		return nil, errors.New("owned database current Job changed")
	}
	podBytes, err := databaseScanGetBytes(ctx, scoped, claim.ClusterID, podPath, databaseScanMaximumAPIBytes)
	if err != nil {
		return nil, err
	}
	defer clear(podBytes)
	var list corev1.PodList
	if jsoncontract.Decode(podBytes, &list) != nil || list.APIVersion != "v1" || list.Kind != "PodList" || list.Continue != "" || len(list.Items) > 1 {
		return nil, errors.New("owned database Pod selection invalid")
	}
	if len(list.Items) == 0 {
		return nil, errDatabaseScanPending
	}
	pod := &list.Items[0]
	if err := normalizeDatabasePodListItem(pod); err != nil {
		return nil, err
	}
	if !validDatabaseScanUID(pod.UID) || pod.Kind != "Pod" || pod.APIVersion != "v1" || pod.Namespace != databasejob.ScanNamespace || pod.DeletionTimestamp != nil || !databaseScanOwnedPod(pod, &job) || !databaseScanPodSpecMatches(expected.Spec.Template.Spec, pod.Spec) || !databaseScanExpectedLabels(pod.Labels, expected.Spec.Template.Labels) {
		return nil, errors.New("owned database selected Pod differs")
	}
	stored := claim.PreparationPodUid
	name := claim.PreparationPodName
	if expected.Name == "database-scan-"+claim.ID.String()+"-scan" {
		stored = claim.ScanPodUid
		name = claim.ScanPodName
	}
	if stored.Valid && (pod.UID != types.UID(uuid.UUID(stored.Bytes).String()) || pod.Name != name) {
		return nil, errors.New("owned database selected Pod UID changed")
	}
	return pod, nil
}
func databaseScanJobDigest(job *batchv1.Job) (string, error) {
	payload, err := json.Marshal(job.Spec)
	if err != nil || len(payload) > databaseScanMaximumAPIBytes {
		clear(payload)
		return "", errors.New("owned database Job spec bound invalid")
	}
	defer clear(payload)
	return digest.FromBytes(payload).String(), nil
}

func (owner *DatabaseScanOwner) recheckOwnedScanObjects(ctx context.Context, claim sqlc.VulnerabilityDatabaseScanClaim) error {
	jobs, err := owner.buildClaimJobs(ctx, claim)
	if err != nil {
		return err
	}
	if _, err := owner.ensureScanMaterial(ctx, claim, true); err != nil {
		return err
	}
	if _, err := owner.ensureScanImagePullSecret(ctx, claim, true); err != nil {
		return err
	}
	if _, err := owner.ensureScanVolume(ctx, claim, jobs.Volume, true); err != nil {
		return err
	}
	if _, err := owner.ensureDenyPolicy(ctx, claim, jobs.DenyEgress, jobs.Scan.Spec.Template.Labels, true); err != nil {
		return err
	}
	return owner.revalidateScanMaterial(ctx)
}

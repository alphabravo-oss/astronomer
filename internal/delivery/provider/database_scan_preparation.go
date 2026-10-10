package provider

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
	"github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
)

// A preparation record is accepted only from the exact completed owned helper
// Pod/log, never an annotation/report supplied by a user or the scanner. It
// establishes staged DATA; the final post-scan measurement is still mandatory.
func (owner *DatabaseScanOwner) collectPreparation(ctx context.Context, claim sqlc.VulnerabilityDatabaseScanClaim, expected *batchv1.Job) ([]byte, error) {
	if !claim.PreparationJobUid.Valid || !claim.PreparationPodUid.Valid || claim.PreparationPodName == "" {
		return nil, errors.New("owned database preparation ownership absent")
	}
	pod, err := owner.lookupClaimPod(ctx, claim, expected, types.UID(uuid.UUID(claim.PreparationJobUid.Bytes).String()))
	if err != nil {
		return nil, err
	}
	// lookup already checked current exact job and pod specs. Read the same
	// actual Job again for completion; no successful Pod alone is sufficient.
	path := "/apis/batch/v1/namespaces/" + databasejob.ScanNamespace + "/jobs/" + url.PathEscape(expected.Name)
	logs := databaseScanPodPath(claim.PreparationPodName) + "/log?container=prepare&timestamps=true&limitBytes=16513"
	scoped, err := newDatabaseScanScopedRequester(owner.requester, claim.ClusterID, []string{path, logs}, nil)
	if err != nil {
		return nil, err
	}
	jobBytes, err := databaseScanGetBytes(ctx, scoped, claim.ClusterID, path, databaseScanMaximumAPIBytes)
	if err != nil {
		return nil, err
	}
	defer clear(jobBytes)
	job, err := decodeDatabasePreparationJob(jobBytes, expected, claim)
	if err != nil {
		return nil, err
	}
	status, err := databasePreparationTerminated(job, pod)
	if err != nil {
		return nil, err
	}
	images, err := owner.projection.authority.DatabaseScanImages(ctx, owner.projection.proof)
	if err != nil {
		return nil, err
	}
	repository, _, found := strings.Cut(images.Helper.Reference, "@")
	if !found {
		return nil, errors.New("owned database preparation helper reference unavailable")
	}
	metadata, err := owner.projection.authority.ReadWorkloadImageMetadata(ctx, owner.projection.proof, repository, claim.HelperChildDigest, claim.Architecture)
	if err != nil {
		return nil, err
	}
	defer clear(metadata.Config)
	defer clear(metadata.Manifest)
	for _, actual := range append(append([]corev1.ContainerStatus(nil), pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		if !databaseRuntimeImageIDMatches(actual.ImageID, repository, metadata) {
			return nil, errors.New("owned database preparation actual helper imageID differs")
		}
	}
	payload, err := databaseScanGetBytes(ctx, scoped, claim.ClusterID, logs, 16512)
	if err != nil {
		return nil, err
	}
	defer clear(payload)
	record, err := decodeOwnedPreparationLog(payload, status.State.Terminated.StartedAt.Time.UTC(), status.State.Terminated.FinishedAt.Time.UTC())
	if err != nil {
		return nil, err
	}
	retained := false
	defer func() {
		if !retained {
			clear(record)
		}
	}()
	prepared, err := databasejob.DecodePreparation(record)
	if err != nil || prepared.SchemaVersion != 2 || prepared.ClaimID != claim.ID || prepared.Cache.FeedDigest != claim.FeedDigest || prepared.ScannerDigest != claim.ScannerIndexDigest || prepared.Workload == nil || prepared.Workload.ManifestDigest != claim.WorkloadManifestDigest || prepared.Workload.Architecture != claim.Architecture {
		return nil, errors.New("owned database preparation contents differ")
	}
	update, err := owner.projection.authority.LoadUpdate(ctx, owner.projection.proof, owner.repository, claim.FeedDigest, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	_, document, valid := update.Snapshot()
	if !valid {
		return nil, errors.New("owned database preparation signed update unavailable")
	}
	for _, database := range document.Databases {
		actual := prepared.MainMetadata
		if database.Role == "trivy-java-database" {
			actual = prepared.JavaMetadata
		} else if database.Role != "trivy-database" {
			return nil, errors.New("owned database preparation role invalid")
		}
		if !reflect.DeepEqual(actual, database.Metadata) {
			return nil, errors.New("owned database preparation signed metadata differs")
		}
	}
	// A restart cannot silently accept a new preparation record for an existing
	// accepted claim/PVC. This is exact byte equality, not a metadata-only match.
	if len(claim.PreparedRecord) != 0 && !bytes.Equal(record, claim.PreparedRecord) {
		return nil, errors.New("owned database preparation record changed")
	}
	again, err := owner.lookupClaimPod(ctx, claim, expected, types.UID(uuid.UUID(claim.PreparationJobUid.Bytes).String()))
	if err != nil || !reflect.DeepEqual(again.Status, pod.Status) {
		return nil, errors.New("owned database preparation changed after log read")
	}
	lastJobBytes, err := databaseScanGetBytes(ctx, scoped, claim.ClusterID, path, databaseScanMaximumAPIBytes)
	if err != nil {
		return nil, err
	}
	defer clear(lastJobBytes)
	lastJob, err := decodeDatabasePreparationJob(lastJobBytes, expected, claim)
	if err != nil || !reflect.DeepEqual(lastJob.Status, job.Status) {
		return nil, errors.New("owned database preparation Job changed after log read")
	}
	if err := owner.revalidateScanMaterial(ctx); err != nil {
		return nil, err
	}
	retained = true
	return record, nil
}

func decodeDatabasePreparationJob(payload []byte, expected *batchv1.Job, claim sqlc.VulnerabilityDatabaseScanClaim) (*batchv1.Job, error) {
	var job batchv1.Job
	if jsoncontract.Decode(payload, &job) != nil || job.APIVersion != "batch/v1" || job.Kind != "Job" || job.UID != types.UID(uuid.UUID(claim.PreparationJobUid.Bytes).String()) || !ownedDatabaseObjectMetadata(job.ObjectMeta, expected.ObjectMeta) || !databaseScanJobSpecMatches(expected, &job) {
		return nil, errors.New("owned database preparation Job changed")
	}
	return &job, nil
}
func databasePreparationTerminated(job *batchv1.Job, pod *corev1.Pod) (corev1.ContainerStatus, error) {
	empty := corev1.ContainerStatus{}
	if job == nil || pod == nil || job.Status.Failed != 0 || pod.Status.Phase == corev1.PodFailed {
		return empty, errors.New("owned database preparation failed")
	}
	complete := false
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			return empty, errors.New("owned database preparation Job failed")
		}
		if condition.Type == batchv1.JobComplete && condition.Status == corev1.ConditionTrue {
			complete = true
		}
	}
	if !complete || job.Status.Succeeded != 1 || job.Status.Active != 0 || pod.Status.Phase != corev1.PodSucceeded {
		return empty, errDatabaseScanPending
	}
	if len(pod.Spec.Containers) != 1 || len(pod.Status.ContainerStatuses) != 1 || len(pod.Status.InitContainerStatuses) != len(pod.Spec.InitContainers) {
		return empty, errors.New("owned database preparation statuses incomplete")
	}
	for index, container := range pod.Spec.InitContainers {
		status := pod.Status.InitContainerStatuses[index]
		if status.Name != container.Name || status.Image != container.Image || !databaseScanTerminatedCleanly(status) {
			return empty, errors.New("owned database preparation initializer failed")
		}
	}
	status := pod.Status.ContainerStatuses[0]
	if status.Name != "prepare" || status.Image != pod.Spec.Containers[0].Image || !databaseScanTerminatedCleanly(status) || status.State.Terminated.FinishedAt.After(time.Now().UTC()) {
		return empty, errors.New("owned database preparation termination invalid")
	}
	return status, nil
}
func decodeOwnedPreparationLog(payload []byte, started, finished time.Time) ([]byte, error) {
	if len(payload) == 0 || len(payload) > 16512 || !finished.After(started) {
		return nil, errors.New("owned database preparation log bound invalid")
	}
	line := bytes.TrimSuffix(payload, []byte("\n"))
	if bytes.ContainsAny(line, "\r\n") {
		return nil, errors.New("owned database preparation log must have one record")
	}
	clock, value, found := bytes.Cut(line, []byte(" "))
	timestamp, err := time.Parse(time.RFC3339Nano, string(clock))
	if !found || err != nil || !strings.HasSuffix(string(clock), "Z") || timestamp.Before(started) || timestamp.After(finished) || len(value) == 0 || len(value) > databasejob.MaximumRecordBytes {
		return nil, errors.New("owned database preparation log timestamp invalid")
	}
	if _, err := databasejob.DecodePreparation(value); err != nil {
		return nil, errors.New("owned database preparation record invalid")
	}
	return append([]byte(nil), value...), nil
}

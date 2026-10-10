package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// These typed API fixtures exercise comparisons and actual request routing only.
// They are not opaque physical trust, an actual Kubernetes Pod or a DB witness.
type databaseScanReadPeer struct {
	request           databaseScanObservationRequest
	job               *batchv1.Job
	pod               *corev1.Pod
	helperLog, report []byte
	calls             int
	changeFinal       bool
	status            int
	malformedBase64   bool
	cancel            context.CancelFunc
}

func (peer *databaseScanReadPeer) Do(ctx context.Context, cluster, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error) {
	peer.calls++
	if cluster != peer.request.cluster.String() || method != "GET" || len(body) != 0 || len(headers) != 0 {
		return nil, errors.New("comparison reader attempted mutation or changed cluster")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var payload []byte
	switch {
	case strings.Contains(path, "/jobs/"):
		job := peer.job.DeepCopy()
		if peer.changeFinal && peer.calls > 4 {
			job.UID = types.UID("ce96c14e-6a3b-4c50-a83c-140158a6b0c9")
		}
		payload, _ = json.Marshal(job)
	case strings.Contains(path, "/pods?"):
		payload, _ = json.Marshal(corev1.PodList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"}, Items: []corev1.Pod{*peer.pod}})
	case strings.Contains(path, "container=observe"):
		if !strings.Contains(path, "timestamps=true") {
			return nil, errors.New("helper ordering lacks Kubernetes timestamps")
		}
		payload = peer.helperLog
	case strings.Contains(path, "container=scanner"):
		if !strings.Contains(path, "timestamps=false") {
			return nil, errors.New("report changed JSON wire format")
		}
		payload = peer.report
	default:
		return nil, errors.New("unexpected observation path")
	}
	if peer.cancel != nil {
		peer.cancel()
	}
	status := peer.status
	if status == 0 {
		status = 200
	}
	encoded := base64.StdEncoding.EncodeToString(payload)
	if peer.malformedBase64 {
		encoded = "not-base64!"
	}
	return &protocol.K8sResponsePayload{StatusCode: status, Body: encoded}, nil
}
func databaseScanReadFixture(t *testing.T) (databaseScanObservationRequest, *databaseScanReadPeer) {
	t.Helper()
	clock := time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)
	dig := func(c string) string { return "sha256:" + strings.Repeat(c, 64) }
	prepared := databasejob.Preparation{SchemaVersion: 2, Kind: "AstronomerOwnedDatabaseScanPreparation", ClaimID: uuid.MustParse("857e0a17-a815-49b8-b62c-52e404b00667"), ScannerDigest: dig("a"), Cache: distribution.DatabaseCacheSnapshot{FeedDigest: dig("a"), Main: distribution.DatabaseCacheContent{Role: "trivy-database", File: "trivy.db", Digest: dig("a"), Size: 10}, Java: distribution.DatabaseCacheContent{Role: "trivy-java-database", File: "trivy-java.db", Digest: dig("a"), Size: 20}}, MainMetadata: distribution.DatabaseUpdateMetadata{Digest: dig("a"), Size: 10, Schema: 2, UpdatedAt: clock, NextUpdate: clock.Add(time.Hour)}, JavaMetadata: distribution.DatabaseUpdateMetadata{Digest: dig("a"), Size: 20, Schema: 1, UpdatedAt: clock, NextUpdate: clock.Add(time.Hour)}, Workload: &databasejob.WorkloadInputSnapshot{ManifestDigest: dig("c"), Architecture: "amd64", IndexDigest: dig("b"), Members: []databasejob.WorkloadInputMember{{Name: "oci-layout", Digest: dig("a"), Size: 1}, {Name: "index.json", Digest: dig("b"), Size: 1}, {Name: "blobs/sha256/" + strings.Repeat("c", 64), Digest: dig("c"), Size: 1}, {Name: "blobs/sha256/" + strings.Repeat("d", 64), Digest: dig("d"), Size: 1}}}}
	encoded, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := databasejob.DecodePreparation(encoded); err != nil {
		t.Fatal("fixture does not reach canonical comparison contract")
	}
	falseValue := false
	zero := int32(0)
	deadline := int64(3000)
	grace := int64(960)
	native := corev1.ContainerRestartPolicyAlways
	helper := "registry.example.test/helper@" + dig("b")
	scanner := "registry.example.test/scanner@" + dig("a")
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "database-scan-857e0a17-scan", Namespace: databasejob.ScanNamespace}, Spec: batchv1.JobSpec{BackoffLimit: &zero, ActiveDeadlineSeconds: &deadline, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"astronomer.io/database-role": "scan"}}, Spec: corev1.PodSpec{NodeName: "owned-node", NodeSelector: map[string]string{"kubernetes.io/arch": "amd64", "kubernetes.io/os": "linux"}, AutomountServiceAccountToken: &falseValue, RestartPolicy: corev1.RestartPolicyNever, TerminationGracePeriodSeconds: &grace, Containers: []corev1.Container{{Name: "scanner", Image: scanner}}, InitContainers: []corev1.Container{{Name: "coordination-bootstrap", Image: helper}, {Name: "observe", Image: helper, RestartPolicy: &native}, {Name: "await", Image: helper}}}}}}
	request := databaseScanObservationRequest{cluster: uuid.MustParse("9824ad66-a4ed-42a5-8101-d9436c7de0e6"), job: job, jobUID: types.UID("55f03c73-6ab7-4506-981f-c1a56e1b4d9b"), podUID: types.UID("26bba7d7-9771-43f5-8dff-a733e39538f5"), podName: "database-scan-pod", prepared: prepared}
	actual := job.DeepCopy()
	actual.UID = request.jobUID
	actual.APIVersion = "batch/v1"
	actual.Kind = "Job"
	actual.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"batch.kubernetes.io/controller-uid": string(request.jobUID)}}
	actual.Status = batchv1.JobStatus{Succeeded: 1, Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}}
	controller := true
	pod := &corev1.Pod{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"}, ObjectMeta: metav1.ObjectMeta{Name: "database-scan-pod", Namespace: databasejob.ScanNamespace, UID: request.podUID, Labels: job.Spec.Template.Labels, OwnerReferences: []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: job.Name, UID: request.jobUID, Controller: &controller}}}, Spec: *job.Spec.Template.Spec.DeepCopy(), Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}
	terminated := func(name, image string, start, finish time.Time) corev1.ContainerStatus {
		return corev1.ContainerStatus{Name: name, Image: image, ImageID: "containerd://" + dig("d"), ContainerID: "containerd://actual-runtime-fixture", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0, Reason: "Completed", StartedAt: metav1.NewTime(start), FinishedAt: metav1.NewTime(finish)}}}
	}
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{terminated("scanner", scanner, clock.Add(2*time.Minute), clock.Add(4*time.Minute))}
	pod.Status.InitContainerStatuses = []corev1.ContainerStatus{terminated("coordination-bootstrap", helper, clock, clock.Add(time.Second)), terminated("observe", helper, clock.Add(time.Minute), clock.Add(6*time.Minute)), terminated("await", helper, clock.Add(time.Minute), clock.Add(90*time.Second))}
	logs := make([][]byte, 2)
	for index, phase := range []string{"initial", "final"} {
		stamp := clock.Add(time.Minute)
		if index == 1 {
			stamp = clock.Add(5 * time.Minute)
		}
		row := databasejob.Measurement{SchemaVersion: 2, Kind: "AstronomerOwnedDatabaseScanMeasurement", Phase: phase, ClaimID: prepared.ClaimID, ScannerDigest: prepared.ScannerDigest, Cache: prepared.Cache, MainMetadataDigest: prepared.MainMetadata.Digest, JavaMetadataDigest: prepared.JavaMetadata.Digest, Workload: prepared.Workload, ObservedAt: stamp}
		body, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		logs[index] = append([]byte(stamp.Format(time.RFC3339Nano)+" "), body...)
	}
	helperLog := append(bytes.Join(logs, []byte{'\n'}), '\n')
	return request, &databaseScanReadPeer{request: request, job: actual, pod: pod, helperLog: helperLog, report: []byte(`{"SchemaVersion":2,"Metadata":{"ImageConfig":{"Env":["PRIVATE_IMAGE_DEFAULT=fixture"]}},"Results":[]}`)}
}
func TestOwnedDatabaseScanObservationReadsActualRoutesAndRechecksAfterLogs(t *testing.T) {
	request, peer := databaseScanReadFixture(t)
	observation, err := observeDatabaseScan(t.Context(), peer, request)
	if err != nil {
		t.Fatal(err)
	}
	defer observation.close()
	if peer.calls != 6 || observation.jobUID != request.jobUID || observation.podUID != request.podUID || len(observation.report) == 0 || !observation.measurements.FinalLogTimestamp.After(observation.scannerFinished) {
		t.Fatal("reader did not retain complete owned runtime comparisons")
	}
	for _, value := range []any{observation, &observation} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%d"} {
			if strings.Contains(fmt.Sprintf(verb, value), "PRIVATE_IMAGE_DEFAULT") {
				t.Fatal("raw image configuration leaked from private observation formatting")
			}
		}
	}
	original := observation.report
	observation.close()
	if len(observation.report) != 0 || !bytes.Equal(original, make([]byte, len(original))) {
		t.Fatal("private retained report bytes were not cleared")
	}
}
func TestOwnedDatabaseScanObservationRejectsChangedOwnershipSpecAndIncompleteFinal(t *testing.T) {
	for _, name := range []string{"job-uid", "pod-uid", "wrong-api-kind", "controller", "readonly-intent", "extra-container", "privileged-pod", "mutable-image", "restart", "scanner-failed", "helper-failed", "helper-missing", "initial-only", "periodic-final", "pretermination-final", "changed-after-logs", "extra-parallelism", "missing-selector", "wrong-selector", "absent-status", "future-status"} {
		t.Run(name, func(t *testing.T) {
			request, peer := databaseScanReadFixture(t)
			switch name {
			case "job-uid":
				peer.job.UID = request.podUID
			case "pod-uid":
				peer.pod.UID = request.jobUID
			case "wrong-api-kind":
				peer.job.Kind = "Secret"
			case "controller":
				peer.pod.OwnerReferences[0].UID = request.podUID
			case "readonly-intent":
				peer.pod.Spec.AutomountServiceAccountToken = new(bool)
				*peer.pod.Spec.AutomountServiceAccountToken = true
			case "extra-container":
				peer.pod.Spec.Containers = append(peer.pod.Spec.Containers, corev1.Container{Name: "other"})
			case "privileged-pod":
				peer.pod.Spec.HostPID = true
			case "mutable-image":
				peer.pod.Spec.Containers[0].Image = "registry.example.test/scanner:latest"
			case "restart":
				peer.pod.Status.InitContainerStatuses[1].RestartCount = 1
			case "scanner-failed":
				peer.pod.Status.ContainerStatuses[0].State.Terminated.ExitCode = 1
			case "helper-failed":
				peer.pod.Status.InitContainerStatuses[1].State.Terminated.ExitCode = 1
			case "helper-missing":
				peer.pod.Status.InitContainerStatuses = peer.pod.Status.InitContainerStatuses[:2]
			case "initial-only":
				peer.helperLog = bytes.SplitAfter(peer.helperLog, []byte{'\n'})[0]
			case "periodic-final":
				peer.helperLog = append(peer.helperLog, peer.helperLog...)
			case "pretermination-final":
				peer.pod.Status.ContainerStatuses[0].State.Terminated.FinishedAt = metav1.NewTime(peer.pod.Status.InitContainerStatuses[1].State.Terminated.FinishedAt.Add(-time.Second))
			case "changed-after-logs":
				peer.changeFinal = true
			case "extra-parallelism":
				two := int32(2)
				peer.job.Spec.Parallelism = &two
			case "missing-selector":
				peer.job.Spec.Selector = nil
			case "wrong-selector":
				peer.job.Spec.Selector.MatchLabels["batch.kubernetes.io/controller-uid"] = string(request.podUID)
			case "absent-status":
				peer.job.Status = batchv1.JobStatus{}
			case "future-status":
				peer.pod.Status.InitContainerStatuses[1].State.Terminated.FinishedAt = metav1.NewTime(time.Now().UTC().Add(time.Hour))
			}
			observation, err := observeDatabaseScan(t.Context(), peer, request)
			if err == nil || len(observation.report) != 0 {
				observation.close()
				t.Fatal("changed or incomplete scan DATA produced a usable observation")
			}
			if name == "changed-after-logs" && peer.calls != 5 {
				t.Fatal("post-report Job recheck was not exercised")
			}
		})
	}
}
func TestOwnedDatabaseScanObservationBoundsReadPrivacyAndCancellation(t *testing.T) {
	for _, name := range []string{"base64", "status", "oversized-api", "oversized-report", "canceled", "canceled-in-request"} {
		t.Run(name, func(t *testing.T) {
			request, peer := databaseScanReadFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch name {
			case "base64":
				peer.malformedBase64 = true
			case "status":
				peer.status = 401
			case "oversized-api":
				peer.job.Annotations = map[string]string{"large": strings.Repeat("x", databaseScanMaximumAPIBytes+1)}
			case "oversized-report":
				peer.report = make([]byte, databaseScanMaximumReportBytes+1)
			case "canceled":
				cancel()
			case "canceled-in-request":
				peer.cancel = cancel
			}
			observation, err := observeDatabaseScan(ctx, peer, request)
			if err == nil || len(observation.report) != 0 {
				observation.close()
				t.Fatal("unbounded/failed/canceled read produced report DATA")
			}
			if strings.HasPrefix(name, "canceled") && !errors.Is(err, context.Canceled) {
				t.Fatal("owned observation lost caller cancellation")
			}
		})
	}
}

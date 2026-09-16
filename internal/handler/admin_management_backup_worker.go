package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (h *AdminDrillHandler) probeManagementBackupS3(ctx context.Context, row sqlc.ManagementBackupDestination, accessKey, secretKey string) error {
	endpoint := strings.TrimSpace(row.EndpointUrl)
	region := row.Region
	if region == "" {
		region = "us-east-1"
	}
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://s3.%s.amazonaws.com", region)
	}
	host, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint url")
	}
	host.Path = strings.TrimRight(host.Path, "/") + "/" + row.Bucket + "/"
	q := host.Query()
	q.Set("list-type", "2")
	q.Set("max-keys", "1")
	host.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, host.String(), nil)
	if err != nil {
		return err
	}
	if accessKey != "" && secretKey != "" {
		signAWSV4(req, accessKey, secretKey, region, "s3", time.Now().UTC())
	}
	client := h.httpClient
	if client == nil {
		client = httpclient.DefaultExternal()
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connectivity failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusPartialContent:
		return nil
	case http.StatusForbidden:
		return fmt.Errorf("forbidden (likely invalid credentials)")
	case http.StatusNotFound:
		return fmt.Errorf("bucket not found: %s", row.Bucket)
	default:
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func destInt32Ptr(v int32) *int32 { return &v }

func destBoolPtr(v bool) *bool { return &v }

func managementBackupObjectGeneration(annotations map[string]string) int64 {
	if annotations == nil {
		return 0
	}
	generation, err := strconv.ParseInt(annotations["astronomer.io/management-backup-generation"], 10, 64)
	if err != nil || generation < 0 {
		return 0
	}
	return generation
}

func resourcePtr(v string) *resource.Quantity {
	q := resource.MustParse(v)
	return &q
}

// ReconcileManagementBackup is the generation-fenced worker boundary. It is
// the only path allowed to create/delete management-cluster backup resources.
func (h *AdminDrillHandler) ReconcileManagementBackup(ctx context.Context, id uuid.UUID, generation int64) error {
	q, ok := h.queries.(managementBackupWorkerQuerier)
	if !ok {
		return errors.New("management backup worker store is not configured")
	}
	row, err := q.ClaimManagementBackupDestinationGeneration(ctx, sqlc.ClaimManagementBackupDestinationGenerationParams{ID: id, Generation: generation})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := q.GetManagementBackupDestination(ctx, id)
		return managementBackupReconcileClaimMiss(existing, generation, loadErr)
	}
	if err != nil {
		return err
	}
	if h.k8s == nil || h.namespace == "" {
		return h.failManagementBackupGeneration(ctx, row, errors.New("management Kubernetes runtime unavailable"))
	}
	fence := func() error {
		current, loadErr := q.GetManagementBackupDestination(ctx, id)
		if loadErr != nil {
			return loadErr
		}
		if current.DesiredGeneration != generation || current.ReconcileStatus != "applying" {
			return errManagementBackupStaleGeneration
		}
		return nil
	}
	if row.DesiredState == "deleted" || !row.Enabled {
		err = h.deleteDestinationResources(ctx, id, generation, fence)
	} else {
		var access, secret string
		access, secret, err = h.decryptDestinationCredentials(row)
		if err == nil {
			err = h.reconcileDestination(ctx, row, access, secret, fence)
		}
	}
	if errors.Is(err, errManagementBackupStaleGeneration) {
		return nil
	}
	if err != nil {
		return h.failManagementBackupGeneration(ctx, row, err)
	}
	_, err = q.CompleteManagementBackupDestinationGeneration(ctx, sqlc.CompleteManagementBackupDestinationGenerationParams{ID: id, Generation: generation})
	return err
}

func (h *AdminDrillHandler) failManagementBackupGeneration(ctx context.Context, row sqlc.ManagementBackupDestination, effectErr error) error {
	q, ok := h.queries.(managementBackupWorkerQuerier)
	if !ok {
		return errors.New("management backup worker store is not configured")
	}
	category := sanitizedManagementBackupError(effectErr)
	terminal := managementBackupTerminalCategory(category) || managementBackupRetryExhausted(ctx)
	var persistErr error
	if terminal {
		_, persistErr = q.FailManagementBackupDestinationGeneration(ctx, sqlc.FailManagementBackupDestinationGenerationParams{ErrorMessage: category, ID: row.ID, Generation: row.DesiredGeneration})
	} else {
		_, persistErr = q.RetryManagementBackupDestinationGeneration(ctx, sqlc.RetryManagementBackupDestinationGenerationParams{ErrorMessage: category, ID: row.ID, Generation: row.DesiredGeneration})
	}
	if persistErr != nil {
		return fmt.Errorf("management backup %s; persist status failed", category)
	}
	if terminal {
		return fmt.Errorf("%w: management backup %s", asynq.SkipRetry, category)
	}
	return errors.New(category)
}

// ExecuteManagementBackupOperation claims a durable test/run receipt with a
// lease longer than the task timeout, then records a truthful terminal state.
func (h *AdminDrillHandler) ExecuteManagementBackupOperation(ctx context.Context, operationID uuid.UUID) error {
	q, ok := h.queries.(managementBackupWorkerQuerier)
	if !ok {
		return errors.New("management backup worker store is not configured")
	}
	op, err := q.ClaimManagementBackupOperation(ctx, operationID)
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := q.GetWorkloadOperation(ctx, operationID)
		return managementBackupOperationClaimMiss(existing, loadErr)
	}
	if err != nil {
		return err
	}
	destinationID, err := uuid.Parse(op.TargetKey)
	if err == nil {
		row, loadErr := q.GetManagementBackupDestination(ctx, destinationID)
		err = loadErr
		if err == nil && row.DesiredState == "deleted" {
			err = errors.New("destination is deleted")
		}
		if err == nil && op.OperationType == "management_backup_test" {
			var access, secret string
			access, secret, err = h.decryptDestinationCredentials(row)
			if err == nil {
				err = h.probeManagementBackupS3(ctx, row, access, secret)
			}
		} else if err == nil && op.OperationType == "management_backup_run" {
			if row.ReconcileStatus != "ready" || row.AppliedGeneration != row.DesiredGeneration {
				err = errors.New("destination is not reconciled")
			} else if h.k8s == nil || h.namespace == "" {
				err = errors.New("management Kubernetes runtime unavailable")
			} else {
				var cj *batchv1.CronJob
				cj, err = h.destinationCronJob(ctx, destinationID)
				if err == nil && cj == nil {
					err = errors.New("backup CronJob is not ready")
				}
				if err == nil {
					err = h.executeManagementBackupRun(ctx, operationID, destinationID, cj)
				}
			}
		}
	}
	if err != nil {
		category := sanitizedManagementBackupError(err)
		terminal := managementBackupTerminalCategory(category) || managementBackupRetryExhausted(ctx)
		var persistErr error
		if terminal {
			_, persistErr = q.MarkWorkloadOperationFailed(ctx, sqlc.MarkWorkloadOperationFailedParams{ID: operationID, AttemptCount: op.AttemptCount, ErrorMessage: category})
		} else {
			_, persistErr = q.MarkWorkloadOperationRetrying(ctx, sqlc.MarkWorkloadOperationRetryingParams{ID: operationID, AttemptCount: op.AttemptCount, ErrorMessage: category})
		}
		if errors.Is(persistErr, pgx.ErrNoRows) {
			return nil
		}
		if persistErr != nil {
			return persistErr
		}
		if terminal {
			return fmt.Errorf("%w: management backup %s", asynq.SkipRetry, category)
		}
		return errors.New(category)
	}
	_, err = q.MarkWorkloadOperationCompleted(ctx, sqlc.MarkWorkloadOperationCompletedParams{ID: operationID, AttemptCount: op.AttemptCount})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

func (h *AdminDrillHandler) executeManagementBackupRun(ctx context.Context, operationID, destinationID uuid.UUID, cj *batchv1.CronJob) error {
	jobs := h.k8s.BatchV1().Jobs(h.namespace)
	jobName := "management-backup-op-" + operationID.String()
	job, err := jobs.Get(ctx, jobName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		// CronJob ForbidConcurrent only serializes Jobs created by that CronJob;
		// include scheduled and other manual Jobs in the destination fence.
		active, listErr := jobs.List(ctx, metav1.ListOptions{LabelSelector: destinationIDLabel + "=" + destinationID.String()})
		if listErr != nil {
			return listErr
		}
		for i := range active.Items {
			candidate := &active.Items[i]
			if candidate.Name != jobName && managementBackupJobOutcome(candidate) == "running" {
				return errors.New("backup_in_progress")
			}
		}
		job = &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: jobName, Namespace: h.namespace, Labels: cj.Spec.JobTemplate.Labels}, Spec: cj.Spec.JobTemplate.Spec}
		job, err = jobs.Create(ctx, job, metav1.CreateOptions{})
	}
	if err != nil {
		return err
	}
	deadline := time.NewTimer(4 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		switch managementBackupJobOutcome(job) {
		case "succeeded":
			return nil
		case "failed":
			return errors.New("backup_job_failed")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("backup_in_progress")
		case <-ticker.C:
			job, err = jobs.Get(ctx, jobName, metav1.GetOptions{})
			if err != nil {
				return err
			}
		}
	}
}

func managementBackupReconcileClaimMiss(row sqlc.ManagementBackupDestination, generation int64, loadErr error) error {
	if errors.Is(loadErr, pgx.ErrNoRows) {
		return nil
	}
	if loadErr != nil {
		return errors.New("load management backup destination failed")
	}
	if row.DesiredGeneration != generation {
		return nil
	}
	if row.AppliedGeneration >= generation && row.ReconcileStatus == "ready" {
		return nil
	}
	if row.ReconcileStatus == "failed" {
		return nil
	}
	return errors.New("management backup reconciliation lease is held")
}

func managementBackupOperationClaimMiss(operation sqlc.WorkloadOperation, loadErr error) error {
	if errors.Is(loadErr, pgx.ErrNoRows) {
		return nil
	}
	if loadErr != nil {
		return errors.New("load management backup operation failed")
	}
	switch operation.Status {
	case "completed", "failed", "cancelled", "superseded":
		return nil
	default:
		return errors.New("management backup operation lease is held")
	}
}

func managementBackupRetryExhausted(ctx context.Context) bool {
	retried, retryOK := asynq.GetRetryCount(ctx)
	maximum, maximumOK := asynq.GetMaxRetry(ctx)
	return retryOK && maximumOK && retried >= maximum
}

func managementBackupTerminalCategory(category string) bool {
	switch category {
	case "credentials_unavailable", "external_forbidden", "external_not_found", "backup_job_failed":
		return true
	default:
		return false
	}
}

func sanitizedManagementBackupError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "backup_in_progress"):
		return "backup_in_progress"
	case strings.Contains(msg, "backup_job_failed"):
		return "backup_job_failed"
	case strings.Contains(msg, "credential"), strings.Contains(msg, "encrypt"), strings.Contains(msg, "decrypt"):
		return "credentials_unavailable"
	case strings.Contains(msg, "not reconciled"), strings.Contains(msg, "cronjob is not ready"):
		return "destination_not_ready"
	case strings.Contains(msg, "kubernetes runtime unavailable"), strings.Contains(msg, "connectivity failed"), strings.Contains(msg, "timeout"):
		return "external_unreachable"
	case strings.Contains(msg, "forbidden"):
		return "external_forbidden"
	case strings.Contains(msg, "not found"):
		return "external_not_found"
	case strings.Contains(msg, "unexpected status"):
		return "external_http_error"
	default:
		return "internal_error"
	}
}

func managementBackupJobOutcome(job *batchv1.Job) string {
	if job == nil {
		return "running"
	}
	for _, condition := range job.Status.Conditions {
		if condition.Status != corev1.ConditionTrue {
			continue
		}
		switch condition.Type {
		case batchv1.JobComplete:
			return "succeeded"
		case batchv1.JobFailed:
			return "failed"
		}
	}
	if job.Status.Succeeded > 0 {
		return "succeeded"
	}
	return "running"
}

// Package handler — management-plane (Astronomer-itself) backup status.
//
// Complements admin_drill.go. The drill is the restore-proof half of
// NIST CP-9; this endpoint is the operator-facing status of the nightly
// pg_dump CronJob itself:
//
//	GET /api/v1/admin/management-backup/  — CronJob schedule, destination
//	                                        (no credentials), last Job,
//	                                        encryption-key wrapping flag
//
// Superuser-gated inside the handler, same pattern as the drill viewer.
// Destination fields are copied from CronJob env (bucket / prefix /
// region / endpoint). Credentials and wrapping passphrases never leave
// the cluster — we only report whether wrapping is configured.
package handler

import (
	"context"
	"net/http"
	"sort"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
)

const (
	managementBackupComponent = "management-backup"
	restoreDrillComponent     = "restore-drill"
)

// SetKubernetes attaches the in-cluster client used to read the
// management-backup and restore-drill CronJobs. Optional — when unset
// GetStatus still 200s with enabled=false so the dashboard can render
// "not wired" instead of a 503.
func (h *AdminDrillHandler) SetKubernetes(k8s kubernetes.Interface, namespace, releaseName string) {
	if h == nil {
		return
	}
	h.k8s = k8s
	h.namespace = namespace
	h.releaseName = releaseName
}

// ManagementBackupStatusResponse is the wire shape for
// GET /admin/management-backup/.
type ManagementBackupStatusResponse struct {
	Enabled             bool                         `json:"enabled"`
	Reason              string                       `json:"reason,omitempty"`
	CronJob             *ManagementCronJobStatus     `json:"cronjob,omitempty"`
	Destination         *ManagementBackupDestination `json:"destination,omitempty"`
	Retention           *ManagementBackupRetention   `json:"retention,omitempty"`
	EncryptionKeyBackup ManagementKeyBackupStatus    `json:"encryption_key_backup"`
	LastJob             *ManagementBackupJob         `json:"last_job,omitempty"`
	Drill               *ManagementCronJobStatus     `json:"drill,omitempty"`
}

// ManagementCronJobStatus is the operator-visible slice of a CronJob.
type ManagementCronJobStatus struct {
	Name               string     `json:"name"`
	Schedule           string     `json:"schedule"`
	Suspended          bool       `json:"suspended"`
	LastScheduleTime   *time.Time `json:"last_schedule_time,omitempty"`
	LastSuccessfulTime *time.Time `json:"last_successful_time,omitempty"`
}

// ManagementBackupDestination is the S3 target. No credentials.
type ManagementBackupDestination struct {
	Bucket   string `json:"bucket"`
	Prefix   string `json:"prefix"`
	Region   string `json:"region"`
	Endpoint string `json:"endpoint,omitempty"`
}

// ManagementBackupRetention is the per-tier keep count from CronJob env.
type ManagementBackupRetention struct {
	Daily   string `json:"daily,omitempty"`
	Weekly  string `json:"weekly,omitempty"`
	Monthly string `json:"monthly,omitempty"`
}

// ManagementKeyBackupStatus reports whether the Fernet/JWT key is being
// wrapped into S3 alongside the dump. WrappingConfigured is the DR-critical
// flag: the chart refuses to upload the key in plaintext, so an enabled
// CronJob without wrapping means dumps exist but a restore onto a new
// cluster cannot decrypt agent tokens / SSO secrets.
type ManagementKeyBackupStatus struct {
	WrappingConfigured bool `json:"wrapping_configured"`
}

// ManagementBackupJob is the most recent Job spawned by the backup CronJob.
type ManagementBackupJob struct {
	Name            string     `json:"name"`
	StartTime       *time.Time `json:"start_time,omitempty"`
	CompletionTime  *time.Time `json:"completion_time,omitempty"`
	Succeeded       int32      `json:"succeeded"`
	Failed          int32      `json:"failed"`
	Active          int32      `json:"active"`
	DurationSeconds *int64     `json:"duration_seconds,omitempty"`
}

// GetStatus handles GET /api/v1/admin/management-backup/.
func (h *AdminDrillHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	if !h.gateAction(w, r, "admin.management_backup.viewed") {
		return
	}

	out := ManagementBackupStatusResponse{}

	if h.k8s == nil || h.namespace == "" {
		out.Reason = "Management Kubernetes client is not available."
		RespondJSON(w, http.StatusOK, out)
		return
	}

	ctx := r.Context()
	backupCJ, err := h.findCronJob(ctx, managementBackupComponent)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, err.Error())
		return
	}
	if backupCJ == nil {
		out.Reason = "Management-plane backup CronJob is not installed. Set managementBackup.s3.bucket and credentialsSecretRef in Helm values."
		RespondJSON(w, http.StatusOK, out)
		return
	}

	out.Enabled = true
	out.CronJob = cronJobStatus(backupCJ)
	env := cronJobEnv(backupCJ)
	out.Destination = &ManagementBackupDestination{
		Bucket:   env["MANAGEMENT_BACKUP_BUCKET"],
		Prefix:   env["MANAGEMENT_BACKUP_PREFIX"],
		Region:   env["MANAGEMENT_BACKUP_REGION"],
		Endpoint: env["MANAGEMENT_BACKUP_ENDPOINT"],
	}
	out.Retention = &ManagementBackupRetention{
		Daily:   env["MANAGEMENT_BACKUP_KEEP_DAILY"],
		Weekly:  env["MANAGEMENT_BACKUP_KEEP_WEEKLY"],
		Monthly: env["MANAGEMENT_BACKUP_KEEP_MONTHLY"],
	}
	out.EncryptionKeyBackup = ManagementKeyBackupStatus{
		WrappingConfigured: env["KEYBACKUP_ENABLED"] != "",
	}
	if job, err := h.latestJob(ctx, managementBackupComponent); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, err.Error())
		return
	} else if job != nil {
		out.LastJob = jobSummary(job)
	}

	if drillCJ, err := h.findCronJob(ctx, restoreDrillComponent); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, err.Error())
		return
	} else if drillCJ != nil {
		out.Drill = cronJobStatus(drillCJ)
	}

	RespondJSON(w, http.StatusOK, out)
}

func (h *AdminDrillHandler) findCronJob(ctx context.Context, component string) (*batchv1.CronJob, error) {
	list, err := h.k8s.BatchV1().CronJobs(h.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: h.componentSelector(component),
	})
	if err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, nil
	}
	cj := list.Items[0]
	return &cj, nil
}

func (h *AdminDrillHandler) latestJob(ctx context.Context, component string) (*batchv1.Job, error) {
	list, err := h.k8s.BatchV1().Jobs(h.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: h.componentSelector(component),
	})
	if err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, nil
	}
	items := append([]batchv1.Job(nil), list.Items...)
	sort.Slice(items, func(i, j int) bool {
		return items[i].CreationTimestamp.After(items[j].CreationTimestamp.Time)
	})
	return &items[0], nil
}

func (h *AdminDrillHandler) componentSelector(component string) string {
	sel := "app.kubernetes.io/component=" + component
	if h.releaseName != "" {
		sel += ",app.kubernetes.io/instance=" + h.releaseName
	}
	return sel
}

func cronJobStatus(cj *batchv1.CronJob) *ManagementCronJobStatus {
	out := &ManagementCronJobStatus{
		Name:     cj.Name,
		Schedule: cj.Spec.Schedule,
	}
	if cj.Spec.Suspend != nil {
		out.Suspended = *cj.Spec.Suspend
	}
	if t := cj.Status.LastScheduleTime; t != nil {
		v := t.Time
		out.LastScheduleTime = &v
	}
	if t := cj.Status.LastSuccessfulTime; t != nil {
		v := t.Time
		out.LastSuccessfulTime = &v
	}
	return out
}

func cronJobEnv(cj *batchv1.CronJob) map[string]string {
	out := map[string]string{}
	for _, c := range cj.Spec.JobTemplate.Spec.Template.Spec.Containers {
		for _, e := range c.Env {
			if e.ValueFrom != nil {
				continue
			}
			out[e.Name] = e.Value
		}
	}
	return out
}

func jobSummary(job *batchv1.Job) *ManagementBackupJob {
	out := &ManagementBackupJob{
		Name:      job.Name,
		Succeeded: job.Status.Succeeded,
		Failed:    job.Status.Failed,
		Active:    job.Status.Active,
	}
	if t := job.Status.StartTime; t != nil {
		v := t.Time
		out.StartTime = &v
	}
	if t := job.Status.CompletionTime; t != nil {
		v := t.Time
		out.CompletionTime = &v
	}
	if out.StartTime != nil && out.CompletionTime != nil {
		d := int64(out.CompletionTime.Sub(*out.StartTime).Seconds())
		out.DurationSeconds = &d
	}
	return out
}

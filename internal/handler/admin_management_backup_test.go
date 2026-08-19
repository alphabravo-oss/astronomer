package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestManagementBackupStatus_NotWired(t *testing.T) {
	callerID := uuid.New()
	q := &fakeDrillQuerier{user: sqlc.User{ID: callerID, IsSuperuser: true}}
	h := NewAdminDrillHandler(q)

	w := httptest.NewRecorder()
	h.GetStatus(w, makeRequest("/api/v1/admin/management-backup/", callerID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Data ManagementBackupStatusResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Data.Enabled {
		t.Fatalf("expected enabled=false when k8s is unset: %+v", got.Data)
	}
	if got.Data.Reason == "" {
		t.Fatal("expected a reason when k8s is unset")
	}
}

func TestManagementBackupStatus_ReportsCronJob(t *testing.T) {
	callerID := uuid.New()
	q := &fakeDrillQuerier{user: sqlc.User{ID: callerID, IsSuperuser: true}}
	h := NewAdminDrillHandler(q)

	lastSuccess := metav1.NewTime(time.Date(2026, 8, 18, 3, 0, 0, 0, time.UTC))
	start := metav1.NewTime(time.Date(2026, 8, 18, 3, 0, 5, 0, time.UTC))
	done := metav1.NewTime(time.Date(2026, 8, 18, 3, 4, 5, 0, time.UTC))
	k8s := fake.NewSimpleClientset(
		&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "astronomer-management-backup",
				Namespace: "astronomer",
				Labels: map[string]string{
					"app.kubernetes.io/component": "management-backup",
					"app.kubernetes.io/instance":  "astronomer",
				},
			},
			Spec: batchv1.CronJobSpec{
				Schedule: "0 3 * * *",
				JobTemplate: batchv1.JobTemplateSpec{
					Spec: batchv1.JobSpec{
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{{
									Name: "pgdump-s3",
									Env: []corev1.EnvVar{
										{Name: "MANAGEMENT_BACKUP_BUCKET", Value: "astronomer-backups"},
										{Name: "MANAGEMENT_BACKUP_PREFIX", Value: "astronomer-pg"},
										{Name: "MANAGEMENT_BACKUP_REGION", Value: "us-east-1"},
										{Name: "MANAGEMENT_BACKUP_KEEP_DAILY", Value: "30"},
										{Name: "KEYBACKUP_ENABLED", Value: "1"},
									},
								}},
							},
						},
					},
				},
			},
			Status: batchv1.CronJobStatus{LastSuccessfulTime: &lastSuccess},
		},
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "astronomer-management-backup-123",
				Namespace: "astronomer",
				Labels: map[string]string{
					"app.kubernetes.io/component": "management-backup",
					"app.kubernetes.io/instance":  "astronomer",
				},
				CreationTimestamp: start,
			},
			Status: batchv1.JobStatus{
				Succeeded:      1,
				StartTime:      &start,
				CompletionTime: &done,
			},
		},
		&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "astronomer-restore-drill",
				Namespace: "astronomer",
				Labels: map[string]string{
					"app.kubernetes.io/component": "restore-drill",
					"app.kubernetes.io/instance":  "astronomer",
				},
			},
			Spec: batchv1.CronJobSpec{Schedule: "0 4 * * 1"},
		},
	)
	h.SetKubernetes(k8s, "astronomer", "astronomer")

	w := httptest.NewRecorder()
	h.GetStatus(w, makeRequest("/api/v1/admin/management-backup/", callerID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Data ManagementBackupStatusResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, w.Body.String())
	}
	if !got.Data.Enabled {
		t.Fatalf("expected enabled=true: %+v", got.Data)
	}
	if got.Data.CronJob == nil || got.Data.CronJob.Schedule != "0 3 * * *" {
		t.Fatalf("cronjob = %+v", got.Data.CronJob)
	}
	if got.Data.Destination == nil || got.Data.Destination.Bucket != "astronomer-backups" {
		t.Fatalf("destination = %+v", got.Data.Destination)
	}
	if !got.Data.EncryptionKeyBackup.WrappingConfigured {
		t.Fatal("expected wrapping_configured=true")
	}
	if got.Data.LastJob == nil || got.Data.LastJob.Succeeded != 1 {
		t.Fatalf("last_job = %+v", got.Data.LastJob)
	}
	if got.Data.Drill == nil || got.Data.Drill.Schedule != "0 4 * * 1" {
		t.Fatalf("drill = %+v", got.Data.Drill)
	}
	if q.auditCalls != 1 {
		t.Fatalf("audit calls = %d, want 1", q.auditCalls)
	}
}

func TestManagementBackupStatus_MissingCronJob(t *testing.T) {
	callerID := uuid.New()
	q := &fakeDrillQuerier{user: sqlc.User{ID: callerID, IsSuperuser: true}}
	h := NewAdminDrillHandler(q)
	h.SetKubernetes(fake.NewSimpleClientset(), "astronomer", "astronomer")

	w := httptest.NewRecorder()
	h.GetStatus(w, makeRequest("/api/v1/admin/management-backup/", callerID))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Data ManagementBackupStatusResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Data.Enabled {
		t.Fatalf("expected enabled=false: %+v", got.Data)
	}
	if got.Data.Reason == "" {
		t.Fatal("expected a reason when CronJob is missing")
	}
}

func TestManagementBackupStatus_RequiresSuperuser(t *testing.T) {
	callerID := uuid.New()
	q := &fakeDrillQuerier{user: sqlc.User{ID: callerID, IsSuperuser: false}}
	h := NewAdminDrillHandler(q)

	w := httptest.NewRecorder()
	h.GetStatus(w, makeRequest("/api/v1/admin/management-backup/", callerID))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}

	w = httptest.NewRecorder()
	h.GetStatus(w, httptest.NewRequest(http.MethodGet, "/api/v1/admin/management-backup/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anon status = %d, want 401", w.Code)
	}
}

func TestManagementBackupStatus_OmitsSecretEnv(t *testing.T) {
	callerID := uuid.New()
	q := &fakeDrillQuerier{user: sqlc.User{ID: callerID, IsSuperuser: true}}
	h := NewAdminDrillHandler(q)
	k8s := fake.NewSimpleClientset(&batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "astronomer-management-backup",
			Namespace: "astronomer",
			Labels: map[string]string{
				"app.kubernetes.io/component": "management-backup",
				"app.kubernetes.io/instance":  "astronomer",
			},
		},
		Spec: batchv1.CronJobSpec{
			Schedule: "0 3 * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{
								Name: "pgdump-s3",
								Env: []corev1.EnvVar{
									{Name: "MANAGEMENT_BACKUP_BUCKET", Value: "astronomer-backups"},
									{Name: "DATABASE_URL", ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{Key: "dsn"},
									}},
								},
							}},
						},
					},
				},
			},
		},
	})
	h.SetKubernetes(k8s, "astronomer", "astronomer")

	w := httptest.NewRecorder()
	h.GetStatus(w, makeRequest("/api/v1/admin/management-backup/", callerID))
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, body)
	}
	for _, leak := range []string{"DATABASE_URL", "passphrase", "credentials"} {
		if strings.Contains(body, leak) {
			t.Fatalf("response leaked %q: %s", leak, body)
		}
	}
}

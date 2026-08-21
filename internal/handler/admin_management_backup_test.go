package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
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
	for _, leak := range []string{"DATABASE_URL", "passphrase", "aws_secret_access_key"} {
		if strings.Contains(body, leak) {
			t.Fatalf("response leaked %q: %s", leak, body)
		}
	}
}

func TestManagementBackupCreateDestination_CreatesCronJob(t *testing.T) {
	callerID := uuid.New()
	q := &fakeDrillQuerier{user: sqlc.User{ID: callerID, IsSuperuser: true}}
	h := NewAdminDrillHandler(q)
	h.SetKubernetes(fake.NewSimpleClientset(), "astronomer", "astronomer")
	h.SetBackupRuntime("pgdump-s3:test", "astronomer")

	body := `{"name":"primary","bucket":"astronomer-backups","region":"us-east-1","access_key":"AKIA","secret_key":"secret","schedule":"0 3 * * *"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/management-backup/destinations/", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(makeRequest("/api/v1/admin/management-backup/destinations/", callerID).Context())

	w := httptest.NewRecorder()
	h.CreateDestination(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if len(q.destinations) != 1 {
		t.Fatalf("destinations = %d, want 1", len(q.destinations))
	}
	name := h.destinationResourceName(q.destinations[0].ID)
	if _, err := h.k8s.BatchV1().CronJobs("astronomer").Get(req.Context(), name, metav1.GetOptions{}); err != nil {
		t.Fatalf("cronjob missing: %v", err)
	}
	if _, err := h.k8s.CoreV1().Secrets("astronomer").Get(req.Context(), name+"-aws", metav1.GetOptions{}); err != nil {
		t.Fatalf("secret missing: %v", err)
	}
	if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "AKIA") {
		t.Fatalf("response leaked credentials: %s", w.Body.String())
	}

	statusW := httptest.NewRecorder()
	h.GetStatus(statusW, makeRequest("/api/v1/admin/management-backup/", callerID))
	if statusW.Code != http.StatusOK {
		t.Fatalf("status page = %d; body=%s", statusW.Code, statusW.Body.String())
	}
	if !strings.Contains(statusW.Body.String(), `"enabled":true`) {
		t.Fatalf("expected enabled after create: %s", statusW.Body.String())
	}
}

func TestManagementBackupDeleteDestination(t *testing.T) {
	callerID := uuid.New()
	id := uuid.New()
	q := &fakeDrillQuerier{
		user: sqlc.User{ID: callerID, IsSuperuser: true},
		destinations: []sqlc.ManagementBackupDestination{{
			ID: id, Name: "primary", Bucket: "b", Prefix: "astronomer-pg",
			Region: "us-east-1", Schedule: "0 3 * * *", Enabled: true,
		}},
	}
	h := NewAdminDrillHandler(q)
	h.SetKubernetes(fake.NewSimpleClientset(), "astronomer", "astronomer")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/management-backup/destinations/"+id.String()+"/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id.String())
	ctx := makeRequest("/api/v1/admin/management-backup/destinations/"+id.String()+"/", callerID).Context()
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	h.DeleteDestination(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}
	if len(q.destinations) != 0 {
		t.Fatalf("destinations left = %d", len(q.destinations))
	}
}

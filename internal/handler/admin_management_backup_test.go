package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type managementBackupDeleteReplayTx struct {
	ManagementBackupMutationTx
	fakeOperationIdempotencyStore
	row    sqlc.ManagementBackupDestination
	marks  int
	tasks  []sqlc.UpsertTaskOutboxParams
	audits []sqlc.UpsertAuditOutboxParams
}

func (tx *managementBackupDeleteReplayTx) GetManagementBackupDestinationForUpdate(_ context.Context, id uuid.UUID) (sqlc.ManagementBackupDestination, error) {
	if tx.row.ID != id {
		return sqlc.ManagementBackupDestination{}, pgx.ErrNoRows
	}
	return tx.row, nil
}

func (tx *managementBackupDeleteReplayTx) MarkManagementBackupDestinationDeleted(_ context.Context, id uuid.UUID) (sqlc.ManagementBackupDestination, error) {
	tx.marks++
	tx.row.ID, tx.row.DesiredState, tx.row.ReconcileStatus = id, "deleted", "pending"
	tx.row.DesiredGeneration++
	return tx.row, nil
}

func (tx *managementBackupDeleteReplayTx) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	tx.tasks = append(tx.tasks, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), Status: "pending"}, nil
}

func (tx *managementBackupDeleteReplayTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID}, nil
}

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

func TestManagementBackupCreateDestinationFailsClosedWithoutEncryptor(t *testing.T) {
	callerID := uuid.New()
	q := &fakeDrillQuerier{user: sqlc.User{ID: callerID, IsSuperuser: true}}
	h := NewAdminDrillHandler(q)
	h.SetManagementBackupEnabled(true)
	h.SetKubernetes(fake.NewSimpleClientset(), "astronomer", "astronomer")
	h.SetBackupRuntime("pgdump-s3:test", "astronomer")

	body := `{"name":"primary","bucket":"astronomer-backups","region":"us-east-1","access_key":"AKIA","secret_key":"secret","schedule":"0 3 * * *"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/management-backup/destinations/", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(makeRequest("/api/v1/admin/management-backup/destinations/", callerID).Context())

	w := httptest.NewRecorder()
	h.CreateDestination(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", w.Code, w.Body.String())
	}
	if len(q.destinations) != 0 {
		t.Fatalf("destinations = %d, want 0", len(q.destinations))
	}
	if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "AKIA") {
		t.Fatalf("response leaked credentials: %s", w.Body.String())
	}
}

func TestManagementBackupDisabledRejectsEveryMutationBeforeTransaction(t *testing.T) {
	callerID := uuid.New()
	h := NewAdminDrillHandler(&fakeDrillQuerier{user: sqlc.User{ID: callerID, IsSuperuser: true}})
	h.SetManagementBackupEnabled(false)
	transactions := 0
	h.SetRunTx(func(context.Context, func(ManagementBackupMutationTx) error) error {
		transactions++
		return nil
	})

	for _, test := range []struct {
		name   string
		method string
		path   string
		call   func(http.ResponseWriter, *http.Request)
	}{
		{"create", http.MethodPost, "/api/v1/admin/management-backup/destinations/", h.CreateDestination},
		{"update", http.MethodPut, "/api/v1/admin/management-backup/destinations/id/", h.UpdateDestination},
		{"delete", http.MethodDelete, "/api/v1/admin/management-backup/destinations/id/", h.DeleteDestination},
		{"test", http.MethodPost, "/api/v1/admin/management-backup/destinations/id/test/", h.TestDestination},
		{"run", http.MethodPost, "/api/v1/admin/management-backup/destinations/id/run/", h.RunDestination},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, test.path, nil)
			req = req.WithContext(makeRequest(test.path, callerID).Context())
			w := httptest.NewRecorder()
			test.call(w, req)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503; body=%s", w.Code, w.Body.String())
			}
		})
	}
	if transactions != 0 {
		t.Fatalf("transaction runner called %d times while management backup was disabled", transactions)
	}
}

func TestManagementBackupDeleteDestinationFailsClosedWithoutTransactionRunner(t *testing.T) {
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
	h.SetManagementBackupEnabled(true)
	h.SetKubernetes(fake.NewSimpleClientset(), "astronomer", "astronomer")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/management-backup/destinations/"+id.String()+"/", nil)
	req.Header.Set("Idempotency-Key", "delete-destination-test")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id.String())
	ctx := makeRequest("/api/v1/admin/management-backup/destinations/"+id.String()+"/", callerID).Context()
	req = req.WithContext(context.WithValue(ctx, chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	h.DeleteDestination(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", w.Code, w.Body.String())
	}
	if len(q.destinations) != 1 {
		t.Fatalf("destinations left = %d, want unchanged", len(q.destinations))
	}
}

func TestManagementBackupGetDestinationReturnsExactRedactedState(t *testing.T) {
	callerID, id := uuid.New(), uuid.New()
	q := &fakeDrillQuerier{
		user: sqlc.User{ID: callerID, IsActive: true, IsSuperuser: true},
		destinations: []sqlc.ManagementBackupDestination{{
			ID: id, Name: "primary", Bucket: "backups", Prefix: "astronomer-pg",
			Region: "us-east-1", EndpointUrl: "https://s3.example.test", Schedule: "0 3 * * *",
			Enabled: true, KeepDaily: 30, KeepWeekly: 12, KeepMonthly: 6,
			DesiredState: "deleted", ReconcileStatus: "pending", DesiredGeneration: 4,
			EncryptedCredentials: "must-not-leak",
		}},
	}
	h := NewAdminDrillHandler(q)
	router := chi.NewRouter()
	router.Get("/api/v1/admin/management-backup/destinations/{id}/", h.GetDestination)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/management-backup/destinations/"+id.String()+"/", nil)
	req = req.WithContext(makeRequest(req.URL.Path, callerID).Context())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "must-not-leak") {
		t.Fatalf("encrypted credentials leaked: %s", response.Body.String())
	}
	var envelope struct {
		Data ManagementBackupDestinationView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	got := envelope.Data
	if got.ID != id.String() || got.DesiredState != "deleted" || got.ReconcileStatus != "pending" || got.DesiredGeneration != 4 {
		t.Fatalf("destination status=%+v", got)
	}
}

func TestManagementBackupDeleteReplaysExactReceiptOnceUnderRace(t *testing.T) {
	callerID, id := uuid.New(), uuid.New()
	queries := &fakeDrillQuerier{user: sqlc.User{ID: callerID, IsActive: true, IsSuperuser: true}}
	tx := &managementBackupDeleteReplayTx{row: sqlc.ManagementBackupDestination{
		ID: id, Name: "primary", DesiredState: "present", ReconcileStatus: "ready", DesiredGeneration: 3,
	}}
	h := NewAdminDrillHandler(queries)
	h.SetManagementBackupEnabled(true)
	var transactionMu sync.Mutex
	h.SetRunTx(func(_ context.Context, fn func(ManagementBackupMutationTx) error) error {
		transactionMu.Lock()
		defer transactionMu.Unlock()
		return fn(tx)
	})
	router := chi.NewRouter()
	router.Delete("/api/v1/admin/management-backup/destinations/{id}/", h.DeleteDestination)
	responses := make([]*httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/management-backup/destinations/"+id.String()+"/", nil)
			req.Header.Set("Idempotency-Key", "management-backup-delete-race")
			req = req.WithContext(makeRequest(req.URL.Path, callerID).Context())
			responses[index] = httptest.NewRecorder()
			router.ServeHTTP(responses[index], req)
		}(i)
	}
	wg.Wait()
	for _, response := range responses {
		if response.Code != http.StatusAccepted {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if responses[0].Body.String() != responses[1].Body.String() || responses[0].Header().Get("Location") != responses[1].Header().Get("Location") {
		t.Fatalf("replay receipt changed: first=%s second=%s", responses[0].Body.String(), responses[1].Body.String())
	}
	if tx.marks != 1 || len(tx.tasks) != 1 || len(tx.audits) != 1 {
		t.Fatalf("racing replay marks/tasks/audits=%d/%d/%d, want 1/1/1", tx.marks, len(tx.tasks), len(tx.audits))
	}
}

func TestManagementBackupDeleteKeyRejectsChangedTarget(t *testing.T) {
	callerID, firstID := uuid.New(), uuid.New()
	queries := &fakeDrillQuerier{user: sqlc.User{ID: callerID, IsActive: true, IsSuperuser: true}}
	tx := &managementBackupDeleteReplayTx{row: sqlc.ManagementBackupDestination{ID: firstID, Name: "primary", DesiredState: "present", ReconcileStatus: "ready", DesiredGeneration: 1}}
	h := NewAdminDrillHandler(queries)
	h.SetManagementBackupEnabled(true)
	h.SetRunTx(func(_ context.Context, fn func(ManagementBackupMutationTx) error) error { return fn(tx) })
	router := chi.NewRouter()
	router.Delete("/api/v1/admin/management-backup/destinations/{id}/", h.DeleteDestination)
	request := func(id uuid.UUID) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/management-backup/destinations/"+id.String()+"/", nil)
		req.Header.Set("Idempotency-Key", "management-backup-delete-conflict")
		req = req.WithContext(makeRequest(req.URL.Path, callerID).Context())
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	first, changed := request(firstID), request(uuid.New())
	if first.Code != http.StatusAccepted || changed.Code != http.StatusConflict {
		t.Fatalf("first/changed status=%d/%d changed=%s", first.Code, changed.Code, changed.Body.String())
	}
	if tx.marks != 1 || len(tx.tasks) != 1 || len(tx.audits) != 1 {
		t.Fatalf("changed target marks/tasks/audits=%d/%d/%d", tx.marks, len(tx.tasks), len(tx.audits))
	}
}

func TestManagementBackupManualRunDefersToAnyActiveDestinationJob(t *testing.T) {
	destinationID := uuid.New()
	h := NewAdminDrillHandler(&fakeDrillQuerier{})
	k8s := fake.NewSimpleClientset(&batchv1.Job{ObjectMeta: metav1.ObjectMeta{
		Name: "scheduled-backup", Namespace: "astronomer",
		Labels: map[string]string{destinationIDLabel: destinationID.String()},
	}})
	h.SetKubernetes(k8s, "astronomer", "astronomer")
	cj := &batchv1.CronJob{Spec: batchv1.CronJobSpec{JobTemplate: batchv1.JobTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{destinationIDLabel: destinationID.String()}}}}}
	err := h.executeManagementBackupRun(context.Background(), uuid.New(), destinationID, cj)
	if err == nil || sanitizedManagementBackupError(err) != "backup_in_progress" {
		t.Fatalf("active scheduled job was not fenced: %v", err)
	}
}

func TestManagementBackupSecretUpdatePreservesResourceVersionAndGeneration(t *testing.T) {
	id := uuid.New()
	h := NewAdminDrillHandler(&fakeDrillQuerier{})
	h.SetKubernetes(fake.NewSimpleClientset(), "astronomer", "astronomer")
	name := h.destinationResourceName(id) + "-aws"
	k8s := fake.NewSimpleClientset(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: "astronomer", ResourceVersion: "17",
		Annotations: map[string]string{"astronomer.io/management-backup-generation": "2"},
	}})
	h.SetKubernetes(k8s, "astronomer", "astronomer")
	if err := h.upsertDestinationSecret(context.Background(), id, 3, "access", "secret", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	got, err := k8s.CoreV1().Secrets("astronomer").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.ResourceVersion != "17" || managementBackupObjectGeneration(got.Annotations) != 3 {
		t.Fatalf("resourceVersion/generation = %q/%d", got.ResourceVersion, managementBackupObjectGeneration(got.Annotations))
	}
	if err := h.upsertDestinationSecret(context.Background(), id, 2, "access", "secret", func() error { return nil }); !errors.Is(err, errManagementBackupStaleGeneration) {
		t.Fatalf("stale generation updated newer Secret: %v", err)
	}
}

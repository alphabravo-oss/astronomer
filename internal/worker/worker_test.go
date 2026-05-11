package worker

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/hibiken/asynq"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestNewWorker(t *testing.T) {
	w, err := NewWorker("redis://localhost:6379/0", testLogger())
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	if w == nil {
		t.Fatal("expected non-nil Worker")
	}
	if w.server == nil {
		t.Fatal("expected non-nil asynq.Server")
	}
	if w.mux == nil {
		t.Fatal("expected non-nil asynq.ServeMux")
	}
}

// FEATURES-051126 T02: invalid REDIS_URL must be fail-fast (error returned,
// nil Worker), NOT silently fall back to localhost. The previous behavior
// was a production footgun.
func TestNewWorkerInvalidRedis(t *testing.T) {
	w, err := NewWorker("not-a-valid-url", testLogger())
	if err == nil {
		t.Fatal("expected error for invalid REDIS_URL, got nil")
	}
	if w != nil {
		t.Fatal("expected nil Worker on parse error")
	}
}

func TestRegisterHandlers(t *testing.T) {
	w, err := NewWorker("redis://localhost:6379/0", testLogger())
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	// Should not panic.
	w.RegisterHandlers()
}

func TestNewScheduler(t *testing.T) {
	s, err := NewScheduler("redis://localhost:6379/0", testLogger())
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil Scheduler")
	}
	if s.scheduler == nil {
		t.Fatal("expected non-nil asynq.Scheduler")
	}
}

// FEATURES-051126 T02: same fail-fast contract for the scheduler.
func TestNewSchedulerInvalidRedis(t *testing.T) {
	s, err := NewScheduler("not-a-valid-url", testLogger())
	if err == nil {
		t.Fatal("expected error for invalid REDIS_URL, got nil")
	}
	if s != nil {
		t.Fatal("expected nil Scheduler on parse error")
	}
}

func TestTaskConstants(t *testing.T) {
	expected := map[string]string{
		"TypeHealthCheck":              "cluster:health_check",
		"TypeAlertEvaluation":          "alert:evaluate",
		"TypeCatalogSync":              "catalog:sync",
		"TypeMetricsAggregation":       "metrics:aggregate",
		"TypeMonitoringReconcile":      "monitoring:reconcile",
		"TypeBackupExecution":          "backup:execute",
		"TypeSecurityScan":             "security:scan",
		"TypeNotificationSend":         "notification:send",
		"TypeAgentManifest":            "agent:generate_manifest",
		"TypeEnsureAuditLogPartitions": "audit_log:ensure_partitions",
		"TypeEnforceAuditLogRetention": "audit_log:enforce_retention",
	}

	actual := map[string]string{
		"TypeHealthCheck":              TypeHealthCheck,
		"TypeAlertEvaluation":          TypeAlertEvaluation,
		"TypeCatalogSync":              TypeCatalogSync,
		"TypeMetricsAggregation":       TypeMetricsAggregation,
		"TypeMonitoringReconcile":      TypeMonitoringReconcile,
		"TypeBackupExecution":          TypeBackupExecution,
		"TypeSecurityScan":             TypeSecurityScan,
		"TypeNotificationSend":         TypeNotificationSend,
		"TypeAgentManifest":            TypeAgentManifest,
		"TypeEnsureAuditLogPartitions": TypeEnsureAuditLogPartitions,
		"TypeEnforceAuditLogRetention": TypeEnforceAuditLogRetention,
	}

	for name, want := range expected {
		got := actual[name]
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// --- Payload encoding/decoding tests ---

func TestHealthCheckPayloadRoundTrip(t *testing.T) {
	p := tasks.HealthCheckPayload{ClusterID: "cluster-123"}
	task, err := tasks.NewHealthCheckTask(p)
	if err != nil {
		t.Fatalf("NewHealthCheckTask: %v", err)
	}

	var decoded tasks.HealthCheckPayload
	if err := json.Unmarshal(task.Payload(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ClusterID != p.ClusterID {
		t.Errorf("ClusterID = %q, want %q", decoded.ClusterID, p.ClusterID)
	}
}

func TestBackupExecutionPayloadRoundTrip(t *testing.T) {
	p := tasks.BackupExecutionPayload{ClusterID: "c1", BackupID: "b1"}
	task, err := tasks.NewBackupExecutionTask(p)
	if err != nil {
		t.Fatalf("NewBackupExecutionTask: %v", err)
	}

	var decoded tasks.BackupExecutionPayload
	if err := json.Unmarshal(task.Payload(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ClusterID != "c1" || decoded.BackupID != "b1" {
		t.Errorf("got %+v, want cluster_id=c1, backup_id=b1", decoded)
	}
}

func TestNotificationPayloadRoundTrip(t *testing.T) {
	p := tasks.NotificationSendPayload{
		Channel:    "slack",
		Subject:    "Alert",
		Body:       "Something happened",
		Recipients: []string{"#ops", "#alerts"},
	}
	task, err := tasks.NewNotificationSendTask(p)
	if err != nil {
		t.Fatalf("NewNotificationSendTask: %v", err)
	}

	var decoded tasks.NotificationSendPayload
	if err := json.Unmarshal(task.Payload(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Channel != "slack" || len(decoded.Recipients) != 2 {
		t.Errorf("got %+v, want channel=slack, 2 recipients", decoded)
	}
}

func TestAgentManifestPayloadRoundTrip(t *testing.T) {
	p := tasks.AgentManifestPayload{
		ClusterID:       "c1",
		AgentToken:      "tok-abc",
		ImageRepository: "ghcr.io/test",
		ImageTag:        "v1.0.0",
	}
	task, err := tasks.NewAgentManifestTask(p)
	if err != nil {
		t.Fatalf("NewAgentManifestTask: %v", err)
	}

	var decoded tasks.AgentManifestPayload
	if err := json.Unmarshal(task.Payload(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.ClusterID != "c1" || decoded.AgentToken != "tok-abc" {
		t.Errorf("got %+v", decoded)
	}
}

// --- Handler tests (with nil/empty payloads) ---

func TestHandleHealthCheckEmptyPayload(t *testing.T) {
	task := asynq.NewTask(TypeHealthCheck, nil)
	if err := tasks.HandleHealthCheck(context.Background(), task); err != nil {
		t.Fatalf("HandleHealthCheck: %v", err)
	}
}

func TestHandleAlertEvaluationEmptyPayload(t *testing.T) {
	task := asynq.NewTask(TypeAlertEvaluation, nil)
	if err := tasks.HandleAlertEvaluation(context.Background(), task); err != nil {
		t.Fatalf("HandleAlertEvaluation: %v", err)
	}
}

func TestHandleCatalogSyncEmptyPayload(t *testing.T) {
	task := asynq.NewTask(TypeCatalogSync, nil)
	if err := tasks.HandleCatalogSync(context.Background(), task); err != nil {
		t.Fatalf("HandleCatalogSync: %v", err)
	}
}

func TestHandleMetricsAggregationEmptyPayload(t *testing.T) {
	task := asynq.NewTask(TypeMetricsAggregation, nil)
	if err := tasks.HandleMetricsAggregation(context.Background(), task); err != nil {
		t.Fatalf("HandleMetricsAggregation: %v", err)
	}
}

func TestHandleMonitoringReconcileEmptyPayload(t *testing.T) {
	task := asynq.NewTask(TypeMonitoringReconcile, nil)
	if err := tasks.HandleMonitoringReconcile(context.Background(), task); err != nil {
		t.Fatalf("HandleMonitoringReconcile: %v", err)
	}
}

func TestHandleBackupExecutionMissingFields(t *testing.T) {
	// Empty payload should fail (requires cluster_id and backup_id).
	task := asynq.NewTask(TypeBackupExecution, []byte(`{}`))
	err := tasks.HandleBackupExecution(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for missing fields")
	}
}

func TestHandleSecurityScanMissingCluster(t *testing.T) {
	task := asynq.NewTask(TypeSecurityScan, []byte(`{}`))
	err := tasks.HandleSecurityScan(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for missing cluster_id")
	}
}

func TestHandleNotificationMissingChannel(t *testing.T) {
	task := asynq.NewTask(TypeNotificationSend, []byte(`{"recipients":["a"]}`))
	err := tasks.HandleNotificationSend(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for missing channel")
	}
}

func TestHandleAgentManifestMissingCluster(t *testing.T) {
	task := asynq.NewTask(TypeAgentManifest, []byte(`{}`))
	err := tasks.HandleAgentManifest(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for missing cluster_id")
	}
}

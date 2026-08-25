package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

func TestManagementBackupStoredErrorsAreStrictCategories(t *testing.T) {
	const sentinel = "SUPER-SECRET bucket-customer endpoint.internal response-body"
	for _, input := range []error{
		errors.New("unexpected status 500: " + sentinel),
		errors.New("connectivity failed: " + sentinel),
		errors.New("forbidden invalid credential " + sentinel),
		errors.New(sentinel),
	} {
		got := sanitizedManagementBackupError(input)
		if strings.Contains(got, "secret") || strings.Contains(got, "bucket") || strings.Contains(got, "endpoint") || strings.Contains(got, "response") {
			t.Fatalf("categorized error leaked external detail: %q", got)
		}
		switch got {
		case "credentials_unavailable", "destination_not_ready", "external_unreachable", "external_forbidden", "external_not_found", "external_http_error", "backup_in_progress", "backup_job_failed", "internal_error":
		default:
			t.Fatalf("error category is not allow-listed: %q", got)
		}
	}
}

func TestManagementBackupWriteErrorDoesNotExposeDriverDetail(t *testing.T) {
	h := NewAdminDrillHandler(nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/management-backup/destinations/", nil)
	h.respondDestinationWriteError(recorder, request, errors.New("SENTINEL driver credentials host.internal"))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	for _, forbidden := range []string{"SENTINEL", "credentials", "host.internal"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, body)
		}
	}
}

func TestManagementBackupJobOutcomeIsTruthful(t *testing.T) {
	if got := managementBackupJobOutcome(nil); got != "running" {
		t.Fatalf("nil job = %q", got)
	}
	if got := managementBackupJobOutcome(&batchv1.Job{Status: batchv1.JobStatus{Succeeded: 1}}); got != "succeeded" {
		t.Fatalf("succeeded job = %q", got)
	}
	failed := &batchv1.Job{Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}}}
	if got := managementBackupJobOutcome(failed); got != "failed" {
		t.Fatalf("failed job = %q", got)
	}
}

func TestManagementBackupHTTPEntriesContainNoRemoteEffects(t *testing.T) {
	raw, err := os.ReadFile("admin_management_backup_destinations.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, method := range []string{"CreateDestination", "UpdateDestination", "DeleteDestination", "TestDestination", "RunDestination"} {
		start := strings.Index(source, "func (h *AdminDrillHandler) "+method)
		if start < 0 {
			t.Fatalf("missing %s", method)
		}
		end := strings.Index(source[start+5:], "\nfunc ")
		if end < 0 {
			end = len(source) - start
		} else {
			end += 5
		}
		body := source[start : start+end]
		for _, forbidden := range []string{"reconcileDestination(", "deleteDestinationResources(", "probeManagementBackupS3(", ".BatchV1()", ".CoreV1()"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s performs request-time external effect %q", method, forbidden)
			}
		}
	}
}

func TestManagementBackupProductionTransactionAndWorkerWiring(t *testing.T) {
	files := map[string][]string{
		"../server/management_backup_enablement.go": {
			"SetRunTx(sqlcMutationTxRunner[handler.ManagementBackupMutationTx](database))",
		},
		"../server/app_router_composition.go": {
			"AdminDrill: newManagementBackupHandler(cfg.ManagementBackupEnabled, queries, database, encryptor, localK8s, localNamespace)",
		},
		"../server/app_runtime_tasks.go": {
			"ManagementBackup: routed.deps.AdminDrill",
		},
	}
	for path, required := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		for _, needle := range required {
			if !strings.Contains(text, needle) {
				t.Fatalf("production management-backup wiring in %s missing %q", path, needle)
			}
		}
	}
}

func TestManagementBackupMutationEntriesUseMandatoryTransactionalAudit(t *testing.T) {
	raw, err := os.ReadFile("admin_management_backup_destinations.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, method := range []string{"CreateDestination", "UpdateDestination", "DeleteDestination", "TestDestination", "RunDestination"} {
		start := strings.Index(source, "func (h *AdminDrillHandler) "+method)
		if start < 0 {
			t.Fatalf("missing %s", method)
		}
		end := strings.Index(source[start+5:], "\nfunc ")
		if end < 0 {
			end = len(source) - start
		} else {
			end += 5
		}
		body := source[start : start+end]
		requiredCalls := []string{"gateManagementBackupMutation("}
		if method == "TestDestination" || method == "RunDestination" {
			requiredCalls = append(requiredCalls, "createManagementBackupOperation(")
		} else {
			requiredCalls = append(requiredCalls, "h.runTx(")
		}
		for _, required := range requiredCalls {
			if !strings.Contains(body, required) {
				t.Fatalf("%s does not use %q", method, required)
			}
		}
		if strings.Contains(body, "gateAction(") {
			t.Fatalf("%s uses best-effort pre-mutation audit", method)
		}
	}
	helperStart := strings.Index(source, "func (h *AdminDrillHandler) createManagementBackupOperation")
	if helperStart < 0 {
		t.Fatal("missing durable management backup operation helper")
	}
	helper := source[helperStart:]
	for _, required := range []string{"h.runTx(", "recordAuditOutbox(", "EnqueueTaskOutbox("} {
		if !strings.Contains(helper, required) {
			t.Fatalf("management backup operation helper missing %q", required)
		}
	}
}

func TestManagementBackupClaimsAreGenerationFencedAndRecoverable(t *testing.T) {
	raw, err := os.ReadFile("../db/queries/management_backup.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, required := range []string{
		"desired_generation = sqlc.arg(generation)",
		"reconcile_status = 'applying' AND updated_at < now() - interval '6 minutes'",
		"status = 'running' AND started_at < now() - interval '6 minutes'",
		"status IN ('pending', 'retrying')",
		"WHERE id = sqlc.arg(id) AND desired_generation = sqlc.arg(generation)",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("management backup lease/generation contract missing %q", required)
		}
	}
}

func TestManagementBackupOperationReceiptHasPollingHeadersAndRetryTruth(t *testing.T) {
	raw, err := os.ReadFile("admin_management_backup_destinations.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	for _, required := range []string{
		`w.Header().Set("Location", operationURL)`,
		`w.Header().Set("Retry-After", "2")`,
		"MarkWorkloadOperationRetrying(",
		"RetryManagementBackupDestinationGeneration(",
		"managementBackupRetryExhausted(ctx)",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("management backup receipt/retry contract missing %q", required)
		}
	}
}

func TestManagementBackupCrashWindowClaimMissesRemainRetryable(t *testing.T) {
	applying := sqlc.ManagementBackupDestination{DesiredGeneration: 4, AppliedGeneration: 3, ReconcileStatus: "applying"}
	if err := managementBackupReconcileClaimMiss(applying, 4, nil); err == nil || !strings.Contains(err.Error(), "lease") {
		t.Fatalf("same-generation applying claim miss was dropped: %v", err)
	}
	ready := applying
	ready.AppliedGeneration = 4
	ready.ReconcileStatus = "ready"
	if err := managementBackupReconcileClaimMiss(ready, 4, nil); err != nil {
		t.Fatalf("completed generation did not converge: %v", err)
	}
	stale := applying
	stale.DesiredGeneration = 5
	if err := managementBackupReconcileClaimMiss(stale, 4, nil); err != nil {
		t.Fatalf("stale generation did not converge: %v", err)
	}
	if err := managementBackupOperationClaimMiss(sqlc.WorkloadOperation{Status: "running"}, nil); err == nil || !strings.Contains(err.Error(), "lease") {
		t.Fatalf("running operation claim miss was dropped: %v", err)
	}
	if err := managementBackupOperationClaimMiss(sqlc.WorkloadOperation{Status: "completed"}, nil); err != nil {
		t.Fatalf("completed operation did not converge: %v", err)
	}
	if err := managementBackupOperationClaimMiss(sqlc.WorkloadOperation{}, pgx.ErrNoRows); err != nil {
		t.Fatalf("missing operation should converge: %v", err)
	}
}

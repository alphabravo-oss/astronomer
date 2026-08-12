package charlie

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func productionReadAdapterFixture(t *testing.T) *CatalogExecutor {
	t.Helper()
	const fixtureID = "11111111-1111-4111-8111-111111111111"
	id := uuid.MustParse(fixtureID)
	now := time.Now().UTC()
	replicas := int32(2)

	fleetQueries := &fleetQueriesFake{
		summary: sqlc.CharlieAgentFleetSummaryRow{TotalClusters: 1, ConnectedClusters: 1},
		list: []sqlc.CharlieAgentFleetListRow{{
			ClusterID: id, ClusterName: "internal-SENTINEL", DisplayName: "production", Labels: json.RawMessage(`{"token":"SENTINEL"}`),
			Environment: "prod", Region: "us-east", AgentID: "agent-a", ConnectionState: "connected",
		}},
		get: sqlc.CharlieAgentFleetGetRow{ClusterID: id, DisplayName: "production", Environment: "prod", Region: "us-east", AgentID: "agent-a", ConnectionState: "connected"},
	}
	fleet, err := NewFleetCapabilityAdapter(fleetQueries)
	if err != nil {
		t.Fatal(err)
	}

	deployment := ownedDeployment("astronomer-server", replicas)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "astronomer-server", Namespace: "astronomer", Labels: map[string]string{"app.kubernetes.io/instance": "astronomer", "secret": "SENTINEL"}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "server", Env: []corev1.EnvVar{{Name: "TOKEN", Value: "SENTINEL"}}}}},
	}
	management := managementAdapterFixture(t,
		deployment,
		pod,
		&corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "event-a", Namespace: "astronomer"}, InvolvedObject: corev1.ObjectReference{Name: "astronomer-server"}, Type: "Warning", Reason: "Unavailable", Message: "token=SENTINEL", EventTime: metav1.MicroTime{Time: now}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "management-node"}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-data", Namespace: "astronomer", Labels: map[string]string{"app.kubernetes.io/instance": "astronomer", "secret": "SENTINEL"}}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-server", Namespace: "astronomer", Labels: map[string]string{"app.kubernetes.io/instance": "astronomer", "secret": "SENTINEL"}}},
		&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "astronomer-maintenance", Namespace: "astronomer", Labels: map[string]string{"app.kubernetes.io/instance": "astronomer", "secret": "SENTINEL"}}, Status: batchv1.JobStatus{Succeeded: 1}},
	)
	management.logStream = func(context.Context, string, string, int64) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("token=SENTINEL\nready\n")), nil
	}

	queueInspector := &queueInspectorFake{
		queues: map[string]*asynq.QueueInfo{"default": {Queue: "default", Size: 1, Archived: 1}},
		tasks: map[string]map[string]*asynq.TaskInfo{"default": {"task-a": {
			ID: "task-a", Type: "catalog:sync", State: asynq.TaskStateArchived,
			Payload: []byte(`{"api_key":"SENTINEL"}`), LastErr: "password=SENTINEL", LastFailedAt: now,
		}}},
	}
	queue, err := NewQueueCapabilityAdapter(queueInspector)
	if err != nil {
		t.Fatal(err)
	}

	argo := argoAdapterFixture(t, argoApplication("astronomer-self-manage", true))
	queries := &operationalQueriesFake{
		settings: map[string]json.RawMessage{"feature.charlie": json.RawMessage(`true`), "database.password": json.RawMessage(`"SENTINEL"`)},
		backups:  []sqlc.Backup{{ID: id, Name: "management", Status: "completed"}},
		alerts:   []sqlc.AlertEvent{{ID: id, RuleID: uuid.New(), Status: "active", Message: "token=SENTINEL unavailable", Details: json.RawMessage(`{"secret":"SENTINEL"}`), FiredAt: now}},
		audits:   []sqlc.AuditLog{{ID: id, Action: "settings.updated", ResourceType: "platform_setting", ResourceID: "feature.charlie", Detail: json.RawMessage(`{"api_key":"SENTINEL"}`), CreatedAt: now}},
	}
	operational := operationalAdapterFixture(t, queries)
	operational.databaseSnapshot = func(context.Context) (bool, int64, error) { return false, 1024, nil }

	executor, err := NewCatalogExecutor(MergeCapabilityAdapters(
		FleetCapabilityAdapters(fleet),
		ManagementKubernetesCapabilityAdapters(management),
		QueueCapabilityAdapters(queue),
		ArgoCDCapabilityAdapters(argo),
		OperationalCapabilityAdapters(operational),
		WorkPipelineCapabilityAdapters(staticCapabilityAdapter{}),
		RuntimeCapabilityAdapters(staticCapabilityAdapter{}),
		AdminVisibilityCapabilityAdapters(staticCapabilityAdapter{}),
	))
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func TestProductionReadAdaptersExecuteEntireCatalogWithSafeBoundedShapes(t *testing.T) {
	executor := productionReadAdapterFixture(t)
	paginated := map[string]bool{
		"astronomer.management.workloads":      true,
		"astronomer.queue.failed_tasks":        true,
		"astronomer.queue.tasks":               true,
		"astronomer.alert.list":                true,
		"astronomer.catalog.repositories":      true,
		"astronomer.agent_fleet.list":          true,
		"astronomer.audit.search":              true,
		"astronomer.management.resource_usage": true,
		"astronomer.management.jobs":           true,
		"astronomer.task_outbox.list":          true,
		"astronomer.controllers.alerts":        true,
		"astronomer.catalog.operations":        true,
		"astronomer.argocd.operations":         true,
		"astronomer.tools.operations":          true,
		"astronomer.monitoring.operations":     true,
		"astronomer.logging.operations":        true,
		"astronomer.workloads.operations":      true,
	}
	for _, descriptor := range ReadCapabilityCatalog() {
		descriptor := descriptor
		t.Run(descriptor.Name, func(t *testing.T) {
			if !executor.SupportsCapability(context.Background(), descriptor.Name) {
				t.Fatal("production adapter is not registered")
			}
			arguments := rawArguments(t, validReadArguments(descriptor.Name))
			result, err := executor.Execute(context.Background(), descriptor, arguments)
			if err != nil {
				t.Fatalf("production read failed: %v", err)
			}
			if !json.Valid(result) || len(result) > descriptor.MaxResponseBytes {
				t.Fatalf("unbounded or invalid production result: bytes=%d valid=%t", len(result), json.Valid(result))
			}
			if strings.Contains(string(result), "SENTINEL") {
				t.Fatalf("production adapter leaked a sentinel: %s", result)
			}
			if paginated[descriptor.Name] && (!strings.Contains(string(result), `"page":2`) || !strings.Contains(string(result), `"page_size":10`)) {
				t.Fatalf("pagination contract was not preserved: %s", result)
			}
			verified, err := executor.Verify(context.Background(), descriptor, arguments, result)
			if err != nil || !verified {
				t.Fatalf("read result was not verifiable: verified=%t err=%v", verified, err)
			}
		})
	}
}

func TestProductionReadAdaptersExposeEmptyAndPartialStateWithoutFailure(t *testing.T) {
	emptyFleet, _ := NewFleetCapabilityAdapter(&fleetQueriesFake{})
	emptyQueue, _ := NewQueueCapabilityAdapter(&queueInspectorFake{queues: map[string]*asynq.QueueInfo{}, tasks: map[string]map[string]*asynq.TaskInfo{}})
	partialQueue, _ := NewQueueCapabilityAdapter(&queueInspectorFake{
		queues: map[string]*asynq.QueueInfo{}, tasks: map[string]map[string]*asynq.TaskInfo{},
		listedQueues: []string{"low"}, queueErrors: map[string]error{"low": errors.New("inspection unavailable")},
	})
	emptyOperational := operationalAdapterFixture(t, &operationalQueriesFake{settings: map[string]json.RawMessage{}})
	emptyOperational.databaseSnapshot = func(context.Context) (bool, int64, error) { return false, 0, nil }
	emptyManagement := managementAdapterFixture(t)

	cases := []struct {
		name       string
		adapter    CapabilityExecutor
		capability string
		want       string
	}{
		{"empty fleet list", emptyFleet, "astronomer.agent_fleet.list", `"items":[]`},
		{"empty tunnel errors", emptyFleet, "astronomer.tunnel.recent_errors", `"items":[]`},
		{"empty workloads", emptyManagement, "astronomer.management.workloads", `"items":[]`},
		{"empty storage", emptyManagement, "astronomer.management.storage", `"items":[]`},
		{"empty failed tasks", emptyQueue, "astronomer.queue.failed_tasks", `"items":[]`},
		{"empty pending tasks", emptyQueue, "astronomer.queue.tasks", `"items":[]`},
		{"empty queue is configured idle", emptyQueue, "astronomer.queue.health", `"materialized":false`},
		{"partial queue inspection outage", partialQueue, "astronomer.queue.health", `"available":false`},
		{"empty backups", emptyOperational, "astronomer.backups.status", `"management_backups":[]`},
		{"empty alerts", emptyOperational, "astronomer.alert.list", `"items":[]`},
		{"empty catalog repositories", emptyOperational, "astronomer.catalog.repositories", `"items":[]`},
		{"partial readiness", emptyOperational, "astronomer.installation.readiness", `"ready":false`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			descriptor, _ := capabilityByName(tc.capability)
			result, err := tc.adapter.Execute(context.Background(), descriptor, rawArguments(t, validReadArguments(tc.capability)))
			if err != nil || !json.Valid(result) || !strings.Contains(string(result), tc.want) {
				t.Fatalf("result=%s err=%v, want %s", result, err, tc.want)
			}
		})
	}
}

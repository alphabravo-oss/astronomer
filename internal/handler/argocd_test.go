package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	argocdclient "github.com/alphabravocompany/astronomer-go/internal/handler/argocd"
)

// argoCDQueryRecorder captures the calls executeOperation makes against the
// query layer so we can assert the reconciler honors upstream truth.
type argoCDQueryRecorder struct {
	mu sync.Mutex

	app             sqlc.ArgocdApplication
	instance        sqlc.ArgocdInstance
	operation       sqlc.ArgocdOperation
	operationEvents []sqlc.ArgocdOperationEvent

	progress   []sqlc.UpdateArgoCDOperationProgressParams
	completed  []sqlc.CompleteArgoCDOperationWithResultParams
	failed     []sqlc.FailArgoCDOperationWithResultParams
	appUpdate  []sqlc.UpdateArgoCDApplicationParams
	events     []sqlc.CreateArgoCDOperationEventParams
	created    []sqlc.CreateArgoCDOperationParams
	auditRows  []sqlc.CreateAuditLogV1Params
	requeued   []sqlc.RequeueArgoCDOperationParams
	runningOps []sqlc.ArgocdOperation
}

func (q *argoCDQueryRecorder) GetArgoCDApplicationByID(_ context.Context, _ uuid.UUID) (sqlc.ArgocdApplication, error) {
	return q.app, nil
}

func (q *argoCDQueryRecorder) GetArgoCDInstanceByID(_ context.Context, _ uuid.UUID) (sqlc.ArgocdInstance, error) {
	return q.instance, nil
}

func (q *argoCDQueryRecorder) UpdateArgoCDApplication(_ context.Context, arg sqlc.UpdateArgoCDApplicationParams) (sqlc.ArgocdApplication, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.appUpdate = append(q.appUpdate, arg)
	return q.app, nil
}

func (q *argoCDQueryRecorder) CreateArgoCDOperationEvent(_ context.Context, arg sqlc.CreateArgoCDOperationEventParams) (sqlc.ArgocdOperationEvent, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.events = append(q.events, arg)
	return sqlc.ArgocdOperationEvent{}, nil
}

func (q *argoCDQueryRecorder) UpdateArgoCDOperationProgress(_ context.Context, arg sqlc.UpdateArgoCDOperationProgressParams) (sqlc.ArgocdOperation, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.progress = append(q.progress, arg)
	return sqlc.ArgocdOperation{ID: arg.ID, Phase: arg.Phase, OperationID: arg.OperationID, Revision: arg.Revision, Message: arg.Message}, nil
}

func (q *argoCDQueryRecorder) CompleteArgoCDOperationWithResult(_ context.Context, arg sqlc.CompleteArgoCDOperationWithResultParams) (sqlc.ArgocdOperation, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.completed = append(q.completed, arg)
	return sqlc.ArgocdOperation{ID: arg.ID, Status: "completed", Phase: arg.Phase}, nil
}

func (q *argoCDQueryRecorder) FailArgoCDOperationWithResult(_ context.Context, arg sqlc.FailArgoCDOperationWithResultParams) (sqlc.ArgocdOperation, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.failed = append(q.failed, arg)
	return sqlc.ArgocdOperation{ID: arg.ID, Status: "failed", Phase: arg.Phase, ErrorMessage: arg.ErrorMessage}, nil
}

// The remaining ArgoCDQuerier methods are unused by the tests but required
// to satisfy the interface. They panic to make accidental calls obvious.

func (q *argoCDQueryRecorder) GetArgoCDInstanceByName(context.Context, string) (sqlc.ArgocdInstance, error) {
	panic("not used")
}
func (q *argoCDQueryRecorder) GetArgoCDApplicationByName(context.Context, sqlc.GetArgoCDApplicationByNameParams) (sqlc.ArgocdApplication, error) {
	panic("not used")
}
func (q *argoCDQueryRecorder) ListArgoCDInstances(context.Context, sqlc.ListArgoCDInstancesParams) ([]sqlc.ArgocdInstance, error) {
	return nil, nil
}
func (q *argoCDQueryRecorder) CreateArgoCDInstance(context.Context, sqlc.CreateArgoCDInstanceParams) (sqlc.ArgocdInstance, error) {
	panic("not used")
}
func (q *argoCDQueryRecorder) UpdateArgoCDInstance(context.Context, sqlc.UpdateArgoCDInstanceParams) (sqlc.ArgocdInstance, error) {
	panic("not used")
}
func (q *argoCDQueryRecorder) UpdateArgoCDInstanceHealth(context.Context, sqlc.UpdateArgoCDInstanceHealthParams) error {
	return nil
}
func (q *argoCDQueryRecorder) DeleteArgoCDInstance(context.Context, uuid.UUID) error { return nil }
func (q *argoCDQueryRecorder) CountArgoCDInstances(context.Context) (int64, error)   { return 0, nil }
func (q *argoCDQueryRecorder) ListArgoCDApplications(context.Context, sqlc.ListArgoCDApplicationsParams) ([]sqlc.ArgocdApplication, error) {
	return nil, nil
}
func (q *argoCDQueryRecorder) ListAppsByInstance(context.Context, sqlc.ListAppsByInstanceParams) ([]sqlc.ArgocdApplication, error) {
	return nil, nil
}
func (q *argoCDQueryRecorder) CountArgoCDApplications(context.Context) (int64, error) { return 0, nil }
func (q *argoCDQueryRecorder) CountAppsByInstance(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}
func (q *argoCDQueryRecorder) CreateArgoCDOperation(_ context.Context, arg sqlc.CreateArgoCDOperationParams) (sqlc.ArgocdOperation, error) {
	q.created = append(q.created, arg)
	now := time.Now().UTC()
	return sqlc.ArgocdOperation{ID: uuid.New(), TargetType: arg.TargetType, TargetKey: arg.TargetKey, OperationType: arg.OperationType, Payload: arg.Payload, Status: arg.Status, CreatedAt: now, UpdatedAt: now}, nil
}

func (q *argoCDQueryRecorder) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	q.auditRows = append(q.auditRows, arg)
	return nil
}
func (q *argoCDQueryRecorder) GetArgoCDOperation(context.Context, uuid.UUID) (sqlc.ArgocdOperation, error) {
	return q.operation, nil
}
func (q *argoCDQueryRecorder) ListArgoCDOperations(context.Context, sqlc.ListArgoCDOperationsParams) ([]sqlc.ArgocdOperation, error) {
	return nil, nil
}
func (q *argoCDQueryRecorder) CountArgoCDOperations(context.Context, sqlc.CountArgoCDOperationsParams) (int64, error) {
	return 0, nil
}
func (q *argoCDQueryRecorder) ListPendingArgoCDOperations(context.Context, int32) ([]sqlc.ArgocdOperation, error) {
	return nil, nil
}
func (q *argoCDQueryRecorder) GetLatestArgoCDOperationForTarget(context.Context, sqlc.GetLatestArgoCDOperationForTargetParams) (sqlc.ArgocdOperation, error) {
	return sqlc.ArgocdOperation{}, nil
}
func (q *argoCDQueryRecorder) MarkArgoCDOperationRunning(context.Context, uuid.UUID) (sqlc.ArgocdOperation, error) {
	return sqlc.ArgocdOperation{}, nil
}
func (q *argoCDQueryRecorder) MarkArgoCDOperationCompleted(context.Context, uuid.UUID) (sqlc.ArgocdOperation, error) {
	return sqlc.ArgocdOperation{}, nil
}
func (q *argoCDQueryRecorder) MarkArgoCDOperationFailed(context.Context, sqlc.MarkArgoCDOperationFailedParams) (sqlc.ArgocdOperation, error) {
	return sqlc.ArgocdOperation{}, nil
}
func (q *argoCDQueryRecorder) MarkArgoCDOperationSuperseded(context.Context, sqlc.MarkArgoCDOperationSupersededParams) (sqlc.ArgocdOperation, error) {
	return sqlc.ArgocdOperation{}, nil
}
func (q *argoCDQueryRecorder) RequeueArgoCDOperation(_ context.Context, arg sqlc.RequeueArgoCDOperationParams) (sqlc.ArgocdOperation, error) {
	q.requeued = append(q.requeued, arg)
	row := q.operation
	row.Payload = arg.Payload
	row.Status = OpStatusPending
	return row, nil
}
func (q *argoCDQueryRecorder) ListArgoCDOperationEvents(context.Context, uuid.UUID) ([]sqlc.ArgocdOperationEvent, error) {
	return q.operationEvents, nil
}
func (q *argoCDQueryRecorder) ListRunningArgoCDOperations(context.Context, int32) ([]sqlc.ArgocdOperation, error) {
	return q.runningOps, nil
}

// Phase B1 stubs for managed-cluster index + cluster reads.
func (q *argoCDQueryRecorder) GetClusterByID(context.Context, uuid.UUID) (sqlc.Cluster, error) {
	panic("not used")
}
func (q *argoCDQueryRecorder) CreateArgoCDManagedCluster(context.Context, sqlc.CreateArgoCDManagedClusterParams) (sqlc.ArgocdManagedCluster, error) {
	return sqlc.ArgocdManagedCluster{}, nil
}
func (q *argoCDQueryRecorder) GetArgoCDManagedCluster(context.Context, sqlc.GetArgoCDManagedClusterParams) (sqlc.ArgocdManagedCluster, error) {
	return sqlc.ArgocdManagedCluster{}, nil
}
func (q *argoCDQueryRecorder) ListArgoCDManagedClusters(context.Context, uuid.UUID) ([]sqlc.ArgocdManagedCluster, error) {
	return nil, nil
}
func (q *argoCDQueryRecorder) ListArgoCDManagedClustersByCluster(context.Context, uuid.UUID) ([]sqlc.ArgocdManagedCluster, error) {
	return nil, nil
}
func (q *argoCDQueryRecorder) DeleteArgoCDManagedCluster(context.Context, sqlc.DeleteArgoCDManagedClusterParams) error {
	return nil
}
func (q *argoCDQueryRecorder) UpdateArgoCDManagedClusterLabels(context.Context, sqlc.UpdateArgoCDManagedClusterLabelsParams) (sqlc.ArgocdManagedCluster, error) {
	return sqlc.ArgocdManagedCluster{}, nil
}
func (q *argoCDQueryRecorder) ListArgoCDBaselineOwnershipDecisions(context.Context, uuid.UUID) ([]sqlc.ArgocdBaselineOwnershipDecision, error) {
	return nil, nil
}
func (q *argoCDQueryRecorder) UpsertArgoCDBaselineOwnershipDecision(context.Context, sqlc.UpsertArgoCDBaselineOwnershipDecisionParams) (sqlc.ArgocdBaselineOwnershipDecision, error) {
	return sqlc.ArgocdBaselineOwnershipDecision{}, nil
}

// newArgoCDFixture wires a fake upstream ArgoCD HTTP server, a query
// recorder, and a handler ready for executeOperation/pollRunningOperations.
func newArgoCDFixture(t *testing.T, handler http.HandlerFunc) (*ArgoCDHandler, *argoCDQueryRecorder, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	rec := &argoCDQueryRecorder{
		app: sqlc.ArgocdApplication{
			ID:                   uuid.New(),
			ArgocdInstanceID:     uuid.New(),
			Name:                 "myapp",
			Project:              "default",
			RepoUrl:              "https://example.com/repo",
			Path:                 "manifests",
			TargetRevision:       "main",
			DestinationCluster:   "in-cluster",
			DestinationNamespace: "default",
			SyncStatus:           "OutOfSync",
			HealthStatus:         "Healthy",
		},
	}
	rec.instance = sqlc.ArgocdInstance{
		ID:                 rec.app.ArgocdInstanceID,
		Name:               "argocd",
		ApiUrl:             srv.URL,
		AuthTokenEncrypted: "test-token",
		IsHealthy:          true,
		VerifySsl:          true,
	}

	h := NewArgoCDHandler(rec)
	h.http = srv.Client()
	return h, rec, srv
}

func TestExecuteSyncCallsUpstreamAndReflectsResponse(t *testing.T) {
	var seenAuth, seenPath, seenBody string
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenPath = r.URL.Path
		raw := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(raw)
		seenBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"metadata": {"name": "myapp"},
			"status": {
				"sync": {"status": "OutOfSync", "revision": "abc123"},
				"health": {"status": "Progressing"},
				"operationState": {"phase": "Running", "message": "syncing"},
				"resources": [
					{"kind": "Deployment", "namespace": "default", "name": "web", "status": "Missing"},
					{"kind": "ConfigMap", "namespace": "default", "name": "web-config", "status": "OutOfSync"},
					{"kind": "Job", "namespace": "default", "name": "migrate", "status": "Modified"},
					{"kind": "Secret", "namespace": "default", "name": "old-secret", "status": "Synced", "requiresPruning": true},
					{"kind": "Service", "namespace": "default", "name": "web", "status": "Synced"}
				]
			}
		}`))
	})

	op := sqlc.ArgocdOperation{
		ID:            uuid.New(),
		OperationType: "sync",
		Status:        "running",
		Payload:       mustJSON(t, argocdOperationEnvelope{ApplicationID: rec.app.ID.String(), InstanceID: rec.instance.ID.String(), SyncOptions: &argocdclient.SyncOptions{Revision: "main", Prune: true}}),
	}

	res, err := h.executeOperation(context.Background(), op)
	if err != nil {
		t.Fatalf("executeOperation: %v", err)
	}
	if !res.async {
		t.Errorf("expected async result while phase=Running, got %+v", res)
	}
	if res.phase != "Running" {
		t.Errorf("phase = %q, want Running", res.phase)
	}
	if seenAuth != "Bearer test-token" {
		t.Errorf("auth = %q", seenAuth)
	}
	if seenPath != "/api/v1/applications/myapp/sync" {
		t.Errorf("path = %q", seenPath)
	}
	if seenBody == "" || !contains(seenBody, `"prune":true`) || !contains(seenBody, `"revision":"main"`) {
		t.Errorf("body = %q; missing expected fields", seenBody)
	}
	// The cached app row should have been updated to mirror upstream sync/health.
	if len(rec.appUpdate) != 1 {
		t.Fatalf("want 1 app update, got %d", len(rec.appUpdate))
	}
	if rec.appUpdate[0].SyncStatus != "OutOfSync" {
		t.Errorf("cached SyncStatus = %s; want OutOfSync (upstream truth)", rec.appUpdate[0].SyncStatus)
	}
	if rec.appUpdate[0].HealthStatus != "Progressing" {
		t.Errorf("cached HealthStatus = %s; want Progressing", rec.appUpdate[0].HealthStatus)
	}
	if rec.appUpdate[0].ResourceCreatedCount != 1 || rec.appUpdate[0].ResourceChangedCount != 2 || rec.appUpdate[0].ResourcePrunedCount != 1 {
		t.Errorf("cached resource counts = created:%d changed:%d pruned:%d", rec.appUpdate[0].ResourceCreatedCount, rec.appUpdate[0].ResourceChangedCount, rec.appUpdate[0].ResourcePrunedCount)
	}
	if rec.appUpdate[0].LastSynced.Valid {
		t.Errorf("LastSynced should not be stamped while sync is in flight")
	}
}

func TestBuildArgoCDOrphanReportDetectsStaleBaselineApplications(t *testing.T) {
	instanceID := uuid.New()
	managedClusterID := uuid.New()
	report := buildArgoCDOrphanReport(instanceID, []sqlc.ArgocdApplication{
		{
			ID:                   uuid.New(),
			Name:                 "astronomer-cert-manager-prod",
			DestinationCluster:   "https://proxy.example.test/clusters/prod",
			DestinationNamespace: "cert-manager",
		},
		{
			ID:                   uuid.New(),
			Name:                 "astronomer-trivy-deleted",
			DestinationCluster:   "https://proxy.example.test/clusters/deleted",
			DestinationNamespace: "trivy-system",
		},
		{
			ID:                 uuid.New(),
			Name:               "astronomer-fluent-bit-empty",
			DestinationCluster: "",
		},
		{
			ID:                 uuid.New(),
			Name:               "user-owned-app",
			DestinationCluster: "https://proxy.example.test/clusters/deleted",
		},
	}, nil, []sqlc.ArgocdManagedCluster{
		{
			ArgocdInstanceID:  instanceID,
			ClusterID:         managedClusterID,
			ClusterSecretName: "prod",
			ServerUrl:         "https://proxy.example.test/clusters/prod",
			Labels:            mustJSON(t, map[string]string{"astronomer.io/cluster-name": "prod", "astronomer.io/cluster-id": managedClusterID.String()}),
		},
	})

	if report.InstanceID != instanceID.String() {
		t.Fatalf("instance id = %q, want %q", report.InstanceID, instanceID.String())
	}
	if report.ApplicationCount != 4 {
		t.Fatalf("application count = %d, want 4", report.ApplicationCount)
	}
	if report.CachedApplicationCount != 4 || report.LiveApplicationCount != 0 {
		t.Fatalf("cache/live counts = %d/%d, want 4/0", report.CachedApplicationCount, report.LiveApplicationCount)
	}
	if report.ManagedTargetCount != 3 {
		t.Fatalf("managed target count = %d, want 3", report.ManagedTargetCount)
	}
	if report.OrphanApplicationCount != 2 {
		t.Fatalf("orphan count = %d, want 2 (%+v)", report.OrphanApplicationCount, report.OrphanApplications)
	}
	if report.OrphanApplications[0].Name != "astronomer-trivy-deleted" || report.OrphanApplications[0].ComponentSlug != "trivy-operator" {
		t.Fatalf("first orphan = %+v, want trivy baseline app", report.OrphanApplications[0])
	}
	if report.OrphanApplications[0].Source != argoCDOrphanSourceCache {
		t.Fatalf("first orphan source = %q, want cache", report.OrphanApplications[0].Source)
	}
	if report.OrphanApplications[0].Reason != argoCDOrphanReasonStaleDestination {
		t.Fatalf("first orphan reason = %q, want %q", report.OrphanApplications[0].Reason, argoCDOrphanReasonStaleDestination)
	}
	if report.OrphanApplications[1].Reason != argoCDOrphanReasonMissingDestination {
		t.Fatalf("second orphan reason = %q, want %q", report.OrphanApplications[1].Reason, argoCDOrphanReasonMissingDestination)
	}
}

func TestBuildArgoCDOrphanReportDetectsLiveArgoApplications(t *testing.T) {
	instanceID := uuid.New()
	managedClusterID := uuid.New()
	report := buildArgoCDOrphanReport(instanceID, nil, []argocdclient.Application{
		mustArgoApplication(t, `{
			"metadata": {
				"name": "astronomer-cert-manager-prod",
				"labels": {
					"app.kubernetes.io/managed-by": "astronomer",
					"astronomer.io/baseline": "platform",
					"astronomer.io/tool-slug": "cert-manager"
				},
				"ownerReferences": [{"apiVersion": "argoproj.io/v1alpha1", "kind": "ApplicationSet", "name": "astronomer-baseline-cert-manager"}]
			},
			"spec": {"destination": {"server": "https://proxy.example.test/clusters/prod", "namespace": "cert-manager"}}
		}`),
		mustArgoApplication(t, `{
			"metadata": {
				"name": "astronomer-externally-managed",
				"labels": {"app.kubernetes.io/managed-by": "astronomer"}
			},
			"spec": {"destination": {"server": "https://proxy.example.test/clusters/deleted", "namespace": "default"}}
		}`),
		mustArgoApplication(t, `{
			"metadata": {
				"name": "astronomer-trivy-prod",
				"labels": {
					"app.kubernetes.io/managed-by": "astronomer",
					"astronomer.io/baseline": "platform",
					"astronomer.io/tool-slug": "fluent-bit"
				},
				"ownerReferences": [{"apiVersion": "argoproj.io/v1alpha1", "kind": "ApplicationSet", "name": "stale-trivy-owner"}]
			},
			"spec": {"destination": {"server": "https://proxy.example.test/clusters/prod", "namespace": "trivy-system"}}
		}`),
		mustArgoApplication(t, `{
			"metadata": {"name": "user-owned-app"},
			"spec": {"destination": {"server": "https://proxy.example.test/clusters/deleted", "namespace": "default"}}
		}`),
		mustArgoApplication(t, `{
			"metadata": {
				"name": "user-appset-owned-app",
				"ownerReferences": [{"apiVersion": "argoproj.io/v1alpha1", "kind": "ApplicationSet", "name": "customer-appset"}]
			},
			"spec": {"destination": {"server": "https://proxy.example.test/clusters/deleted", "namespace": "default"}}
		}`),
	}, []sqlc.ArgocdManagedCluster{
		{
			ArgocdInstanceID:  instanceID,
			ClusterID:         managedClusterID,
			ClusterSecretName: "prod",
			ServerUrl:         "https://proxy.example.test/clusters/prod",
			Labels:            mustJSON(t, map[string]string{"astronomer.io/cluster-name": "prod", "astronomer.io/cluster-id": managedClusterID.String()}),
		},
	})

	if report.CachedApplicationCount != 0 || report.LiveApplicationCount != 5 || report.ApplicationCount != 5 {
		t.Fatalf("counts = cache:%d live:%d total:%d, want 0/5/5", report.CachedApplicationCount, report.LiveApplicationCount, report.ApplicationCount)
	}
	if report.OrphanApplicationCount != 2 {
		t.Fatalf("orphan count = %d, want 2 (%+v)", report.OrphanApplicationCount, report.OrphanApplications)
	}
	if report.OrphanApplications[0].Name != "astronomer-externally-managed" || report.OrphanApplications[0].Reason != argoCDOrphanReasonLiveStaleDestination {
		t.Fatalf("first orphan = %+v, want live stale destination", report.OrphanApplications[0])
	}
	if report.OrphanApplications[0].Source != argoCDOrphanSourceLive {
		t.Fatalf("first orphan source = %q, want live", report.OrphanApplications[0].Source)
	}
	if report.OrphanApplications[1].Name != "astronomer-trivy-prod" || report.OrphanApplications[1].Reason != argoCDOrphanReasonStaleApplicationSetOwner {
		t.Fatalf("second orphan = %+v, want stale ApplicationSet metadata", report.OrphanApplications[1])
	}
	if report.OrphanApplications[1].ApplicationSetName != "astronomer-baseline-trivy" {
		t.Fatalf("second orphan appset = %q, want astronomer-baseline-trivy", report.OrphanApplications[1].ApplicationSetName)
	}
}

func TestPollRunningOperationCompletesOnSucceeded(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"metadata": {"name": "myapp"},
			"status": {
				"sync": {"status": "Synced", "revision": "deadbeef"},
				"health": {"status": "Healthy"},
				"operationState": {
					"phase": "Succeeded",
					"finishedAt": "2026-05-08T12:01:00Z",
					"syncResult": {"revision": "deadbeef"}
				}
			}
		}`))
	})

	// Replace the recorder's ListRunningArgoCDOperations with a single op so
	// pollRunningOperations has work to do.
	op := sqlc.ArgocdOperation{
		ID:            uuid.New(),
		OperationType: "sync",
		Status:        "running",
		Phase:         "Running",
		PollAttempts:  3,
		StartedAt:     pgtype.Timestamptz{Time: time.Now().Add(-1 * time.Minute), Valid: true},
		Payload:       mustJSON(t, argocdOperationEnvelope{ApplicationID: rec.app.ID.String(), InstanceID: rec.instance.ID.String()}),
	}
	rec.injectRunning(op)

	h.pollRunningOperations(context.Background())

	if len(rec.completed) != 1 {
		t.Fatalf("want 1 completion, got %d (failed=%d, progress=%d)", len(rec.completed), len(rec.failed), len(rec.progress))
	}
	if rec.completed[0].Phase != "Succeeded" {
		t.Errorf("phase = %s", rec.completed[0].Phase)
	}
	if rec.completed[0].Revision != "deadbeef" {
		t.Errorf("revision = %s", rec.completed[0].Revision)
	}
}

func TestPollRunningOperationFailsOnTerminalFailed(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"metadata": {"name": "myapp"},
			"status": {
				"sync": {"status": "OutOfSync"},
				"operationState": {"phase": "Failed", "message": "manifest error"}
			}
		}`))
	})
	op := sqlc.ArgocdOperation{
		ID:            uuid.New(),
		OperationType: "sync",
		Status:        "running",
		Phase:         "Running",
		Payload:       mustJSON(t, argocdOperationEnvelope{ApplicationID: rec.app.ID.String(), InstanceID: rec.instance.ID.String()}),
	}
	rec.injectRunning(op)
	h.pollRunningOperations(context.Background())

	if len(rec.failed) != 1 {
		t.Fatalf("want 1 fail, got %d", len(rec.failed))
	}
	if rec.failed[0].Phase != "Failed" {
		t.Errorf("phase = %s", rec.failed[0].Phase)
	}
	if rec.failed[0].Message != "Argo CD sync failed" {
		t.Errorf("message = %s", rec.failed[0].Message)
	}
}

func TestPollRunningOperationCapsAttempts(t *testing.T) {
	h, rec, _ := newArgoCDFixture(t, func(w http.ResponseWriter, r *http.Request) {
		// Should not be called: the cap fires before any HTTP request.
		t.Error("upstream should not be called once the poll cap is hit")
	})
	op := sqlc.ArgocdOperation{
		ID:            uuid.New(),
		OperationType: "sync",
		Status:        "running",
		Phase:         "Running",
		PollAttempts:  MaxArgoCDOperationPolls,
		Payload:       mustJSON(t, argocdOperationEnvelope{ApplicationID: rec.app.ID.String(), InstanceID: rec.instance.ID.String()}),
	}
	rec.injectRunning(op)
	h.pollRunningOperations(context.Background())

	if len(rec.failed) != 1 {
		t.Fatalf("want timeout fail, got %d", len(rec.failed))
	}
	if rec.failed[0].Message == "" || !contains(rec.failed[0].Message, "timed out") {
		t.Errorf("message = %q; want timeout message", rec.failed[0].Message)
	}
}

func (q *argoCDQueryRecorder) injectRunning(op sqlc.ArgocdOperation) {
	q.runningOps = []sqlc.ArgocdOperation{op}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func mustArgoApplication(t *testing.T, raw string) argocdclient.Application {
	t.Helper()
	var app argocdclient.Application
	if err := json.Unmarshal([]byte(raw), &app); err != nil {
		t.Fatalf("unmarshal Argo application fixture: %v", err)
	}
	return app
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

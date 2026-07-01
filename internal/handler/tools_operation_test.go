package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type toolHelmStub struct {
	statusResult *protocol.HelmResultPayload
	statusErr    error
	doResult     *protocol.HelmResultPayload
	doErr        error
	statusCalls  int
	doCalls      int
}

func (h *toolHelmStub) Do(ctx context.Context, clusterID string, msgType protocol.MessageType, payload protocol.HelmRequestPayload) (*protocol.HelmResultPayload, error) {
	h.doCalls++
	return h.doResult, h.doErr
}

func (h *toolHelmStub) Status(ctx context.Context, clusterID, releaseName, namespace string) (*protocol.HelmResultPayload, error) {
	h.statusCalls++
	return h.statusResult, h.statusErr
}

type toolQueryRecorder struct {
	clusterID       uuid.UUID
	installedBySlug map[string]sqlc.InstalledChart
	installedByRef  map[string]sqlc.InstalledChart
	created         []sqlc.CreateInstalledChartParams
	adopted         []sqlc.AdoptInstalledChartByReleaseParams
	events          []sqlc.CreateToolOperationEventParams
	operations      []sqlc.CreateToolOperationParams
	idemOperations  []sqlc.CreateToolOperationIdempotentParams
	idemByKey       map[string]sqlc.ToolOperation
	valuesUpdates   []sqlc.UpdateInstalledChartValuesParams
}

func newToolQueryRecorder(clusterID uuid.UUID) *toolQueryRecorder {
	return &toolQueryRecorder{
		clusterID:       clusterID,
		installedBySlug: map[string]sqlc.InstalledChart{},
		installedByRef:  map[string]sqlc.InstalledChart{},
		idemByKey:       map[string]sqlc.ToolOperation{},
	}
}

func installedRefKey(clusterID uuid.UUID, releaseName, namespace string) string {
	return clusterID.String() + "|" + releaseName + "|" + namespace
}

func (q *toolQueryRecorder) GetClusterByID(context.Context, uuid.UUID) (sqlc.Cluster, error) {
	return sqlc.Cluster{}, nil
}
func (q *toolQueryRecorder) GetClusterToolByID(context.Context, uuid.UUID) (sqlc.ClusterTool, error) {
	return sqlc.ClusterTool{}, pgx.ErrNoRows
}
func (q *toolQueryRecorder) GetToolBySlug(context.Context, string) (sqlc.ClusterTool, error) {
	return sqlc.ClusterTool{}, pgx.ErrNoRows
}
func (q *toolQueryRecorder) ListClusterTools(context.Context, sqlc.ListClusterToolsParams) ([]sqlc.ClusterTool, error) {
	return nil, nil
}
func (q *toolQueryRecorder) ListEnabledTools(context.Context) ([]sqlc.ClusterTool, error) {
	return nil, nil
}
func (q *toolQueryRecorder) CountClusterTools(context.Context) (int64, error) { return 0, nil }
func (q *toolQueryRecorder) CountInstalledCharts(context.Context) (int64, error) {
	return int64(len(q.installedByRef)), nil
}
func (q *toolQueryRecorder) ListInstalledChartsByCluster(context.Context, sqlc.ListInstalledChartsByClusterParams) ([]sqlc.InstalledChart, error) {
	items := make([]sqlc.InstalledChart, 0, len(q.installedBySlug))
	for _, item := range q.installedBySlug {
		items = append(items, item)
	}
	return items, nil
}
func (q *toolQueryRecorder) GetInstalledChartByRelease(_ context.Context, arg sqlc.GetInstalledChartByReleaseParams) (sqlc.InstalledChart, error) {
	item, ok := q.installedByRef[installedRefKey(arg.ClusterID, arg.ReleaseName, arg.Namespace)]
	if !ok {
		return sqlc.InstalledChart{}, pgx.ErrNoRows
	}
	return item, nil
}
func (q *toolQueryRecorder) GetInstalledChartByClusterAndTool(_ context.Context, arg sqlc.GetInstalledChartByClusterAndToolParams) (sqlc.InstalledChart, error) {
	item, ok := q.installedBySlug[arg.ToolSlug]
	if !ok {
		return sqlc.InstalledChart{}, pgx.ErrNoRows
	}
	return item, nil
}
func (q *toolQueryRecorder) CreateInstalledChart(_ context.Context, arg sqlc.CreateInstalledChartParams) (sqlc.InstalledChart, error) {
	q.created = append(q.created, arg)
	item := sqlc.InstalledChart{
		ID:          uuid.New(),
		ClusterID:   arg.ClusterID,
		ReleaseName: arg.ReleaseName,
		Namespace:   arg.Namespace,
		Status:      arg.Status,
		Revision:    arg.Revision,
		ToolSlug:    arg.ToolSlug,
		PresetUsed:  arg.PresetUsed,
	}
	q.installedByRef[installedRefKey(arg.ClusterID, arg.ReleaseName, arg.Namespace)] = item
	if arg.ToolSlug.Valid {
		q.installedBySlug[arg.ToolSlug.String] = item
	}
	return item, nil
}
func (q *toolQueryRecorder) UpdateInstalledChartStatus(context.Context, sqlc.UpdateInstalledChartStatusParams) error {
	return nil
}
func (q *toolQueryRecorder) AdoptInstalledChartByRelease(_ context.Context, arg sqlc.AdoptInstalledChartByReleaseParams) (sqlc.InstalledChart, error) {
	q.adopted = append(q.adopted, arg)
	item := sqlc.InstalledChart{
		ID:             uuid.New(),
		ClusterID:      arg.ClusterID,
		ReleaseName:    arg.ReleaseName,
		Namespace:      arg.Namespace,
		ValuesOverride: arg.ValuesOverride,
		Status:         arg.Status,
		Revision:       arg.Revision,
		ToolSlug:       arg.ToolSlug,
		PresetUsed:     arg.PresetUsed,
	}
	q.installedByRef[installedRefKey(arg.ClusterID, arg.ReleaseName, arg.Namespace)] = item
	if arg.ToolSlug.Valid {
		q.installedBySlug[arg.ToolSlug.String] = item
	}
	return item, nil
}
func (q *toolQueryRecorder) UpdateInstalledChartValues(_ context.Context, arg sqlc.UpdateInstalledChartValuesParams) (sqlc.InstalledChart, error) {
	q.valuesUpdates = append(q.valuesUpdates, arg)
	return sqlc.InstalledChart{
		ID:             arg.ID,
		ValuesOverride: arg.ValuesOverride,
		Status:         arg.Status,
		Revision:       arg.Revision,
	}, nil
}
func (q *toolQueryRecorder) DeleteInstalledChart(context.Context, uuid.UUID) error { return nil }
func (q *toolQueryRecorder) CreateToolOperation(_ context.Context, arg sqlc.CreateToolOperationParams) (sqlc.ToolOperation, error) {
	q.operations = append(q.operations, arg)
	return sqlc.ToolOperation{
		ID:            uuid.New(),
		TargetType:    arg.TargetType,
		TargetKey:     arg.TargetKey,
		OperationType: arg.OperationType,
		Status:        arg.Status,
		CreatedByID:   arg.CreatedByID,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}, nil
}
func (q *toolQueryRecorder) CreateToolOperationIdempotent(_ context.Context, arg sqlc.CreateToolOperationIdempotentParams) (sqlc.ToolOperation, error) {
	q.idemOperations = append(q.idemOperations, arg)
	key := arg.Scope + "|" + arg.IdempotencyKey
	if op, ok := q.idemByKey[key]; ok {
		return op, nil
	}
	op := sqlc.ToolOperation{
		ID:            uuid.New(),
		TargetType:    arg.TargetType,
		TargetKey:     arg.TargetKey,
		OperationType: arg.OperationType,
		Status:        arg.Status,
		CreatedByID:   arg.CreatedByID,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	q.idemByKey[key] = op
	return op, nil
}
func (q *toolQueryRecorder) GetToolOperation(context.Context, uuid.UUID) (sqlc.ToolOperation, error) {
	return sqlc.ToolOperation{}, nil
}
func (q *toolQueryRecorder) ListToolOperations(context.Context, sqlc.ListToolOperationsParams) ([]sqlc.ToolOperation, error) {
	return nil, nil
}
func (q *toolQueryRecorder) ListPendingToolOperations(context.Context, int32) ([]sqlc.ToolOperation, error) {
	return nil, nil
}
func (q *toolQueryRecorder) GetLatestToolOperationForTarget(context.Context, sqlc.GetLatestToolOperationForTargetParams) (sqlc.ToolOperation, error) {
	return sqlc.ToolOperation{}, nil
}
func (q *toolQueryRecorder) MarkToolOperationRunning(context.Context, uuid.UUID) (sqlc.ToolOperation, error) {
	return sqlc.ToolOperation{}, nil
}
func (q *toolQueryRecorder) MarkToolOperationCompleted(context.Context, uuid.UUID) (sqlc.ToolOperation, error) {
	return sqlc.ToolOperation{}, nil
}
func (q *toolQueryRecorder) MarkToolOperationFailed(context.Context, sqlc.MarkToolOperationFailedParams) (sqlc.ToolOperation, error) {
	return sqlc.ToolOperation{}, nil
}
func (q *toolQueryRecorder) MarkToolOperationSuperseded(context.Context, sqlc.MarkToolOperationSupersededParams) (sqlc.ToolOperation, error) {
	return sqlc.ToolOperation{}, nil
}
func (q *toolQueryRecorder) RequeueToolOperation(context.Context, uuid.UUID) (sqlc.ToolOperation, error) {
	return sqlc.ToolOperation{}, nil
}
func (q *toolQueryRecorder) CreateToolOperationEvent(_ context.Context, arg sqlc.CreateToolOperationEventParams) (sqlc.ToolOperationEvent, error) {
	q.events = append(q.events, arg)
	return sqlc.ToolOperationEvent{}, nil
}
func (q *toolQueryRecorder) ListToolOperationEvents(context.Context, uuid.UUID) ([]sqlc.ToolOperationEvent, error) {
	return nil, nil
}

func TestExistingHelmReleaseStatusTreatsNotFoundAsAbsent(t *testing.T) {
	t.Parallel()

	status, exists, err := existingHelmReleaseStatus(context.Background(), &toolHelmStub{
		statusErr: errors.New("release: not found"),
	}, "cluster-1", "argocd", "argocd")
	if err != nil {
		t.Fatalf("existingHelmReleaseStatus() error = %v", err)
	}
	if exists {
		t.Fatal("expected release to be absent")
	}
	if status != nil {
		t.Fatalf("expected nil status, got %+v", status)
	}
}

func TestExecuteOperationInstallAdoptsExistingHelmRelease(t *testing.T) {
	t.Parallel()

	clusterID := uuid.New()
	queries := newToolQueryRecorder(clusterID)
	helm := &toolHelmStub{
		statusResult: &protocol.HelmResultPayload{
			Success:     true,
			ReleaseName: "argocd",
			Namespace:   "argocd",
			Status:      "deployed",
			Revision:    7,
		},
	}
	h := &ToolHandler{queries: queries, helm: helm}

	env := toolOperationEnvelope{
		ClusterID:   clusterID.String(),
		ToolSlug:    "argocd",
		ReleaseName: "argocd",
		Namespace:   "argocd",
		Preset:      "default",
	}
	payload, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal env: %v", err)
	}
	op := sqlc.ToolOperation{
		ID:            uuid.New(),
		TargetType:    "tool_installation",
		TargetKey:     clusterID.String() + ":argocd",
		OperationType: "install",
		Payload:       payload,
		Status:        "running",
		CreatedAt:     time.Now().UTC(),
	}

	if err := h.executeOperation(context.Background(), op); err != nil {
		t.Fatalf("executeOperation() error = %v", err)
	}
	if helm.statusCalls != 1 {
		t.Fatalf("statusCalls = %d, want 1", helm.statusCalls)
	}
	if helm.doCalls != 0 {
		t.Fatalf("doCalls = %d, want 0", helm.doCalls)
	}
	if len(queries.created) != 1 {
		t.Fatalf("created rows = %d, want 1", len(queries.created))
	}
	created := queries.created[0]
	if created.ReleaseName != "argocd" || created.Namespace != "argocd" {
		t.Fatalf("created release = %s/%s", created.Namespace, created.ReleaseName)
	}
	if created.Status != "installed" {
		t.Fatalf("created status = %q, want installed", created.Status)
	}
	if created.Revision != 7 {
		t.Fatalf("created revision = %d, want 7", created.Revision)
	}
	if !created.ToolSlug.Valid || created.ToolSlug.String != "argocd" {
		t.Fatalf("created tool slug = %+v", created.ToolSlug)
	}
	if !created.PresetUsed.Valid || created.PresetUsed.String != "default" {
		t.Fatalf("created preset = %+v", created.PresetUsed)
	}
}

func TestAdoptExistingToolReleaseUpdatesExistingRow(t *testing.T) {
	t.Parallel()

	clusterID := uuid.New()
	queries := newToolQueryRecorder(clusterID)
	queries.installedByRef[installedRefKey(clusterID, "argocd", "argocd")] = sqlc.InstalledChart{
		ID:          uuid.New(),
		ClusterID:   clusterID,
		ReleaseName: "argocd",
		Namespace:   "argocd",
		Status:      "installed_unmanaged",
		Revision:    1,
	}

	err := adoptExistingToolRelease(context.Background(), queries, clusterID, toolOperationEnvelope{
		ClusterID:      clusterID.String(),
		ToolSlug:       "argocd",
		ReleaseName:    "argocd",
		Namespace:      "argocd",
		ValuesYAML:     "server:\n  insecure: true\n",
		Preset:         "default",
		InstalledChart: nil,
	}, &protocol.HelmResultPayload{
		Status:   "deployed",
		Revision: 3,
	})
	if err != nil {
		t.Fatalf("adoptExistingToolRelease() error = %v", err)
	}
	if len(queries.adopted) != 1 {
		t.Fatalf("adopted rows = %d, want 1", len(queries.adopted))
	}
	adopted := queries.adopted[0]
	if adopted.Status != "installed" {
		t.Fatalf("adopted status = %q, want installed", adopted.Status)
	}
	if adopted.Revision != 3 {
		t.Fatalf("adopted revision = %d, want 3", adopted.Revision)
	}
	if !adopted.ToolSlug.Valid || adopted.ToolSlug.String != "argocd" {
		t.Fatalf("adopted tool slug = %+v", adopted.ToolSlug)
	}
	if !adopted.PresetUsed.Valid || adopted.PresetUsed.String != "default" {
		t.Fatalf("adopted preset = %+v", adopted.PresetUsed)
	}
}

func TestExecuteOperationUpgradePersistsHelmRevision(t *testing.T) {
	t.Parallel()

	clusterID := uuid.New()
	chartID := uuid.New()
	queries := newToolQueryRecorder(clusterID)
	helm := &toolHelmStub{
		// Helm returns the real post-upgrade revision (e.g. after an
		// out-of-band rollback the next upgrade jumps to 9, not prev+1).
		doResult:     &protocol.HelmResultPayload{Success: true, Status: "deployed", Revision: 9},
		statusResult: &protocol.HelmResultPayload{Success: true, Status: "deployed", Revision: 9},
	}
	h := &ToolHandler{queries: queries, helm: helm}

	env := toolOperationEnvelope{
		ClusterID:      clusterID.String(),
		ToolSlug:       "argocd",
		ReleaseName:    "argocd",
		Namespace:      "argocd",
		ValuesYAML:     "server:\n  insecure: true\n",
		InstalledChart: &chartID,
	}
	payload, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal env: %v", err)
	}
	op := sqlc.ToolOperation{
		ID:            uuid.New(),
		OperationType: "upgrade",
		Payload:       payload,
		Status:        "running",
		CreatedAt:     time.Now().UTC(),
	}

	if err := h.executeOperation(context.Background(), op); err != nil {
		t.Fatalf("executeOperation() error = %v", err)
	}
	if len(queries.valuesUpdates) != 1 {
		t.Fatalf("values updates = %d, want 1", len(queries.valuesUpdates))
	}
	if got := queries.valuesUpdates[0].Revision; got != 9 {
		t.Fatalf("persisted revision = %d, want 9 (the real Helm revision)", got)
	}
	if queries.valuesUpdates[0].ID != chartID {
		t.Fatalf("updated chart id = %s, want %s", queries.valuesUpdates[0].ID, chartID)
	}
}

func TestCheckToolReleaseReadyReflectsReadiness(t *testing.T) {
	t.Parallel()

	clusterID := uuid.New()
	env := toolOperationEnvelope{
		ClusterID:   clusterID.String(),
		ToolSlug:    "argocd",
		ReleaseName: "argocd",
		Namespace:   "argocd",
	}
	op := sqlc.ToolOperation{ID: uuid.New(), OperationType: "install"}

	// Not-ready release: helm reports a non-"deployed" status. The Helm
	// apply already committed, so readiness must NOT fail the operation;
	// it records a warning and lets the drift sweep reconcile readiness.
	notReady := newToolQueryRecorder(clusterID)
	hNotReady := &ToolHandler{queries: notReady, helm: &toolHelmStub{
		statusResult: &protocol.HelmResultPayload{Success: true, Status: "pending-install"},
	}}
	if err := hNotReady.checkToolReleaseReady(context.Background(), op, env); err != nil {
		t.Fatalf("checkToolReleaseReady() error = %v, want nil", err)
	}
	if !hasReadinessEvent(notReady.events, "warn") {
		t.Fatalf("expected a readiness warn event, got %+v", notReady.events)
	}

	// Ready release: helm reports "deployed", readiness passes and the
	// recorded event is informational.
	ready := newToolQueryRecorder(clusterID)
	hReady := &ToolHandler{queries: ready, helm: &toolHelmStub{
		statusResult: &protocol.HelmResultPayload{Success: true, Status: "deployed", Revision: 2},
	}}
	if err := hReady.checkToolReleaseReady(context.Background(), op, env); err != nil {
		t.Fatalf("checkToolReleaseReady() error = %v, want nil", err)
	}
	if !hasReadinessEvent(ready.events, "info") {
		t.Fatalf("expected a readiness info event, got %+v", ready.events)
	}
}

func hasReadinessEvent(events []sqlc.CreateToolOperationEventParams, level string) bool {
	for _, e := range events {
		if e.Stage == "readiness" && e.Level == level {
			return true
		}
	}
	return false
}

var _ ToolQuerier = (*toolQueryRecorder)(nil)

func TestToolEnqueueOperationUsesIdempotencyKey(t *testing.T) {
	t.Parallel()

	userID := uuid.New()
	q := newToolQueryRecorder(uuid.New())
	h := NewToolHandler(q)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tools/install", nil)
	req.Header.Set("Idempotency-Key", "retry-1")
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: userID.String()}))
	ctx := withOperationIdempotency(req, "tools")
	env := toolOperationEnvelope{ClusterID: q.clusterID.String(), ToolSlug: "prometheus"}

	first, err := h.enqueueOperation(ctx, "tool_installation", "cluster/prometheus", "install", env, currentUserUUID(req))
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	second, err := h.enqueueOperation(ctx, "tool_installation", "cluster/prometheus", "install", env, currentUserUUID(req))
	if err != nil {
		t.Fatalf("second enqueue: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("operation id = %s then %s, want replay to return original operation", first.ID, second.ID)
	}
	if len(q.operations) != 0 {
		t.Fatalf("plain operations = %d, want idempotent path only", len(q.operations))
	}
	if len(q.idemOperations) != 2 {
		t.Fatalf("idempotent operations = %d, want 2 calls", len(q.idemOperations))
	}
	if q.idemOperations[0].Scope == "" || q.idemOperations[0].IdempotencyKey != "retry-1" {
		t.Fatalf("idempotency params = %+v", q.idemOperations[0])
	}
}

var _ HelmRequester = (*toolHelmStub)(nil)
var _ = pgtype.Text{}

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type plannedHelm struct {
	releases    map[string]protocol.HelmResultPayload
	markers     map[string]string
	calls       []string
	failRelease string
}

func (h *plannedHelm) Status(_ context.Context, _, release, namespace string) (*protocol.HelmResultPayload, error) {
	status, ok := h.releases[namespace+"/"+release]
	if !ok {
		return nil, errors.New("release: not found")
	}
	return &status, nil
}
func (h *plannedHelm) History(ctx context.Context, cluster, release, namespace string) (*protocol.HelmResultPayload, error) {
	status, err := h.Status(ctx, cluster, release, namespace)
	if err != nil {
		return nil, err
	}
	return &protocol.HelmResultPayload{Success: true, Revisions: []protocol.HelmRevision{{Revision: status.Revision, Status: status.Status, Description: h.markers[namespace+"/"+release]}}}, nil
}
func (h *plannedHelm) Do(_ context.Context, _ string, message protocol.MessageType, request protocol.HelmRequestPayload) (*protocol.HelmResultPayload, error) {
	h.calls = append(h.calls, string(message)+":"+request.ReleaseName)
	if request.ReleaseName == h.failRelease {
		return nil, errors.New("chart rejected")
	}
	key := request.Namespace + "/" + request.ReleaseName
	status := protocol.HelmResultPayload{Success: true, ReleaseName: request.ReleaseName, Namespace: request.Namespace, Status: "deployed", Revision: h.releases[key].Revision + 1}
	if message == protocol.MsgHelmUninstall {
		delete(h.releases, key)
		status.Status = "uninstalled"
	} else {
		h.releases[key] = status
		h.markers[key] = request.Description
	}
	return &status, nil
}

type planQueries struct {
	*toolQueryRecorder
	failCheckpoint string
	leaseLost      bool
}

func (q *planQueries) CheckpointToolOperation(ctx context.Context, arg sqlc.CheckpointToolOperationParams) (sqlc.CheckpointToolOperationRow, error) {
	if q.leaseLost {
		return sqlc.CheckpointToolOperationRow{}, pgx.ErrNoRows
	}
	if arg.EventStage == q.failCheckpoint {
		q.failCheckpoint = ""
		return sqlc.CheckpointToolOperationRow{}, errors.New("database unavailable")
	}
	q.events = append(q.events, sqlc.CreateToolOperationEventParams{OperationID: arg.ID, Level: arg.EventLevel, Stage: arg.EventStage, Message: arg.EventMessage, Detail: arg.EventDetail})
	return q.toolQueryRecorder.CheckpointToolOperation(ctx, arg)
}
func (q *planQueries) DeleteInstalledChart(_ context.Context, id uuid.UUID) error {
	for key, item := range q.installedByRef {
		if item.ID == id {
			delete(q.installedByRef, key)
			delete(q.installedBySlug, item.ToolSlug.String)
			return nil
		}
	}
	return pgx.ErrNoRows
}

func newPlanFixture(t *testing.T, count int) (*ToolHandler, *planQueries, *plannedHelm, sqlc.ToolOperation) {
	t.Helper()
	id := uuid.New()
	queries := &planQueries{toolQueryRecorder: newToolQueryRecorder(id)}
	helm := &plannedHelm{releases: map[string]protocol.HelmResultPayload{}, markers: map[string]string{}}
	env := toolOperationEnvelope{ClusterID: id.String(), ToolSlug: "istio", Preset: "default", Releases: []toolRelease{
		{ReleaseName: "istio-base", Namespace: "istio-system", ChartName: "base", RepoURL: "https://blob.istio.io/istio-release/charts", Version: "1.31.0", State: "pending"},
		{ReleaseName: "istiod", Namespace: "istio-system", ChartName: "istiod", RepoURL: "https://blob.istio.io/istio-release/charts", Version: "1.31.0", State: "pending"},
	}}
	env.Releases = env.Releases[:count]
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return NewToolHandlerWithHelm(queries, helm), queries, helm, sqlc.ToolOperation{ID: uuid.New(), OperationType: "install", Payload: raw, Status: "running", AttemptCount: 1}
}

func TestToolPlanInstallAndRetryResumeAtFailedRelease(t *testing.T) {
	h, q, helm, op := newPlanFixture(t, 2)
	helm.failRelease = "istiod"
	if err := h.executeOperation(context.Background(), op); err == nil {
		t.Fatal("expected second release failure")
	}
	var checkpoint toolOperationEnvelope
	if err := json.Unmarshal(q.checkpoints[op.ID], &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.Releases[0].State != "completed" || checkpoint.Releases[1].State != "failed" {
		t.Fatalf("checkpoint=%+v", checkpoint.Releases)
	}
	helm.failRelease = ""
	op.Payload = q.checkpoints[op.ID]
	op.AttemptCount++
	if err := h.executeOperation(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	want := []string{"HELM_INSTALL:istio-base", "HELM_INSTALL:istiod", "HELM_INSTALL:istiod"}
	if !reflect.DeepEqual(helm.calls, want) {
		t.Fatalf("calls=%v, want %v", helm.calls, want)
	}
	if len(q.installedByRef) != 2 {
		t.Fatalf("installed release rows=%d", len(q.installedByRef))
	}
	for _, event := range q.events {
		var detail map[string]any
		if err := json.Unmarshal(event.Detail, &detail); err != nil {
			t.Fatal(err)
		}
		if detail["releaseName"] == nil || detail["stepIndex"] == nil || detail["stepCount"] != float64(2) {
			t.Errorf("missing release identity: %s", event.Detail)
		}
	}
}

func TestToolPlanCrashAfterHelmBeforeCheckpointDoesNotRepeatInstall(t *testing.T) {
	h, q, helm, op := newPlanFixture(t, 1)
	q.failCheckpoint = "release.completed"
	if err := h.executeOperation(context.Background(), op); err == nil {
		t.Fatal("expected checkpoint failure")
	}
	op.Payload = q.checkpoints[op.ID]
	op.AttemptCount++
	if err := h.executeOperation(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if len(helm.calls) != 1 {
		t.Fatalf("Helm replayed committed work: %v", helm.calls)
	}
}

func TestToolPlanUninstallReversesOrderAndResumes(t *testing.T) {
	h, q, helm, op := newPlanFixture(t, 2)
	if err := h.executeOperation(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	env, err := resetToolPlan(q.checkpoints[op.ID])
	if err != nil {
		t.Fatal(err)
	}
	op.ID = uuid.New()
	op.OperationType = "uninstall"
	op.Payload, _ = json.Marshal(env)
	helm.calls = nil
	helm.failRelease = "istio-base"
	if err := h.executeOperation(context.Background(), op); err == nil {
		t.Fatal("expected base removal failure")
	}
	op.Payload = q.checkpoints[op.ID]
	op.AttemptCount++
	helm.failRelease = ""
	if err := h.executeOperation(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	want := []string{"HELM_UNINSTALL:istiod", "HELM_UNINSTALL:istio-base", "HELM_UNINSTALL:istio-base"}
	if !reflect.DeepEqual(helm.calls, want) {
		t.Fatalf("calls=%v, want %v", helm.calls, want)
	}
	if len(q.installedByRef) != 0 {
		t.Fatalf("installed rows remain: %v", q.installedByRef)
	}
}

func TestToolPlanRollbackReversesOrder(t *testing.T) {
	h, q, helm, op := newPlanFixture(t, 2)
	if err := h.executeOperation(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	env, err := resetToolPlan(q.checkpoints[op.ID])
	if err != nil {
		t.Fatal(err)
	}
	for i := range env.Releases {
		env.Releases[i].RollbackRevision = 1
	}
	op.ID = uuid.New()
	op.OperationType = "rollback"
	op.Payload, _ = json.Marshal(env)
	helm.calls = nil
	if err := h.executeOperation(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	want := []string{"HELM_ROLLBACK:istiod", "HELM_ROLLBACK:istio-base"}
	if !reflect.DeepEqual(helm.calls, want) {
		t.Fatalf("calls=%v, want %v", helm.calls, want)
	}
}

func TestToolPlanAdoptionRequiresEveryRelease(t *testing.T) {
	h, q, helm, op := newPlanFixture(t, 2)
	helm.releases["istio-system/istio-base"] = protocol.HelmResultPayload{Success: true, Status: "deployed", Revision: 3}
	op.OperationType = "adopt"
	if err := h.executeOperation(context.Background(), op); err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("error=%v", err)
	}
	if len(q.installedByRef) != 1 || len(helm.calls) != 0 {
		t.Fatalf("adoption installed a missing release: rows=%d calls=%v", len(q.installedByRef), helm.calls)
	}
}

func TestToolPlanLostLeasePreventsMutation(t *testing.T) {
	h, q, helm, op := newPlanFixture(t, 1)
	q.leaseLost = true
	if err := h.executeOperation(context.Background(), op); !errors.Is(err, errToolLeaseLost) {
		t.Fatalf("error=%v", err)
	}
	if len(helm.calls) != 0 {
		t.Fatalf("stale executor mutated Helm: %v", helm.calls)
	}
}

func TestBuildToolPlanOrdersChartsAndIsolatesValues(t *testing.T) {
	tool := sqlc.ClusterTool{Slug: "istio", DefaultNamespace: "istio-system", VersionConstraint: "1.31.0", Charts: json.RawMessage(`[{"chart_name":"istiod","repo_url":"https://blob.istio.io/istio-release/charts","release_name":"istiod","values_key":"istiod","order":1},{"chart_name":"base","repo_url":"https://blob.istio.io/istio-release/charts","release_name":"istio-base","values_key":"base","order":0}]`)}
	plan, err := buildToolReleasePlan(tool, "", "base:\n  defaultRevision: default\nistiod:\n  replicaCount: 2\n")
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].ReleaseName != "istio-base" || plan[1].ReleaseName != "istiod" {
		t.Fatalf("plan order=%+v", plan)
	}
	if strings.Contains(plan[0].ValuesYAML, "replicaCount") || strings.Contains(plan[1].ValuesYAML, "defaultRevision") {
		t.Fatalf("values leaked between charts: %+v", plan)
	}
	if _, err := buildToolReleasePlan(tool, "custom", ""); err == nil {
		t.Fatal("ambiguous multi-release rename accepted")
	}
	if _, err := buildToolReleasePlan(tool, "", "base: bad\n"); err == nil {
		t.Fatal("non-object release values accepted")
	}
}

func TestToolPlanRejectsExternalRevisionAfterCrash(t *testing.T) {
	h, q, helm, op := newPlanFixture(t, 1)
	q.failCheckpoint = "release.completed"
	if err := h.executeOperation(context.Background(), op); err == nil {
		t.Fatal("expected checkpoint failure")
	}
	op.Payload = q.checkpoints[op.ID]
	helm.releases["istio-system/istio-base"] = protocol.HelmResultPayload{Success: true, Status: "deployed", Revision: 2}
	helm.markers["istio-system/istio-base"] = "external operator"
	if err := h.executeOperation(context.Background(), op); err == nil || !strings.Contains(err.Error(), "outside this operation") {
		t.Fatalf("expected external revision refusal, got %v", err)
	}
	if len(helm.calls) != 1 {
		t.Fatalf("retry mutated external revision: %v", helm.calls)
	}
}

func TestToolPlanRollbackOwnsCrashWindowReleaseWithoutInstalledRow(t *testing.T) {
	h, q, helm, op := newPlanFixture(t, 1)
	q.failCheckpoint = "release.completed"
	if err := h.executeOperation(context.Background(), op); err == nil {
		t.Fatal("expected checkpoint failure")
	}
	env, err := resetToolPlan(q.checkpoints[op.ID])
	if err != nil {
		t.Fatal(err)
	}
	for key := range q.installedByRef {
		delete(q.installedByRef, key)
	}
	op.ID = uuid.New()
	op.OperationType = "rollback"
	op.Payload, _ = json.Marshal(env)
	if err := h.executeOperation(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	if len(helm.releases) != 0 {
		t.Fatalf("orphaned release survived rollback: %v", helm.releases)
	}
}

func TestToolPlanStatusRequiresEveryRelease(t *testing.T) {
	_, _, _, op := newPlanFixture(t, 2)
	rows := []sqlc.InstalledChart{{ReleaseName: "istio-base", Namespace: "istio-system", Status: "deployed"}}
	if status, detail := installedToolPlanStatus(sqlc.ClusterTool{}, rows, op); status != "failed" || !strings.Contains(detail.(string), "1 of 2") {
		t.Fatalf("partial status=%s detail=%v", status, detail)
	}
	rows = append(rows, sqlc.InstalledChart{ReleaseName: "istiod", Namespace: "istio-system", Status: "deployed"})
	if status, detail := installedToolPlanStatus(sqlc.ClusterTool{}, rows, op); status != "installed" || detail != nil {
		t.Fatalf("complete status=%s detail=%v", status, detail)
	}
}

func TestToolPlanIdempotencyIgnoresProgressButNotIntent(t *testing.T) {
	_, _, _, op := newPlanFixture(t, 1)
	var original toolOperationEnvelope
	if err := json.Unmarshal(op.Payload, &original); err != nil {
		t.Fatal(err)
	}
	progress := original
	progress.Releases = append([]toolRelease(nil), original.Releases...)
	progress.Releases[0].State = "completed"
	progress.Releases[0].PreviousRevision = 2
	progress.Releases[0].Revision = 3
	progress.Releases[0].OperationMarker = "operation marker"
	progressRaw, _ := json.Marshal(progress)
	if !toolOperationIntentEqual(op.Payload, progressRaw) {
		t.Fatal("progress changed idempotency identity")
	}
	progress.Releases[0].Version = "1.31.1"
	progressRaw, _ = json.Marshal(progress)
	if toolOperationIntentEqual(op.Payload, progressRaw) {
		t.Fatal("different chart version reused idempotency identity")
	}
}

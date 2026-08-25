package tasks

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type gatekeeperConstraintTaskQ struct {
	RuntimeQuerier
	rows        map[string]sqlc.AuthoredConstraint
	recoverable []sqlc.AuthoredConstraint
	updates     []sqlc.MarkAuthoredConstraintReconcileResultParams
}

func gatekeeperConstraintKey(clusterID uuid.UUID, name string) string {
	return clusterID.String() + "/" + name
}

func (q *gatekeeperConstraintTaskQ) GetAuthoredConstraintByName(_ context.Context, arg sqlc.GetAuthoredConstraintByNameParams) (sqlc.AuthoredConstraint, error) {
	row, ok := q.rows[gatekeeperConstraintKey(arg.ClusterID, arg.Name)]
	if !ok {
		return sqlc.AuthoredConstraint{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *gatekeeperConstraintTaskQ) ListRecoverableAuthoredConstraints(_ context.Context, limit int32) ([]sqlc.AuthoredConstraint, error) {
	rows := q.recoverable
	if int32(len(rows)) > limit {
		rows = rows[:limit]
	}
	return append([]sqlc.AuthoredConstraint(nil), rows...), nil
}

func (q *gatekeeperConstraintTaskQ) MarkAuthoredConstraintReconcileResult(_ context.Context, arg sqlc.MarkAuthoredConstraintReconcileResultParams) (int64, error) {
	key := gatekeeperConstraintKey(arg.ClusterID, arg.Name)
	row, ok := q.rows[key]
	if !ok || row.Generation != arg.ObservedGeneration {
		return 0, nil
	}
	q.updates = append(q.updates, arg)
	row.SyncStatus = arg.SyncStatus
	row.LastError = arg.LastError
	if arg.SyncStatus == "synced" {
		row.ObservedGeneration = arg.ObservedGeneration
	}
	q.rows[key] = row
	return 1, nil
}

type gatekeeperConstraintK8sCall struct {
	clusterID string
	method    string
	path      string
	body      []byte
}

type gatekeeperConstraintK8s struct {
	response *protocol.K8sResponsePayload
	err      error
	calls    []gatekeeperConstraintK8sCall
}

func (k *gatekeeperConstraintK8s) Do(_ context.Context, clusterID, method, path string, body []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	k.calls = append(k.calls, gatekeeperConstraintK8sCall{clusterID: clusterID, method: method, path: path, body: append([]byte(nil), body...)})
	return k.response, k.err
}

func gatekeeperConstraintTaskFixture(desired string) (*gatekeeperConstraintTaskQ, *gatekeeperConstraintK8s, sqlc.AuthoredConstraint, context.Context) {
	row := sqlc.AuthoredConstraint{
		ID: uuid.New(), ClusterID: uuid.New(), Name: "must-have-foo", Kind: "K8sRequiredFoo",
		ApiVersion: "constraints.gatekeeper.sh/v1beta1", Yaml: `apiVersion: constraints.gatekeeper.sh/v1beta1
kind: K8sRequiredFoo
metadata:
  name: must-have-foo
spec:
  enforcementAction: dryrun
`,
		DesiredState: desired, SyncStatus: "pending", Generation: 3,
	}
	q := &gatekeeperConstraintTaskQ{rows: map[string]sqlc.AuthoredConstraint{gatekeeperConstraintKey(row.ClusterID, row.Name): row}}
	k8s := &gatekeeperConstraintK8s{response: &protocol.K8sResponsePayload{StatusCode: http.StatusOK}}
	ctx := testRuntimeContext(RuntimeDependencies{Queries: q, K8s: k8s})
	return q, k8s, row, ctx
}

func TestGatekeeperConstraintReconcileAppliesPersistedDesiredState(t *testing.T) {
	q, k8s, row, ctx := gatekeeperConstraintTaskFixture("present")
	task, err := NewGatekeeperConstraintReconcileTask(row.ClusterID, row.Name, row.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if err := HandleGatekeeperConstraintReconcile(ctx, task); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(k8s.calls) != 1 || k8s.calls[0].method != http.MethodPatch || !strings.Contains(k8s.calls[0].path, "fieldManager=astronomer-authored-constraint") {
		t.Fatalf("calls=%+v", k8s.calls)
	}
	got := q.rows[gatekeeperConstraintKey(row.ClusterID, row.Name)]
	if got.SyncStatus != "synced" || got.ObservedGeneration != row.Generation || got.LastError != "" {
		t.Fatalf("row=%+v", got)
	}
}

func TestGatekeeperConstraintReconcileDeleteTreatsNotFoundAsConverged(t *testing.T) {
	q, k8s, row, ctx := gatekeeperConstraintTaskFixture("absent")
	k8s.response.StatusCode = http.StatusNotFound
	task, _ := NewGatekeeperConstraintReconcileTask(row.ClusterID, row.Name, row.Generation)
	if err := HandleGatekeeperConstraintReconcile(ctx, task); err != nil {
		t.Fatalf("reconcile delete: %v", err)
	}
	if len(k8s.calls) != 1 || k8s.calls[0].method != http.MethodDelete || len(k8s.calls[0].body) != 0 {
		t.Fatalf("calls=%+v", k8s.calls)
	}
	got := q.rows[gatekeeperConstraintKey(row.ClusterID, row.Name)]
	if got.SyncStatus != "synced" || got.ObservedGeneration != row.Generation {
		t.Fatalf("row=%+v", got)
	}
}

func TestGatekeeperConstraintReconcileStaleGenerationIsNoop(t *testing.T) {
	q, k8s, row, ctx := gatekeeperConstraintTaskFixture("present")
	task, _ := NewGatekeeperConstraintReconcileTask(row.ClusterID, row.Name, row.Generation-1)
	if err := HandleGatekeeperConstraintReconcile(ctx, task); err != nil {
		t.Fatalf("stale reconcile: %v", err)
	}
	if len(k8s.calls) != 0 || len(q.updates) != 0 {
		t.Fatalf("stale task calls=%d updates=%d", len(k8s.calls), len(q.updates))
	}
}

func TestGatekeeperConstraintReconcileStoresSanitizedFailure(t *testing.T) {
	q, k8s, row, ctx := gatekeeperConstraintTaskFixture("present")
	k8s.response = &protocol.K8sResponsePayload{StatusCode: http.StatusForbidden, Body: "credential-secret-upstream-body"}
	task, _ := NewGatekeeperConstraintReconcileTask(row.ClusterID, row.Name, row.Generation)
	err := HandleGatekeeperConstraintReconcile(ctx, task)
	if err == nil || strings.Contains(err.Error(), "credential-secret") {
		t.Fatalf("error=%v", err)
	}
	got := q.rows[gatekeeperConstraintKey(row.ClusterID, row.Name)]
	if got.SyncStatus != "failed" || !strings.Contains(got.LastError, "status 403") || strings.Contains(got.LastError, "credential-secret") {
		t.Fatalf("row=%+v", got)
	}
}

func TestGatekeeperConstraintPendingSweepRepairsCommittedIntent(t *testing.T) {
	q, k8s, row, ctx := gatekeeperConstraintTaskFixture("present")
	q.recoverable = []sqlc.AuthoredConstraint{row}
	if err := HandleGatekeeperConstraintReconcileAll(ctx, NewGatekeeperConstraintReconcileAllTask()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(k8s.calls) != 1 || q.rows[gatekeeperConstraintKey(row.ClusterID, row.Name)].SyncStatus != "synced" {
		t.Fatalf("calls=%d row=%+v", len(k8s.calls), q.rows[gatekeeperConstraintKey(row.ClusterID, row.Name)])
	}
}

func TestGatekeeperConstraintRecoverySweepRetriesAgedFailure(t *testing.T) {
	q, k8s, row, ctx := gatekeeperConstraintTaskFixture("present")
	row.SyncStatus = "failed"
	row.LastError = "prior terminal delivery failure"
	q.rows[gatekeeperConstraintKey(row.ClusterID, row.Name)] = row
	q.recoverable = []sqlc.AuthoredConstraint{row}

	if err := HandleGatekeeperConstraintReconcileAll(ctx, NewGatekeeperConstraintReconcileAllTask()); err != nil {
		t.Fatalf("recovery sweep: %v", err)
	}
	got := q.rows[gatekeeperConstraintKey(row.ClusterID, row.Name)]
	if len(k8s.calls) != 1 || got.SyncStatus != "synced" || got.LastError != "" {
		t.Fatalf("calls=%d row=%+v", len(k8s.calls), got)
	}
}

func TestGatekeeperConstraintReconcileRejectsMalformedPayload(t *testing.T) {
	err := HandleGatekeeperConstraintReconcile(context.Background(), asynq.NewTask(GatekeeperConstraintReconcileType, []byte(`{"cluster_id":"bad"}`)))
	if err == nil || !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("error=%v, want SkipRetry", err)
	}
}

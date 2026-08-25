package handler

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type podDeleteRequesterProbe struct{ calls int }

func (p *podDeleteRequesterProbe) Do(context.Context, string, string, string, []byte, map[string]string) (*protocol.K8sResponsePayload, error) {
	p.calls++
	return nil, errors.New("request-time Kubernetes effect is forbidden")
}

func podDeleteRequest(clusterID, namespace, pod string) *http.Request {
	r := requestWith(http.MethodDelete, "/api/v1/workloads/pods/"+clusterID+"/"+namespace+"/"+pod+"/", map[string]string{
		"cluster_id": clusterID, "namespace": namespace, "pod": pod,
	})
	r.Header.Set("Idempotency-Key", uuid.NewString())
	return r
}

func TestDeletePodCommitsOperationTaskAndAuditBefore202(t *testing.T) {
	clusterID := uuid.NewString()
	tx := &stagedWorkloadMutationTx{}
	probe := &podDeleteRequesterProbe{}
	h := NewWorkloadHandlerWithRequester(probe)
	h.SetRunTx(func(_ context.Context, fn func(WorkloadMutationTx) error) error { return fn(tx) })
	recorder := httptest.NewRecorder()
	request := podDeleteRequest(clusterID, "payments", "api-0")
	request.URL.RawQuery = "token=TOP-SECRET"
	request.Header.Set("Authorization", "Bearer TOP-SECRET")
	h.DeletePod(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if probe.calls != 0 {
		t.Fatalf("request path performed %d Kubernetes calls", probe.calls)
	}
	if len(tx.ops) != 1 || len(tx.tasks) != 1 || len(tx.audits) != 1 {
		t.Fatalf("operation/task/audit=%d/%d/%d", len(tx.ops), len(tx.tasks), len(tx.audits))
	}
	if tx.ops[0].OperationType != "delete_pod" || tx.ops[0].TargetType != "pod" || tx.tasks[0].TaskType != "pod:delete" {
		t.Fatalf("operation=%+v task=%+v", tx.ops[0], tx.tasks[0])
	}
	if tx.tasks[0].QueueName != tasks.ClusterTemplateApplyQueueName || tx.tasks[0].TimeoutSeconds != 120 || tx.tasks[0].MaxRetry != 5 || tx.tasks[0].MaxDeliveryAttempts != 20 {
		t.Fatalf("unsafe tunnel task policy: %+v", tx.tasks[0])
	}
	if !tx.tasks[0].DedupeKey.Valid || tx.tasks[0].DedupeKey.String != "pod:delete:"+tx.ops[0].ID.String() {
		t.Fatalf("task dedupe key=%+v", tx.tasks[0].DedupeKey)
	}
	taskPayload := string(tx.tasks[0].Payload)
	if strings.Contains(taskPayload, clusterID) || strings.Contains(taskPayload, "payments") || strings.Contains(taskPayload, "api-0") {
		t.Fatalf("task payload is not identifier-only: %s", taskPayload)
	}
	if !strings.Contains(recorder.Body.String(), tx.ops[0].ID.String()) || !strings.Contains(recorder.Body.String(), `"status":"pending"`) {
		t.Fatalf("receipt is not pollable: %s", recorder.Body.String())
	}
	wantLocation := "/api/v1/workloads/operations/" + tx.ops[0].ID.String() + "/"
	if recorder.Header().Get("Location") != wantLocation || recorder.Header().Get("Retry-After") != "2" {
		t.Fatalf("receipt headers Location=%q Retry-After=%q", recorder.Header().Get("Location"), recorder.Header().Get("Retry-After"))
	}
	auditDetail := string(tx.audits[0].Detail)
	auditRow, err := json.Marshal(tx.audits[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(auditRow), "TOP-SECRET") || strings.Contains(strings.ToLower(string(auditRow)), "authorization") {
		t.Fatalf("audit intent contains request secrets: %s", auditRow)
	}
	if !strings.Contains(auditDetail, clusterID) || !strings.Contains(auditDetail, "payments") || !strings.Contains(auditDetail, "api-0") {
		t.Fatalf("audit detail omits safe identifiers: %s", auditDetail)
	}
}

func TestDeletePodRollsBackWhenTaskOrAuditPersistenceFails(t *testing.T) {
	for _, test := range []struct {
		name     string
		taskErr  error
		auditErr error
		status   int
	}{
		{name: "task", taskErr: errors.New("task unavailable"), status: http.StatusInternalServerError},
		{name: "audit", auditErr: errors.New("audit unavailable"), status: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			committedOps, committedTasks, committedAudits := 0, 0, 0
			h := NewWorkloadHandler()
			h.SetRunTx(func(_ context.Context, fn func(WorkloadMutationTx) error) error {
				tx := &stagedWorkloadMutationTx{taskErr: test.taskErr, auditErr: test.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				committedOps, committedTasks, committedAudits = len(tx.ops), len(tx.tasks), len(tx.audits)
				return nil
			})
			recorder := httptest.NewRecorder()
			h.DeletePod(recorder, podDeleteRequest(uuid.NewString(), "payments", "api-0"))
			if recorder.Code != test.status || committedOps != 0 || committedTasks != 0 || committedAudits != 0 {
				t.Fatalf("status=%d operation/task/audit=%d/%d/%d body=%s", recorder.Code, committedOps, committedTasks, committedAudits, recorder.Body.String())
			}
		})
	}
}

func TestDeletePodUsesDurableIdempotencyKey(t *testing.T) {
	tx := &stagedWorkloadMutationTx{}
	h := NewWorkloadHandler()
	h.SetRunTx(func(_ context.Context, fn func(WorkloadMutationTx) error) error { return fn(tx) })
	request := podDeleteRequest(uuid.NewString(), "payments", "api-0")
	request.Header.Set("Idempotency-Key", "delete-pod-once")
	recorder := httptest.NewRecorder()
	h.DeletePod(recorder, request)
	if recorder.Code != http.StatusAccepted || tx.idemCalls != 1 {
		t.Fatalf("status=%d idempotent calls=%d body=%s", recorder.Code, tx.idemCalls, recorder.Body.String())
	}
}

func TestDeletePodReplayReturnsSameReceiptWithoutDuplicateIntent(t *testing.T) {
	tx := &stagedWorkloadMutationTx{}
	h := NewWorkloadHandler()
	h.SetRunTx(func(_ context.Context, fn func(WorkloadMutationTx) error) error { return fn(tx) })
	request := podDeleteRequest(uuid.NewString(), "payments", "api-0")
	request.Header.Set("Idempotency-Key", "delete-pod-once")
	first := httptest.NewRecorder()
	h.DeletePod(first, request)
	second := httptest.NewRecorder()
	h.DeletePod(second, request.Clone(request.Context()))
	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("statuses=%d/%d", first.Code, second.Code)
	}
	if first.Header().Get("Location") != second.Header().Get("Location") || first.Body.String() != second.Body.String() {
		t.Fatalf("replay receipt changed: first=%s second=%s", first.Body.String(), second.Body.String())
	}
	if len(tx.ops) != 1 || len(tx.tasks) != 1 || len(tx.audits) != 1 {
		t.Fatalf("replay duplicated operation/task/audit: %d/%d/%d", len(tx.ops), len(tx.tasks), len(tx.audits))
	}
}

func TestDeletePodRejectsIdempotencyKeyReuseForDifferentIntent(t *testing.T) {
	tx := &stagedWorkloadMutationTx{idemOp: sqlc.WorkloadOperation{
		ID: uuid.New(), TargetType: "pod", TargetKey: "different:Pod:payments:api-0",
		OperationType: "delete_pod", Payload: []byte(`{"clusterId":"different","kind":"Pod","namespace":"payments","name":"api-0"}`), Status: "pending",
	}}
	h := NewWorkloadHandler()
	h.SetRunTx(func(_ context.Context, fn func(WorkloadMutationTx) error) error { return fn(tx) })
	request := podDeleteRequest(uuid.NewString(), "payments", "api-0")
	request.Header.Set("Idempotency-Key", "delete-pod-once")
	recorder := httptest.NewRecorder()
	h.DeletePod(recorder, request)
	if recorder.Code != http.StatusConflict || len(tx.tasks) != 0 || len(tx.audits) != 0 {
		t.Fatalf("status=%d task/audit=%d/%d body=%s", recorder.Code, len(tx.tasks), len(tx.audits), recorder.Body.String())
	}
}

func TestDeletePodRejectsInvalidIdempotencyKeyBeforeTransaction(t *testing.T) {
	for _, configure := range []func(*http.Request){
		func(r *http.Request) { r.Header.Del("Idempotency-Key") },
		func(r *http.Request) { r.Header.Set("Idempotency-Key", strings.Repeat("x", 129)) },
		func(r *http.Request) { r.Header.Add("Idempotency-Key", "one"); r.Header.Add("Idempotency-Key", "two") },
	} {
		txCalls := 0
		h := NewWorkloadHandler()
		h.SetRunTx(func(_ context.Context, _ func(WorkloadMutationTx) error) error { txCalls++; return nil })
		request := podDeleteRequest(uuid.NewString(), "payments", "api-0")
		configure(request)
		recorder := httptest.NewRecorder()
		h.DeletePod(recorder, request)
		if recorder.Code != http.StatusBadRequest || txCalls != 0 {
			t.Fatalf("status=%d txCalls=%d body=%s", recorder.Code, txCalls, recorder.Body.String())
		}
	}
}

func TestDeletePodFailsClosedWithoutTransactionRunner(t *testing.T) {
	h := NewWorkloadHandler()
	recorder := httptest.NewRecorder()
	h.DeletePod(recorder, podDeleteRequest(uuid.NewString(), "payments", "api-0"))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDeletePodAllEntriesAreTransactionOnly(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "workloads.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found, runTxCalls, remoteCalls := false, 0, 0
	ast.Inspect(file, func(node ast.Node) bool {
		declaration, ok := node.(*ast.FuncDecl)
		if !ok || declaration.Name.Name != "DeletePod" {
			return true
		}
		found = true
		ast.Inspect(declaration.Body, func(child ast.Node) bool {
			call, ok := child.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if selector.Sel.Name == "runTx" {
				runTxCalls++
			}
			if selector.Sel.Name == "Do" {
				remoteCalls++
			}
			return true
		})
		return false
	})
	if !found || runTxCalls != 1 || remoteCalls != 0 {
		t.Fatalf("found=%t runTxCalls=%d remoteCalls=%d", found, runTxCalls, remoteCalls)
	}
}

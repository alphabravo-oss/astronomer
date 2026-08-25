package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

type nodeOperationMutationTx struct {
	operation sqlc.NodeOperation
	creates   []sqlc.CreateNodeOperationIdempotentParams
	tasks     []sqlc.UpsertTaskOutboxParams
	audits    []sqlc.UpsertAuditOutboxParams
	auditErr  error
	createErr error
}

func (tx *nodeOperationMutationTx) CreateNodeOperationIdempotent(_ context.Context, arg sqlc.CreateNodeOperationIdempotentParams) (sqlc.NodeOperation, error) {
	tx.creates = append(tx.creates, arg)
	if tx.createErr != nil {
		return sqlc.NodeOperation{}, tx.createErr
	}
	if tx.operation.ID != uuid.Nil {
		return tx.operation, nil
	}
	now := time.Now().UTC()
	tx.operation = sqlc.NodeOperation{
		ID: uuid.New(), IdempotencyScope: arg.IdempotencyScope, IdempotencyKey: arg.IdempotencyKey,
		RequestDigest: arg.RequestDigest, ClusterID: arg.ClusterID, NodeName: arg.NodeName,
		Action: arg.Action, ParametersEncrypted: arg.ParametersEncrypted, Generation: 1,
		Status: "pending", CreatedByID: arg.CreatedByID, Progress: json.RawMessage(`{}`),
		CreatedAt: now, UpdatedAt: now,
	}
	return tx.operation, nil
}

func (tx *nodeOperationMutationTx) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	tx.tasks = append(tx.tasks, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType, Payload: arg.Payload}, nil
}

func (tx *nodeOperationMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

type nodeOperationStore struct {
	operation sqlc.NodeOperation
	err       error
}

func (s nodeOperationStore) GetNodeOperation(context.Context, uuid.UUID) (sqlc.NodeOperation, error) {
	return s.operation, s.err
}

func nodeMutationRequest(method, body, clusterID, nodeName string) *http.Request {
	r := resourceMutationRequest(method, "/", body, map[string]string{"cluster_id": clusterID, "node_name": nodeName})
	r.Header.Set("Idempotency-Key", "node-once")
	return r
}

func TestAllNodeMutationEntriesCommitReceiptTaskAndAuditBeforeAnyEffect(t *testing.T) {
	clusterID := uuid.NewString()
	for _, test := range []struct {
		name   string
		body   string
		invoke func(*ResourceHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "cordon", invoke: (*ResourceHandler).CordonNode},
		{name: "uncordon", invoke: (*ResourceHandler).UncordonNode},
		{name: "set label", body: `{"key":"team","value":"SENTINEL"}`, invoke: (*ResourceHandler).SetNodeLabel},
		{name: "remove label", body: `{"key":"team"}`, invoke: (*ResourceHandler).RemoveNodeLabel},
		{name: "set annotation", body: `{"key":"example.com/note","value":"SENTINEL"}`, invoke: (*ResourceHandler).SetNodeAnnotation},
		{name: "remove annotation", body: `{"key":"example.com/note"}`, invoke: (*ResourceHandler).RemoveNodeAnnotation},
		{name: "add taint", body: `{"key":"dedicated","value":"SENTINEL","effect":"NoSchedule"}`, invoke: (*ResourceHandler).AddNodeTaint},
		{name: "remove taint", body: `{"key":"dedicated","effect":"NoSchedule"}`, invoke: (*ResourceHandler).RemoveNodeTaint},
		{name: "drain with optional body omitted", invoke: (*ResourceHandler).DrainNode},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := &nodeOperationMutationTx{}
			requester := &resourceOperationRequesterProbe{}
			h := NewResourceHandlerWithRequester(requester)
			enc := resourceOperationEncryptor(t)
			h.SetEncryptor(enc)
			h.SetNodeMutationRunTx(func(_ context.Context, fn func(NodeMutationTx) error) error { return fn(tx) })
			recorder := httptest.NewRecorder()
			test.invoke(h, recorder, nodeMutationRequest(http.MethodPost, test.body, clusterID, "worker-1"))
			if recorder.Code != http.StatusAccepted || requester.calls != 0 || len(tx.creates) != 1 || len(tx.tasks) != 1 || len(tx.audits) != 1 {
				t.Fatalf("status=%d effects=%d operation/task/audit=%d/%d/%d body=%s", recorder.Code, requester.calls, len(tx.creates), len(tx.tasks), len(tx.audits), recorder.Body.String())
			}
			if tx.tasks[0].TaskType != tasks.NodeOperationType || strings.Contains(string(tx.tasks[0].Payload), clusterID) || strings.Contains(string(tx.tasks[0].Payload), "worker-1") || strings.Contains(string(tx.tasks[0].Payload), "SENTINEL") {
				t.Fatalf("task is not identifier-only: %s", tx.tasks[0].Payload)
			}
			if strings.Contains(string(tx.audits[0].Detail), "SENTINEL") || strings.Contains(tx.creates[0].ParametersEncrypted, "SENTINEL") {
				t.Fatalf("request parameters leaked: audit=%s encrypted=%s", tx.audits[0].Detail, tx.creates[0].ParametersEncrypted)
			}
			plain, err := enc.DecryptBytes(tx.creates[0].ParametersEncrypted)
			if err != nil || !json.Valid(plain) {
				t.Fatalf("parameters are not encrypted worker-readable JSON: err=%v", err)
			}
		})
	}
}

func TestNodeMutationRequiresIdempotencyAndMandatoryAudit(t *testing.T) {
	clusterID := uuid.NewString()
	for _, test := range []struct {
		name      string
		configure func(*http.Request, *nodeOperationMutationTx)
		status    int
	}{
		{name: "missing key", status: http.StatusBadRequest, configure: func(r *http.Request, _ *nodeOperationMutationTx) { r.Header.Del("Idempotency-Key") }},
		{name: "audit unavailable", status: http.StatusServiceUnavailable, configure: func(_ *http.Request, tx *nodeOperationMutationTx) { tx.auditErr = errors.New("unavailable") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := &nodeOperationMutationTx{}
			commits := 0
			h := NewResourceHandler()
			h.SetEncryptor(resourceOperationEncryptor(t))
			h.SetNodeMutationRunTx(func(_ context.Context, fn func(NodeMutationTx) error) error {
				if err := fn(tx); err != nil {
					return err
				}
				commits++
				return nil
			})
			r := nodeMutationRequest(http.MethodPost, "", clusterID, "worker-1")
			test.configure(r, tx)
			recorder := httptest.NewRecorder()
			h.CordonNode(recorder, r)
			if recorder.Code != test.status || commits != 0 {
				t.Fatalf("status=%d commits=%d body=%s", recorder.Code, commits, recorder.Body.String())
			}
		})
	}
}

func TestNodeMutationRejectsCompetingActiveIntent(t *testing.T) {
	tx := &nodeOperationMutationTx{createErr: &pgconn.PgError{Code: "23505", ConstraintName: "node_operations_active_target_idx"}}
	h := NewResourceHandler()
	h.SetEncryptor(resourceOperationEncryptor(t))
	h.SetNodeMutationRunTx(func(_ context.Context, fn func(NodeMutationTx) error) error { return fn(tx) })
	recorder := httptest.NewRecorder()
	h.CordonNode(recorder, nodeMutationRequest(http.MethodPost, "", uuid.NewString(), "worker-1"))
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "already active") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestGetNodeOperationHidesCrossClusterAndCrossNodeIDs(t *testing.T) {
	clusterID := uuid.New()
	operationID := uuid.New()
	now := time.Now().UTC()
	h := NewResourceHandler()
	h.SetNodeOperationStore(nodeOperationStore{operation: sqlc.NodeOperation{
		ID: operationID, ClusterID: clusterID, NodeName: "worker-1", Action: "cordon",
		Status: "pending", Generation: 1, Progress: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now,
	}})
	h.SetNodeOperationReadAuthorizer(func(context.Context, string, string) (bool, error) { return true, nil })
	for _, test := range []struct {
		cluster string
		node    string
		status  int
	}{
		{cluster: clusterID.String(), node: "worker-1", status: http.StatusOK},
		{cluster: uuid.NewString(), node: "worker-1", status: http.StatusNotFound},
		{cluster: clusterID.String(), node: "worker-2", status: http.StatusNotFound},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("cluster_id", test.cluster)
		rctx.URLParams.Add("node_name", test.node)
		rctx.URLParams.Add("id", operationID.String())
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
		recorder := httptest.NewRecorder()
		h.GetNodeOperation(recorder, r)
		if recorder.Code != test.status {
			t.Fatalf("cluster=%s node=%s status=%d body=%s", test.cluster, test.node, recorder.Code, recorder.Body.String())
		}
	}
}

func TestGetNodeOperationRequiresCurrentActionAwareAuthorization(t *testing.T) {
	clusterID := uuid.New()
	operationID := uuid.New()
	operation := sqlc.NodeOperation{ID: operationID, ClusterID: clusterID, NodeName: "worker-1", Action: "drain", Status: "running", Generation: 1, Progress: json.RawMessage(`{}`), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	for _, test := range []struct {
		name    string
		allowed bool
		status  int
	}{
		{name: "retained manage or read capability", allowed: true, status: http.StatusOK},
		{name: "revoked originating capability", allowed: false, status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := NewResourceHandler()
			h.SetNodeOperationStore(nodeOperationStore{operation: operation})
			h.SetNodeOperationReadAuthorizer(func(_ context.Context, gotCluster, gotAction string) (bool, error) {
				if gotCluster != clusterID.String() || gotAction != "drain" {
					t.Fatalf("authorization scope = %s/%s", gotCluster, gotAction)
				}
				return test.allowed, nil
			})
			r := resourceMutationRequest(http.MethodGet, "/", "", map[string]string{
				"cluster_id": clusterID.String(), "node_name": "worker-1", "id": operationID.String(),
			})
			recorder := httptest.NewRecorder()
			h.GetNodeOperation(recorder, r)
			if recorder.Code != test.status {
				t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestNodeProductionWiringAndRouteGuards(t *testing.T) {
	server, err := os.ReadFile("../server/app_integrations.go")
	if err != nil {
		t.Fatal(err)
	}
	routes, routeErr := os.ReadFile("../server/routes_resources_workloads.go")
	if routeErr != nil {
		t.Fatal(routeErr)
	}
	authorizer, authorizerErr := os.ReadFile("../server/node_operation_authorization.go")
	if authorizerErr != nil {
		t.Fatal(authorizerErr)
	}
	for _, required := range []string{
		"SetNodeOperationStore(queries)",
		"SetNodeMutationRunTx(sqlcMutationTxRunner[handler.NodeMutationTx](database))",
		"SetNodeOperationReadAuthorizer(nodeOperationReadAuthorizer(rbacEngine, rbacQuerier))",
	} {
		if !strings.Contains(string(server), required) {
			t.Fatalf("node production wiring missing %q", required)
		}
	}
	for _, required := range []string{
		"rbac.ResourceNodes, rbac.VerbRead",
		"primary := rbac.VerbUpdate",
		`if action == "drain"`,
		"primary = rbac.VerbManage",
	} {
		if !strings.Contains(string(authorizer), required) {
			t.Fatalf("node post-load authorizer missing %q", required)
		}
	}
	if !strings.Contains(string(routes), `r.With(requireAuth(deps.JWT, deps.AuthQueries)).Get("/nodes/{cluster_id}/{node_name}/operations/{id}/"`) {
		t.Fatal("node operation poll route does not require authentication before post-load authorization")
	}
}

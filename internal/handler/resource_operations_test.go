package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type resourceOperationMutationTx struct {
	operation sqlc.ResourceOperation
	creates   []sqlc.CreateResourceOperationIdempotentParams
	tasks     []sqlc.UpsertTaskOutboxParams
	audits    []sqlc.UpsertAuditOutboxParams
	taskErr   error
	auditErr  error
	createErr error
}

func (tx *resourceOperationMutationTx) CreateResourceOperationIdempotent(_ context.Context, arg sqlc.CreateResourceOperationIdempotentParams) (sqlc.ResourceOperation, error) {
	if tx.createErr != nil {
		return sqlc.ResourceOperation{}, tx.createErr
	}
	tx.creates = append(tx.creates, arg)
	if tx.operation.ID != uuid.Nil {
		return tx.operation, nil
	}
	now := time.Now().UTC()
	tx.operation = sqlc.ResourceOperation{
		ID: uuid.New(), IdempotencyScope: arg.IdempotencyScope, IdempotencyKey: arg.IdempotencyKey,
		RequestDigest: arg.RequestDigest, ClusterID: arg.ClusterID, ResourceType: arg.ResourceType,
		Namespace: arg.Namespace, ResourceName: arg.ResourceName, Action: arg.Action,
		ApiPath: arg.ApiPath, RequiredVerb: arg.RequiredVerb, ManifestEncrypted: arg.ManifestEncrypted, ForceApply: arg.ForceApply,
		Generation: 1, Status: "pending", CreatedByID: arg.CreatedByID, CreatedAt: now, UpdatedAt: now,
	}
	return tx.operation, nil
}

func (tx *resourceOperationMutationTx) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	if tx.taskErr != nil {
		return sqlc.TaskOutbox{}, tx.taskErr
	}
	tx.tasks = append(tx.tasks, arg)
	return sqlc.TaskOutbox{ID: uuid.New(), TaskType: arg.TaskType, Payload: arg.Payload}, nil
}

func (tx *resourceOperationMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

type resourceOperationStore struct {
	operation sqlc.ResourceOperation
	err       error
}

func (s resourceOperationStore) GetResourceOperation(context.Context, uuid.UUID) (sqlc.ResourceOperation, error) {
	return s.operation, s.err
}

type resourceOperationRequesterProbe struct{ calls int }

func (p *resourceOperationRequesterProbe) Do(context.Context, string, string, string, []byte, map[string]string) (*protocol.K8sResponsePayload, error) {
	p.calls++
	return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: "e30="}, nil
}

func resourceOperationEncryptor(t *testing.T) *auth.Encryptor {
	t.Helper()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	return enc
}

func resourceMutationRequest(method, target, body string, params map[string]string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Idempotency-Key", "resource-once")
	userID := uuid.NewString()
	r = r.WithContext(middleware.SetAuthenticatedUserForTest(r.Context(), &middleware.AuthenticatedUser{ID: userID, AuthMethod: "jwt"}))
	ctx := chi.NewRouteContext()
	for key, value := range params {
		ctx.URLParams.Add(key, value)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx))
}

func TestResourceCreateCommitsEncryptedOperationIdentifierTaskAndAuditBefore202(t *testing.T) {
	clusterID := uuid.NewString()
	const sentinel = "SUPER-SECRET-TOKEN"
	manifest := `{"apiVersion":"v1","kind":"Service","metadata":{"name":"api","namespace":"payments"},"stringData":{"token":"` + sentinel + `"}}`
	tx := &resourceOperationMutationTx{}
	requester := &resourceOperationRequesterProbe{}
	h := NewResourceHandlerWithRequester(requester)
	enc := resourceOperationEncryptor(t)
	h.SetEncryptor(enc)
	h.SetResourceMutationRunTx(func(_ context.Context, fn func(ResourceMutationTx) error) error { return fn(tx) })
	recorder := httptest.NewRecorder()
	request := resourceMutationRequest(http.MethodPost, "/api/v1/clusters/"+clusterID+"/resources/services/", manifest, map[string]string{
		"cluster_id": clusterID, "resource_type": "services",
	})
	h.CreateNamedResource(recorder, request)
	if recorder.Code != http.StatusAccepted || requester.calls != 0 {
		t.Fatalf("status=%d requester_calls=%d body=%s", recorder.Code, requester.calls, recorder.Body.String())
	}
	if len(tx.creates) != 1 || len(tx.tasks) != 1 || len(tx.audits) != 1 {
		t.Fatalf("operation/task/audit=%d/%d/%d", len(tx.creates), len(tx.tasks), len(tx.audits))
	}
	if strings.Contains(tx.creates[0].ManifestEncrypted, sentinel) || tx.creates[0].ManifestEncrypted == manifest {
		t.Fatalf("manifest was not encrypted at rest")
	}
	plaintext, err := enc.DecryptBytes(tx.creates[0].ManifestEncrypted)
	if err != nil || string(plaintext) != manifest {
		t.Fatalf("handler ciphertext is not worker-compatible: err=%v", err)
	}
	if tx.tasks[0].TaskType != tasks.ResourceOperationType || strings.Contains(string(tx.tasks[0].Payload), sentinel) || strings.Contains(string(tx.audits[0].Detail), sentinel) {
		t.Fatalf("secret escaped into task/audit: task=%s audit=%s", tx.tasks[0].Payload, tx.audits[0].Detail)
	}
	if strings.Contains(string(tx.tasks[0].Payload), clusterID) || !strings.Contains(string(tx.tasks[0].Payload), tx.operation.ID.String()) {
		t.Fatalf("task is not identifier-only: %s", tx.tasks[0].Payload)
	}
	wantLocation := "/api/v1/clusters/" + clusterID + "/resources/operations/" + tx.operation.ID.String() + "/"
	if recorder.Header().Get("Location") != wantLocation || recorder.Header().Get("Retry-After") != "2" {
		t.Fatalf("receipt headers=%v", recorder.Header())
	}
}

func TestResourceMutationRejectsMissingKeyOversizeAndUnwiredEncryptionBeforeTransaction(t *testing.T) {
	clusterID := uuid.NewString()
	base := `{"metadata":{"name":"secret","namespace":"default"},"data":{"token":"SENTINEL"}}`
	for _, test := range []struct {
		name      string
		body      string
		configure func(*http.Request, *ResourceHandler)
		status    int
	}{
		{name: "missing idempotency", body: base, status: http.StatusBadRequest, configure: func(r *http.Request, _ *ResourceHandler) { r.Header.Del("Idempotency-Key") }},
		{name: "oversized secret", body: base + strings.Repeat("x", int(maxResourceManifestBytes)), status: http.StatusRequestEntityTooLarge},
		{name: "encryptor unavailable", body: base, status: http.StatusServiceUnavailable, configure: func(_ *http.Request, h *ResourceHandler) { h.SetEncryptor(nil) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			txCalls := 0
			h := NewResourceHandler()
			h.SetEncryptor(resourceOperationEncryptor(t))
			h.SetResourceMutationRunTx(func(context.Context, func(ResourceMutationTx) error) error { txCalls++; return nil })
			r := resourceMutationRequest(http.MethodPost, "/", test.body, map[string]string{"cluster_id": clusterID, "resource_type": "secrets"})
			if test.configure != nil {
				test.configure(r, h)
			}
			recorder := httptest.NewRecorder()
			h.CreateNamedResource(recorder, r)
			if recorder.Code != test.status || txCalls != 0 || strings.Contains(recorder.Body.String(), "SENTINEL") {
				t.Fatalf("status=%d tx=%d body=%s", recorder.Code, txCalls, recorder.Body.String())
			}
		})
	}
}

func TestResourceMutationRollsBackOnTaskOrAuditFailure(t *testing.T) {
	for _, test := range []struct {
		name     string
		taskErr  error
		auditErr error
		status   int
	}{
		{name: "task", taskErr: errors.New("unavailable"), status: http.StatusInternalServerError},
		{name: "audit", auditErr: errors.New("unavailable"), status: http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			commits := 0
			h := NewResourceHandler()
			h.SetEncryptor(resourceOperationEncryptor(t))
			h.SetResourceMutationRunTx(func(_ context.Context, fn func(ResourceMutationTx) error) error {
				tx := &resourceOperationMutationTx{taskErr: test.taskErr, auditErr: test.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				commits++
				return nil
			})
			r := resourceMutationRequest(http.MethodDelete, "/", "", map[string]string{
				"cluster_id": uuid.NewString(), "resource_type": "persistentvolumes", "name": "pv-a",
			})
			recorder := httptest.NewRecorder()
			h.DeleteNamedResource(recorder, r)
			if recorder.Code != test.status || commits != 0 {
				t.Fatalf("status=%d commits=%d body=%s", recorder.Code, commits, recorder.Body.String())
			}
		})
	}
}

func TestAllNamedResourceMutationEntryPointsUseDurableSaga(t *testing.T) {
	clusterID := uuid.NewString()
	for _, test := range []struct {
		name   string
		method string
		body   string
		verb   string
		params map[string]string
		invoke func(*ResourceHandler, http.ResponseWriter, *http.Request)
	}{
		{name: "collection create", method: http.MethodPost, body: `{"metadata":{"name":"api","namespace":"default"}}`, verb: "create", params: map[string]string{"cluster_id": clusterID, "resource_type": "services"}, invoke: (*ResourceHandler).CreateNamedResource},
		{name: "collection delete", method: http.MethodDelete, verb: "delete", params: map[string]string{"cluster_id": clusterID, "resource_type": "services", "namespace": "default", "name": "api"}, invoke: (*ResourceHandler).DeleteNamedResource},
		{name: "rest update", method: http.MethodPut, body: `{"metadata":{"name":"api","namespace":"default"}}`, verb: "update", params: map[string]string{"cluster_id": clusterID, "type": "services", "namespace": "default", "name": "api"}, invoke: (*ResourceHandler).UpdateNamedResource},
		{name: "rest delete", method: http.MethodDelete, verb: "delete", params: map[string]string{"cluster_id": clusterID, "type": "services", "namespace": "default", "name": "api"}, invoke: (*ResourceHandler).DeleteNamedResourceREST},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx := &resourceOperationMutationTx{}
			requester := &resourceOperationRequesterProbe{}
			h := NewResourceHandlerWithRequester(requester)
			h.SetEncryptor(resourceOperationEncryptor(t))
			h.SetResourceMutationRunTx(func(_ context.Context, fn func(ResourceMutationTx) error) error { return fn(tx) })
			recorder := httptest.NewRecorder()
			test.invoke(h, recorder, resourceMutationRequest(test.method, "/", test.body, test.params))
			if recorder.Code != http.StatusAccepted || requester.calls != 0 || len(tx.creates) != 1 || len(tx.tasks) != 1 || len(tx.audits) != 1 {
				t.Fatalf("status=%d k8s=%d operation/task/audit=%d/%d/%d body=%s", recorder.Code, requester.calls, len(tx.creates), len(tx.tasks), len(tx.audits), recorder.Body.String())
			}
			if tx.creates[0].RequiredVerb != test.verb {
				t.Fatalf("required verb=%q, want %q", tx.creates[0].RequiredVerb, test.verb)
			}
		})
	}
}

func TestCompetingResourceTargetReturnsConflictWithoutTaskAuditOrEffect(t *testing.T) {
	tx := &resourceOperationMutationTx{createErr: &pgconn.PgError{
		Code: "23505", ConstraintName: "resource_operations_active_target_unique",
	}}
	requester := &resourceOperationRequesterProbe{}
	h := NewResourceHandlerWithRequester(requester)
	h.SetEncryptor(resourceOperationEncryptor(t))
	commits := 0
	h.SetResourceMutationRunTx(func(_ context.Context, fn func(ResourceMutationTx) error) error {
		if err := fn(tx); err != nil {
			return err
		}
		commits++
		return nil
	})
	recorder := httptest.NewRecorder()
	clusterID := uuid.NewString()
	r := resourceMutationRequest(http.MethodPut, "/", `{"metadata":{"name":"api","namespace":"default"}}`, map[string]string{
		"cluster_id": clusterID, "type": "services", "namespace": "default", "name": "api",
	})
	h.UpdateNamedResource(recorder, r)
	if recorder.Code != http.StatusConflict || commits != 0 || len(tx.tasks) != 0 || len(tx.audits) != 0 || requester.calls != 0 {
		t.Fatalf("status=%d commits=%d task/audit/effects=%d/%d/%d body=%s", recorder.Code, commits, len(tx.tasks), len(tx.audits), requester.calls, recorder.Body.String())
	}
}

func TestGetResourceOperationIsClusterScoped(t *testing.T) {
	operationCluster := uuid.New()
	operationID := uuid.New()
	creatorID := uuid.New()
	now := time.Now()
	h := NewResourceHandler()
	h.SetResourceOperationStore(resourceOperationStore{operation: sqlc.ResourceOperation{
		ID: operationID, ClusterID: operationCluster, ResourceType: "secrets", ResourceName: "db",
		Action: "apply", RequiredVerb: "create", Status: "pending", Generation: 1, CreatedAt: now, UpdatedAt: now,
		CreatedByID: pgtype.UUID{Bytes: creatorID, Valid: true},
	}})
	for _, test := range []struct {
		name     string
		cluster  string
		bindings []rbac.RoleBinding
		status   int
	}{
		{name: "originating permission", cluster: operationCluster.String(), bindings: []rbac.RoleBinding{{RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceSecrets), Verbs: []string{string(rbac.VerbCreate)}}}}}, status: http.StatusOK},
		{name: "support cluster read", cluster: operationCluster.String(), bindings: []rbac.RoleBinding{{RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceClusters), Verbs: []string{string(rbac.VerbRead)}}}}}, status: http.StatusOK},
		{name: "revoked permission hidden", cluster: operationCluster.String(), status: http.StatusNotFound},
		{name: "cross cluster hidden", cluster: uuid.NewString(), bindings: []rbac.RoleBinding{{IsSuperuser: true}}, status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: test.bindings})
			r := resourceMutationRequest(http.MethodGet, "/", "", map[string]string{"cluster_id": test.cluster, "id": operationID.String()})
			r = r.WithContext(middleware.SetAuthenticatedUserForTest(r.Context(), &middleware.AuthenticatedUser{ID: creatorID.String(), AuthMethod: "jwt"}))
			recorder := httptest.NewRecorder()
			h.GetResourceOperation(recorder, r)
			if recorder.Code != test.status {
				t.Fatalf("cluster=%s status=%d body=%s", test.cluster, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestGetResourceOperationFailsClosedWithoutAuthorizationWiring(t *testing.T) {
	clusterID, operationID, callerID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now()
	h := NewResourceHandler()
	h.SetResourceOperationStore(resourceOperationStore{operation: sqlc.ResourceOperation{
		ID: operationID, ClusterID: clusterID, ResourceType: "services", ResourceName: "api",
		Action: "apply", RequiredVerb: "update", Status: "pending", Generation: 1,
		CreatedByID: pgtype.UUID{Bytes: callerID, Valid: true}, CreatedAt: now, UpdatedAt: now,
	}})
	r := resourceMutationRequest(http.MethodGet, "/", "", map[string]string{"cluster_id": clusterID.String(), "id": operationID.String()})
	r = r.WithContext(middleware.SetAuthenticatedUserForTest(r.Context(), &middleware.AuthenticatedUser{ID: callerID.String(), AuthMethod: "jwt"}))
	recorder := httptest.NewRecorder()
	h.GetResourceOperation(recorder, r)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

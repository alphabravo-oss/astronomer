package tasks

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type resourceOperationTaskStore struct {
	RuntimeQuerier
	operation sqlc.ResourceOperation
	claimErr  error
	succeeded []sqlc.MarkResourceOperationSucceededParams
	failed    []sqlc.MarkResourceOperationFailedParams
	retrying  []sqlc.MarkResourceOperationRetryingParams
}

func (s *resourceOperationTaskStore) ClaimResourceOperationGeneration(context.Context, sqlc.ClaimResourceOperationGenerationParams) (sqlc.ResourceOperation, error) {
	return s.operation, s.claimErr
}

func (s *resourceOperationTaskStore) GetResourceOperation(context.Context, uuid.UUID) (sqlc.ResourceOperation, error) {
	return s.operation, nil
}

func (s *resourceOperationTaskStore) MarkResourceOperationSucceeded(_ context.Context, arg sqlc.MarkResourceOperationSucceededParams) (sqlc.ResourceOperation, error) {
	s.succeeded = append(s.succeeded, arg)
	return s.operation, nil
}

func (s *resourceOperationTaskStore) MarkResourceOperationFailed(_ context.Context, arg sqlc.MarkResourceOperationFailedParams) (sqlc.ResourceOperation, error) {
	s.failed = append(s.failed, arg)
	return s.operation, nil
}

func (s *resourceOperationTaskStore) MarkResourceOperationRetrying(_ context.Context, arg sqlc.MarkResourceOperationRetryingParams) (sqlc.ResourceOperation, error) {
	s.retrying = append(s.retrying, arg)
	return s.operation, nil
}

type resourceOperationK8s struct {
	method  string
	path    string
	body    []byte
	status  int
	err     error
	encoded string
}

func (k *resourceOperationK8s) Do(_ context.Context, _ string, method, path string, body []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	k.method, k.path, k.body = method, path, append([]byte(nil), body...)
	if k.err != nil {
		return nil, k.err
	}
	return &protocol.K8sResponsePayload{StatusCode: k.status, Body: k.encoded}, nil
}

func resourceTaskEncryptor(t *testing.T) *auth.Encryptor {
	t.Helper()
	key, _ := auth.GenerateKey()
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	return enc
}

func TestHandleResourceOperationApplyUsesGenerationFenceAndSanitizedEvidence(t *testing.T) {
	enc := resourceTaskEncryptor(t)
	const manifest = `{"apiVersion":"v1","kind":"Secret","metadata":{"name":"db","namespace":"payments"},"stringData":{"password":"SENTINEL"}}`
	ciphertext, err := enc.Encrypt(manifest)
	if err != nil {
		t.Fatal(err)
	}
	op := sqlc.ResourceOperation{
		ID: uuid.New(), ClusterID: uuid.New(), Action: "apply", ApiPath: "/api/v1/namespaces/payments/secrets/db",
		ManifestEncrypted: ciphertext, ForceApply: true, Generation: 3, ObservedGeneration: 2, Status: "running",
	}
	store := &resourceOperationTaskStore{operation: op}
	response := base64.StdEncoding.EncodeToString([]byte(`{"metadata":{"resourceVersion":"42"},"stringData":{"password":"SENTINEL"}}`))
	k8s := &resourceOperationK8s{status: http.StatusOK, encoded: response}
	runtime := CoreRuntime{Deps: RuntimeDependencies{Queries: store, K8s: k8s, ResourceDecryptor: enc}}
	task, _ := NewResourceOperationTask(op.ID, op.Generation)
	if err := HandleResourceOperation(runtime.Context(context.Background()), task); err != nil {
		t.Fatal(err)
	}
	if k8s.method != http.MethodPatch || string(k8s.body) != manifest || !strings.Contains(k8s.path, "fieldManager=astronomer") || !strings.Contains(k8s.path, "force=true") {
		t.Fatalf("request method=%s path=%s body=%s", k8s.method, k8s.path, k8s.body)
	}
	if len(store.succeeded) != 1 || store.succeeded[0].Generation != 3 || store.succeeded[0].ObservedResourceVersion != "42" {
		t.Fatalf("success evidence=%+v", store.succeeded)
	}
	if strings.Contains(store.succeeded[0].ObservedResourceVersion, "SENTINEL") {
		t.Fatal("secret escaped into terminal evidence")
	}
}

func TestHandleResourceOperationDelete404ConvergesAndStaleGenerationDoesNotApply(t *testing.T) {
	op := sqlc.ResourceOperation{ID: uuid.New(), ClusterID: uuid.New(), Action: "delete", ApiPath: "/api/v1/namespaces/default/services/api", Generation: 1, Status: "running"}
	store := &resourceOperationTaskStore{operation: op}
	k8s := &resourceOperationK8s{status: http.StatusNotFound}
	runtime := CoreRuntime{Deps: RuntimeDependencies{Queries: store, K8s: k8s}}
	task, _ := NewResourceOperationTask(op.ID, 1)
	if err := HandleResourceOperation(runtime.Context(context.Background()), task); err != nil {
		t.Fatal(err)
	}
	if k8s.method != http.MethodDelete || len(store.succeeded) != 1 || store.succeeded[0].ObservedStatusCode.Int32 != http.StatusNoContent {
		t.Fatalf("delete did not converge: method=%s success=%+v", k8s.method, store.succeeded)
	}

	store = &resourceOperationTaskStore{operation: op, claimErr: pgx.ErrNoRows}
	store.operation.Generation = 2
	k8s = &resourceOperationK8s{status: http.StatusOK}
	runtime = CoreRuntime{Deps: RuntimeDependencies{Queries: store, K8s: k8s}}
	task, _ = NewResourceOperationTask(op.ID, 1)
	err := HandleResourceOperation(runtime.Context(context.Background()), task)
	if !errors.Is(err, asynq.SkipRetry) || k8s.method != "" {
		t.Fatalf("stale generation was not fenced: err=%v method=%s", err, k8s.method)
	}
}

func TestHandleResourceOperationFailureNeverPersistsKubernetesBody(t *testing.T) {
	op := sqlc.ResourceOperation{ID: uuid.New(), ClusterID: uuid.New(), Action: "delete", ApiPath: "/api/v1/services/api", Generation: 1, Status: "running", CreatedAt: time.Now()}
	store := &resourceOperationTaskStore{operation: op}
	k8s := &resourceOperationK8s{status: http.StatusInternalServerError, encoded: base64.StdEncoding.EncodeToString([]byte(`{"message":"SENTINEL"}`))}
	runtime := CoreRuntime{Deps: RuntimeDependencies{Queries: store, K8s: k8s}}
	task, _ := NewResourceOperationTask(op.ID, 1)
	err := HandleResourceOperation(runtime.Context(context.Background()), task)
	if err == nil || strings.Contains(err.Error(), "SENTINEL") || len(store.retrying) != 1 || strings.Contains(store.retrying[0].ErrorCode, "SENTINEL") || len(store.failed) != 0 {
		t.Fatalf("unsanitized/transiently-terminal failure: err=%v retrying=%+v failed=%+v", err, store.retrying, store.failed)
	}
}

func TestHandleResourceOperationDeterministicKubernetes4xxIsTerminal(t *testing.T) {
	op := sqlc.ResourceOperation{ID: uuid.New(), ClusterID: uuid.New(), Action: "delete", ApiPath: "/api/v1/services/api", Generation: 1, Status: "running"}
	store := &resourceOperationTaskStore{operation: op}
	k8s := &resourceOperationK8s{status: http.StatusForbidden}
	runtime := CoreRuntime{Deps: RuntimeDependencies{Queries: store, K8s: k8s}}
	task, _ := NewResourceOperationTask(op.ID, 1)
	err := HandleResourceOperation(runtime.Context(context.Background()), task)
	if !errors.Is(err, asynq.SkipRetry) || len(store.failed) != 1 || len(store.retrying) != 0 || store.failed[0].ObservedStatusCode.Int32 != http.StatusForbidden {
		t.Fatalf("deterministic 4xx err=%v failed=%+v retrying=%+v", err, store.failed, store.retrying)
	}
}

func TestHandleResourceOperationNonRetryableFailureIsTerminal(t *testing.T) {
	op := sqlc.ResourceOperation{ID: uuid.New(), ClusterID: uuid.New(), Action: "exec", ApiPath: "/api/v1/services/api", Generation: 1, Status: "running"}
	store := &resourceOperationTaskStore{operation: op}
	k8s := &resourceOperationK8s{status: http.StatusOK}
	runtime := CoreRuntime{Deps: RuntimeDependencies{Queries: store, K8s: k8s}}
	task, _ := NewResourceOperationTask(op.ID, 1)
	err := HandleResourceOperation(runtime.Context(context.Background()), task)
	if !errors.Is(err, asynq.SkipRetry) || len(store.failed) != 1 || len(store.retrying) != 0 || k8s.method != "" {
		t.Fatalf("non-retryable outcome err=%v failed=%+v retrying=%+v method=%s", err, store.failed, store.retrying, k8s.method)
	}
}

package tasks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type nodeOperationTaskStore struct {
	RuntimeQuerier
	operation sqlc.NodeOperation
	progress  []sqlc.UpdateNodeOperationProgressParams
	succeeded []sqlc.MarkNodeOperationSucceededParams
	blocked   []sqlc.MarkNodeOperationBlockedParams
	retrying  []sqlc.MarkNodeOperationRetryingParams
	failed    []sqlc.MarkNodeOperationFailedParams
}

func (s *nodeOperationTaskStore) ClaimNodeOperationGeneration(context.Context, sqlc.ClaimNodeOperationGenerationParams) (sqlc.NodeOperation, error) {
	return s.operation, nil
}
func (s *nodeOperationTaskStore) GetNodeOperation(context.Context, uuid.UUID) (sqlc.NodeOperation, error) {
	return s.operation, nil
}
func (s *nodeOperationTaskStore) UpdateNodeOperationProgress(_ context.Context, arg sqlc.UpdateNodeOperationProgressParams) (sqlc.NodeOperation, error) {
	s.progress = append(s.progress, arg)
	s.operation.Progress = arg.Progress
	return s.operation, nil
}
func (s *nodeOperationTaskStore) MarkNodeOperationSucceeded(_ context.Context, arg sqlc.MarkNodeOperationSucceededParams) (sqlc.NodeOperation, error) {
	s.succeeded = append(s.succeeded, arg)
	return s.operation, nil
}
func (s *nodeOperationTaskStore) MarkNodeOperationBlocked(_ context.Context, arg sqlc.MarkNodeOperationBlockedParams) (sqlc.NodeOperation, error) {
	s.blocked = append(s.blocked, arg)
	return s.operation, nil
}
func (s *nodeOperationTaskStore) MarkNodeOperationRetrying(_ context.Context, arg sqlc.MarkNodeOperationRetryingParams) (sqlc.NodeOperation, error) {
	s.retrying = append(s.retrying, arg)
	return s.operation, nil
}
func (s *nodeOperationTaskStore) MarkNodeOperationFailed(_ context.Context, arg sqlc.MarkNodeOperationFailedParams) (sqlc.NodeOperation, error) {
	s.failed = append(s.failed, arg)
	return s.operation, nil
}

type nodeOperationK8s struct {
	do func(method, path string, body []byte) (*protocol.K8sResponsePayload, error)
}

func (k nodeOperationK8s) Do(_ context.Context, _ string, method, path string, body []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	return k.do(method, path, body)
}

func encodedNodeResponse(status int, value any) *protocol.K8sResponsePayload {
	body, _ := json.Marshal(value)
	return &protocol.K8sResponsePayload{StatusCode: status, Body: base64.StdEncoding.EncodeToString(body)}
}

func runNodeDrainTest(t *testing.T, operation sqlc.NodeOperation, store *nodeOperationTaskStore, k8s K8sRequester) error {
	t.Helper()
	enc := resourceTaskEncryptor(t)
	parameters, err := enc.Encrypt(`{"ignore_daemonsets":true}`)
	if err != nil {
		t.Fatal(err)
	}
	operation.ParametersEncrypted = parameters
	store.operation = operation
	task, err := NewNodeOperationTask(operation.ID, operation.Generation)
	if err != nil {
		t.Fatal(err)
	}
	runtime := CoreRuntime{Deps: RuntimeDependencies{Queries: store, K8s: k8s, ResourceDecryptor: enc}}
	return HandleNodeOperation(runtime.Context(context.Background()), task)
}

func candidatePodList() map[string]any {
	return map[string]any{"items": []any{map[string]any{
		"metadata": map[string]any{
			"name": "api-1", "namespace": "payments",
			"ownerReferences": []any{map[string]any{"kind": "ReplicaSet"}},
		},
		"spec": map[string]any{"volumes": []any{}}, "status": map[string]any{"phase": "Running"},
	}}}
}

func TestNodeOperationTaskContainsIdentifiersOnly(t *testing.T) {
	if nodeOperationLease <= nodeOperationTimeout {
		t.Fatalf("lease %s must exceed timeout %s", nodeOperationLease, nodeOperationTimeout)
	}
	id := uuid.New()
	task, err := NewNodeOperationTask(id, 7)
	if err != nil {
		t.Fatal(err)
	}
	payload := string(task.Payload())
	if !strings.Contains(payload, id.String()) || !strings.Contains(payload, `"generation":7`) {
		t.Fatalf("missing identity fence: %s", payload)
	}
	for _, forbidden := range []string{"node_name", "key", "value", "effect", "grace", "force"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("task leaked %q: %s", forbidden, payload)
		}
	}
}

func TestNodeDrain409IsRetryableAndNeverClaimedSuccessful(t *testing.T) {
	op := sqlc.NodeOperation{ID: uuid.New(), ClusterID: uuid.New(), NodeName: "worker-1", Action: "drain", Generation: 1, Status: "running", Progress: json.RawMessage(`{}`)}
	store := &nodeOperationTaskStore{}
	k8s := nodeOperationK8s{do: func(method, path string, _ []byte) (*protocol.K8sResponsePayload, error) {
		switch method {
		case http.MethodPatch:
			return encodedNodeResponse(http.StatusOK, map[string]any{}), nil
		case http.MethodGet:
			return encodedNodeResponse(http.StatusOK, candidatePodList()), nil
		default:
			return encodedNodeResponse(http.StatusConflict, map[string]any{"message": "sentinel"}), nil
		}
	}}
	err := runNodeDrainTest(t, op, store, k8s)
	if err == nil || len(store.retrying) != 1 || store.retrying[0].ErrorCode != "kubernetes_conflict" || len(store.succeeded) != 0 || len(store.failed) != 0 {
		t.Fatalf("409 truthfulness: err=%v retrying=%+v succeeded=%+v failed=%+v", err, store.retrying, store.succeeded, store.failed)
	}
}

func TestNodeDrain429PersistsPartialPDBProgress(t *testing.T) {
	op := sqlc.NodeOperation{ID: uuid.New(), ClusterID: uuid.New(), NodeName: "worker-1", Action: "drain", Generation: 1, Status: "running", Progress: json.RawMessage(`{}`)}
	store := &nodeOperationTaskStore{}
	k8s := nodeOperationK8s{do: func(method, path string, _ []byte) (*protocol.K8sResponsePayload, error) {
		if method == http.MethodPatch {
			return encodedNodeResponse(http.StatusOK, map[string]any{}), nil
		}
		if method == http.MethodGet {
			return encodedNodeResponse(http.StatusOK, candidatePodList()), nil
		}
		return encodedNodeResponse(http.StatusTooManyRequests, map[string]any{"message": "pdb details"}), nil
	}}
	err := runNodeDrainTest(t, op, store, k8s)
	if err == nil || len(store.retrying) != 1 || store.retrying[0].ErrorCode != "pdb_blocked" {
		t.Fatalf("429 state: err=%v retrying=%+v", err, store.retrying)
	}
	var progress nodeOperationProgress
	_ = json.Unmarshal(store.retrying[0].Progress, &progress)
	if !progress.Cordoned || len(progress.Failed) != 1 || progress.Failed[0].Reason != "pdb_blocked" {
		t.Fatalf("partial progress not truthful: %+v", progress)
	}
}

func TestNodeDrainRetryClearsStaleFailedPods(t *testing.T) {
	prior := nodeOperationProgress{Cordoned: true, Failed: []nodeOperationPod{{Namespace: "payments", Name: "gone", Reason: "pdb_blocked"}}}
	op := sqlc.NodeOperation{ID: uuid.New(), ClusterID: uuid.New(), NodeName: "worker-1", Action: "drain", Generation: 1, Status: "running", Progress: encodeNodeProgress(prior)}
	store := &nodeOperationTaskStore{}
	k8s := nodeOperationK8s{do: func(method, _ string, _ []byte) (*protocol.K8sResponsePayload, error) {
		if method == http.MethodPatch {
			return encodedNodeResponse(http.StatusOK, map[string]any{}), nil
		}
		return encodedNodeResponse(http.StatusOK, map[string]any{"items": []any{}}), nil
	}}
	if err := runNodeDrainTest(t, op, store, k8s); err != nil {
		t.Fatal(err)
	}
	var progress nodeOperationProgress
	_ = json.Unmarshal(store.succeeded[0].Progress, &progress)
	if len(progress.Failed) != 0 || len(progress.Skipped) != 1 || progress.Skipped[0].Reason != "no_longer_present" {
		t.Fatalf("stale failed evidence survived success: %+v", progress)
	}
}

func TestNodeDrainDoesNotSucceedWhileEvictionRequestedPodStillExists(t *testing.T) {
	prior := nodeOperationProgress{Cordoned: true, Evicted: []nodeOperationPod{{Namespace: "payments", Name: "api-1"}}}
	op := sqlc.NodeOperation{ID: uuid.New(), ClusterID: uuid.New(), NodeName: "worker-1", Action: "drain", Generation: 1, Status: "running", Progress: encodeNodeProgress(prior)}
	store := &nodeOperationTaskStore{}
	evictions := 0
	k8s := nodeOperationK8s{do: func(method, _ string, _ []byte) (*protocol.K8sResponsePayload, error) {
		if method == http.MethodPatch {
			return encodedNodeResponse(http.StatusOK, map[string]any{}), nil
		}
		if method == http.MethodPost {
			evictions++
		}
		return encodedNodeResponse(http.StatusOK, candidatePodList()), nil
	}}
	err := runNodeDrainTest(t, op, store, k8s)
	if err == nil || len(store.retrying) != 1 || store.retrying[0].ErrorCode != "eviction_in_progress" || len(store.succeeded) != 0 || evictions != 0 {
		t.Fatalf("stuck eviction truthfulness: err=%v retrying=%+v succeeded=%+v evictions=%d", err, store.retrying, store.succeeded, evictions)
	}
}

func TestNodeDrainBlockedStillReportsCordon(t *testing.T) {
	op := sqlc.NodeOperation{ID: uuid.New(), ClusterID: uuid.New(), NodeName: "worker-1", Action: "drain", Generation: 1, Status: "running", Progress: json.RawMessage(`{}`)}
	store := &nodeOperationTaskStore{}
	unmanaged := candidatePodList()
	unmanaged["items"].([]any)[0].(map[string]any)["metadata"].(map[string]any)["ownerReferences"] = []any{}
	k8s := nodeOperationK8s{do: func(method, _ string, _ []byte) (*protocol.K8sResponsePayload, error) {
		if method == http.MethodPatch {
			return encodedNodeResponse(http.StatusOK, map[string]any{}), nil
		}
		return encodedNodeResponse(http.StatusOK, unmanaged), nil
	}}
	if err := runNodeDrainTest(t, op, store, k8s); err != nil {
		t.Fatal(err)
	}
	if len(store.blocked) != 1 || store.blocked[0].ErrorCode != "drain_blocked" {
		t.Fatalf("blocked terminal state missing: %+v", store.blocked)
	}
	var progress nodeOperationProgress
	_ = json.Unmarshal(store.blocked[0].Progress, &progress)
	if !progress.Cordoned || len(progress.Blockers) != 1 {
		t.Fatalf("blocked drain hid partial cordon: %+v", progress)
	}
}

func TestNodeErrorCategoryNeverContainsRemoteBody(t *testing.T) {
	if got := sanitizeNodeErrorCode("sentinel endpoint response-body"); got != "internal_error" {
		t.Fatalf("unexpected category %q", got)
	}
}

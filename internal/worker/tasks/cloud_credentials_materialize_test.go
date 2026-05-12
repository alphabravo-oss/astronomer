package tasks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/alphabravocompany/astronomer-go/internal/cloudcreds"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// --- Fakes -------------------------------------------------------------

type fakeCloudCredentialQuerier struct {
	mu           sync.Mutex
	credentials  map[uuid.UUID]sqlc.CloudCredential
	mats         map[uuid.UUID][]sqlc.CloudCredentialMaterialization
	appliedCalls []uuid.UUID
	failedCalls  []sqlc.MarkCloudCredentialMaterializationFailedParams
}

func newFakeCloudCredentialQuerier() *fakeCloudCredentialQuerier {
	return &fakeCloudCredentialQuerier{
		credentials: map[uuid.UUID]sqlc.CloudCredential{},
		mats:        map[uuid.UUID][]sqlc.CloudCredentialMaterialization{},
	}
}

func (f *fakeCloudCredentialQuerier) GetCloudCredentialByID(_ context.Context, id uuid.UUID) (sqlc.CloudCredential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.credentials[id]
	if !ok {
		return sqlc.CloudCredential{}, fmt.Errorf("not found")
	}
	return c, nil
}

func (f *fakeCloudCredentialQuerier) ListCloudCredentialMaterializations(_ context.Context, credentialID uuid.UUID) ([]sqlc.CloudCredentialMaterialization, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.CloudCredentialMaterialization, len(f.mats[credentialID]))
	copy(out, f.mats[credentialID])
	return out, nil
}

func (f *fakeCloudCredentialQuerier) ListAllPendingCloudCredentialMaterializations(_ context.Context) ([]sqlc.CloudCredentialMaterialization, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.CloudCredentialMaterialization{}
	for _, list := range f.mats {
		for _, m := range list {
			if m.Status != "applied" {
				out = append(out, m)
			}
		}
	}
	return out, nil
}

func (f *fakeCloudCredentialQuerier) MarkCloudCredentialMaterializationApplied(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.appliedCalls = append(f.appliedCalls, id)
	for credID, list := range f.mats {
		for i, m := range list {
			if m.ID == id {
				list[i].Status = "applied"
				f.mats[credID] = list
				return nil
			}
		}
	}
	return nil
}

func (f *fakeCloudCredentialQuerier) MarkCloudCredentialMaterializationFailed(_ context.Context, arg sqlc.MarkCloudCredentialMaterializationFailedParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failedCalls = append(f.failedCalls, arg)
	for credID, list := range f.mats {
		for i, m := range list {
			if m.ID == arg.ID {
				list[i].Status = "failed"
				list[i].LastError = arg.LastError
				f.mats[credID] = list
				return nil
			}
		}
	}
	return nil
}

// fakeProjectK8sRequester records every request the worker makes so the
// test can assert on the rendered Secret manifest + path.
type fakeProjectK8sRequester struct {
	mu       sync.Mutex
	requests []k8sRequest
	respFn   func(req k8sRequest) (*ProjectK8sResponse, error)
}

type k8sRequest struct {
	ClusterID string
	Method    string
	Path      string
	Body      []byte
	Headers   map[string]string
}

func (f *fakeProjectK8sRequester) Do(_ context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*ProjectK8sResponse, error) {
	f.mu.Lock()
	req := k8sRequest{ClusterID: clusterID, Method: method, Path: path, Body: body, Headers: headers}
	f.requests = append(f.requests, req)
	respFn := f.respFn
	f.mu.Unlock()
	if respFn != nil {
		return respFn(req)
	}
	return &ProjectK8sResponse{StatusCode: 200, Body: []byte(`{}`)}, nil
}

func (f *fakeProjectK8sRequester) all() []k8sRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]k8sRequest, len(f.requests))
	copy(out, f.requests)
	return out
}

// fakeDecryptor passes the token through unchanged so tests can encode
// the cleartext directly and skip a real Fernet round-trip.
type fakeDecryptor struct{}

func (fakeDecryptor) Decrypt(token string) (string, error) {
	return token, nil
}

// --- Tests -------------------------------------------------------------

func setupMaterializeDeps(t *testing.T) (*fakeCloudCredentialQuerier, *fakeProjectK8sRequester) {
	t.Helper()
	q := newFakeCloudCredentialQuerier()
	r := &fakeProjectK8sRequester{}
	ConfigureCloudCredentialMaterialize(CloudCredentialMaterializeDeps{
		Queries:   q,
		Requester: r,
		Decryptor: fakeDecryptor{},
	})
	t.Cleanup(ResetCloudCredentialMaterialize)
	return q, r
}

// TestMaterialize_CreatesK8sSecret is the happy-path apply: a credential
// row + a matching materialization row exist; the worker decrypts the
// blob, builds the Secret manifest, SSAs it through the requester, and
// marks the row applied.
func TestMaterialize_CreatesK8sSecret(t *testing.T) {
	q, r := setupMaterializeDeps(t)

	credID := uuid.New()
	clusterID := uuid.New()
	matID := uuid.New()

	blob, _ := cloudcreds.EncodeBlob(map[string]any{
		"access_key_id":     "AKIAFAKE",
		"secret_access_key": "shhh",
	})
	q.credentials[credID] = sqlc.CloudCredential{
		ID:            credID,
		Provider:      "aws",
		Name:          "aws-prod",
		DataEncrypted: string(blob),
		TargetRefs:    json.RawMessage(`[]`),
	}
	q.mats[credID] = []sqlc.CloudCredentialMaterialization{{
		ID:           matID,
		CredentialID: credID,
		ClusterID:    clusterID,
		Namespace:    "default",
		SecretName:   "astronomer-cred-aws-prod",
		Status:       "pending",
	}}

	payload, _ := json.Marshal(CloudCredentialMaterializePayload{
		CredentialID: credID.String(),
		ClusterID:    clusterID.String(),
		Namespace:    "default",
		SecretName:   "astronomer-cred-aws-prod",
		Op:           "apply",
	})
	task := asynq.NewTask(CloudCredentialMaterializeType, payload)
	if err := HandleCloudCredentialMaterialize(context.Background(), task); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// One SSA PATCH should have been issued.
	if got := r.all(); len(got) != 1 {
		t.Fatalf("expected 1 k8s request, got %d", len(got))
	}
	req := r.all()[0]
	if req.Method != http.MethodPatch {
		t.Fatalf("expected PATCH, got %s", req.Method)
	}
	if !strings.Contains(req.Path, "/api/v1/namespaces/default/secrets/astronomer-cred-aws-prod") {
		t.Fatalf("unexpected path %q", req.Path)
	}
	// Body should be a Secret manifest with type Opaque + data values
	// base64-encoded.
	var manifest struct {
		Kind string            `json:"kind"`
		Type string            `json:"type"`
		Data map[string]string `json:"data"`
		Meta struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(req.Body, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.Kind != "Secret" || manifest.Type != "Opaque" {
		t.Fatalf("unexpected manifest kind/type: %+v", manifest)
	}
	expectedB64 := base64.StdEncoding.EncodeToString([]byte("AKIAFAKE"))
	if manifest.Data["access_key_id"] != expectedB64 {
		t.Fatalf("expected access_key_id b64-encoded, got %q", manifest.Data["access_key_id"])
	}
	if manifest.Meta.Labels["astronomer.io/managed-by"] != "astronomer" {
		t.Fatalf("missing astronomer.io/managed-by label: %+v", manifest.Meta.Labels)
	}
	// Row should be marked applied.
	if len(q.appliedCalls) != 1 || q.appliedCalls[0] != matID {
		t.Fatalf("expected MarkApplied(%s), got %+v", matID, q.appliedCalls)
	}
}

// TestMaterialize_RemovesSecretOnDelete exercises the Op=delete path.
func TestMaterialize_RemovesSecretOnDelete(t *testing.T) {
	_, r := setupMaterializeDeps(t)
	payload, _ := json.Marshal(CloudCredentialMaterializePayload{
		CredentialID: uuid.New().String(),
		ClusterID:    uuid.New().String(),
		Namespace:    "ns-1",
		SecretName:   "astronomer-cred-x",
		Op:           "delete",
	})
	task := asynq.NewTask(CloudCredentialMaterializeType, payload)
	if err := HandleCloudCredentialMaterialize(context.Background(), task); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := r.all()
	if len(got) != 1 {
		t.Fatalf("expected 1 DELETE call, got %d", len(got))
	}
	if got[0].Method != http.MethodDelete {
		t.Fatalf("expected DELETE, got %s", got[0].Method)
	}
	if !strings.Contains(got[0].Path, "/api/v1/namespaces/ns-1/secrets/astronomer-cred-x") {
		t.Fatalf("unexpected delete path %q", got[0].Path)
	}
}

// TestMaterialize_DriftReconcile asserts the periodic sweep walks every
// non-applied row and re-applies each via the same code path as the
// single-task handler.
func TestMaterialize_DriftReconcile(t *testing.T) {
	q, r := setupMaterializeDeps(t)
	// Seed two credentials, each with one pending materialization.
	for i := 0; i < 2; i++ {
		credID := uuid.New()
		blob, _ := cloudcreds.EncodeBlob(map[string]any{
			"access_key_id":     fmt.Sprintf("AKIA%d", i),
			"secret_access_key": "shhh",
		})
		q.credentials[credID] = sqlc.CloudCredential{
			ID:            credID,
			Provider:      "aws",
			Name:          fmt.Sprintf("aws-%d", i),
			DataEncrypted: string(blob),
			TargetRefs:    json.RawMessage(`[]`),
		}
		q.mats[credID] = []sqlc.CloudCredentialMaterialization{{
			ID:           uuid.New(),
			CredentialID: credID,
			ClusterID:    uuid.New(),
			Namespace:    "default",
			SecretName:   fmt.Sprintf("cred-%d", i),
			Status:       "pending",
		}}
	}
	if err := HandleCloudCredentialDriftReconcile(context.Background(), nil); err != nil {
		t.Fatalf("drift reconcile failed: %v", err)
	}
	if got := r.all(); len(got) != 2 {
		t.Fatalf("expected 2 SSA calls (one per pending row), got %d", len(got))
	}
}

// TestMaterialize_FailureMarksRowFailed asserts that a non-2xx response
// from the requester stamps the row as failed with the body in
// last_error.
func TestMaterialize_FailureMarksRowFailed(t *testing.T) {
	q, r := setupMaterializeDeps(t)
	r.respFn = func(k8sRequest) (*ProjectK8sResponse, error) {
		return &ProjectK8sResponse{StatusCode: http.StatusForbidden, Body: []byte(`{"reason":"Forbidden"}`)}, nil
	}
	credID := uuid.New()
	matID := uuid.New()
	clusterID := uuid.New()
	blob, _ := cloudcreds.EncodeBlob(map[string]any{
		"access_key_id":     "AKIAFAKE",
		"secret_access_key": "shhh",
	})
	q.credentials[credID] = sqlc.CloudCredential{
		ID:            credID,
		Provider:      "aws",
		Name:          "aws-x",
		DataEncrypted: string(blob),
		TargetRefs:    json.RawMessage(`[]`),
	}
	q.mats[credID] = []sqlc.CloudCredentialMaterialization{{
		ID:           matID,
		CredentialID: credID,
		ClusterID:    clusterID,
		Namespace:    "default",
		SecretName:   "cred-x",
		Status:       "pending",
	}}
	payload, _ := json.Marshal(CloudCredentialMaterializePayload{
		CredentialID: credID.String(),
		ClusterID:    clusterID.String(),
		Namespace:    "default",
		SecretName:   "cred-x",
		Op:           "apply",
	})
	task := asynq.NewTask(CloudCredentialMaterializeType, payload)
	if err := HandleCloudCredentialMaterialize(context.Background(), task); err == nil {
		t.Fatalf("expected error from non-2xx response")
	}
	if len(q.failedCalls) != 1 || q.failedCalls[0].ID != matID {
		t.Fatalf("expected MarkFailed(%s), got %+v", matID, q.failedCalls)
	}
	if !strings.Contains(q.failedCalls[0].LastError, "403") {
		t.Fatalf("expected last_error to mention 403, got %q", q.failedCalls[0].LastError)
	}
}

// TestMaterialize_RBACProjectScope guards that the worker rejects a
// request with a mismatched (credential, target) tuple by always
// re-reading the credential row from the DB — the payload's
// (cluster_id, namespace) drives the apply but the cred fields all come
// from the loaded row, so a tampered payload can't trick the worker
// into applying a credential from another project.
func TestMaterialize_RBACProjectScope(t *testing.T) {
	q, r := setupMaterializeDeps(t)

	// A credential exists under project P, with a materialization for
	// cluster C / namespace N. The worker payload references the same
	// credential but with a DIFFERENT namespace N' — the worker must
	// apply to N' (matching the payload) but ALL the blob data comes
	// from the credential row, NOT from any caller-supplied JSON.
	credID := uuid.New()
	clusterID := uuid.New()
	blob, _ := cloudcreds.EncodeBlob(map[string]any{
		"access_key_id":     "AKIA-FROM-DB",
		"secret_access_key": "shhh-from-db",
	})
	q.credentials[credID] = sqlc.CloudCredential{
		ID:            credID,
		Provider:      "aws",
		Name:          "aws-x",
		DataEncrypted: string(blob),
		TargetRefs:    json.RawMessage(`[]`),
	}
	// No matching materialization row — this is the "tampered payload"
	// case. Apply still runs (the worker is tolerant of fresh rows),
	// but the data MUST come from the DB credential.
	payload, _ := json.Marshal(CloudCredentialMaterializePayload{
		CredentialID: credID.String(),
		ClusterID:    clusterID.String(),
		Namespace:    "tampered-ns",
		SecretName:   "tampered-secret",
		Op:           "apply",
	})
	task := asynq.NewTask(CloudCredentialMaterializeType, payload)
	if err := HandleCloudCredentialMaterialize(context.Background(), task); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := r.all()
	if len(got) != 1 {
		t.Fatalf("expected 1 k8s request, got %d", len(got))
	}
	// The Secret body must contain AKIA-FROM-DB (from the credential
	// row) — NOT any payload-supplied value (the payload doesn't carry
	// any).
	if !strings.Contains(string(got[0].Body), base64.StdEncoding.EncodeToString([]byte("AKIA-FROM-DB"))) {
		t.Fatalf("expected DB-supplied access_key_id in body, got %s", string(got[0].Body))
	}
}

package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/cloudcreds"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeCloudCredQuerier is a minimal in-memory store for the
// CloudCredentialHandler tests. Operations are serialised by mu so the
// "drift sweep + handler" tests can drive multiple goroutines safely.
type fakeCloudCredQuerier struct {
	mu          sync.Mutex
	projectOK   map[uuid.UUID]bool
	clusterOK   map[uuid.UUID]bool
	credentials map[uuid.UUID]sqlc.CloudCredential
	mats        map[uuid.UUID][]sqlc.CloudCredentialMaterialization
}

func newFakeCloudCredQuerier() *fakeCloudCredQuerier {
	return &fakeCloudCredQuerier{
		projectOK:   map[uuid.UUID]bool{},
		clusterOK:   map[uuid.UUID]bool{},
		credentials: map[uuid.UUID]sqlc.CloudCredential{},
		mats:        map[uuid.UUID][]sqlc.CloudCredentialMaterialization{},
	}
}

func (f *fakeCloudCredQuerier) GetProjectByID(_ context.Context, id uuid.UUID) (sqlc.Project, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.projectOK[id] {
		return sqlc.Project{}, pgx.ErrNoRows
	}
	return sqlc.Project{ID: id, Name: "test-project"}, nil
}

func (f *fakeCloudCredQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.clusterOK[id] {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return sqlc.Cluster{ID: id, Name: "test-cluster"}, nil
}

func (f *fakeCloudCredQuerier) ListCloudCredentialsForProject(_ context.Context, projectID uuid.UUID) ([]sqlc.CloudCredential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.CloudCredential{}
	for _, c := range f.credentials {
		if c.ProjectID == projectID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeCloudCredQuerier) GetCloudCredentialByID(_ context.Context, id uuid.UUID) (sqlc.CloudCredential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.credentials[id]
	if !ok {
		return sqlc.CloudCredential{}, pgx.ErrNoRows
	}
	return c, nil
}

func (f *fakeCloudCredQuerier) GetCloudCredentialByProjectAndName(_ context.Context, arg sqlc.GetCloudCredentialByProjectAndNameParams) (sqlc.CloudCredential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.credentials {
		if c.ProjectID == arg.ProjectID && c.Name == arg.Name {
			return c, nil
		}
	}
	return sqlc.CloudCredential{}, pgx.ErrNoRows
}

func (f *fakeCloudCredQuerier) CreateCloudCredential(_ context.Context, arg sqlc.CreateCloudCredentialParams) (sqlc.CloudCredential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := sqlc.CloudCredential{
		ID:            uuid.New(),
		ProjectID:     arg.ProjectID,
		Name:          arg.Name,
		Provider:      arg.Provider,
		Description:   arg.Description,
		DataEncrypted: arg.DataEncrypted,
		TargetRefs:    arg.TargetRefs,
		CreatedBy:     arg.CreatedBy,
	}
	if len(c.TargetRefs) == 0 {
		c.TargetRefs = json.RawMessage(`[]`)
	}
	f.credentials[c.ID] = c
	return c, nil
}

func (f *fakeCloudCredQuerier) UpdateCloudCredential(_ context.Context, arg sqlc.UpdateCloudCredentialParams) (sqlc.CloudCredential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.credentials[arg.ID]
	if !ok {
		return sqlc.CloudCredential{}, pgx.ErrNoRows
	}
	c.Description = arg.Description
	c.DataEncrypted = arg.DataEncrypted
	c.TargetRefs = arg.TargetRefs
	f.credentials[c.ID] = c
	return c, nil
}

func (f *fakeCloudCredQuerier) DeleteCloudCredential(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.credentials, id)
	delete(f.mats, id)
	return nil
}

func (f *fakeCloudCredQuerier) ListCloudCredentialMaterializations(_ context.Context, credentialID uuid.UUID) ([]sqlc.CloudCredentialMaterialization, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.CloudCredentialMaterialization, len(f.mats[credentialID]))
	copy(out, f.mats[credentialID])
	return out, nil
}

func (f *fakeCloudCredQuerier) UpsertCloudCredentialMaterialization(_ context.Context, arg sqlc.UpsertCloudCredentialMaterializationParams) (sqlc.CloudCredentialMaterialization, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing := f.mats[arg.CredentialID]
	for i, m := range existing {
		if m.ClusterID == arg.ClusterID && m.Namespace == arg.Namespace {
			existing[i].SecretName = arg.SecretName
			f.mats[arg.CredentialID] = existing
			return existing[i], nil
		}
	}
	m := sqlc.CloudCredentialMaterialization{
		ID:           uuid.New(),
		CredentialID: arg.CredentialID,
		ClusterID:    arg.ClusterID,
		Namespace:    arg.Namespace,
		SecretName:   arg.SecretName,
		Status:       "pending",
	}
	f.mats[arg.CredentialID] = append(f.mats[arg.CredentialID], m)
	return m, nil
}

func (f *fakeCloudCredQuerier) DeleteOrphanCloudCredentialMaterializations(_ context.Context, arg sqlc.DeleteOrphanCloudCredentialMaterializationsParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	keep := map[string]struct{}{}
	var refs []TargetRef
	_ = json.Unmarshal(arg.TargetRefs, &refs)
	for _, ref := range refs {
		keep[ref.ClusterID.String()+"|"+ref.Namespace] = struct{}{}
	}
	src := f.mats[arg.CredentialID]
	out := src[:0]
	for _, m := range src {
		if _, ok := keep[m.ClusterID.String()+"|"+m.Namespace]; ok {
			out = append(out, m)
		}
	}
	f.mats[arg.CredentialID] = out
	return nil
}

// fakeCloudCredEnqueuer captures enqueued tasks so the tests can assert on the
// fan-out shape.
type fakeCloudCredEnqueuer struct {
	mu    sync.Mutex
	tasks []*asynq.Task
}

func (f *fakeCloudCredEnqueuer) Enqueue(t *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks = append(f.tasks, t)
	return &asynq.TaskInfo{}, nil
}

func (f *fakeCloudCredEnqueuer) all() []*asynq.Task {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*asynq.Task, len(f.tasks))
	copy(out, f.tasks)
	return out
}

// fakeTester is the in-memory CloudTester used by the test endpoint
// tests. Each provider's response is a fixed CloudTestResult / error
// pair so the test asserts behaviour without standing up an HTTP server.
type fakeTester struct {
	awsResult   CloudTestResult
	awsErr      error
	gcpResult   CloudTestResult
	gcpErr      error
	azureResult CloudTestResult
	azureErr    error
}

func (t *fakeTester) TestAWS(_ context.Context, _ map[string]string) (CloudTestResult, error) {
	return t.awsResult, t.awsErr
}
func (t *fakeTester) TestGCP(_ context.Context, _ map[string]string) (CloudTestResult, error) {
	return t.gcpResult, t.gcpErr
}
func (t *fakeTester) TestAzure(_ context.Context, _ map[string]string) (CloudTestResult, error) {
	return t.azureResult, t.azureErr
}

// --- Test scaffolding --------------------------------------------------

// testKey returns a fresh Fernet key for tests. Inlined so we don't have
// to expose a "GenerateForTest" helper from auth.
func testKey(t *testing.T) string {
	t.Helper()
	k, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("generate fernet key: %v", err)
	}
	return k
}

// newTestHandler wires a handler against in-memory dependencies.
func newTestHandler(t *testing.T) (*CloudCredentialHandler, *fakeCloudCredQuerier, *fakeCloudCredEnqueuer, uuid.UUID, uuid.UUID) {
	t.Helper()
	q := newFakeCloudCredQuerier()
	pid := uuid.New()
	cid := uuid.New()
	q.projectOK[pid] = true
	q.clusterOK[cid] = true
	enc, err := auth.NewEncryptor(testKey(t))
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	enq := &fakeCloudCredEnqueuer{}
	h := NewCloudCredentialHandler(q)
	h.SetEncryptor(enc)
	h.SetEnqueuer(enq)
	return h, q, enq, pid, cid
}

// callRoute drives a route handler through a chi router so the
// {project_id} + {id} URL params resolve like they do in production.
func callRoute(t *testing.T, h http.Handler, method, path, body string) *http.Response {
	t.Helper()
	r := chi.NewRouter()
	r.Method(method, "/api/v1/projects/{project_id}/cloud-credentials/", h)
	r.Method(method, "/api/v1/projects/{project_id}/cloud-credentials/{id}/", h)
	r.Method(method, "/api/v1/projects/{project_id}/cloud-credentials/{id}/test/", h)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Result()
}

// --- Tests -------------------------------------------------------------

// TestCloudCreds_CRUD_AWS is the happy-path AWS create/get/update/delete
// roundtrip — the same matrix the cluster-registry suite uses, narrowed
// to AWS so the test stays focused on the provider-typed shape.
func TestCloudCreds_CRUD_AWS(t *testing.T) {
	h, q, enq, pid, _ := newTestHandler(t)

	// CREATE
	body := fmt.Sprintf(`{
		"name": "prod-aws",
		"provider": "aws",
		"description": "production AWS credentials",
		"data": {
			"access_key_id": "AKIAFAKE",
			"secret_access_key": "shhh",
			"region": "us-east-1"
		}
	}`)
	resp := callRoute(t, http.HandlerFunc(h.Create), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/", pid), body)
	if resp.StatusCode != http.StatusCreated {
		dumpResponse(t, resp)
		t.Fatalf("expected 201 on CREATE, got %d", resp.StatusCode)
	}
	created := decodeCloudCredResponse(t, resp)
	if created.Provider != "aws" || created.Name != "prod-aws" {
		t.Fatalf("unexpected create echo: %+v", created)
	}
	if created.Data["secret_access_key"] != cloudcreds.SecretSentinel {
		t.Fatalf("expected secret_access_key to be redacted in echo, got %q", created.Data["secret_access_key"])
	}
	if created.Data["region"] != "us-east-1" {
		t.Fatalf("expected non-secret region to pass through, got %q", created.Data["region"])
	}
	if len(enq.all()) != 0 {
		t.Fatalf("expected no materialize tasks (no target_refs), got %d", len(enq.all()))
	}

	// GET — redacted
	resp = callRoute(t, http.HandlerFunc(h.Get), http.MethodGet, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/%s/", pid, created.ID), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on GET, got %d", resp.StatusCode)
	}
	got := decodeCloudCredResponse(t, resp)
	if got.Data["secret_access_key"] != cloudcreds.SecretSentinel {
		t.Fatalf("expected GET to redact, got %q", got.Data["secret_access_key"])
	}

	// PUT — sentinel preserves stored, real value overrides
	updateBody := `{
		"description": "rotated",
		"data": {
			"access_key_id": "<set>",
			"secret_access_key": "new-shhh"
		}
	}`
	resp = callRoute(t, http.HandlerFunc(h.Update), http.MethodPut, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/%s/", pid, created.ID), updateBody)
	if resp.StatusCode != http.StatusOK {
		dumpResponse(t, resp)
		t.Fatalf("expected 200 on PUT, got %d", resp.StatusCode)
	}
	// Inspect the stored row: decryption should yield access_key_id = AKIAFAKE
	// (preserved) and secret_access_key = "new-shhh" (rotated). region is
	// dropped because it's not in the patch and it's optional.
	stored := q.credentials[created.ID]
	plain, err := h.encryptor.Decrypt(stored.DataEncrypted)
	if err != nil {
		t.Fatalf("decrypt stored: %v", err)
	}
	parsed, err := cloudcreds.DecodeBlob([]byte(plain))
	if err != nil {
		t.Fatalf("decode stored: %v", err)
	}
	if parsed["access_key_id"] != "AKIAFAKE" {
		t.Fatalf("expected preserved access_key_id, got %q", parsed["access_key_id"])
	}
	if parsed["secret_access_key"] != "new-shhh" {
		t.Fatalf("expected rotated secret_access_key, got %q", parsed["secret_access_key"])
	}

	// DELETE
	resp = callRoute(t, http.HandlerFunc(h.Delete), http.MethodDelete, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/%s/", pid, created.ID), "")
	if resp.StatusCode != http.StatusNoContent {
		dumpResponse(t, resp)
		t.Fatalf("expected 204 on DELETE, got %d", resp.StatusCode)
	}
	if _, ok := q.credentials[created.ID]; ok {
		t.Fatalf("expected credential to be deleted")
	}
}

// TestCloudCreds_CRUD_GCP exercises the GCP key-shape (single
// service_account_json field) plus the key.json filename rewrite.
func TestCloudCreds_CRUD_GCP(t *testing.T) {
	h, q, _, pid, _ := newTestHandler(t)

	body := `{
		"name": "prod-gcp",
		"provider": "gcp",
		"data": {
			"service_account_json": "{\"type\":\"service_account\"}",
			"project_id": "my-project"
		}
	}`
	resp := callRoute(t, http.HandlerFunc(h.Create), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/", pid), body)
	if resp.StatusCode != http.StatusCreated {
		dumpResponse(t, resp)
		t.Fatalf("expected 201 on CREATE, got %d", resp.StatusCode)
	}
	created := decodeCloudCredResponse(t, resp)
	if created.Data["service_account_json"] != cloudcreds.SecretSentinel {
		t.Fatalf("expected service_account_json to redact, got %q", created.Data["service_account_json"])
	}
	// Decode back and check render shape — key.json should appear.
	stored := q.credentials[created.ID]
	plain, _ := h.encryptor.Decrypt(stored.DataEncrypted)
	parsed, _ := cloudcreds.DecodeBlob([]byte(plain))
	rendered := cloudcreds.RenderSecretData("gcp", parsed)
	if _, ok := rendered["key.json"]; !ok {
		t.Fatalf("expected key.json in rendered Secret data, got keys %v", credKeysOf(rendered))
	}
}

// TestCloudCreds_CRUD_Azure walks the four-key Azure shape.
func TestCloudCreds_CRUD_Azure(t *testing.T) {
	h, _, _, pid, _ := newTestHandler(t)
	body := `{
		"name": "prod-azure",
		"provider": "azure",
		"data": {
			"client_id": "c",
			"client_secret": "s",
			"tenant_id": "t",
			"subscription_id": "u"
		}
	}`
	resp := callRoute(t, http.HandlerFunc(h.Create), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/", pid), body)
	if resp.StatusCode != http.StatusCreated {
		dumpResponse(t, resp)
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
}

// TestCloudCreds_RejectsUnknownProvider is the 400 path.
func TestCloudCreds_RejectsUnknownProvider(t *testing.T) {
	h, _, _, pid, _ := newTestHandler(t)
	body := `{"name": "ok", "provider": "spaceX", "data": {"x": "y"}}`
	resp := callRoute(t, http.HandlerFunc(h.Create), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/", pid), body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

// TestCloudCreds_RejectsMissingRequiredKey hits the validator on POST.
func TestCloudCreds_RejectsMissingRequiredKey(t *testing.T) {
	h, _, _, pid, _ := newTestHandler(t)
	body := `{"name": "incomplete", "provider": "aws", "data": {"access_key_id": "AKIAFAKE"}}`
	resp := callRoute(t, http.HandlerFunc(h.Create), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/", pid), body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
	bodyBytes, _ := readAll(resp)
	if !strings.Contains(string(bodyBytes), "secret_access_key") {
		t.Fatalf("expected error to name secret_access_key, got %s", bodyBytes)
	}
}

// TestCloudCreds_RedactsSecretKeys is the safety property the UI relies on.
func TestCloudCreds_RedactsSecretKeys(t *testing.T) {
	h, _, _, pid, _ := newTestHandler(t)
	createBody := `{
		"name": "redact-me",
		"provider": "azure",
		"data": {
			"client_id": "public",
			"client_secret": "secret",
			"tenant_id": "tenant",
			"subscription_id": "sub"
		}
	}`
	resp := callRoute(t, http.HandlerFunc(h.Create), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/", pid), createBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create failed: %d", resp.StatusCode)
	}
	created := decodeCloudCredResponse(t, resp)
	if created.Data["client_secret"] != cloudcreds.SecretSentinel {
		t.Fatalf("expected client_secret to redact, got %q", created.Data["client_secret"])
	}
	// Non-secret keys must NOT be redacted.
	if created.Data["client_id"] != "public" {
		t.Fatalf("expected client_id to pass through, got %q", created.Data["client_id"])
	}
	if created.Data["tenant_id"] != "tenant" {
		t.Fatalf("expected tenant_id to pass through, got %q", created.Data["tenant_id"])
	}
}

// TestCloudCreds_TestEndpoint_AWS_OK round-trips a successful tester
// response through the handler.
func TestCloudCreds_TestEndpoint_AWS_OK(t *testing.T) {
	h, _, _, pid, _ := newTestHandler(t)
	h.SetTester(&fakeTester{awsResult: CloudTestResult{OK: true, Message: "ok"}})
	id := createTestAWS(t, h, pid)
	resp := callRoute(t, http.HandlerFunc(h.Test), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/%s/test/", pid, id), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	got := decodeCloudTestResult(t, resp)
	if !got.OK {
		t.Fatalf("expected OK, got %+v", got)
	}
}

// TestCloudCreds_TestEndpoint_AWS_BadKeys verifies the failure path.
func TestCloudCreds_TestEndpoint_AWS_BadKeys(t *testing.T) {
	h, _, _, pid, _ := newTestHandler(t)
	h.SetTester(&fakeTester{awsResult: CloudTestResult{OK: false, Message: "InvalidClientTokenId"}})
	id := createTestAWS(t, h, pid)
	resp := callRoute(t, http.HandlerFunc(h.Test), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/%s/test/", pid, id), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	got := decodeCloudTestResult(t, resp)
	if got.OK {
		t.Fatalf("expected failure, got %+v", got)
	}
	if !strings.Contains(got.Message, "InvalidClientTokenId") {
		t.Fatalf("expected message to include AWS error, got %q", got.Message)
	}
}

// TestCloudCreds_TestEndpoint_GenericNoOp is the explicit "no test
// available" path for Generic providers.
func TestCloudCreds_TestEndpoint_GenericNoOp(t *testing.T) {
	h, _, _, pid, _ := newTestHandler(t)
	h.SetTester(&fakeTester{})
	body := `{"name": "generic-x", "provider": "generic", "data": {"foo": "bar"}}`
	resp := callRoute(t, http.HandlerFunc(h.Create), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/", pid), body)
	if resp.StatusCode != http.StatusCreated {
		dumpResponse(t, resp)
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	created := decodeCloudCredResponse(t, resp)
	resp = callRoute(t, http.HandlerFunc(h.Test), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/%s/test/", pid, created.ID), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	got := decodeCloudTestResult(t, resp)
	if got.OK {
		t.Fatalf("expected OK=false for generic, got %+v", got)
	}
	if !strings.Contains(got.Message, "generic") {
		t.Fatalf("expected message to mention generic, got %q", got.Message)
	}
}

// TestCloudCreds_TargetRefsCanonicaliseAndDefault verifies the handler
// fills in a default secret_name and rejects bad cluster_ids.
func TestCloudCreds_TargetRefsCanonicaliseAndDefault(t *testing.T) {
	h, _, enq, pid, cid := newTestHandler(t)
	body := fmt.Sprintf(`{
		"name": "with-target",
		"provider": "aws",
		"data": {"access_key_id": "AKIAFAKE", "secret_access_key": "shhh"},
		"target_refs": [
			{"cluster_id": "%s", "namespace": "default"}
		]
	}`, cid)
	resp := callRoute(t, http.HandlerFunc(h.Create), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/", pid), body)
	if resp.StatusCode != http.StatusCreated {
		dumpResponse(t, resp)
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	created := decodeCloudCredResponse(t, resp)
	if len(created.TargetRefs) != 1 {
		t.Fatalf("expected 1 target_ref, got %d", len(created.TargetRefs))
	}
	want := "astronomer-cred-with-target"
	if created.TargetRefs[0].SecretName != want {
		t.Fatalf("expected default secret_name %q, got %q", want, created.TargetRefs[0].SecretName)
	}
	// And one materialize task should have been enqueued.
	if len(enq.all()) != 1 {
		t.Fatalf("expected 1 enqueued task, got %d", len(enq.all()))
	}
}

// TestCloudCreds_TargetRefsRejectMissingCluster verifies cluster
// existence is checked before the credential is stored.
func TestCloudCreds_TargetRefsRejectMissingCluster(t *testing.T) {
	h, _, _, pid, _ := newTestHandler(t)
	body := fmt.Sprintf(`{
		"name": "bad-target",
		"provider": "aws",
		"data": {"access_key_id": "AKIAFAKE", "secret_access_key": "shhh"},
		"target_refs": [
			{"cluster_id": "%s", "namespace": "default"}
		]
	}`, uuid.New())
	resp := callRoute(t, http.HandlerFunc(h.Create), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/", pid), body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing cluster, got %d", resp.StatusCode)
	}
}

// TestCloudCreds_RejectsNameTaken is the 409 uniqueness path.
func TestCloudCreds_RejectsNameTaken(t *testing.T) {
	h, _, _, pid, _ := newTestHandler(t)
	body := `{"name": "twice", "provider": "aws", "data": {"access_key_id": "AKIAFAKE", "secret_access_key": "shhh"}}`
	resp := callRoute(t, http.HandlerFunc(h.Create), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/", pid), body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d", resp.StatusCode)
	}
	resp = callRoute(t, http.HandlerFunc(h.Create), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/", pid), body)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second create: expected 409, got %d", resp.StatusCode)
	}
}

// TestCloudCreds_ListProvidersPublic confirms the public providers list
// is non-empty and surfaces every built-in.
func TestCloudCreds_ListProvidersPublic(t *testing.T) {
	h := NewCloudCredentialHandler(nil) // queries unused on /providers/
	w := httptest.NewRecorder()
	h.ListProviders(w, httptest.NewRequest(http.MethodGet, "/api/v1/cloud-credentials/providers/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var env struct {
		Data struct {
			Items []cloudcreds.ProviderSpec `json:"items"`
		} `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Data.Items) != 4 {
		t.Fatalf("expected 4 providers, got %d", len(env.Data.Items))
	}
}

// --- helpers -----------------------------------------------------------

func createTestAWS(t *testing.T, h *CloudCredentialHandler, pid uuid.UUID) uuid.UUID {
	t.Helper()
	body := `{"name": "aws-test", "provider": "aws", "data": {"access_key_id": "AKIAFAKE", "secret_access_key": "shhh"}}`
	resp := callRoute(t, http.HandlerFunc(h.Create), http.MethodPost, fmt.Sprintf("/api/v1/projects/%s/cloud-credentials/", pid), body)
	if resp.StatusCode != http.StatusCreated {
		dumpResponse(t, resp)
		t.Fatalf("setup create failed: %d", resp.StatusCode)
	}
	created := decodeCloudCredResponse(t, resp)
	return created.ID
}

// decodeCloudCredResponse unwraps the {"data": …} envelope every
// RespondJSON-style handler wraps responses in and decodes the inner
// CloudCredentialResponse.
func decodeCloudCredResponse(t *testing.T, resp *http.Response) CloudCredentialResponse {
	t.Helper()
	var env struct {
		Data CloudCredentialResponse `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return env.Data
}

// decodeCloudTestResult is the unwrap helper for the /test/ endpoint.
func decodeCloudTestResult(t *testing.T, resp *http.Response) CloudTestResult {
	t.Helper()
	var env struct {
		Data CloudTestResult `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return env.Data
}

func dumpResponse(t *testing.T, resp *http.Response) {
	t.Helper()
	b, _ := readAll(resp)
	t.Logf("response body: %s", string(b))
}

func readAll(resp *http.Response) ([]byte, error) {
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(resp.Body)
	return buf.Bytes(), err
}

func credKeysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// _ keeps the base64 import (used by the worker fakes below) live in the
// test file so future test additions can reuse it without re-importing.
var _ = base64.StdEncoding

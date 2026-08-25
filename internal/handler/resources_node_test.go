package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type resourceDrainCall struct {
	method string
	path   string
	body   []byte
}

type resourceDrainRequester struct {
	pods  drainPodList
	calls []resourceDrainCall
}

func (r *resourceDrainRequester) Do(_ context.Context, _ string, method, path string, body []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	r.calls = append(r.calls, resourceDrainCall{method: method, path: path, body: body})
	if method == http.MethodGet && strings.HasPrefix(path, "/api/v1/pods?") {
		return k8sJSONResponse(http.StatusOK, r.pods), nil
	}
	return k8sJSONResponse(http.StatusNotFound, map[string]any{"message": "not found"}), nil
}

type resourceMutationRequester struct {
	calls []resourceDrainCall
}

func (r *resourceMutationRequester) Do(_ context.Context, _ string, method, path string, body []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	r.calls = append(r.calls, resourceDrainCall{method: method, path: path, body: body})
	return k8sJSONResponse(http.StatusOK, map[string]any{"kind": "Status", "status": "Success"}), nil
}

type resourceAuditQuerier struct {
	rows []sqlc.CreateAuditLogV1Params
}

func (q *resourceAuditQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	q.rows = append(q.rows, arg)
	return nil
}

func (q *resourceAuditQuerier) GetPlatformConfig(context.Context) (sqlc.PlatformConfiguration, error) {
	return sqlc.PlatformConfiguration{}, nil
}
func (q *resourceAuditQuerier) UpsertPlatformConfig(context.Context, sqlc.UpsertPlatformConfigParams) (sqlc.PlatformConfiguration, error) {
	return sqlc.PlatformConfiguration{}, nil
}
func (q *resourceAuditQuerier) ListAuditLogV1(context.Context, sqlc.ListAuditLogsParams) ([]sqlc.AuditLog, error) {
	return nil, nil
}
func (q *resourceAuditQuerier) CountAuditLogV1(context.Context) (int64, error) { return 0, nil }
func (q *resourceAuditQuerier) ListUsers(context.Context, sqlc.ListUsersParams) ([]sqlc.User, error) {
	return nil, nil
}
func (q *resourceAuditQuerier) CountUsers(context.Context) (int64, error) { return 0, nil }
func (q *resourceAuditQuerier) GetUserByID(context.Context, uuid.UUID) (sqlc.User, error) {
	return sqlc.User{}, nil
}
func (q *resourceAuditQuerier) GetUserByEmail(context.Context, string) (sqlc.User, error) {
	return sqlc.User{}, nil
}
func (q *resourceAuditQuerier) GetUserByUsername(context.Context, string) (sqlc.User, error) {
	return sqlc.User{}, nil
}
func (q *resourceAuditQuerier) CreateUser(context.Context, sqlc.CreateUserParams) (sqlc.User, error) {
	return sqlc.User{}, nil
}
func (q *resourceAuditQuerier) UpdateUser(context.Context, sqlc.UpdateUserParams) (sqlc.User, error) {
	return sqlc.User{}, nil
}
func (q *resourceAuditQuerier) DeleteUser(context.Context, uuid.UUID) error { return nil }
func (q *resourceAuditQuerier) UpdateUserPassword(context.Context, sqlc.UpdateUserPasswordParams) error {
	return nil
}
func (q *resourceAuditQuerier) UnlockUser(context.Context, uuid.UUID) error { return nil }
func (q *resourceAuditQuerier) InvalidateAllTokens(context.Context, sqlc.InvalidateAllTokensParams) error {
	return nil
}

func TestResourceHandlerDrainDryRunIsSynchronousReadOnly(t *testing.T) {
	requester := &resourceDrainRequester{pods: drainPodList{Items: []drainPod{
		testDrainPod("default", "app-0", "ReplicaSet", false),
		testDrainPod("kube-system", "node-agent", "DaemonSet", false),
	}}}
	h := NewResourceHandlerWithRequester(requester)
	req := resourceRouteRequestWithBody(http.MethodPost, "/api/v1/nodes/cluster-1/node-1/drain/", map[string]string{
		"cluster_id": "cluster-1", "node_name": "node-1",
	}, `{"dry_run":true}`)
	req.Header.Set("Idempotency-Key", "node-drain-preview")
	recorder := httptest.NewRecorder()
	h.DrainNode(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data drainNodeResponse `json:"data"`
	}
	if json.NewDecoder(recorder.Body).Decode(&envelope) != nil || envelope.Data.Status != "dry_run" || len(envelope.Data.Evicted) != 1 || len(envelope.Data.Skipped) != 1 {
		t.Fatalf("dry-run response=%+v", envelope.Data)
	}
	if len(requester.calls) != 1 || requester.calls[0].method != http.MethodGet {
		t.Fatalf("dry-run performed a mutation: %+v", requester.calls)
	}
}

func TestResourceHandlerNamedResourceDryRunRemainsSynchronousAndAudited(t *testing.T) {
	requester := &resourceMutationRequester{}
	audit := &resourceAuditQuerier{}
	h := NewResourceHandlerWithQueries(audit, requester)
	req := resourceRouteRequestWithBody(http.MethodPut, "/api/v1/resources/cluster-1/services/default/demo/?dry_run=true", map[string]string{
		"cluster_id": "cluster-1", "type": "services", "namespace": "default", "name": "demo",
	}, `{"metadata":{"namespace":"default","name":"demo"}}`)
	recorder := httptest.NewRecorder()
	h.UpdateNamedResource(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("dry-run status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(requester.calls) != 1 || requester.calls[0].method != http.MethodPatch ||
		!strings.Contains(requester.calls[0].path, "fieldManager=astronomer") ||
		!strings.Contains(requester.calls[0].path, "dryRun=All") {
		t.Fatalf("expected synchronous no-effect SSA validation: %+v", requester.calls)
	}
}

func testDrainPod(namespace, name, ownerKind string, emptyDir bool) drainPod {
	var pod drainPod
	pod.Metadata.Namespace = namespace
	pod.Metadata.Name = name
	pod.Status.Phase = "Running"
	if ownerKind != "" {
		pod.Metadata.OwnerReferences = append(pod.Metadata.OwnerReferences, struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		}{Kind: ownerKind, Name: name + "-owner"})
	}
	if emptyDir {
		pod.Spec.Volumes = append(pod.Spec.Volumes, struct {
			Name     string         `json:"name"`
			EmptyDir map[string]any `json:"emptyDir,omitempty"`
		}{Name: "scratch", EmptyDir: map[string]any{"medium": ""}})
	}
	return pod
}

func resourceRouteRequestWithBody(method, target string, params map[string]string, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rctx := chi.NewRouteContext()
	for key, value := range params {
		rctx.URLParams.Add(key, value)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func k8sJSONResponse(status int, payload any) *protocol.K8sResponsePayload {
	body, _ := json.Marshal(payload)
	return &protocol.K8sResponsePayload{
		StatusCode: status,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       base64.StdEncoding.EncodeToString(body),
	}
}

package handler

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
)

func TestEventsContinueBeforeAuthorizedPagination(t *testing.T) {
	cluster := uuid.New()
	calls := 0
	stub := &stubK8sRequester{respFn: func(req stubReq) (*protocol.K8sResponsePayload, error) {
		calls++
		u, _ := url.Parse(req.Path)
		if u.Path != "/api/v1/namespaces/team-a/events" || u.Query().Get("limit") != "2" {
			t.Fatalf("unbounded or unauthorized request %s", req.Path)
		}
		id, next, ns := "first", "later", "team-a"
		if u.Query().Get("continue") != "" {
			id, next = "second", ""
		}
		items := []any{map[string]any{"metadata": map[string]any{"uid": id}, "involvedObject": map[string]any{"namespace": ns}, "lastTimestamp": "2026-09-24T00:00:00Z"}}
		// Defense in depth also excludes a wrong-namespace upstream event.
		if next != "" {
			items = append(items, map[string]any{"metadata": map[string]any{"uid": "forbidden"}, "involvedObject": map[string]any{"namespace": "other"}})
		}
		body, _ := json.Marshal(map[string]any{"metadata": map[string]any{"continue": next}, "items": items})
		return &protocol.K8sResponsePayload{StatusCode: 200, Body: base64.StdEncoding.EncodeToString(body)}, nil
	}}
	h := NewWorkloadHandlerWithRequester(stub)
	h.SetNamespaceScopedRBAC(true)
	h.SetAuthorization(rbac.NewEngine(), stubWorkloadRBACQuerier{bindings: []rbac.RoleBinding{{ClusterID: cluster.String(), Namespace: "team-a", RoleRules: []rbac.Rule{{Resource: "clusters", Verbs: []string{"read"}}}}}})
	rec := httptest.NewRecorder()
	h.ListEvents(rec, authedCatalogReq("GET", "/?limit=2&offset=1", map[string]string{"cluster_id": cluster.String()}))
	var out listEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || calls != 2 || out.Pagination.Total == nil || *out.Pagination.Total != 2 || len(out.Data) != 1 || out.Data[0]["id"] != "second" {
		t.Fatalf("wrong event page %d %s", rec.Code, rec.Body.String())
	}
}

func TestEventsRejectNonAdvancingContinuation(t *testing.T) {
	body := base64.StdEncoding.EncodeToString([]byte(`{"metadata":{"continue":"repeat"},"items":[]}`))
	stub := &stubK8sRequester{respFn: func(stubReq) (*protocol.K8sResponsePayload, error) {
		return &protocol.K8sResponsePayload{StatusCode: 200, Body: body}, nil
	}}
	h := NewWorkloadHandlerWithRequester(stub)
	rec := httptest.NewRecorder()
	h.ListEvents(rec, authedCatalogReq("GET", "/", map[string]string{"cluster_id": uuid.NewString()}))
	if rec.Code != 503 || len(stub.snapshot()) != 2 || !strings.Contains(rec.Body.String(), "proxy_error") {
		t.Fatalf("repeated continuation accepted %d %s", rec.Code, rec.Body.String())
	}
}

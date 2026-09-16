package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type resourceCountRequester struct {
	mu    sync.Mutex
	calls []resourceCountCall
}

type resourceCountCall struct {
	path    string
	headers map[string]string
}

func (f *resourceCountRequester) Do(_ context.Context, _, _, path string, _ []byte, headers map[string]string) (*protocol.K8sResponsePayload, error) {
	f.mu.Lock()
	f.calls = append(f.calls, resourceCountCall{path: path, headers: headers})
	f.mu.Unlock()
	body := `{"metadata":{"remainingItemCount":4},"items":[{}]}`
	return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString([]byte(body))}, nil
}

func (f *resourceCountRequester) snapshot() []resourceCountCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]resourceCountCall(nil), f.calls...)
}

func countRequest(t *testing.T, h *ResourceHandler, clusterID, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Get("/clusters/{cluster_id}/resource-counts/", h.CountResources)
	req := httptest.NewRequest(http.MethodGet, "/clusters/"+clusterID+"/resource-counts/?"+rawQuery, nil)
	req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: uuid.NewString()}))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestCountResourcesUsesMetadataOnlyListsAndRemainingCount(t *testing.T) {
	clusterID := uuid.New()
	requester := &resourceCountRequester{}
	h := NewResourceHandlerWithRequester(requester)
	h.SetAuthorization(rbac.NewEngine(), stubSearchRBACQuerier{bindings: []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceConfigMaps), Verbs: []string{string(rbac.VerbList)}}},
	}}})

	recorder := countRequest(t, h, clusterID.String(), "resources=configmaps")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data resourceCountsResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := envelope.Data.Counts["configmaps"]; got != 5 {
		t.Fatalf("configmap count=%d, want 5", got)
	}
	calls := requester.snapshot()
	if len(calls) != 1 || calls[0].path != "/api/v1/configmaps?limit=1" {
		t.Fatalf("calls=%+v", calls)
	}
	if got := calls[0].headers["Accept"]; got != metadataListAccept || strings.Contains(got, ",application/json") {
		t.Fatalf("Accept=%q, must require metadata-only representation", got)
	}
}

func TestCountResourcesEnforcesResourceAndNamespaceRBAC(t *testing.T) {
	clusterID := uuid.New()
	requester := &resourceCountRequester{}
	h := NewResourceHandlerWithRequester(requester)
	h.SetAuthorization(rbac.NewEngine(), stubSearchRBACQuerier{bindings: []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		Namespace: "team-a",
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourcePods), Verbs: []string{string(rbac.VerbList)}}},
	}}})

	recorder := countRequest(t, h, clusterID.String(), "resources=pods,secrets&namespace=team-a&namespace=team-b")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data resourceCountsResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := envelope.Data.Counts["pods"]; got != 5 {
		t.Fatalf("pod count=%d, want 5", got)
	}
	if _, leaked := envelope.Data.Counts["secrets"]; leaked {
		t.Fatal("unauthorized Secret count was disclosed")
	}
	calls := requester.snapshot()
	if len(calls) != 1 || calls[0].path != "/api/v1/namespaces/team-a/pods?limit=1" {
		t.Fatalf("calls=%+v, want one authorized namespace", calls)
	}
}

func TestCountResourcesRejectsInvalidOrUnboundedRequests(t *testing.T) {
	h := NewResourceHandlerWithRequester(&resourceCountRequester{})
	for _, query := range []string{"resources=made-up", "resources="} {
		recorder := countRequest(t, h, uuid.NewString(), query)
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("query %q status=%d body=%s", query, recorder.Code, recorder.Body.String())
		}
	}
}

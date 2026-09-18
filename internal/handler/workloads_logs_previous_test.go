package handler

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestPodLogsForwardsPreviousContainerFlag(t *testing.T) {
	var requestedPath string
	requester := &stubK8sRequester{respFn: func(req stubReq) (*protocol.K8sResponsePayload, error) {
		requestedPath = req.Path
		return &protocol.K8sResponsePayload{
			StatusCode: http.StatusOK,
			Body: base64.StdEncoding.EncodeToString(
				[]byte("2026-09-18T12:00:00Z previous process output\n"),
			),
		}, nil
	}}
	handler := NewWorkloadHandlerWithRequester(requester)
	clusterID := uuid.NewString()
	route := chi.NewRouteContext()
	route.URLParams.Add("cluster_id", clusterID)
	route.URLParams.Add("namespace", "apps")
	route.URLParams.Add("pod", "api-0")
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/clusters/"+clusterID+"/workloads/pods/apps/api-0/logs?container=api&previous=true&tail_lines=200",
		nil,
	)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, route))
	recorder := httptest.NewRecorder()

	handler.PodLogs(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	for _, part := range []string{"container=api", "previous=true", "tailLines=200", "timestamps=true"} {
		if !strings.Contains(requestedPath, part) {
			t.Fatalf("request path %q does not contain %q", requestedPath, part)
		}
	}
}

package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type clusterGrafanaAvailabilityRequester struct {
	proxyPresent bool
}

func (f clusterGrafanaAvailabilityRequester) Do(_ context.Context, _, _, path string, _ []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	response := func(status int, body string) *protocol.K8sResponsePayload {
		return &protocol.K8sResponsePayload{StatusCode: status, Body: base64.StdEncoding.EncodeToString([]byte(body))}
	}
	switch {
	case strings.Contains(path, "/services?labelSelector="):
		return response(http.StatusOK, `{"items":[{"metadata":{"name":"astronomer-monitoring-grafana"},"spec":{"ports":[{"port":80}]}}]}`), nil
	case strings.HasSuffix(path, "/services/astronomer-monitoring-grafana-proxy"):
		if !f.proxyPresent {
			return response(http.StatusNotFound, `{"kind":"Status","code":404}`), nil
		}
		return response(http.StatusOK, `{"metadata":{"name":"astronomer-monitoring-grafana-proxy"},"spec":{"ports":[{"port":8080}]}}`), nil
	case strings.Contains(path, "/pods?"):
		return response(http.StatusOK, `{"items":[]}`), nil
	default:
		return response(http.StatusNotFound, `{"kind":"Status","code":404}`), nil
	}
}

func TestClusterGrafanaAvailabilityRequiresAuthenticatedProxyService(t *testing.T) {
	for _, tc := range []struct {
		name         string
		proxyPresent bool
		want         bool
	}{
		{name: "legacy stack without proxy", proxyPresent: false, want: false},
		{name: "stack with proxy", proxyPresent: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, q := newStackLifecycleHandler(t)
			q.clusterErr = nil
			q.clusterCfg = sqlc.ClusterMonitoringConfig{
				ClusterID:             uuid.MustParse(stackTestClusterID),
				StackNamespace:        "astronomer-monitoring",
				PrometheusReleaseName: "astronomer-monitoring",
				Status:                "healthy",
			}
			h.requester = clusterGrafanaAvailabilityRequester{proxyPresent: tc.proxyPresent}

			router := chi.NewRouter()
			router.Get("/api/v1/clusters/{id}/monitoring/stack/status", h.GetStackStatus)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/clusters/"+stackTestClusterID+"/monitoring/stack/status", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			var body struct {
				Data struct {
					GrafanaAvailable bool   `json:"grafanaAvailable"`
					GrafanaProxyPath string `json:"grafanaProxyPath"`
				} `json:"data"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Data.GrafanaAvailable != tc.want {
				t.Fatalf("grafanaAvailable = %v, want %v", body.Data.GrafanaAvailable, tc.want)
			}
			if tc.want && body.Data.GrafanaProxyPath == "" {
				t.Fatal("available Grafana did not return its proxy path")
			}
			if !tc.want && body.Data.GrafanaProxyPath != "" {
				t.Fatalf("unavailable Grafana returned proxy path %q", body.Data.GrafanaProxyPath)
			}
		})
	}
}

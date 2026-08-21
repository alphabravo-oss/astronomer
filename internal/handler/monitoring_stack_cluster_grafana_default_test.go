package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

func TestMonitoringStackPayloadOmitsGrafanaWhenFleetHealthyAndNotConfigured(t *testing.T) {
	t.Parallel()

	previewGrafanaEnabled := func(t *testing.T, q *stackLifecycleQuerier, body string) bool {
		t.Helper()
		h := NewMonitoringHandlerWithDeps(q, grafanaPassingK8sFake(t), stackLifecycleHelmStub{})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters/"+stackTestClusterID+"/monitoring/stack/preview/", strings.NewReader(body))
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", stackTestClusterID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
		req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
			ID: uuid.NewString(), AuthMethod: "jwt",
		}))
		h.PreviewStack(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		var wrap struct {
			Data struct {
				Values map[string]any `json:"values"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
			t.Fatalf("decode: %v body=%s", err, rec.Body.String())
		}
		grafana, _ := wrap.Data.Values["grafana"].(map[string]any)
		enabled, ok := grafana["enabled"].(bool)
		if !ok {
			t.Fatalf("grafana.enabled missing or not bool: %#v", wrap.Data.Values["grafana"])
		}
		return enabled
	}

	healthyGrafana := json.RawMessage(`{"sharedGrafana":{"status":"healthy"}}`)
	degradedGrafana := json.RawMessage(`{"sharedGrafana":{"status":"degraded"}}`)
	omitted := `{}`
	explicitTrue := `{"enableGrafana":true}`
	explicitFalse := `{"enableGrafana":false}`

	t.Run("omitted+healthy+not_configured=false", func(t *testing.T) {
		_, q := newStackLifecycleHandler(t)
		q.backend.AuthConfig = healthyGrafana
		if previewGrafanaEnabled(t, q, omitted) {
			t.Fatal("new stacks must default Grafana off when fleet Grafana is healthy")
		}
	})

	t.Run("omitted+no_lobby=true", func(t *testing.T) {
		_, q := newStackLifecycleHandler(t)
		if !previewGrafanaEnabled(t, q, omitted) {
			t.Fatal("omitted enableGrafana must stay true when fleet Grafana is not healthy")
		}
	})

	t.Run("omitted+degraded=true", func(t *testing.T) {
		_, q := newStackLifecycleHandler(t)
		q.backend.AuthConfig = degradedGrafana
		if !previewGrafanaEnabled(t, q, omitted) {
			t.Fatal("degraded fleet Grafana must not change the historical true default")
		}
	})

	t.Run("explicit true still on", func(t *testing.T) {
		_, q := newStackLifecycleHandler(t)
		q.backend.AuthConfig = healthyGrafana
		if !previewGrafanaEnabled(t, q, explicitTrue) {
			t.Fatal("explicit enableGrafana=true must still install cluster Grafana")
		}
	})

	t.Run("explicit false still off", func(t *testing.T) {
		_, q := newStackLifecycleHandler(t)
		if previewGrafanaEnabled(t, q, explicitFalse) {
			t.Fatal("explicit enableGrafana=false must stay false")
		}
	})

	t.Run("omitted+healthy+already_configured=true", func(t *testing.T) {
		_, q := newStackLifecycleHandler(t)
		q.backend.AuthConfig = healthyGrafana
		q.clusterErr = nil
		q.clusterCfg = sqlc.ClusterMonitoringConfig{
			ClusterID: uuid.MustParse(stackTestClusterID),
			Status:    "healthy",
		}
		if !previewGrafanaEnabled(t, q, omitted) {
			t.Fatal("already-configured stacks must keep the historical true default on omitted enableGrafana")
		}
	})

	t.Run("omitted+healthy+uninstalled=true", func(t *testing.T) {
		_, q := newStackLifecycleHandler(t)
		q.backend.AuthConfig = healthyGrafana
		q.clusterErr = nil
		q.clusterCfg = sqlc.ClusterMonitoringConfig{
			ClusterID: uuid.MustParse(stackTestClusterID),
			Status:    "uninstalled",
		}
		if !previewGrafanaEnabled(t, q, omitted) {
			t.Fatal("uninstalled is not not_configured; omitted enableGrafana stays true")
		}
	})
}

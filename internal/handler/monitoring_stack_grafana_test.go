package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/google/uuid"
)

func TestSharedGrafanaInstallPrecheck412BelowFloor(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	h.requester = grafanaBelowFloorK8sFake(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/monitoring/grafana/install/", strings.NewReader(sharedGrafanaBody))
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID: uuid.NewString(), AuthMethod: "jwt",
	}))
	h.InstallSharedGrafanaStack(rec, req)
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412: %s", rec.Code, rec.Body.String())
	}
	var wrap struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if wrap.Error.Code != apierror.SizerFailed {
		t.Fatalf("code = %q, want %q", wrap.Error.Code, apierror.SizerFailed)
	}
	if !strings.Contains(wrap.Error.Message, "below_grafana_floor") {
		t.Fatalf("message = %q, want below_grafana_floor", wrap.Error.Message)
	}
	body := rec.Body.String()
	for _, secret := range []string{"password", "admin-password", "adminPassword"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(secret)) && strings.Contains(body, "admin") {
			if strings.Contains(body, `"adminPassword"`) || strings.Contains(body, "admin-password") {
				t.Errorf("response leaked admin secret key: %s", body)
			}
		}
	}
}

func TestSharedGrafanaUpgradeSkipsFloor(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	h.requester = grafanaBelowFloorK8sFake(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings/monitoring/grafana/upgrade/", strings.NewReader(sharedGrafanaBody))
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID: uuid.NewString(), AuthMethod: "jwt",
	}))
	h.UpgradeSharedGrafanaStack(rec, req)
	if rec.Code == http.StatusPreconditionFailed {
		t.Fatalf("upgrade must skip leftover floor, got 412: %s", rec.Body.String())
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
}

func TestSharedGrafanaPreviewDoesNotCallPrecheck(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	h.requester = grafanaBelowFloorK8sFake(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/monitoring/grafana/preview/", strings.NewReader(sharedGrafanaBody))
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID: uuid.NewString(), AuthMethod: "jwt",
	}))
	h.PreviewSharedGrafanaStack(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200 (precheck must not run): %s", rec.Code, rec.Body.String())
	}
}

func TestSharedGrafanaPreviewIsClusterIPOnly(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/monitoring/grafana/preview/", strings.NewReader(sharedGrafanaBody))
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID: uuid.NewString(), AuthMethod: "jwt",
	}))
	h.PreviewSharedGrafanaStack(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var wrap struct {
		Data struct {
			Chart struct {
				RepoURL   string `json:"repoUrl"`
				ChartName string `json:"chartName"`
			} `json:"chart"`
			Values map[string]any `json:"values"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if wrap.Data.Chart.RepoURL != sharedGrafanaChartRepo || wrap.Data.Chart.ChartName != sharedGrafanaChartName {
		t.Fatalf("chart = %+v", wrap.Data.Chart)
	}
	svc, _ := wrap.Data.Values["service"].(map[string]any)
	if svc["type"] != "ClusterIP" {
		t.Fatalf("service.type = %v, want ClusterIP", svc["type"])
	}
	ing, _ := wrap.Data.Values["ingress"].(map[string]any)
	if ing["enabled"] != false {
		t.Fatalf("ingress.enabled = %v, want false", ing["enabled"])
	}
	raw, _ := json.Marshal(wrap.Data.Values)
	s := string(raw)
	for _, banned := range []string{"grafana-proxy", "gateway", "Gateway", `"hosts"`} {
		if strings.Contains(s, banned) {
			t.Errorf("values must not include %s: %s", banned, s)
		}
	}
	if strings.Contains(s, `"adminPassword"`) || strings.Contains(s, "admin-password") {
		t.Errorf("values leaked admin secret: %s", s)
	}
	extra, _ := wrap.Data.Values["extraObjects"].([]any)
	if len(extra) < 8 {
		t.Fatalf("extraObjects = %d, want at least 8 dashboard ConfigMaps", len(extra))
	}
	sidecar, _ := wrap.Data.Values["sidecar"].(map[string]any)
	dash, _ := sidecar["dashboards"].(map[string]any)
	if dash["enabled"] != true || dash["label"] != "grafana_dashboard" {
		t.Fatalf("sidecar.dashboards = %+v", dash)
	}
	ds, _ := sidecar["datasources"].(map[string]any)
	if ds["enabled"] != true || ds["label"] != "grafana_datasource" {
		t.Fatalf("sidecar.datasources = %+v", ds)
	}
}

func TestSharedGrafanaStatusProjectsRequiredFields(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})
	trueVal := true
	if err := h.updateSharedGrafanaMetadata(httptest.NewRequest(http.MethodGet, "/", nil).Context(), q.backend, SharedGrafanaRequest{
		ManagementClusterID:   stackTestClusterID,
		Namespace:             "monitoring",
		ReleaseName:           sharedGrafanaDefaultRelease,
		ChartVersion:          sharedGrafanaDefaultChart,
		Replicas:              1,
		AutoRollbackOnFailure: &trueVal,
	}, "healthy"); err != nil {
		t.Fatalf("persist: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/monitoring/grafana/status/", nil)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID: uuid.NewString(), AuthMethod: "jwt",
	}))
	h.GetSharedGrafanaStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var wrap struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if wrap.Data["chartVersion"] != sharedGrafanaDefaultChart {
		t.Fatalf("chartVersion = %v", wrap.Data["chartVersion"])
	}
	if wrap.Data["autoRollbackOnFailure"] != true {
		t.Fatalf("autoRollbackOnFailure = %v, want true", wrap.Data["autoRollbackOnFailure"])
	}
	if wrap.Data["authMode"] != sharedGrafanaAuthModeClusterIP {
		t.Fatalf("authMode = %v, want clusterip", wrap.Data["authMode"])
	}
	raw := rec.Body.String()
	if strings.Contains(raw, "adminPassword") || strings.Contains(raw, "admin-password") {
		t.Errorf("status leaked admin secret: %s", raw)
	}
}

func TestSharedGrafanaDashboardsAreEmbedded(t *testing.T) {
	cms := grafanaDashboardConfigMaps()
	if len(cms) != 8 {
		t.Fatalf("dashboard ConfigMaps = %d, want 8", len(cms))
	}
}

func TestSharedGrafanaThanosDatasourceOmittedWhenMissing(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/monitoring/grafana/preview/", strings.NewReader(sharedGrafanaBody))
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID: uuid.NewString(), AuthMethod: "jwt",
	}))
	h.PreviewSharedGrafanaStack(rec, req)
	var wrap struct {
		Data struct {
			Values map[string]any `json:"values"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	extra, _ := wrap.Data.Values["extraObjects"].([]any)
	for _, obj := range extra {
		m, _ := obj.(map[string]any)
		meta, _ := m["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		if strings.Contains(name, "thanos-datasource") {
			t.Fatalf("thanos datasource ConfigMap present without Thanos: %s", name)
		}
	}
}

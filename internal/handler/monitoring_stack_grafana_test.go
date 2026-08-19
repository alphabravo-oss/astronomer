package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
)

func grafanaAuthed(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	return req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID: uuid.NewString(), AuthMethod: "jwt",
	}))
}

func TestSharedGrafanaInstallPrecheck412BelowFloor(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	h.requester = grafanaBelowFloorK8sFake(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})

	rec := httptest.NewRecorder()
	h.InstallSharedGrafanaStack(rec, grafanaAuthed(http.MethodPost, "/api/v1/settings/monitoring/grafana/install/", sharedGrafanaBody))
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
	h.UpgradeSharedGrafanaStack(rec, grafanaAuthed(http.MethodPut, "/api/v1/settings/monitoring/grafana/upgrade/", sharedGrafanaBody))
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
	h.PreviewSharedGrafanaStack(rec, grafanaAuthed(http.MethodPost, "/api/v1/settings/monitoring/grafana/preview/", sharedGrafanaBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200 (precheck must not run): %s", rec.Code, rec.Body.String())
	}
}

func TestSharedGrafanaReplacePrecheck412BelowFloor(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	h.requester = grafanaBelowFloorK8sFake(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})

	rec := httptest.NewRecorder()
	h.ReplaceSharedGrafanaStack(rec, grafanaAuthed(http.MethodPost, "/api/v1/settings/monitoring/grafana/replace/", sharedGrafanaBody))
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), apierror.SizerFailed) {
		t.Fatalf("body = %s, want sizer_failed", rec.Body.String())
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

func TestSharedGrafanaStampsDegradedThenHealthy(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})
	req := SharedGrafanaRequest{
		ManagementClusterID: stackTestClusterID,
		Namespace:           "monitoring",
		ReleaseName:         sharedGrafanaDefaultRelease,
		ChartVersion:        sharedGrafanaDefaultChart,
		Replicas:            1,
	}
	if err := h.updateSharedGrafanaMetadata(context.Background(), q.backend, req, "installing"); err != nil {
		t.Fatalf("persist installing: %v", err)
	}
	if err := h.stampSharedGrafanaHealth(context.Background(), req); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	rec := httptest.NewRecorder()
	h.GetSharedGrafanaStatus(rec, grafanaAuthed(http.MethodGet, "/api/v1/settings/monitoring/grafana/status/", ""))
	var wrap struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if wrap.Data["status"] != "degraded" {
		t.Fatalf("status = %v, want degraded without Thanos", wrap.Data["status"])
	}

	cfg := decodeJSONMap(q.backend.AuthConfig)
	cfg["sharedThanos"] = map[string]any{
		"status":      "healthy",
		"releaseName": "thanos",
		"namespace":   "monitoring",
	}
	raw, _ := json.Marshal(cfg)
	q.backend.AuthConfig = raw
	if err := h.stampSharedGrafanaHealth(context.Background(), req); err != nil {
		t.Fatalf("stamp with thanos: %v", err)
	}
	meta := sharedStackMetadata(q.backend, "sharedGrafana")
	if meta["status"] != "healthy" {
		t.Fatalf("persisted status = %v, want healthy after Thanos", meta["status"])
	}
}

func TestSharedGrafanaPersistsResolvedAutoRollback(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	q.backend.AuthConfig = json.RawMessage(`{"operationPolicies":{"defaultAutoRollbackOnFailure":true}}`)
	if err := h.updateSharedGrafanaMetadata(context.Background(), q.backend, SharedGrafanaRequest{
		ManagementClusterID: stackTestClusterID,
		Namespace:           "monitoring",
		ReleaseName:         sharedGrafanaDefaultRelease,
		ChartVersion:        sharedGrafanaDefaultChart,
	}, "healthy"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})
	rec := httptest.NewRecorder()
	h.GetSharedGrafanaStatus(rec, grafanaAuthed(http.MethodGet, "/api/v1/settings/monitoring/grafana/status/", ""))
	var wrap struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if wrap.Data["autoRollbackOnFailure"] != true {
		t.Fatalf("autoRollbackOnFailure = %v, want resolved platform default true (not coerced nil→false)", wrap.Data["autoRollbackOnFailure"])
	}
}

func TestSharedGrafanaPrecheckUsesRequestCluster(t *testing.T) {
	const fatID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	h, q := newStackLifecycleHandler(t)
	q.extraClusters = []sqlc.Cluster{{
		ID:                uuid.MustParse(fatID),
		Name:              "fat",
		IsLocal:           false,
		KubernetesVersion: "v1.31.4",
	}}
	h.requester = &grafanaClusterK8sFake{by: map[string]*sizerK8sFake{
		stackTestClusterID: grafanaBelowFloorK8sFake(t),
		fatID:              grafanaPassingK8sFake(t),
	}}
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})

	local := httptest.NewRecorder()
	h.InstallSharedGrafanaStack(local, grafanaAuthed(http.MethodPost, "/api/v1/settings/monitoring/grafana/install/", sharedGrafanaBody))
	if local.Code != http.StatusPreconditionFailed {
		t.Fatalf("local leftover status = %d, want 412: %s", local.Code, local.Body.String())
	}

	fat := httptest.NewRecorder()
	body := `{"managementClusterId":"` + fatID + `"}`
	h.InstallSharedGrafanaStack(fat, grafanaAuthed(http.MethodPost, "/api/v1/settings/monitoring/grafana/install/", body))
	if fat.Code == http.StatusPreconditionFailed {
		t.Fatalf("fat cluster leftover must pass, got 412: %s", fat.Body.String())
	}
	if fat.Code != http.StatusAccepted {
		t.Fatalf("fat cluster status = %d, want 202: %s", fat.Code, fat.Body.String())
	}
}

func TestSharedGrafanaDatasourceYAMLQuotesURL(t *testing.T) {
	got := grafanaBYOLokiDatasourceYAML("http://evil.example\n  - name: Pwned")
	if strings.Contains(got, "\n  - name: Pwned") {
		t.Fatalf("URL injected extra YAML entries:\n%s", got)
	}
	if !strings.Contains(got, "evil.example") {
		t.Fatalf("quoted URL missing from:\n%s", got)
	}
}

func TestSharedGrafanaRejectsMultilineLogDatasourceURL(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})
	body := `{"managementClusterId":"` + stackTestClusterID + `","logDatasourceUrl":"http://x\nbad"}`
	rec := httptest.NewRecorder()
	h.PreviewSharedGrafanaStack(rec, grafanaAuthed(http.MethodPost, "/api/v1/settings/monitoring/grafana/preview/", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestSyncGrafanaThanosDatasourceAppliesConfigMap(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	req := SharedGrafanaRequest{
		ManagementClusterID: stackTestClusterID,
		Namespace:           "monitoring",
		ReleaseName:         sharedGrafanaDefaultRelease,
		ChartVersion:        sharedGrafanaDefaultChart,
	}
	if err := h.updateSharedGrafanaMetadata(context.Background(), q.backend, req, "degraded"); err != nil {
		t.Fatalf("persist grafana: %v", err)
	}
	cfg := decodeJSONMap(q.backend.AuthConfig)
	cfg["sharedThanos"] = map[string]any{"status": "healthy", "releaseName": "thanos", "namespace": "monitoring"}
	raw, _ := json.Marshal(cfg)
	q.backend.AuthConfig = raw
	fake := &grafanaCMK8sFake{}
	h.requester = fake
	if err := h.syncGrafanaThanosDatasource(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	joined := strings.Join(fake.calls, "\n")
	if !strings.Contains(joined, "/configmaps") {
		t.Fatalf("expected configmap apply, calls=%v", fake.calls)
	}
	if fake.lastBody == nil {
		t.Fatal("expected ConfigMap body")
	}
	var cm map[string]any
	if err := json.Unmarshal(fake.lastBody, &cm); err != nil {
		t.Fatalf("decode cm: %v", err)
	}
	md, _ := cm["metadata"].(map[string]any)
	labels, _ := md["labels"].(map[string]any)
	anns, _ := md["annotations"].(map[string]any)
	if labels["app.kubernetes.io/managed-by"] != "Helm" {
		t.Fatalf("managed-by = %v, want Helm", labels["app.kubernetes.io/managed-by"])
	}
	if anns["meta.helm.sh/release-name"] != sharedGrafanaDefaultRelease {
		t.Fatalf("release-name = %v", anns["meta.helm.sh/release-name"])
	}
	if anns["meta.helm.sh/release-namespace"] != "monitoring" {
		t.Fatalf("release-namespace = %v", anns["meta.helm.sh/release-namespace"])
	}
	meta := sharedStackMetadata(q.backend, "sharedGrafana")
	if meta["status"] != "healthy" {
		t.Fatalf("persisted status = %v, want healthy after Thanos datasource sync", meta["status"])
	}
}

func TestDeleteGrafanaThanosDatasourceConfigMapOnUninstall(t *testing.T) {
	h := NewMonitoringHandlerWithDeps(nil, nil, nil)
	fake := &grafanaCMK8sFake{}
	h.requester = fake
	h.deleteGrafanaThanosDatasourceConfigMap(context.Background(), SharedGrafanaRequest{
		ManagementClusterID: stackTestClusterID,
		Namespace:           "monitoring",
		ReleaseName:         sharedGrafanaDefaultRelease,
	})
	want := "DELETE /api/v1/namespaces/monitoring/configmaps/" + grafanaThanosDatasourceConfigMapName(sharedGrafanaDefaultRelease)
	if len(fake.calls) != 1 || fake.calls[0] != want {
		t.Fatalf("calls = %v, want [%s]", fake.calls, want)
	}
}

type grafanaClusterK8sFake struct {
	by map[string]*sizerK8sFake
}

func (f *grafanaClusterK8sFake) Do(ctx context.Context, clusterID, method, path string, body []byte, headers map[string]string) (*protocol.K8sResponsePayload, error) {
	inner := f.by[clusterID]
	if inner == nil {
		return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound}, nil
	}
	return inner.Do(ctx, clusterID, method, path, body, headers)
}

type grafanaCMK8sFake struct {
	calls    []string
	lastBody []byte
}

func (f *grafanaCMK8sFake) Do(_ context.Context, _, method, path string, body []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	f.calls = append(f.calls, method+" "+path)
	if len(body) > 0 {
		f.lastBody = append([]byte(nil), body...)
	}
	switch method {
	case http.MethodPatch:
		return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound}, nil
	case http.MethodDelete:
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK}, nil
	default:
		return &protocol.K8sResponsePayload{StatusCode: http.StatusCreated}, nil
	}
}

var _ K8sRequester = (*grafanaClusterK8sFake)(nil)
var _ K8sRequester = (*grafanaCMK8sFake)(nil)

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

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type grafanaFolderK8sFake struct {
	mu    sync.Mutex
	cms   map[string]map[string]any
	calls []string
}

func newGrafanaFolderK8sFake() *grafanaFolderK8sFake {
	return &grafanaFolderK8sFake{cms: map[string]map[string]any{}}
}

func (f *grafanaFolderK8sFake) Do(_ context.Context, _, method, path string, body []byte, _ map[string]string) (*protocol.K8sResponsePayload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, method+" "+path)
	name := configMapNameFromPath(path)
	switch method {
	case http.MethodGet:
		if strings.Contains(path, "labelSelector=") {
			return f.list(path), nil
		}
		cm, ok := f.cms[name]
		if !ok {
			return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound}, nil
		}
		return k8sJSON(http.StatusOK, cm), nil
	case http.MethodPatch:
		if _, ok := f.cms[name]; !ok {
			return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound}, nil
		}
		var obj map[string]any
		_ = json.Unmarshal(body, &obj)
		f.cms[name] = obj
		return k8sJSON(http.StatusOK, obj), nil
	case http.MethodPost:
		var obj map[string]any
		_ = json.Unmarshal(body, &obj)
		meta, _ := obj["metadata"].(map[string]any)
		n, _ := meta["name"].(string)
		if n == "" {
			n = name
		}
		if _, exists := f.cms[n]; exists {
			return &protocol.K8sResponsePayload{StatusCode: http.StatusConflict}, nil
		}
		f.cms[n] = obj
		return k8sJSON(http.StatusCreated, obj), nil
	case http.MethodDelete:
		if _, ok := f.cms[name]; !ok {
			return &protocol.K8sResponsePayload{StatusCode: http.StatusNotFound}, nil
		}
		delete(f.cms, name)
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK}, nil
	default:
		return &protocol.K8sResponsePayload{StatusCode: http.StatusOK}, nil
	}
}

func (f *grafanaFolderK8sFake) list(_ string) *protocol.K8sResponsePayload {
	items := []any{}
	for _, cm := range f.cms {
		meta, _ := cm["metadata"].(map[string]any)
		labels, _ := meta["labels"].(map[string]any)
		if labels[grafanaClusterFolderLabelKey] != grafanaClusterFolderLabelVal {
			continue
		}
		items = append(items, cm)
	}
	return k8sJSON(http.StatusOK, map[string]any{"kind": "ConfigMapList", "items": items})
}

func configMapNameFromPath(path string) string {
	path = strings.SplitN(path, "?", 2)[0]
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

func k8sJSON(code int, obj any) *protocol.K8sResponsePayload {
	raw, _ := json.Marshal(obj)
	return &protocol.K8sResponsePayload{
		StatusCode: code,
		Body:       base64.StdEncoding.EncodeToString(raw),
	}
}

func persistHealthyGrafana(t *testing.T, h *MonitoringHandler, q *stackLifecycleQuerier) {
	t.Helper()
	req := SharedGrafanaRequest{
		ManagementClusterID: stackTestClusterID,
		Namespace:           "monitoring",
		ReleaseName:         sharedGrafanaDefaultRelease,
		ChartVersion:        sharedGrafanaDefaultChart,
		Replicas:            1,
	}
	if err := h.updateSharedGrafanaMetadata(context.Background(), q.backend, req, "healthy"); err != nil {
		t.Fatalf("persist grafana: %v", err)
	}
}

func TestReconcileGrafanaClusterFoldersProvisionsUIDAndDisplayName(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	persistHealthyGrafana(t, h, q)
	clusterID := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	q.extraClusters = []sqlc.Cluster{{
		ID:          clusterID,
		Name:        "prod-east",
		DisplayName: "Prod East",
	}}
	fake := newGrafanaFolderK8sFake()
	h.requester = fake

	if err := h.ReconcileGrafanaClusterFolders(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	providers, ok := fake.cms[grafanaClusterFolderProvidersCM]
	if !ok {
		t.Fatal("missing cluster folder providers ConfigMap")
	}
	data, _ := providers["data"].(map[string]any)
	yamlBody, _ := data[grafanaClusterFolderProvidersFile].(string)
	if !strings.Contains(yamlBody, "folderUid: "+clusterID.String()) && !strings.Contains(yamlBody, `folderUid: "`+clusterID.String()+`"`) {
		t.Fatalf("providers missing folderUid=cluster UUID:\n%s", yamlBody)
	}
	if !strings.Contains(yamlBody, "Prod East") {
		t.Fatalf("providers missing displayName:\n%s", yamlBody)
	}
	if strings.Contains(yamlBody, "permissions") || strings.Contains(yamlBody, "role") {
		t.Fatalf("folder providers must not set Grafana ACLs (rewrite is the boundary):\n%s", yamlBody)
	}

	cm, ok := fake.cms[grafanaClusterFolderConfigMapName(clusterID.String())]
	if !ok {
		t.Fatal("missing per-cluster dashboard ConfigMap")
	}
	meta, _ := cm["metadata"].(map[string]any)
	anns, _ := meta["annotations"].(map[string]any)
	if anns[grafanaDashboardFolderAnnotationKey] != grafanaClusterDashboardPath(clusterID.String()) {
		t.Fatalf("grafana_folder annotation = %v, want absolute cluster path", anns[grafanaDashboardFolderAnnotationKey])
	}
	dashData, _ := cm["data"].(map[string]any)
	if _, ok := dashData["cluster-overview.json"]; !ok {
		t.Fatalf("cluster-scoped dashboards missing cluster-overview, have %v", grafanaCMKeys(dashData))
	}
	if _, ok := dashData["management-plane.json"]; ok {
		t.Fatal("management-plane dashboard must stay in Management plane, not the cluster folder")
	}
	raw, _ := dashData["cluster-overview.json"].(string)
	var dash map[string]any
	if err := json.Unmarshal([]byte(raw), &dash); err != nil {
		t.Fatalf("dashboard json: %v", err)
	}
	if dash["uid"] != grafanaClusterDashboardUID("cluster-overview", clusterID.String()) {
		t.Fatalf("dashboard uid = %v", dash["uid"])
	}
	if uid, _ := dash["uid"].(string); len(uid) > 40 {
		t.Fatalf("dashboard uid %q exceeds Grafana 40-char limit", uid)
	}
	templating, _ := dash["templating"].(map[string]any)
	list, _ := templating["list"].([]any)
	found := false
	for _, item := range list {
		m, _ := item.(map[string]any)
		if m["name"] != "cluster" {
			continue
		}
		found = true
		cur, _ := m["current"].(map[string]any)
		if cur["value"] != clusterID.String() {
			t.Fatalf("var-cluster current = %v, want cluster UUID", cur)
		}
		if m["hide"] != float64(2) && m["hide"] != 2 {
			t.Fatalf("var-cluster hide = %v, want 2 (UX pin; rewrite is the security boundary)", m["hide"])
		}
	}
	if !found {
		t.Fatal("cluster-overview missing pinned cluster variable")
	}
}

func TestReconcileGrafanaClusterFoldersDeletesOnClusterRemoval(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	persistHealthyGrafana(t, h, q)
	clusterID := uuid.MustParse("bbbbbbbb-cccc-dddd-eeee-ffffffffffff")
	q.extraClusters = []sqlc.Cluster{{ID: clusterID, DisplayName: "Gone Soon"}}
	fake := newGrafanaFolderK8sFake()
	h.requester = fake
	if err := h.ReconcileGrafanaClusterFolders(context.Background()); err != nil {
		t.Fatalf("create reconcile: %v", err)
	}
	name := grafanaClusterFolderConfigMapName(clusterID.String())
	if _, ok := fake.cms[name]; !ok {
		t.Fatal("expected cluster folder ConfigMap after create")
	}

	q.extraClusters = nil
	if err := h.ReconcileGrafanaClusterFolders(context.Background()); err != nil {
		t.Fatalf("delete reconcile: %v", err)
	}
	if _, ok := fake.cms[name]; ok {
		t.Fatal("stale cluster folder ConfigMap must be deleted after cluster removal")
	}
	if _, ok := fake.cms[grafanaClusterFolderProvidersCM]; !ok {
		t.Fatal("providers ConfigMap should remain while Grafana is installed")
	}
	data, _ := fake.cms[grafanaClusterFolderProvidersCM]["data"].(map[string]any)
	yamlBody, _ := data[grafanaClusterFolderProvidersFile].(string)
	if strings.Contains(yamlBody, clusterID.String()) {
		t.Fatalf("providers still mention deleted cluster:\n%s", yamlBody)
	}
}

func TestReconcileGrafanaClusterFoldersNoopsWhenGrafanaUninstalled(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	req := SharedGrafanaRequest{
		ManagementClusterID: stackTestClusterID,
		Namespace:           "monitoring",
		ReleaseName:         sharedGrafanaDefaultRelease,
	}
	if err := h.updateSharedGrafanaMetadata(context.Background(), q.backend, req, "uninstalled"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	clusterID := uuid.New()
	fake := newGrafanaFolderK8sFake()
	fake.cms[grafanaClusterFolderConfigMapName(clusterID.String())] = map[string]any{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]any{
			"name": grafanaClusterFolderConfigMapName(clusterID.String()),
			"labels": map[string]any{
				grafanaClusterFolderLabelKey: grafanaClusterFolderLabelVal,
			},
		},
	}
	fake.cms[grafanaClusterFolderProvidersCM] = map[string]any{
		"metadata": map[string]any{"name": grafanaClusterFolderProvidersCM},
	}
	h.requester = fake
	if err := h.ReconcileGrafanaClusterFolders(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok := fake.cms[grafanaClusterFolderConfigMapName(clusterID.String())]; ok {
		t.Fatal("uninstalled Grafana must drop cluster folder ConfigMaps")
	}
	if _, ok := fake.cms[grafanaClusterFolderProvidersCM]; ok {
		t.Fatal("uninstalled Grafana must drop providers ConfigMap")
	}
}

func TestGrafanaClusterFoldersAreNotASecurityBoundary(t *testing.T) {
	cluster := sqlc.Cluster{
		ID:          uuid.MustParse("cccccccc-dddd-eeee-ffff-000000000000"),
		DisplayName: "Tenant A",
	}
	cm, err := grafanaClusterDashboardConfigMap(cluster)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cm)
	s := string(raw)
	for _, banned := range []string{`"permissions"`, "folderPermissions", "grafana_folder_bindings"} {
		if strings.Contains(s, banned) {
			t.Fatalf("cluster folder ConfigMap must not claim Grafana ACL %q (rewrite is the boundary)", banned)
		}
	}
	providers := grafanaClusterFolderProvidersYAML([]sqlc.Cluster{cluster})
	if strings.Contains(providers, "permissions") {
		t.Fatal("providers YAML must not include Grafana folder permissions")
	}
	if !strings.Contains(providers, "folderUid:") || !strings.Contains(providers, cluster.ID.String()) {
		t.Fatalf("providers must set folderUid to cluster UUID:\n%s", providers)
	}
}

func TestGrafanaClusterDashboardUIDFitsGrafanaLimit(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	for _, slug := range grafanaClusterScopedSlugs() {
		uid := grafanaClusterDashboardUID(slug, id)
		if len(uid) > 40 {
			t.Errorf("%s uid %q len %d > 40", slug, uid, len(uid))
		}
		if !strings.Contains(uid, strings.ReplaceAll(id, "-", "")) && !strings.Contains(uid, id) {
			t.Errorf("%s uid %q must encode cluster id", slug, uid)
		}
	}
}

type grafanaFolderTriggerFake struct{ n int }

func (f *grafanaFolderTriggerFake) TriggerGrafanaFolderReconcile() { f.n++ }

func TestClusterCreateTriggersGrafanaFolderReconcile(t *testing.T) {
	q := newFakeAutoAttachClusterQuerier()
	h := NewClusterHandler(q)
	trig := &grafanaFolderTriggerFake{}
	h.SetGrafanaFolderReconciler(trig)

	w := httptest.NewRecorder()
	h.Create(w, createReq(t, "new-cluster"))
	if w.Code != http.StatusCreated {
		t.Fatalf("Create status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	if trig.n != 1 {
		t.Fatalf("folder reconcile triggers = %d, want 1 on create", trig.n)
	}
}

func TestClusterDeleteTriggersGrafanaFolderReconcile(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeAutoAttachClusterQuerier()
	q.clusters[clusterID] = sqlc.Cluster{
		ID:      clusterID,
		Name:    "member",
		Status:  "connected",
		IsLocal: false,
	}
	h := NewClusterHandler(q)
	trig := &grafanaFolderTriggerFake{}
	h.SetGrafanaFolderReconciler(trig)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/clusters/"+clusterID.String()+"/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", clusterID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{
		ID: uuid.NewString(), AuthMethod: "jwt",
	}))
	w := httptest.NewRecorder()
	h.Delete(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("Delete status = %d, want 202; body=%s", w.Code, w.Body.String())
	}
	if trig.n != 1 {
		t.Fatalf("folder reconcile triggers = %d, want 1 on delete", trig.n)
	}
}

func TestGrafanaClusterScopedSlugsExcludeManagementPlane(t *testing.T) {
	got := map[string]bool{}
	for _, slug := range grafanaClusterScopedSlugs() {
		got[slug] = true
		if grafanaDashboardFolder(slug) != grafanaFolderFleet {
			t.Errorf("%s is cluster-scoped but not a Fleet dashboard", slug)
		}
	}
	for _, slug := range []string{"cluster-overview", "node-usage", "workload-health"} {
		if !got[slug] {
			t.Errorf("missing cluster-scoped dashboard %s", slug)
		}
	}
	if got["management-plane"] || got["baseline-tool-health"] || got["continuous-delivery"] {
		t.Fatalf("management-plane dashboards leaked into cluster folders: %v", got)
	}
}

func grafanaCMKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

var _ K8sRequester = (*grafanaFolderK8sFake)(nil)
var _ grafanaFolderReconciler = (*MonitoringHandler)(nil)
var _ grafanaFolderReconciler = (*grafanaFolderTriggerFake)(nil)

func TestSharedGrafanaPreviewKeepsFleetFoldersOutOfClusterWalk(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})
	rec := httptest.NewRecorder()
	h.PreviewSharedGrafanaStack(rec, grafanaAuthed(http.MethodPost, "/api/v1/settings/monitoring/grafana/preview/", sharedGrafanaBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var wrap struct {
		Data struct {
			Values map[string]any `json:"values"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatal(err)
	}
	sidecar, _ := wrap.Data.Values["sidecar"].(map[string]any)
	dash, _ := sidecar["dashboards"].(map[string]any)
	if dash["defaultFolderName"] != grafanaSidecarSharedFolder {
		t.Fatalf("defaultFolderName = %v", dash["defaultFolderName"])
	}
}

func TestGrafanaClusterFolderTitleFallsBackToName(t *testing.T) {
	c := sqlc.Cluster{ID: uuid.MustParse(stackTestClusterID), Name: "prod", DisplayName: "  "}
	if got := grafanaClusterFolderTitle(c); got != "prod" {
		t.Fatalf("title = %q, want prod", got)
	}
}

func TestGrafanaClusterFolderConfigMapNameIsDNS1123(t *testing.T) {
	id := "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE"
	name := grafanaClusterFolderConfigMapName(id)
	if name != "astronomer-gf-c-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("name = %q", name)
	}
	if len(name) > 63 {
		t.Fatalf("ConfigMap name %q exceeds 63 chars", name)
	}
}

func TestReconcileGrafanaClusterFoldersNoopsWithoutGrafanaMetadata(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	fake := newGrafanaFolderK8sFake()
	h.requester = fake
	if err := h.ReconcileGrafanaClusterFolders(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(fake.cms) != 0 {
		t.Fatalf("expected no ConfigMaps when Grafana is not configured, got %v", fake.cms)
	}
}

func TestPinClusterDashboardHidesPicker(t *testing.T) {
	raw := []byte(`{"title":"Overview","uid":"astronomer-cluster-overview","templating":{"list":[{"name":"cluster","type":"query","query":"label_values(kube_node_info, cluster)"}]}}`)
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	out, err := pinClusterDashboard(raw, id, "Prod", "cluster-overview")
	if err != nil {
		t.Fatal(err)
	}
	var dash map[string]any
	if err := json.Unmarshal(out, &dash); err != nil {
		t.Fatal(err)
	}
	if dash["uid"] != grafanaClusterDashboardUID("cluster-overview", id) {
		t.Fatalf("uid = %v", dash["uid"])
	}
	if dash["title"] != "Overview — Prod" {
		t.Fatalf("title = %v", dash["title"])
	}
}

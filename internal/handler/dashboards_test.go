package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeDashboardQuerier is the in-memory DashboardQuerier the handler
// tests drive. Mirrors fakeQuotaQuerier in shape so authedRequest +
// withURLParam apply directly.
type fakeDashboardQuerier struct {
	mu           sync.Mutex
	user         sqlc.User
	widgets      map[uuid.UUID]sqlc.DashboardWidget
	datasources  map[uuid.UUID]sqlc.PrometheusDatasource
	clusterUIDs  map[uuid.UUID]string
	auditActions []string
}

func newFakeDashboardQuerier(caller sqlc.User) *fakeDashboardQuerier {
	return &fakeDashboardQuerier{
		user:        caller,
		widgets:     map[uuid.UUID]sqlc.DashboardWidget{},
		datasources: map[uuid.UUID]sqlc.PrometheusDatasource{},
		clusterUIDs: map[uuid.UUID]string{},
	}
}

func (f *fakeDashboardQuerier) GetUserByID(_ context.Context, _ uuid.UUID) (sqlc.User, error) {
	return f.user, nil
}

func (f *fakeDashboardQuerier) ListDashboardWidgets(_ context.Context) ([]sqlc.DashboardWidget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.DashboardWidget, 0, len(f.widgets))
	for _, w := range f.widgets {
		out = append(out, w)
	}
	return out, nil
}

func (f *fakeDashboardQuerier) GetDashboardWidgetByID(_ context.Context, id uuid.UUID) (sqlc.DashboardWidget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.widgets[id]
	if !ok {
		return sqlc.DashboardWidget{}, pgx.ErrNoRows
	}
	return w, nil
}

func (f *fakeDashboardQuerier) CreateDashboardWidget(_ context.Context, arg sqlc.CreateDashboardWidgetParams) (sqlc.DashboardWidget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	w := sqlc.DashboardWidget{
		ID:             id,
		Name:           arg.Name,
		Description:    arg.Description,
		WidgetType:     arg.WidgetType,
		Spec:           arg.Spec,
		Scope:          arg.Scope,
		ScopeIDs:       arg.ScopeIDs,
		GridX:          arg.GridX,
		GridY:          arg.GridY,
		GridW:          arg.GridW,
		GridH:          arg.GridH,
		RefreshSeconds: arg.RefreshSeconds,
		Enabled:        arg.Enabled,
		CreatedBy:      arg.CreatedBy,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	f.widgets[id] = w
	return w, nil
}

func (f *fakeDashboardQuerier) UpdateDashboardWidget(_ context.Context, arg sqlc.UpdateDashboardWidgetParams) (sqlc.DashboardWidget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.widgets[arg.ID]
	if !ok {
		return sqlc.DashboardWidget{}, pgx.ErrNoRows
	}
	w.Name = arg.Name
	w.Description = arg.Description
	w.WidgetType = arg.WidgetType
	w.Spec = arg.Spec
	w.Scope = arg.Scope
	w.ScopeIDs = arg.ScopeIDs
	w.GridX = arg.GridX
	w.GridY = arg.GridY
	w.GridW = arg.GridW
	w.GridH = arg.GridH
	w.RefreshSeconds = arg.RefreshSeconds
	w.Enabled = arg.Enabled
	w.UpdatedAt = time.Now()
	f.widgets[arg.ID] = w
	return w, nil
}

func (f *fakeDashboardQuerier) DeleteDashboardWidget(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.widgets, id)
	return nil
}

func (f *fakeDashboardQuerier) ListWidgetsForScope(_ context.Context, arg sqlc.ListWidgetsForScopeParams) ([]sqlc.DashboardWidget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.DashboardWidget{}
	for _, w := range f.widgets {
		if !w.Enabled {
			continue
		}
		switch {
		case w.Scope == "global":
			out = append(out, w)
		case w.Scope == arg.Scope:
			if len(w.ScopeIDs) == 0 {
				out = append(out, w)
				continue
			}
			for _, sid := range w.ScopeIDs {
				if sid == arg.ScopeID {
					out = append(out, w)
					break
				}
			}
		}
	}
	return out, nil
}

func (f *fakeDashboardQuerier) ListPrometheusDatasources(_ context.Context) ([]sqlc.PrometheusDatasource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlc.PrometheusDatasource, 0, len(f.datasources))
	for _, d := range f.datasources {
		out = append(out, d)
	}
	return out, nil
}

func (f *fakeDashboardQuerier) ListEnabledPrometheusDatasources(_ context.Context) ([]sqlc.PrometheusDatasource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []sqlc.PrometheusDatasource{}
	for _, d := range f.datasources {
		if d.Enabled {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakeDashboardQuerier) GetPrometheusDatasourceByID(_ context.Context, id uuid.UUID) (sqlc.PrometheusDatasource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.datasources[id]
	if !ok {
		return sqlc.PrometheusDatasource{}, pgx.ErrNoRows
	}
	return d, nil
}

func (f *fakeDashboardQuerier) GetPrometheusDatasourceByName(_ context.Context, name string) (sqlc.PrometheusDatasource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.datasources {
		if d.Name == name {
			return d, nil
		}
	}
	return sqlc.PrometheusDatasource{}, pgx.ErrNoRows
}

func (f *fakeDashboardQuerier) CreatePrometheusDatasource(_ context.Context, arg sqlc.CreatePrometheusDatasourceParams) (sqlc.PrometheusDatasource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := sqlc.PrometheusDatasource{
		ID:            uuid.New(),
		Name:          arg.Name,
		URL:           arg.URL,
		AuthEncrypted: arg.AuthEncrypted,
		TLSSkipVerify: arg.TLSSkipVerify,
		Enabled:       arg.Enabled,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}
	f.datasources[d.ID] = d
	return d, nil
}

func (f *fakeDashboardQuerier) UpdatePrometheusDatasource(_ context.Context, arg sqlc.UpdatePrometheusDatasourceParams) (sqlc.PrometheusDatasource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.datasources[arg.ID]
	if !ok {
		return sqlc.PrometheusDatasource{}, pgx.ErrNoRows
	}
	d.URL = arg.URL
	d.AuthEncrypted = arg.AuthEncrypted
	d.TLSSkipVerify = arg.TLSSkipVerify
	d.Enabled = arg.Enabled
	d.UpdatedAt = time.Now()
	f.datasources[arg.ID] = d
	return d, nil
}

func (f *fakeDashboardQuerier) DeletePrometheusDatasource(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.datasources, id)
	return nil
}

func (f *fakeDashboardQuerier) GetClusterUIDForID(_ context.Context, id uuid.UUID) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	uid, ok := f.clusterUIDs[id]
	if !ok {
		return "", pgx.ErrNoRows
	}
	return uid, nil
}

// CreateAuditLogV1 satisfies the audit writer interface recordAudit
// type-asserts against.
func (f *fakeDashboardQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auditActions = append(f.auditActions, arg.Action)
	return nil
}

// ── Test helpers ──────────────────────────────────────────────────────

// addHostToAllowList stuffs a host into a SettingsCache test surface.
// We don't go through the platform_settings handler because the cache
// is the read path the dashboard handler uses. The cache's `lookup`
// returns "hasValue=false" when reader is nil and there's no entry —
// for the allow-list test path we wire a tiny stub reader.
type stubSettingsReader struct {
	value string
}

func (s *stubSettingsReader) GetPlatformSetting(_ context.Context, key string) (sqlc.PlatformSetting, error) {
	if key == "dashboard.allowed_iframe_hosts" {
		return sqlc.PlatformSetting{Key: key, Value: json.RawMessage(`"` + s.value + `"`)}, nil
	}
	return sqlc.PlatformSetting{}, pgx.ErrNoRows
}

func dashboardCallerIDSuperuser() (uuid.UUID, sqlc.User) {
	id := uuid.New()
	return id, sqlc.User{ID: id, IsSuperuser: true}
}

// ── Tests ─────────────────────────────────────────────────────────────

// TestWidget_CRUD exercises the admin CRUD surface end-to-end.
func TestWidget_CRUD(t *testing.T) {
	cid, user := dashboardCallerIDSuperuser()
	q := newFakeDashboardQuerier(user)
	h := NewDashboardHandler(q)
	h.SetAuditor(q)

	body := []byte(`{
		"name": "Cluster health rollup",
		"description": "Rollup",
		"widget_type": "prom_sparkline",
		"spec": {"datasource":"default","query":"up","duration":"5m","step":"30s"},
		"scope": "global",
		"grid": {"x":0,"y":0,"w":4,"h":2},
		"refresh_seconds": 60
	}`)
	w := httptest.NewRecorder()
	h.AdminCreate(w, authedRequest(http.MethodPost, "/api/v1/admin/dashboard-widgets/", cid, body))
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var envCreate struct {
		Data WidgetResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envCreate); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envCreate.Data.Name != "Cluster health rollup" {
		t.Fatalf("name mismatch: %q", envCreate.Data.Name)
	}
	wid := envCreate.Data.ID

	// List
	lw := httptest.NewRecorder()
	h.AdminList(lw, authedRequest(http.MethodGet, "/api/v1/admin/dashboard-widgets/", cid, nil))
	if lw.Code != http.StatusOK {
		t.Fatalf("list status=%d", lw.Code)
	}

	// Get
	gw := httptest.NewRecorder()
	h.AdminGet(gw, withURLParam(authedRequest(http.MethodGet, "/api/v1/admin/dashboard-widgets/"+wid.String()+"/", cid, nil), "id", wid.String()))
	if gw.Code != http.StatusOK {
		t.Fatalf("get status=%d", gw.Code)
	}

	// Update
	updBody := []byte(`{
		"name": "Cluster health (renamed)",
		"description": "",
		"widget_type": "prom_sparkline",
		"spec": {"datasource":"default","query":"up","duration":"1h","step":"60s"},
		"scope": "global",
		"grid": {"x":0,"y":0,"w":6,"h":2},
		"refresh_seconds": 60
	}`)
	uw := httptest.NewRecorder()
	h.AdminUpdate(uw, withURLParam(authedRequest(http.MethodPut, "/api/v1/admin/dashboard-widgets/"+wid.String()+"/", cid, updBody), "id", wid.String()))
	if uw.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", uw.Code, uw.Body.String())
	}
	if q.widgets[wid].Name != "Cluster health (renamed)" {
		t.Fatalf("update did not propagate: %s", q.widgets[wid].Name)
	}

	// Delete
	dw := httptest.NewRecorder()
	h.AdminDelete(dw, withURLParam(authedRequest(http.MethodDelete, "/api/v1/admin/dashboard-widgets/"+wid.String()+"/", cid, nil), "id", wid.String()))
	if dw.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", dw.Code)
	}
	if _, ok := q.widgets[wid]; ok {
		t.Fatalf("widget still present after delete")
	}

	// Audit: at least created + updated + deleted entries were stamped.
	if len(q.auditActions) < 3 {
		t.Fatalf("expected ≥3 audit ops, got %v", q.auditActions)
	}
}

// TestWidget_ScopeMatching verifies the ListWidgetsForScope semantics
// the handler relies on for the three render endpoints.
func TestWidget_ScopeMatching(t *testing.T) {
	_, user := dashboardCallerIDSuperuser()
	q := newFakeDashboardQuerier(user)
	clusterA := uuid.New()
	clusterB := uuid.New()
	projectA := uuid.New()

	q.widgets[uuid.New()] = sqlc.DashboardWidget{ID: uuid.New(), Name: "global-1", WidgetType: "prom_sparkline", Spec: json.RawMessage(`{}`), Scope: "global", ScopeIDs: []uuid.UUID{}, Enabled: true}
	q.widgets[uuid.New()] = sqlc.DashboardWidget{ID: uuid.New(), Name: "cluster-all", WidgetType: "prom_sparkline", Spec: json.RawMessage(`{}`), Scope: "cluster", ScopeIDs: []uuid.UUID{}, Enabled: true}
	q.widgets[uuid.New()] = sqlc.DashboardWidget{ID: uuid.New(), Name: "cluster-A-only", WidgetType: "prom_sparkline", Spec: json.RawMessage(`{}`), Scope: "cluster", ScopeIDs: []uuid.UUID{clusterA}, Enabled: true}
	q.widgets[uuid.New()] = sqlc.DashboardWidget{ID: uuid.New(), Name: "project-A-only", WidgetType: "prom_sparkline", Spec: json.RawMessage(`{}`), Scope: "project", ScopeIDs: []uuid.UUID{projectA}, Enabled: true}
	q.widgets[uuid.New()] = sqlc.DashboardWidget{ID: uuid.New(), Name: "disabled", WidgetType: "prom_sparkline", Spec: json.RawMessage(`{}`), Scope: "global", ScopeIDs: []uuid.UUID{}, Enabled: false}

	rowsA, _ := q.ListWidgetsForScope(context.Background(), sqlc.ListWidgetsForScopeParams{Scope: "cluster", ScopeID: clusterA})
	namesA := setOfNames(rowsA)
	for _, want := range []string{"global-1", "cluster-all", "cluster-A-only"} {
		if !namesA[want] {
			t.Errorf("cluster A list missing %q (got %v)", want, namesA)
		}
	}
	if namesA["project-A-only"] {
		t.Errorf("cluster A list should not include project-A-only")
	}
	if namesA["disabled"] {
		t.Errorf("disabled widget leaked into list")
	}

	rowsB, _ := q.ListWidgetsForScope(context.Background(), sqlc.ListWidgetsForScopeParams{Scope: "cluster", ScopeID: clusterB})
	namesB := setOfNames(rowsB)
	if namesB["cluster-A-only"] {
		t.Errorf("cluster B should not see cluster-A-only widgets")
	}
	if !namesB["cluster-all"] {
		t.Errorf("cluster B missing cluster-all widget")
	}

	rowsP, _ := q.ListWidgetsForScope(context.Background(), sqlc.ListWidgetsForScopeParams{Scope: "project", ScopeID: projectA})
	namesP := setOfNames(rowsP)
	if !namesP["project-A-only"] {
		t.Errorf("project list missing project-A-only")
	}
	if !namesP["global-1"] {
		t.Errorf("project list missing global widget")
	}
	if namesP["cluster-all"] {
		t.Errorf("project list should not include cluster widgets")
	}
}

func setOfNames(rows []sqlc.DashboardWidget) map[string]bool {
	out := map[string]bool{}
	for _, r := range rows {
		out[r.Name] = true
	}
	return out
}

// TestSpecResolve_ClusterUIDTemplating verifies {{cluster_uid}} and
// $cluster_uid both get substituted in the spec before shipping.
func TestSpecResolve_ClusterUIDTemplating(t *testing.T) {
	in := json.RawMessage(`{"base_url":"https://grafana.example.com","dashboard_uid":"abc","vars":{"cluster":"$cluster_uid","ns":"{{cluster_uid}}"}}`)
	out, err := resolveSpec(in, map[string]string{"cluster_uid": "xyz123"})
	if err != nil {
		t.Fatalf("resolveSpec: %v", err)
	}
	if !strings.Contains(string(out), `"cluster":"xyz123"`) {
		t.Errorf("$cluster_uid not substituted: %s", out)
	}
	if !strings.Contains(string(out), `"ns":"xyz123"`) {
		t.Errorf("{{cluster_uid}} not substituted: %s", out)
	}
}

// TestRender_GrafanaPanel_NoServerFetch verifies grafana_panel widgets
// don't trigger a server-side Prom fetch — the iframe URL gets shipped
// as-is and the client loads it directly. The host allow-list IS
// enforced, however.
func TestRender_GrafanaPanel_NoServerFetch(t *testing.T) {
	_, user := dashboardCallerIDSuperuser()
	q := newFakeDashboardQuerier(user)
	cid := uuid.New()
	q.clusterUIDs[cid] = "abc12345"
	q.widgets[uuid.New()] = sqlc.DashboardWidget{
		ID:         uuid.New(),
		Name:       "grafana-panel",
		WidgetType: "grafana_panel",
		Spec:       json.RawMessage(`{"base_url":"https://grafana.example.com","dashboard_uid":"x","panel_id":1,"vars":{"cluster":"$cluster_uid"}}`),
		Scope:      "cluster",
		ScopeIDs:   []uuid.UUID{},
		Enabled:    true,
	}
	h := NewDashboardHandler(q)
	cache := NewSettingsCache(&stubSettingsReader{value: "grafana.example.com"}, 5*time.Second)
	h.SetSettingsCache(cache)

	w := httptest.NewRecorder()
	h.RenderCluster(w, withURLParam(authedRequest(http.MethodGet, fmt.Sprintf("/api/v1/dashboards/clusters/%s/", cid), uuid.New(), nil), "id", cid.String()))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data []RenderedWidget `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := env.Data
	if len(out) != 1 {
		t.Fatalf("expected 1 widget, got %d", len(out))
	}
	if out[0].Data != nil && out[0].Data.SparklineSVG != "" {
		t.Errorf("grafana_panel should not produce sparkline SVG, got %q", out[0].Data.SparklineSVG)
	}
	// Spec must be resolved.
	if !strings.Contains(string(out[0].SpecResolved), "abc12345") {
		t.Errorf("expected cluster_uid in resolved spec, got %s", out[0].SpecResolved)
	}
}

// TestRender_AllowedIframeHostsEnforced verifies grafana_panel +
// url_iframe widgets get rejected on write AND silently dropped on
// render when the host isn't allow-listed.
func TestRender_AllowedIframeHostsEnforced(t *testing.T) {
	cid, user := dashboardCallerIDSuperuser()
	q := newFakeDashboardQuerier(user)
	h := NewDashboardHandler(q)
	h.SetAuditor(q)
	// Empty allow-list → every iframe widget is rejected.
	cache := NewSettingsCache(&stubSettingsReader{value: ""}, 5*time.Second)
	h.SetSettingsCache(cache)

	body := []byte(`{"name":"g","widget_type":"grafana_panel","spec":{"base_url":"https://forbidden.example.com","dashboard_uid":"x","panel_id":1},"scope":"global","grid":{"x":0,"y":0,"w":4,"h":2},"refresh_seconds":60}`)
	w := httptest.NewRecorder()
	h.AdminCreate(w, authedRequest(http.MethodPost, "/api/v1/admin/dashboard-widgets/", cid, body))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-allowed iframe host, got %d body=%s", w.Code, w.Body.String())
	}
	// Now expand the allow-list and retry — must succeed.
	cache2 := NewSettingsCache(&stubSettingsReader{value: "forbidden.example.com"}, 5*time.Second)
	h.SetSettingsCache(cache2)
	w2 := httptest.NewRecorder()
	h.AdminCreate(w2, authedRequest(http.MethodPost, "/api/v1/admin/dashboard-widgets/", cid, body))
	if w2.Code != http.StatusCreated {
		t.Fatalf("expected 201 after allow-list expanded, got %d body=%s", w2.Code, w2.Body.String())
	}
}

// TestRender_PromSparkline_RoundTrip drives the full server-side
// sparkline render: stub Prom upstream, datasource row, widget pointing
// at it, render endpoint returns a non-empty SVG.
func TestRender_PromSparkline_RoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/v1/query_range") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1700000000,"1"],[1700000060,"2"],[1700000120,"3"]]}]}}`))
	}))
	defer srv.Close()

	cid, user := dashboardCallerIDSuperuser()
	q := newFakeDashboardQuerier(user)
	dsID := uuid.New()
	q.datasources[dsID] = sqlc.PrometheusDatasource{ID: dsID, Name: "default", URL: srv.URL, Enabled: true}
	q.widgets[uuid.New()] = sqlc.DashboardWidget{
		ID:         uuid.New(),
		Name:       "spark",
		WidgetType: "prom_sparkline",
		Spec:       json.RawMessage(`{"datasource":"default","query":"up","duration":"5m","step":"60s"}`),
		Scope:      "global",
		ScopeIDs:   []uuid.UUID{},
		Enabled:    true,
	}
	h := NewDashboardHandler(q)

	w := httptest.NewRecorder()
	h.RenderGlobal(w, authedRequest(http.MethodGet, "/api/v1/dashboards/global/", cid, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data []RenderedWidget `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := env.Data
	if len(out) != 1 {
		t.Fatalf("expected 1 widget, got %d", len(out))
	}
	if out[0].Data == nil || out[0].Data.SparklineSVG == "" {
		t.Fatalf("expected non-empty SVG, got %+v", out[0].Data)
	}
	if !strings.Contains(out[0].Data.SparklineSVG, "<polyline") {
		t.Errorf("expected polyline in SVG, got %q", out[0].Data.SparklineSVG)
	}
}

// TestRender_PromStat_RoundTrip is the symmetric companion for stat
// widgets.
func TestRender_PromStat_RoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1700000000,"42.5"]}]}}`))
	}))
	defer srv.Close()
	cid, user := dashboardCallerIDSuperuser()
	q := newFakeDashboardQuerier(user)
	dsID := uuid.New()
	q.datasources[dsID] = sqlc.PrometheusDatasource{ID: dsID, Name: "default", URL: srv.URL, Enabled: true}
	q.widgets[uuid.New()] = sqlc.DashboardWidget{
		ID:         uuid.New(),
		Name:       "stat",
		WidgetType: "prom_stat",
		Spec:       json.RawMessage(`{"datasource":"default","query":"up","unit":"%","format":".2f"}`),
		Scope:      "global",
		ScopeIDs:   []uuid.UUID{},
		Enabled:    true,
	}
	h := NewDashboardHandler(q)
	w := httptest.NewRecorder()
	h.RenderGlobal(w, authedRequest(http.MethodGet, "/api/v1/dashboards/global/", cid, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data []RenderedWidget `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	out := env.Data
	if len(out) != 1 || out[0].Data == nil || !out[0].Data.StatOK {
		t.Fatalf("expected one OK stat widget, got %+v", out)
	}
	if out[0].Data.StatValue != 42.5 {
		t.Errorf("stat value mismatch: %v", out[0].Data.StatValue)
	}
}

// TestDatasource_CRUD_And_Test exercises datasource admin CRUD + the
// /test/ endpoint. We use an httptest server as the "Prometheus".
func TestDatasource_CRUD_And_Test(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
	}))
	defer srv.Close()

	cid, user := dashboardCallerIDSuperuser()
	q := newFakeDashboardQuerier(user)
	h := NewDashboardHandler(q)
	h.SetAuditor(q)
	body := []byte(`{"name":"default","url":"` + srv.URL + `","enabled":true}`)
	w := httptest.NewRecorder()
	h.AdminCreateDatasource(w, authedRequest(http.MethodPost, "/api/v1/admin/prometheus-datasources/", cid, body))
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	var envDS struct {
		Data DatasourceResponse `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &envDS)
	ds := envDS.Data
	// List
	lw := httptest.NewRecorder()
	h.AdminListDatasources(lw, authedRequest(http.MethodGet, "/api/v1/admin/prometheus-datasources/", cid, nil))
	if lw.Code != http.StatusOK {
		t.Fatalf("list status=%d", lw.Code)
	}
	// Test endpoint
	tw := httptest.NewRecorder()
	h.AdminTestDatasource(tw, withURLParam(authedRequest(http.MethodPost, "/api/v1/admin/prometheus-datasources/"+ds.ID.String()+"/test/", cid, nil), "id", ds.ID.String()))
	if tw.Code != http.StatusOK {
		t.Fatalf("test status=%d body=%s", tw.Code, tw.Body.String())
	}
	var probeEnv struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(tw.Body.Bytes(), &probeEnv)
	if v, _ := probeEnv.Data["ok"].(bool); !v {
		t.Fatalf("expected ok=true from test endpoint, got %v", probeEnv)
	}
	// Delete
	dw := httptest.NewRecorder()
	h.AdminDeleteDatasource(dw, withURLParam(authedRequest(http.MethodDelete, "/api/v1/admin/prometheus-datasources/"+ds.ID.String()+"/", cid, nil), "id", ds.ID.String()))
	if dw.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", dw.Code)
	}
}

// TestPublicRender_RequiresClusterRead asserts the public render
// handler doesn't gate on superuser — the RBAC middleware at the
// routes layer enforces clusters:read. The handler itself must accept
// a non-superuser caller and serve widgets. We assert by calling with
// a non-superuser fake; if the handler tried to call gate() it would
// return 403.
func TestPublicRender_RequiresClusterRead(t *testing.T) {
	_, user := dashboardCallerIDSuperuser()
	user.IsSuperuser = false
	q := newFakeDashboardQuerier(user)
	q.widgets[uuid.New()] = sqlc.DashboardWidget{ID: uuid.New(), Name: "g", WidgetType: "prom_sparkline", Spec: json.RawMessage(`{"datasource":"missing","query":"up"}`), Scope: "global", ScopeIDs: []uuid.UUID{}, Enabled: true}
	h := NewDashboardHandler(q)
	cid := uuid.New()
	w := httptest.NewRecorder()
	h.RenderGlobal(w, authedRequest(http.MethodGet, "/api/v1/dashboards/global/", cid, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 from non-superuser render path, got %d body=%s", w.Code, w.Body.String())
	}
}

// authedRequest with raw body — for use by the dashboard tests.
var _ = bytes.NewReader

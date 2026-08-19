package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	corev1 "k8s.io/api/core/v1"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Four Preview/Install/Upgrade/Replace/Uninstall/Status families exist: shared
// Thanos, shared Alertmanager, shared Grafana, per-cluster. Each was written by copying the
// last, and the copy dropped the authorization preamble from two of the three
// Preview handlers — /settings/monitoring served rendered Helm values to a
// caller with no monitoring grant until 3799088 put the line back by hand.
//
// These tests are the fence that makes that class non-recurring regardless of
// how the handlers are factored. Every lifecycle endpoint of every family is
// driven with a REAL, otherwise-valid request by a caller holding a real
// binding that simply does not cover monitoring. Each asserts 403 — and each
// asserts the same request DOES get past the gate for a caller who holds the
// grant, so a 403 caused by anything other than authorization fails the test
// too.

// --- fakes -------------------------------------------------------------------

type stackLifecycleQuerier struct {
	MonitoringQuerier
	backend    sqlc.MonitoringBackend
	storage    sqlc.BackupStorageConfig
	clusterCfg sqlc.ClusterMonitoringConfig
	clusterErr error

	audits []sqlc.CreateAuditLogV1Params
}

func (q *stackLifecycleQuerier) GetDefaultMonitoringBackend(context.Context) (sqlc.MonitoringBackend, error) {
	return q.backend, nil
}

func (q *stackLifecycleQuerier) UpsertDefaultMonitoringBackend(_ context.Context, arg sqlc.UpsertDefaultMonitoringBackendParams) (sqlc.MonitoringBackend, error) {
	q.backend.AuthConfig = arg.AuthConfig
	q.backend.AuthConfigEncrypted = arg.AuthConfigEncrypted
	return q.backend, nil
}

func (q *stackLifecycleQuerier) GetBackupStorageConfigByID(context.Context, uuid.UUID) (sqlc.BackupStorageConfig, error) {
	return q.storage, nil
}

func (q *stackLifecycleQuerier) ListNotificationChannels(context.Context, sqlc.ListNotificationChannelsParams) ([]sqlc.NotificationChannel, error) {
	return nil, nil
}

func (q *stackLifecycleQuerier) ListAlertRules(context.Context, sqlc.ListAlertRulesParams) ([]sqlc.AlertRule, error) {
	return nil, nil
}

func (q *stackLifecycleQuerier) ListAlertRuleChannelsByRules(context.Context, []uuid.UUID) ([]sqlc.AlertRuleChannel, error) {
	return nil, nil
}

func (q *stackLifecycleQuerier) GetClusterMonitoringConfig(context.Context, uuid.UUID) (sqlc.ClusterMonitoringConfig, error) {
	if q.clusterErr != nil {
		return sqlc.ClusterMonitoringConfig{}, q.clusterErr
	}
	return q.clusterCfg, nil
}

func (q *stackLifecycleQuerier) UpsertClusterMonitoringConfig(_ context.Context, arg sqlc.UpsertClusterMonitoringConfigParams) (sqlc.ClusterMonitoringConfig, error) {
	q.clusterCfg.ClusterID = arg.ClusterID
	q.clusterCfg.BackendID = arg.BackendID
	q.clusterCfg.StackNamespace = arg.StackNamespace
	q.clusterCfg.PrometheusReleaseName = arg.PrometheusReleaseName
	q.clusterCfg.Status = arg.Status
	q.clusterErr = nil
	return q.clusterCfg, nil
}

func (q *stackLifecycleQuerier) CreateMonitoringOperation(_ context.Context, arg sqlc.CreateMonitoringOperationParams) (sqlc.MonitoringOperation, error) {
	return sqlc.MonitoringOperation{
		ID:            uuid.New(),
		TargetType:    arg.TargetType,
		TargetKey:     arg.TargetKey,
		OperationType: arg.OperationType,
		Status:        arg.Status,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}, nil
}

func (q *stackLifecycleQuerier) GetLatestMonitoringOperationForTarget(context.Context, sqlc.GetLatestMonitoringOperationForTargetParams) (sqlc.MonitoringOperation, error) {
	return sqlc.MonitoringOperation{}, errors.New("none")
}

// CreateAuditLogV1 satisfies the auditWriterV1 surface recordAudit type-asserts
// on, so the audit assertions below see real rows rather than a silent no-op.
func (q *stackLifecycleQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	q.audits = append(q.audits, arg)
	return nil
}

func (q *stackLifecycleQuerier) ListClusters(context.Context, sqlc.ListClustersParams) ([]sqlc.Cluster, error) {
	return []sqlc.Cluster{{
		ID:                uuid.MustParse(stackTestClusterID),
		Name:              "local",
		IsLocal:           true,
		KubernetesVersion: "v1.31.4",
		LastHeartbeat:     pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}}, nil
}

func (q *stackLifecycleQuerier) GetClusterMonitoringContext(context.Context, uuid.UUID) (sqlc.GetClusterMonitoringContextRow, error) {
	return sqlc.GetClusterMonitoringContextRow{}, errors.New("no context")
}

type stackLifecycleHelmStub struct{}

func (stackLifecycleHelmStub) Do(context.Context, string, protocol.MessageType, protocol.HelmRequestPayload) (*protocol.HelmResultPayload, error) {
	return &protocol.HelmResultPayload{}, nil
}

func (stackLifecycleHelmStub) Status(context.Context, string, string, string) (*protocol.HelmResultPayload, error) {
	return nil, errors.New("not deployed")
}

func (stackLifecycleHelmStub) History(context.Context, string, string, string) (*protocol.HelmResultPayload, error) {
	return nil, errors.New("unsupported")
}

var _ HelmRequester = stackLifecycleHelmStub{}

// --- fixtures ----------------------------------------------------------------

const (
	stackTestClusterID = "9c4b0f1e-77c8-4a02-9d7b-2f0e6b3a1d55"
	stackTestStorageID = "1f2e3d4c-5b6a-4988-9a0b-1c2d3e4f5a6b"
)

func newStackLifecycleHandler(t *testing.T) (*MonitoringHandler, *stackLifecycleQuerier) {
	t.Helper()
	q := &stackLifecycleQuerier{
		backend: sqlc.MonitoringBackend{
			ID:          uuid.New(),
			Name:        "default",
			BackendType: "thanos",
			QueryUrl:    "http://thanos.example",
			AuthConfig:  json.RawMessage(`{}`),
		},
		storage: sqlc.BackupStorageConfig{
			ID:     uuid.MustParse(stackTestStorageID),
			Bucket: "metrics",
			Region: "us-east-1",
		},
		clusterErr: pgx.ErrNoRows,
	}
	k8s := grafanaPassingK8sFake(t)
	h := NewMonitoringHandlerWithDeps(q, k8s, stackLifecycleHelmStub{})
	return h, q
}

func grafanaPassingK8sFake(t *testing.T) *sizerK8sFake {
	t.Helper()
	return &sizerK8sFake{
		t:     t,
		nodes: []corev1.Node{sizerTestNode("n1", "4", "8Gi", true, false)},
		pods:  []corev1.Pod{sizerTestPod("p1", "100m", "128Mi", "", "")},
		storage: storageClassWire{
			AccessModes: []string{"ReadWriteOnce"},
		},
	}
}

func grafanaBelowFloorK8sFake(t *testing.T) *sizerK8sFake {
	t.Helper()
	return &sizerK8sFake{
		t:     t,
		nodes: []corev1.Node{sizerTestNode("n1", "4", "8Gi", true, false)},
		pods:  []corev1.Pod{sizerTestPod("p1", "3900m", "8Gi", "", "")},
		storage: storageClassWire{
			AccessModes: []string{"ReadWriteOnce"},
		},
	}
}

// grantMonitoring builds a caller who holds monitoring at every verb the
// lifecycle uses; denyMonitoring builds one who holds a real binding on a
// different resource. Both are "restricted" as far as the engine is concerned,
// which is the only interesting case — an unrestricted caller never reaches a
// permission check.
func grantMonitoring() []rbac.RoleBinding {
	return []rbac.RoleBinding{{
		RoleRules: []rbac.Rule{{
			Resource: string(rbac.ResourceMonitoring),
			Verbs: []string{
				string(rbac.VerbRead),
				string(rbac.VerbCreate),
				string(rbac.VerbUpdate),
				string(rbac.VerbDelete),
			},
		}},
	}}
}

func denyMonitoring() []rbac.RoleBinding {
	return []rbac.RoleBinding{{
		RoleRules: []rbac.Rule{{
			Resource: string(rbac.ResourceClusters),
			Verbs:    []string{string(rbac.VerbRead), string(rbac.VerbUpdate)},
		}},
	}}
}

// stackLifecycleCase is one endpoint: how to build its request and how to
// invoke it.
type stackLifecycleCase struct {
	name    string
	method  string
	target  string
	body    string
	params  map[string]string
	verb    rbac.Verb // route verb, used only by the middleware-gated family
	invoke  func(h *MonitoringHandler) http.HandlerFunc
	audit   string   // expected audit action, "" for the read-only endpoints
	details []string // expected audit detail keys
}

func (c stackLifecycleCase) request() *http.Request {
	var body *strings.Reader
	if c.body != "" {
		body = strings.NewReader(c.body)
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(c.method, c.target, body)
	rc := chi.NewRouteContext()
	for k, v := range c.params {
		rc.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = middleware.SetAuthenticatedUserForTest(ctx, &middleware.AuthenticatedUser{
		ID:         uuid.NewString(),
		AuthMethod: "jwt",
	})
	return req.WithContext(ctx)
}

// sharedThanosBody / sharedAlertmanagerBody are complete, valid request bodies:
// without the gate every one of these endpoints succeeds, which is what makes
// the 403 assertion meaningful.
const (
	sharedThanosBody       = `{"managementClusterId":"` + stackTestClusterID + `","storageConfigId":"` + stackTestStorageID + `"}`
	sharedAlertmanagerBody = `{"managementClusterId":"` + stackTestClusterID + `"}`
	sharedGrafanaBody      = `{"managementClusterId":"` + stackTestClusterID + `"}`
	clusterStackBody       = `{"releaseName":"prometheus","namespace":"monitoring"}`
)

func sharedThanosCases() []stackLifecycleCase {
	base := "/api/v1/settings/monitoring/thanos"
	return []stackLifecycleCase{
		{name: "preview", method: http.MethodPost, target: base + "/preview/", body: sharedThanosBody,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.PreviewSharedThanosStack }},
		{name: "install", method: http.MethodPost, target: base + "/install/", body: sharedThanosBody,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.InstallSharedThanosStack },
			audit:  "monitoring.shared_thanos.install", details: sharedAuditDetailKeys},
		{name: "upgrade", method: http.MethodPut, target: base + "/upgrade/", body: sharedThanosBody,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.UpgradeSharedThanosStack },
			audit:  "monitoring.shared_thanos.upgrade", details: sharedAuditDetailKeys},
		{name: "replace", method: http.MethodPost, target: base + "/replace/", body: sharedThanosBody,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.ReplaceSharedThanosStack },
			audit:  "monitoring.shared_thanos.replace", details: sharedAuditDetailKeys},
		{name: "uninstall", method: http.MethodDelete, target: base + "/uninstall/?clusterId=" + stackTestClusterID,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.UninstallSharedThanosStack },
			audit:  "monitoring.shared_thanos.uninstall", details: sharedAuditDetailKeys},
		{name: "status", method: http.MethodGet, target: base + "/status/",
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.GetSharedThanosStatus }},
	}
}

func sharedGrafanaCases() []stackLifecycleCase {
	base := "/api/v1/settings/monitoring/grafana"
	return []stackLifecycleCase{
		{name: "preview", method: http.MethodPost, target: base + "/preview/", body: sharedGrafanaBody,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.PreviewSharedGrafanaStack }},
		{name: "install", method: http.MethodPost, target: base + "/install/", body: sharedGrafanaBody,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.InstallSharedGrafanaStack },
			audit:  "monitoring.shared_grafana.install", details: sharedAuditDetailKeys},
		{name: "upgrade", method: http.MethodPut, target: base + "/upgrade/", body: sharedGrafanaBody,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.UpgradeSharedGrafanaStack },
			audit:  "monitoring.shared_grafana.upgrade", details: sharedAuditDetailKeys},
		{name: "replace", method: http.MethodPost, target: base + "/replace/", body: sharedGrafanaBody,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.ReplaceSharedGrafanaStack },
			audit:  "monitoring.shared_grafana.replace", details: sharedAuditDetailKeys},
		{name: "uninstall", method: http.MethodDelete, target: base + "/uninstall/?clusterId=" + stackTestClusterID,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.UninstallSharedGrafanaStack },
			audit:  "monitoring.shared_grafana.uninstall", details: sharedAuditDetailKeys},
		{name: "status", method: http.MethodGet, target: base + "/status/",
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.GetSharedGrafanaStatus }},
	}
}

func sharedAlertmanagerCases() []stackLifecycleCase {
	base := "/api/v1/settings/monitoring/alertmanager"
	return []stackLifecycleCase{
		{name: "preview", method: http.MethodPost, target: base + "/preview/", body: sharedAlertmanagerBody,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.PreviewSharedAlertmanager }},
		{name: "install", method: http.MethodPost, target: base + "/install/", body: sharedAlertmanagerBody,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.InstallSharedAlertmanager },
			audit:  "monitoring.shared_alertmanager.install", details: sharedAuditDetailKeys},
		{name: "upgrade", method: http.MethodPut, target: base + "/upgrade/", body: sharedAlertmanagerBody,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.UpgradeSharedAlertmanager },
			audit:  "monitoring.shared_alertmanager.upgrade", details: sharedAuditDetailKeys},
		{name: "replace", method: http.MethodPost, target: base + "/replace/", body: sharedAlertmanagerBody,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.ReplaceSharedAlertmanager },
			audit:  "monitoring.shared_alertmanager.replace", details: sharedAuditDetailKeys},
		{name: "uninstall", method: http.MethodDelete, target: base + "/uninstall/?clusterId=" + stackTestClusterID,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.UninstallSharedAlertmanager },
			audit:  "monitoring.shared_alertmanager.uninstall", details: sharedAuditDetailKeys},
		{name: "status", method: http.MethodGet, target: base + "/status/",
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.GetSharedAlertmanagerStatus }},
	}
}

var (
	sharedAuditDetailKeys  = []string{"managementClusterId", "namespace", "operationId", "releaseName"}
	clusterAuditDetailKeys = []string{"namespace", "operationId"}
)

// clusterStackCases mirrors internal/server/routes_clusters.go: the same eight
// handlers with the same per-route verbs. The verbs are part of the contract —
// install is VerbCreate and uninstall is VerbDelete here, where the shared
// families use VerbUpdate for both — so they are asserted, not assumed.
//
// The URL params mirror production exactly: `id` and nothing else. Every route
// is mounted as `/{id}/monitoring/...` (route_table.golden lines 52, 263-264,
// 526-528, 646-647) and no enclosing pattern declares `{cluster_id}`, which is
// why every handler must read `id`.
func clusterStackCases() []stackLifecycleCase {
	base := "/api/v1/clusters/" + stackTestClusterID + "/monitoring"
	params := map[string]string{"id": stackTestClusterID}
	return []stackLifecycleCase{
		{name: "config-get", method: http.MethodGet, target: base + "/config/", params: params, verb: rbac.VerbRead,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.GetClusterConfig }},
		{name: "config-put", method: http.MethodPut, target: base + "/config/", params: params, verb: rbac.VerbUpdate,
			body:   `{"stackNamespace":"monitoring"}`,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.UpdateClusterConfig },
			audit:  "monitoring.cluster_config.update", details: []string{"backendId", "stackNamespace", "status"}},
		{name: "status", method: http.MethodGet, target: base + "/stack/status/", params: params, verb: rbac.VerbRead,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.GetStackStatus }},
		{name: "preview", method: http.MethodPost, target: base + "/stack/preview/", params: params, verb: rbac.VerbRead,
			body:   clusterStackBody,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.PreviewStack }},
		{name: "install", method: http.MethodPost, target: base + "/stack/install/", params: params, verb: rbac.VerbCreate,
			body:   clusterStackBody,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.InstallStack },
			audit:  "monitoring.stack.install", details: clusterAuditDetailKeys},
		{name: "upgrade", method: http.MethodPut, target: base + "/stack/upgrade/", params: params, verb: rbac.VerbUpdate,
			body:   clusterStackBody,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.UpgradeStack },
			audit:  "monitoring.stack.upgrade", details: clusterAuditDetailKeys},
		{name: "replace", method: http.MethodPost, target: base + "/stack/replace/", params: params, verb: rbac.VerbUpdate,
			body:   clusterStackBody,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.ReplaceStack },
			audit:  "monitoring.stack.replace", details: clusterAuditDetailKeys},
		{name: "uninstall", method: http.MethodDelete, target: base + "/stack/uninstall/", params: params, verb: rbac.VerbDelete,
			invoke: func(h *MonitoringHandler) http.HandlerFunc { return h.UninstallStack },
			audit:  "monitoring.stack.uninstall", details: clusterAuditDetailKeys},
	}
}

// --- the gate ----------------------------------------------------------------

// TestSharedStackLifecycleDeniesCallerWithoutMonitoringPermission covers the two
// families gated inside the handler, by sharedStackLifecycle's preamble.
func TestSharedStackLifecycleDeniesCallerWithoutMonitoringPermission(t *testing.T) {
	families := map[string][]stackLifecycleCase{
		"shared_thanos":       sharedThanosCases(),
		"shared_alertmanager": sharedAlertmanagerCases(),
		"shared_grafana":      sharedGrafanaCases(),
	}
	for family, cases := range families {
		for _, tc := range cases {
			t.Run(family+"/"+tc.name, func(t *testing.T) {
				h, _ := newStackLifecycleHandler(t)
				h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: denyMonitoring()})
				rec := httptest.NewRecorder()
				tc.invoke(h)(rec, tc.request())
				if rec.Code != http.StatusForbidden {
					t.Fatalf("caller without monitoring permission got %d, want 403: %s", rec.Code, rec.Body.String())
				}

				// Positive control: the identical request must get PAST the gate
				// for a caller who holds monitoring, so the 403 above can only
				// have come from authorization.
				h2, _ := newStackLifecycleHandler(t)
				h2.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})
				rec2 := httptest.NewRecorder()
				tc.invoke(h2)(rec2, tc.request())
				if rec2.Code == http.StatusForbidden {
					t.Fatalf("caller WITH monitoring permission also got 403 — the test is not measuring authorization: %s", rec2.Body.String())
				}
			})
		}
	}
}

// TestClusterStackLifecycleDeniesCallerWithoutMonitoringPermission covers the
// third family, whose gate is requirePermission middleware at
// internal/server/routes_clusters.go rather than a preamble in the body. It is
// driven through the same middleware with the same (resource, verb) pairs the
// router mounts, and with the URL params the router actually produces.
func TestClusterStackLifecycleDeniesCallerWithoutMonitoringPermission(t *testing.T) {
	for _, tc := range clusterStackCases() {
		t.Run(tc.name, func(t *testing.T) {
			if tc.verb == "" {
				t.Fatalf("case %s declares no route verb", tc.name)
			}
			h, _ := newStackLifecycleHandler(t)
			gate := middleware.RequirePermission(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: denyMonitoring()}, rbac.ResourceMonitoring, tc.verb)
			rec := httptest.NewRecorder()
			gate(tc.invoke(h)).ServeHTTP(rec, tc.request())
			if rec.Code != http.StatusForbidden {
				t.Fatalf("caller without monitoring:%s got %d, want 403: %s", tc.verb, rec.Code, rec.Body.String())
			}

			h2, _ := newStackLifecycleHandler(t)
			allow := middleware.RequirePermission(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()}, rbac.ResourceMonitoring, tc.verb)
			rec2 := httptest.NewRecorder()
			allow(tc.invoke(h2)).ServeHTTP(rec2, tc.request())
			if rec2.Code == http.StatusForbidden {
				t.Fatalf("caller WITH monitoring:%s also got 403 — the test is not measuring authorization: %s", tc.verb, rec2.Body.String())
			}
		})
	}
}

// --- the routing defect these cases must not paper over ------------------------

// TestClusterStackResolvesClusterIDFromTheRoutedParam is the inverse of a test
// that used to pin the opposite. UninstallStack, GetStackStatus and
// monitoringStackPayload (backing preview/install/upgrade/replace) read
// chi.URLParam(r, "cluster_id") while every route is mounted as
// `/{id}/monitoring/...`, so the param was always empty and all six per-cluster
// endpoints ran with no cluster: install/upgrade/replace failed on
// uuid.Parse(""), uninstall and status answered 400, and preview quietly
// returned a body naming no cluster. No route anywhere declared `{cluster_id}`,
// so these endpoints had never worked.
//
// This asserts the routed param actually reaches every handler. It fails if the
// mismatch is reintroduced at any of the three sites.
//
// NOW CLOSED, and this is where the follow-up landed: requirePermission read
// `cluster_id` too and only fell back to `id` for ResourceClusters, so these
// per-cluster routes were also evaluated as a GLOBAL monitoring check.
// appmiddleware.ClusterScopeFromIDParam, mounted on the whole /clusters subtree
// by registerClusterRoutes, now supplies the routed cluster as the permission
// scope. The scope contract is pinned in
// internal/server/routes_monitoring_scope_test.go (all four directions) and
// internal/server/routes_clusters_scope_test.go (that the declaration reaches
// these routes and nothing whose {id} is not a cluster).
func TestClusterStackResolvesClusterIDFromTheRoutedParam(t *testing.T) {
	byName := map[string]stackLifecycleCase{}
	for _, tc := range clusterStackCases() {
		byName[tc.name] = tc
	}
	for _, name := range []string{"status", "install", "upgrade", "replace", "uninstall"} {
		tc, ok := byName[name]
		if !ok {
			t.Fatalf("case %q disappeared from clusterStackCases", name)
		}
		t.Run(name, func(t *testing.T) {
			h, _ := newStackLifecycleHandler(t)
			rec := httptest.NewRecorder()
			tc.invoke(h)(rec, tc.request())
			if rec.Code >= 400 {
				t.Fatalf("status = %d (%s): the handler could not resolve the cluster from the routed {id} param",
					rec.Code, rec.Body.String())
			}
		})
	}

	// Preview was the quiet one: it answered 200 and simply named no cluster.
	t.Run("preview", func(t *testing.T) {
		h, _ := newStackLifecycleHandler(t)
		rec := httptest.NewRecorder()
		byName["preview"].invoke(h)(rec, byName["preview"].request())
		var body struct {
			Data struct {
				ClusterID string `json:"clusterId"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode preview body: %v (%s)", err, rec.Body.String())
		}
		if body.Data.ClusterID != stackTestClusterID {
			t.Fatalf("clusterId = %q, want %q", body.Data.ClusterID, stackTestClusterID)
		}
	})
}

// --- the audit contract -------------------------------------------------------

// TestStackLifecycleAuditEventsUnchanged pins the audit action names and detail
// keys emitted by all three families. Collapsing the shared families onto one
// driver moved where recordAudit is written; these names are a wire contract
// (internal/audit pins the vocabulary and the coverage contract requires every
// mutating handler in these files to reach an audit call), so they are asserted
// per endpoint rather than left to the build gate, which only checks that SOME
// audit call is reachable.
//
// The per-cluster cases now use the real routed request shape; they previously
// needed a fabricated `cluster_id` param because the handlers read one no route
// supplied. See TestClusterStackResolvesClusterIDFromTheRoutedParam.
func TestStackLifecycleAuditEventsUnchanged(t *testing.T) {
	all := append(append(append(sharedThanosCases(), sharedAlertmanagerCases()...), sharedGrafanaCases()...), clusterStackCases()...)
	for _, tc := range all {
		if tc.audit == "" {
			continue
		}
		t.Run(tc.audit, func(t *testing.T) {
			h, q := newStackLifecycleHandler(t)
			h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})
			rec := httptest.NewRecorder()
			tc.invoke(h)(rec, tc.request())
			if rec.Code != http.StatusAccepted && rec.Code != http.StatusOK {
				t.Fatalf("endpoint did not succeed (%d), so no audit row was produced: %s", rec.Code, rec.Body.String())
			}
			if len(q.audits) != 1 {
				t.Fatalf("want exactly 1 audit row, got %d", len(q.audits))
			}
			row := q.audits[0]
			if row.Action != tc.audit {
				t.Fatalf("audit action = %q, want %q", row.Action, tc.audit)
			}
			var detail map[string]any
			if err := json.Unmarshal(row.Detail, &detail); err != nil {
				t.Fatalf("audit detail is not a JSON object: %v (%s)", err, row.Detail)
			}
			keys := make([]string, 0, len(detail))
			for k := range detail {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if strings.Join(keys, ",") != strings.Join(tc.details, ",") {
				t.Fatalf("audit detail keys = %v, want %v", keys, tc.details)
			}
			if row.ResourceID == "" {
				t.Fatalf("audit row carries no resource id")
			}
		})
	}
}

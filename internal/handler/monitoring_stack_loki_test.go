package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func lokiAuthed(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	return req.WithContext(appmiddleware.SetAuthenticatedUserForTest(req.Context(), &appmiddleware.AuthenticatedUser{
		ID: uuid.NewString(), AuthMethod: "jwt",
	}))
}

func lokiSingleNodeSmallK8sFake(t *testing.T) *sizerK8sFake {
	t.Helper()
	return &sizerK8sFake{
		t:     t,
		nodes: []corev1.Node{sizerTestNode("n1", "4", "8Gi", true, false)},
		pods:  []corev1.Pod{sizerTestPod("p1", "2100m", "4608Mi", "", "")},
		storage: storageClassWire{
			AccessModes: []string{"ReadWriteOnce"},
		},
	}
}

func TestSharedLokiInstall412SingleNodeSmall(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	fake := lokiSingleNodeSmallK8sFake(t)
	h.requester = fake
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})

	rec := httptest.NewRecorder()
	h.InstallSharedLokiStack(rec, lokiAuthed(http.MethodPost, "/api/v1/settings/monitoring/loki/install/", sharedLokiBody))
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
	if !strings.Contains(wrap.Error.Message, "single_node_small") {
		t.Fatalf("message = %q, want single_node_small", wrap.Error.Message)
	}
	for _, call := range fake.calls {
		if strings.Contains(call, "persistentvolumeclaims") {
			t.Fatalf("1-node small install must fail before WAL PVC probe, calls=%v", fake.calls)
		}
	}
}

func TestSharedLokiUpgradeSkipsSizerGate(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	h.requester = lokiSingleNodeSmallK8sFake(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})

	rec := httptest.NewRecorder()
	h.UpgradeSharedLokiStack(rec, lokiAuthed(http.MethodPut, "/api/v1/settings/monitoring/loki/upgrade/", sharedLokiBody))
	if rec.Code == http.StatusPreconditionFailed {
		t.Fatalf("upgrade must skip sizer mode gate, got 412: %s", rec.Body.String())
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
}

func TestSharedLokiPreviewDoesNotCreatePVC(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	fake := grafanaPassingK8sFake(t)
	h.requester = fake
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})

	rec := httptest.NewRecorder()
	h.PreviewSharedLokiStack(rec, lokiAuthed(http.MethodPost, "/api/v1/settings/monitoring/loki/preview/", sharedLokiBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	for _, call := range fake.calls {
		if strings.Contains(call, "persistentvolumeclaims") {
			t.Fatalf("preview must not create or delete PVCs, calls=%v", fake.calls)
		}
		if !strings.HasPrefix(call, http.MethodGet+" ") {
			t.Fatalf("preview issued mutating call %s", call)
		}
	}
}

func TestSharedLokiFeatureGateDefaultFalse(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})
	next := http.HandlerFunc(h.GetSharedLokiStatus)
	mw := appmiddleware.FeatureGateDefault("feature.hosted_loki", nil, false)

	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, lokiAuthed(http.MethodGet, "/api/v1/settings/monitoring/loki/status/", ""))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when feature.hosted_loki defaults false: %s", rec.Code, rec.Body.String())
	}

	raw, err := os.ReadFile("../server/routes.go")
	if err != nil {
		t.Fatalf("read routes.go: %v", err)
	}
	if !strings.Contains(string(raw), `FeatureGateDefault("feature.hosted_loki"`) {
		t.Fatal("Loki status/preview/mutate routes must use FeatureGateDefault(\"feature.hosted_loki\", ..., false)")
	}
	if !strings.Contains(string(raw), `deps.Monitoring.GetSharedLokiStatus`) {
		t.Fatal("Loki status route is not mounted")
	}
}

func TestSharedLokiPreviewIsClusterIPOnly(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})

	rec := httptest.NewRecorder()
	h.PreviewSharedLokiStack(rec, lokiAuthed(http.MethodPost, "/api/v1/settings/monitoring/loki/preview/", sharedLokiBody))
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
	if wrap.Data.Chart.RepoURL != sharedLokiChartRepo || wrap.Data.Chart.ChartName != sharedLokiChartName {
		t.Fatalf("chart = %+v", wrap.Data.Chart)
	}
	if wrap.Data.Values["deploymentMode"] != "SingleBinary" {
		t.Fatalf("deploymentMode = %v, want SingleBinary", wrap.Data.Values["deploymentMode"])
	}
	gw, _ := wrap.Data.Values["gateway"].(map[string]any)
	svc, _ := gw["service"].(map[string]any)
	if svc["type"] != "ClusterIP" {
		t.Fatalf("gateway.service.type = %v, want ClusterIP", svc["type"])
	}
	ing, _ := gw["ingress"].(map[string]any)
	if ing["enabled"] != false {
		t.Fatalf("gateway.ingress.enabled = %v, want false", ing["enabled"])
	}
	raw, _ := json.Marshal(wrap.Data.Values)
	s := string(raw)
	if strings.Contains(s, `"kind":"Ingress"`) || strings.Contains(s, `"kind":"HTTPRoute"`) {
		t.Fatalf("Loki install must not create Ingress/HTTPRoute: %s", s)
	}
	if strings.Contains(s, "objstore.yml") || strings.Contains(s, "objstoreConfig") || strings.Contains(s, `"type":"S3"`) {
		t.Fatalf("must not write Thanos objstore.yml into Loki: %s", s)
	}
	if !strings.Contains(s, "loki-auth") {
		t.Fatalf("values must include loki-auth extraObjects")
	}
	if !strings.Contains(s, "ghcr.io/alphabravo-oss/astronomer-go-server:test-pr3") {
		t.Fatalf("loki-auth image must equal the configured server image")
	}
	if !strings.Contains(s, `"auth_enabled":true`) && !strings.Contains(s, `"auth_enabled": true`) {
		t.Fatalf("loki.auth_enabled must be true: %s", s)
	}
	loki, _ := wrap.Data.Values["loki"].(map[string]any)
	if loki["auth_enabled"] != true {
		t.Fatalf("loki.auth_enabled = %v", loki["auth_enabled"])
	}
	storage, _ := loki["storage"].(map[string]any)
	if storage["type"] != "s3" {
		t.Fatalf("loki.storage.type = %v, want s3", storage["type"])
	}
	if storage["use_thanos_objstore"] != true {
		t.Fatalf("use_thanos_objstore = %v, want true so chart 6.27 honors object_store.prefix", storage["use_thanos_objstore"])
	}
	obj, _ := storage["object_store"].(map[string]any)
	if obj["prefix"] != "loki" {
		t.Fatalf("object_store.prefix = %v, want loki", obj["prefix"])
	}
	if obj["type"] != "s3" {
		t.Fatalf("object_store.type = %v, want s3", obj["type"])
	}
	schema, _ := loki["schemaConfig"].(map[string]any)
	configs, _ := schema["configs"].([]any)
	if len(configs) == 0 {
		t.Fatal("schemaConfig.configs empty")
	}
	first, _ := configs[0].(map[string]any)
	if first["store"] != "tsdb" {
		t.Fatalf("schema store = %v, want tsdb", first["store"])
	}
	if first["from"] != sharedLokiSchemaFrom {
		t.Fatalf("schema from = %v", first["from"])
	}
}

func TestSharedLokiInstallCachesWALAndSizerVerdict(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})

	rec := httptest.NewRecorder()
	h.InstallSharedLokiStack(rec, lokiAuthed(http.MethodPost, "/api/v1/settings/monitoring/loki/install/", sharedLokiBody))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	meta := sharedStackMetadata(q.backend, "sharedLoki")
	if !boolFromAny(meta["walCapacityKnown"]) {
		t.Fatalf("walCapacityKnown = %v after passing install; persist clobbered precheck cache", meta["walCapacityKnown"])
	}
	if int64FromAny(meta["walCapacityBytes"]) != sizerWALSingleBinaryBytes {
		t.Fatalf("walCapacityBytes = %v, want %d", meta["walCapacityBytes"], sizerWALSingleBinaryBytes)
	}
	verdict := mapFromMapValue(meta["lastSizerVerdict"])
	if verdict["result"] != "pass" {
		t.Fatalf("lastSizerVerdict = %v, want result=pass", meta["lastSizerVerdict"])
	}

	statusRec := httptest.NewRecorder()
	h.GetSharedLokiStatus(statusRec, lokiAuthed(http.MethodGet, "/api/v1/settings/monitoring/loki/status/", ""))
	var wrap struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(statusRec.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	statusVerdict := mapFromMapValue(wrap.Data["lastSizerVerdict"])
	if statusVerdict["result"] != "pass" {
		t.Fatalf("status lastSizerVerdict = %v", wrap.Data["lastSizerVerdict"])
	}

	sizerRec := httptest.NewRecorder()
	h.GetMonitoringSizer(sizerRec, lokiAuthed(http.MethodGet, "/api/v1/settings/monitoring/sizer/", ""))
	var sizerWrap struct {
		Data MonitoringSizerResponse `json:"data"`
	}
	if err := json.Unmarshal(sizerRec.Body.Bytes(), &sizerWrap); err != nil {
		t.Fatalf("decode sizer: %v", err)
	}
	if !sizerWrap.Data.StorageClass.WALCapacityKnown {
		t.Fatalf("GET /sizer/ walCapacityKnown=false after install cache")
	}
}

func TestSharedLokiSimpleScalableRequestsMatchSizer(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})
	body := `{"managementClusterId":"` + stackTestClusterID + `","storageConfigId":"` + stackTestStorageID + `","ingestHostname":"loki-ingest.example.com","mode":"simpleScalable"}`
	rec := httptest.NewRecorder()
	h.PreviewSharedLokiStack(rec, lokiAuthed(http.MethodPost, "/api/v1/settings/monitoring/loki/preview/", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var wrap struct {
		Data struct {
			Values map[string]any `json:"values"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	cpu, mem := sumLokiChartRequests(t, wrap.Data.Values)
	wantCPU := int64(sizerSimpleScalableCPUMilli)
	wantMem := int64(sizerSimpleScalableMemBytes)
	if cpu != wantCPU || mem != wantMem {
		t.Fatalf("SimpleScalable requests = %dm / %d bytes, want %dm / %d (sizer floor including loki-auth)", cpu, mem, wantCPU, wantMem)
	}
}

func TestSharedLokiStatusProjectsRequiredFields(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})
	trueVal := true
	if err := h.updateSharedLokiMetadata(httptest.NewRequest(http.MethodGet, "/", nil).Context(), q.backend, SharedLokiRequest{
		ManagementClusterID:   stackTestClusterID,
		Namespace:             "monitoring",
		ReleaseName:           sharedLokiDefaultRelease,
		ChartVersion:          sharedLokiDefaultChart,
		StorageConfigID:       stackTestStorageID,
		IngestHostname:        "loki-ingest.example.com",
		Mode:                  sizerModeSingleBinary,
		SkipDiskCheck:         &trueVal,
		AutoRollbackOnFailure: &trueVal,
	}, "healthy"); err != nil {
		t.Fatalf("persist: %v", err)
	}

	rec := httptest.NewRecorder()
	h.GetSharedLokiStatus(rec, lokiAuthed(http.MethodGet, "/api/v1/settings/monitoring/loki/status/", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var wrap struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if wrap.Data["chartVersion"] != sharedLokiDefaultChart {
		t.Fatalf("chartVersion = %v", wrap.Data["chartVersion"])
	}
	if wrap.Data["autoRollbackOnFailure"] != true {
		t.Fatalf("autoRollbackOnFailure = %v, want true", wrap.Data["autoRollbackOnFailure"])
	}
	if wrap.Data["skipDiskCheck"] != true {
		t.Fatalf("skipDiskCheck = %v, want true", wrap.Data["skipDiskCheck"])
	}
	if wrap.Data["ingestPublic"] != false {
		t.Fatalf("ingestPublic = %v, want false", wrap.Data["ingestPublic"])
	}
	if wrap.Data["computedLokiPrefix"] != "loki" {
		t.Fatalf("computedLokiPrefix = %v", wrap.Data["computedLokiPrefix"])
	}
	if wrap.Data["queryUrl"] != "http://astronomer-loki-gateway.monitoring.svc.cluster.local" {
		t.Fatalf("queryUrl = %v", wrap.Data["queryUrl"])
	}
	if wrap.Data["authUrl"] != "http://astronomer-loki-auth.monitoring.svc.cluster.local:8080" {
		t.Fatalf("authUrl = %v", wrap.Data["authUrl"])
	}
}

func TestSharedLokiIngressOnlyWhenTokensExist(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	h.SetAuthorization(rbac.NewEngine(), stubMonitoringRBACQuerier{bindings: grantMonitoring()})

	preview := func() map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		h.PreviewSharedLokiStack(rec, lokiAuthed(http.MethodPost, "/api/v1/settings/monitoring/loki/preview/", sharedLokiBody))
		if rec.Code != http.StatusOK {
			t.Fatalf("preview status = %d: %s", rec.Code, rec.Body.String())
		}
		var wrap struct {
			Data struct {
				Values map[string]any `json:"values"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return wrap.Data.Values
	}

	assertIngress := func(values map[string]any, want bool) {
		t.Helper()
		raw, _ := json.Marshal(values)
		has := strings.Contains(string(raw), `"kind":"Ingress"`)
		if has != want {
			t.Fatalf("ingress present=%v, want %v: %s", has, want, raw)
		}
		if want && !strings.Contains(string(raw), "loki-ingest.example.com") {
			t.Fatalf("ingress missing explicit ingestHostname: %s", raw)
		}
		if want && strings.Contains(string(raw), "astronomer.localtest.me") {
			t.Fatalf("ingress must not derive host from Astronomer ingress: %s", raw)
		}
		if strings.Contains(string(raw), "token_encrypted") || strings.Contains(string(raw), "bearer_token") {
			t.Fatalf("helm values leaked plaintext token: %s", raw)
		}
	}

	assertIngress(preview(), false)

	q.lokiTokens = []sqlc.ListLokiIngestTokenHashesRow{{
		ClusterID: uuid.MustParse(stackTestClusterID),
		TokenHash: "abc123",
	}}
	values := preview()
	assertIngress(values, true)
	extra, _ := values["extraObjects"].([]any)
	var sawAuthBackend, sawPushPath, sawNP bool
	for _, obj := range extra {
		m, _ := obj.(map[string]any)
		kind, _ := m["kind"].(string)
		raw, _ := json.Marshal(m)
		switch kind {
		case "Ingress":
			if strings.Contains(string(raw), "astronomer-loki-auth") {
				sawAuthBackend = true
			}
			if strings.Contains(string(raw), "/loki/api/v1/push") {
				sawPushPath = true
			}
		case "NetworkPolicy":
			sawNP = true
		}
	}
	if !sawAuthBackend || !sawPushPath {
		t.Fatalf("ingress backend/path missing backend=%v path=%v", sawAuthBackend, sawPushPath)
	}
	if !sawNP {
		t.Fatal("expected NetworkPolicy extraObjects")
	}
}

func TestSharedLokiModeChangeRequiresReplace(t *testing.T) {
	meta := map[string]any{
		"status":          "healthy",
		"namespace":       "monitoring",
		"releaseName":     sharedLokiDefaultRelease,
		"mode":            sizerModeSingleBinary,
		"storageClass":    "default",
		"storageConfigId": stackTestStorageID,
	}
	req := SharedLokiRequest{
		Namespace:       "monitoring",
		ReleaseName:     sharedLokiDefaultRelease,
		Mode:            sizerModeSimpleScalable,
		StorageClass:    "default",
		StorageConfigID: stackTestStorageID,
	}
	needed, reasons := sharedLokiReplaceRequired(meta, req)
	if !needed {
		t.Fatal("mode change must require replace")
	}
	joined := strings.Join(reasons, ",")
	if !strings.Contains(joined, "mode") {
		t.Fatalf("reasons = %v, want mode change", reasons)
	}
}

func TestSharedLokiUpgradeOmittingSkipDiskCheckDoesNotFlip(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	trueVal := true
	if err := h.updateSharedLokiMetadata(context.Background(), q.backend, SharedLokiRequest{
		ManagementClusterID: stackTestClusterID,
		Namespace:           "monitoring",
		ReleaseName:         sharedLokiDefaultRelease,
		ChartVersion:        sharedLokiDefaultChart,
		StorageConfigID:     stackTestStorageID,
		IngestHostname:      "loki-ingest.example.com",
		SkipDiskCheck:       &trueVal,
	}, "healthy"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if err := h.updateSharedLokiMetadata(context.Background(), q.backend, SharedLokiRequest{
		ManagementClusterID: stackTestClusterID,
		Namespace:           "monitoring",
		ReleaseName:         sharedLokiDefaultRelease,
		ChartVersion:        sharedLokiDefaultChart,
		StorageConfigID:     stackTestStorageID,
		IngestHostname:      "loki-ingest.example.com",
	}, "updating"); err != nil {
		t.Fatalf("upgrade persist: %v", err)
	}
	meta := sharedStackMetadata(q.backend, "sharedLoki")
	if !boolFromAny(meta["skipDiskCheck"]) {
		t.Fatalf("skipDiskCheck flipped to false on omit: %v", meta["skipDiskCheck"])
	}
}

func TestSharedLokiHelmApplyNeverTouchesObjstoreSecret(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	var helm helmCapture
	h.helm = &helm
	req := SharedLokiRequest{
		ManagementClusterID: stackTestClusterID,
		Namespace:           "monitoring",
		ReleaseName:         sharedLokiDefaultRelease,
		ChartVersion:        sharedLokiDefaultChart,
	}
	if _, err := h.applySharedLokiStack(context.Background(), protocol.MsgHelmInstall, req, map[string]any{"loki": map[string]any{"storage": map[string]any{"type": "s3"}}}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	raw, _ := json.Marshal(helm.last.Values)
	if strings.Contains(string(raw), "objstore") {
		t.Fatalf("helm values leaked Thanos objstore: %s", raw)
	}
	if helm.last.ChartName != sharedLokiChartName || helm.last.RepoURL != sharedLokiChartRepo {
		t.Fatalf("chart = %s %s", helm.last.RepoURL, helm.last.ChartName)
	}
	if helm.last.Version != sharedLokiDefaultChart {
		t.Fatalf("version = %s", helm.last.Version)
	}
}

type helmCapture struct {
	last protocol.HelmRequestPayload
}

func (h *helmCapture) Do(_ context.Context, _ string, _ protocol.MessageType, req protocol.HelmRequestPayload) (*protocol.HelmResultPayload, error) {
	h.last = req
	return &protocol.HelmResultPayload{}, nil
}

func (h *helmCapture) Status(context.Context, string, string, string) (*protocol.HelmResultPayload, error) {
	return nil, nil
}

func (h *helmCapture) History(context.Context, string, string, string) (*protocol.HelmResultPayload, error) {
	return nil, nil
}

func sumLokiChartRequests(t *testing.T, values map[string]any) (cpuMilli, memBytes int64) {
	t.Helper()
	add := func(replicas int, res map[string]any) {
		if replicas <= 0 || len(res) == 0 {
			return
		}
		reqs, _ := res["requests"].(map[string]any)
		if len(reqs) == 0 {
			return
		}
		cpuMilli += int64(replicas) * mustMilliCPU(t, reqs["cpu"])
		memBytes += int64(replicas) * mustMemBytes(t, reqs["memory"])
	}
	for _, name := range []string{"write", "read", "backend"} {
		comp, _ := values[name].(map[string]any)
		add(int(int64FromAny(comp["replicas"])), mapFromMapValue(comp["resources"]))
	}
	gw, _ := values["gateway"].(map[string]any)
	if gw["enabled"] != false {
		add(1, mapFromMapValue(gw["resources"]))
	}
	extra, _ := values["extraObjects"].([]any)
	for _, obj := range extra {
		m, _ := obj.(map[string]any)
		if m["kind"] != "Deployment" {
			continue
		}
		spec, _ := m["spec"].(map[string]any)
		tmpl, _ := spec["template"].(map[string]any)
		pspec, _ := tmpl["spec"].(map[string]any)
		containers, _ := pspec["containers"].([]any)
		for _, c := range containers {
			cm, _ := c.(map[string]any)
			add(1, mapFromMapValue(cm["resources"]))
		}
	}
	return cpuMilli, memBytes
}

func mustMilliCPU(t *testing.T, v any) int64 {
	t.Helper()
	s, _ := v.(string)
	q := resource.MustParse(s)
	return q.MilliValue()
}

func mustMemBytes(t *testing.T, v any) int64 {
	t.Helper()
	s, _ := v.(string)
	q := resource.MustParse(s)
	return q.Value()
}

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

func TestSystemLoggingOutputUniquePerCluster(t *testing.T) {
	q := newLoggingFakeQuerier()
	clusterID := uuid.New()
	arg := sqlc.CreateLoggingOutputParams{
		Name:          systemLoggingOutputName,
		OutputType:    "loki",
		Configuration: json.RawMessage(`{"host":"loki.example.com"}`),
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		Enabled:       true,
		IsSystem:      true,
	}
	if _, err := q.CreateLoggingOutput(context.Background(), arg); err != nil {
		t.Fatalf("first system row: %v", err)
	}
	if _, err := q.CreateLoggingOutput(context.Background(), arg); err == nil {
		t.Fatal("expected unique constraint on (cluster_id) WHERE is_system")
	} else if !isUniqueViolation(err) {
		t.Fatalf("second system row err = %v, want unique violation", err)
	}
	other := uuid.New()
	arg.ClusterID = pgtype.UUID{Bytes: other, Valid: true}
	if _, err := q.CreateLoggingOutput(context.Background(), arg); err != nil {
		t.Fatalf("system row on a different cluster: %v", err)
	}
	arg.IsSystem = false
	arg.ClusterID = pgtype.UUID{Bytes: clusterID, Valid: true}
	if _, err := q.CreateLoggingOutput(context.Background(), arg); err != nil {
		t.Fatalf("BYO row on same cluster: %v", err)
	}
}

func TestUpsertSystemLoggingOutputIdempotent(t *testing.T) {
	q := newLoggingFakeQuerier()
	h := NewLoggingHandler(q)
	clusterID := uuid.New()
	first, err := h.upsertSystemLoggingOutput(context.Background(), systemLoggingOutputSpec{
		ClusterID: clusterID,
		Host:      "loki-ingest.example.com",
		Port:      "443",
		Enabled:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !first.IsSystem || first.OutputType != "loki" || first.Name != systemLoggingOutputName {
		t.Fatalf("first row = %+v", first)
	}
	if bytes.Contains(first.Configuration, []byte("bearer")) {
		t.Fatalf("configuration stored bearer: %s", first.Configuration)
	}
	second, err := h.upsertSystemLoggingOutput(context.Background(), systemLoggingOutputSpec{
		ClusterID: clusterID,
		Host:      "loki-ingest.example.com",
		Port:      "443",
		Enabled:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("upsert created a second row: %s vs %s", second.ID, first.ID)
	}
	if second.Enabled {
		t.Fatal("second upsert should have disabled the row")
	}
	if n := len(q.outputs); n != 1 {
		t.Fatalf("outputs = %d, want 1", n)
	}
}

func TestLoggingOutputDTORedactsSystemBearer(t *testing.T) {
	clusterID := uuid.New()
	raw, _ := json.Marshal(map[string]any{
		"host":         "loki-ingest.example.com",
		"port":         "443",
		"tls":          "on",
		"tenant_id":    clusterID.String(),
		"labels":       "cluster=" + clusterID.String() + ",job=fluentbit",
		"bearer_token": "super-secret-token",
		"http_passwd":  "also-secret",
	})
	dto := loggingOutputDTO(sqlc.LoggingOutput{
		ID:            uuid.New(),
		Name:          systemLoggingOutputName,
		OutputType:    "loki",
		Configuration: raw,
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		Enabled:       true,
		IsSystem:      true,
	})
	body, _ := json.Marshal(dto)
	if strings.Contains(string(body), "super-secret-token") || strings.Contains(string(body), "bearer") {
		t.Fatalf("DTO leaked bearer: %s", body)
	}
	if strings.Contains(string(body), "also-secret") || strings.Contains(string(body), "http_passwd") {
		t.Fatalf("DTO leaked extra system config: %s", body)
	}
	cfg := decodeConfiguration(dto.Configuration)
	for _, k := range []string{"host", "port", "tls", "tenant_id", "labels"} {
		if _, ok := cfg[k]; !ok {
			t.Errorf("DTO missing %s", k)
		}
	}
	if len(cfg) != 5 {
		t.Fatalf("system DTO config keys = %v, want 5 safe keys", cfg)
	}
}

func TestListOutputsRedactsSystemBearer(t *testing.T) {
	q := newLoggingFakeQuerier()
	h := NewLoggingHandler(q)
	clusterID := uuid.New()
	cfg, _ := json.Marshal(map[string]any{
		"host":         "loki-ingest.example.com",
		"port":         "443",
		"tls":          "on",
		"tenant_id":    clusterID.String(),
		"labels":       "job=fluentbit",
		"bearer_token": "must-not-list",
	})
	q.outputs[uuid.New()] = sqlc.LoggingOutput{
		ID:            uuid.New(),
		Name:          systemLoggingOutputName,
		OutputType:    "loki",
		Configuration: cfg,
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		Enabled:       true,
		IsSystem:      true,
	}
	rec := httptest.NewRecorder()
	h.ListOutputs(rec, httptest.NewRequest(http.MethodGet, "/api/v1/logging/outputs/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "must-not-list") || strings.Contains(rec.Body.String(), "bearer_token") {
		t.Fatalf("list leaked bearer: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"is_system":true`) {
		t.Fatalf("list missing is_system: %s", rec.Body.String())
	}
}

func TestQueryOutputSystemRowReturns501(t *testing.T) {
	q := newLoggingFakeQuerier()
	clusterID := uuid.New()
	out, err := q.CreateLoggingOutput(context.Background(), sqlc.CreateLoggingOutputParams{
		Name:          systemLoggingOutputName,
		OutputType:    "loki",
		Configuration: json.RawMessage(`{"host":"loki-ingest.example.com"}`),
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		Enabled:       true,
		IsSystem:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := NewLoggingHandler(q)
	h.SetAuthorization(rbac.NewEngine(), stubLoggingRBACQuerier{bindings: []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceLogging), Verbs: []string{string(rbac.VerbRead)}}},
	}}})
	req := authedLoggingReq(http.MethodPost, "/api/v1/logging/outputs/"+out.ID.String()+"/query/", []byte(`{"query":"{job=~\".+\"}"}`))
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", out.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: uuid.NewString()}))
	rec := httptest.NewRecorder()
	h.QueryOutput(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501: %s", rec.Code, rec.Body.String())
	}
	var wrap struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if wrap.Error.Code != apierror.NotImplemented {
		t.Fatalf("code = %q", wrap.Error.Code)
	}
	if !strings.Contains(strings.ToLower(wrap.Error.Message), "fleet grafana") {
		t.Fatalf("message = %q, want fleet Grafana", wrap.Error.Message)
	}
}

func TestDeleteSystemOutputForbidden(t *testing.T) {
	q := newLoggingFakeQuerier()
	clusterID := uuid.New()
	out, err := q.CreateLoggingOutput(context.Background(), sqlc.CreateLoggingOutputParams{
		Name:          systemLoggingOutputName,
		OutputType:    "loki",
		Configuration: json.RawMessage(`{"host":"loki-ingest.example.com"}`),
		ClusterID:     pgtype.UUID{Bytes: clusterID, Valid: true},
		Enabled:       true,
		IsSystem:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := NewLoggingHandler(q)
	h.SetAuthorization(rbac.NewEngine(), stubLoggingRBACQuerier{bindings: []rbac.RoleBinding{{
		ClusterID: clusterID.String(),
		RoleRules: []rbac.Rule{{Resource: string(rbac.ResourceLogging), Verbs: []string{string(rbac.VerbDelete)}}},
	}}})
	req := authedLoggingReq(http.MethodDelete, "/api/v1/logging/outputs/"+out.ID.String()+"/", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", out.ID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: uuid.NewString()}))
	rec := httptest.NewRecorder()
	h.DeleteOutput(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if _, ok := q.outputs[out.ID]; !ok {
		t.Fatal("system row was deleted")
	}
}

func TestLokiUninstallDisablesSystemOutputs(t *testing.T) {
	loggingQ := newLoggingFakeQuerier()
	lh := NewLoggingHandler(loggingQ)
	clusterA := uuid.New()
	clusterB := uuid.New()
	if _, err := lh.upsertSystemLoggingOutput(context.Background(), systemLoggingOutputSpec{
		ClusterID: clusterA, Host: "loki-ingest.example.com", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := lh.upsertSystemLoggingOutput(context.Background(), systemLoggingOutputSpec{
		ClusterID: clusterB, Host: "loki-ingest.example.com", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	byo, err := loggingQ.CreateLoggingOutput(context.Background(), sqlc.CreateLoggingOutputParams{
		Name:          "splunk",
		OutputType:    "splunk",
		Configuration: json.RawMessage(`{}`),
		ClusterID:     pgtype.UUID{Bytes: clusterA, Valid: true},
		Enabled:       true,
	})
	if err != nil {
		t.Fatal(err)
	}

	h, q := newStackLifecycleHandler(t)
	h.SetSystemLoggingOutputDisabler(lh)
	if err := h.updateSharedLokiMetadata(context.Background(), q.backend, SharedLokiRequest{
		ManagementClusterID: stackTestClusterID,
		Namespace:           "monitoring",
		ReleaseName:         sharedLokiDefaultRelease,
		IngestHostname:      "loki-ingest.example.com",
	}, "uninstalled"); err != nil {
		t.Fatalf("persist uninstall: %v", err)
	}

	systemCount := 0
	for _, o := range loggingQ.outputs {
		if o.IsSystem {
			systemCount++
			if o.Enabled {
				t.Fatalf("system output %s still enabled after Loki uninstall", o.ID)
			}
		}
	}
	if systemCount != 2 {
		t.Fatalf("system rows = %d, want 2 kept", systemCount)
	}
	if !loggingQ.outputs[byo.ID].Enabled {
		t.Fatal("BYO output was disabled")
	}
	if len(loggingQ.operations) != 2 {
		t.Fatalf("apply ops = %d, want 2 member ConfigMap refreshes", len(loggingQ.operations))
	}
	for _, op := range loggingQ.operations {
		if op.OperationType != "apply" {
			t.Fatalf("op type = %q, want apply", op.OperationType)
		}
		var env loggingOperationEnvelope
		if err := json.Unmarshal(op.Payload, &env); err != nil {
			t.Fatal(err)
		}
		if env.Enabled {
			t.Fatal("refresh envelope still enabled")
		}
		if strings.Contains(string(op.Payload), "bearer") {
			t.Fatalf("operation payload leaked bearer: %s", op.Payload)
		}
	}
}

func TestRenderOutputBlockSystemLokiBearerAndTLS(t *testing.T) {
	clusterID := uuid.New().String()
	cfg, _ := json.Marshal(map[string]any{
		"host":      "loki-ingest.example.com",
		"port":      "443",
		"tls":       "on",
		"tenant_id": "operator-supplied",
		"labels":    "cluster=" + clusterID + ",job=fluentbit",
	})
	out := renderOutputBlock(loggingOperationEnvelope{
		Name:          systemLoggingOutputName,
		OutputType:    "loki",
		Enabled:       true,
		IsSystem:      true,
		ClusterID:     clusterID,
		BearerToken:   "plaintext-bearer",
		Configuration: cfg,
	})
	for _, want := range []string{
		"[OUTPUT]",
		"Name loki",
		"Host loki-ingest.example.com",
		"Port 443",
		"tls on",
		"tls.verify on",
		"bearer_token plaintext-bearer",
		"tenant_id " + clusterID,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "operator-supplied") {
		t.Fatal("system renderer used operator tenant_id")
	}

	disabled := renderOutputBlock(loggingOperationEnvelope{
		Name:          systemLoggingOutputName,
		OutputType:    "loki",
		Enabled:       false,
		IsSystem:      true,
		ClusterID:     clusterID,
		BearerToken:   "plaintext-bearer",
		Configuration: cfg,
	})
	if strings.Contains(disabled, "[OUTPUT]") {
		t.Fatalf("disabled output still emitted [OUTPUT]:\n%s", disabled)
	}
	if strings.Contains(disabled, "plaintext-bearer") {
		t.Fatal("disabled output leaked bearer")
	}
}

func TestCreateOutputStripsBearerFromConfiguration(t *testing.T) {
	q := newLoggingFakeQuerier()
	h := NewLoggingHandler(q)
	clusterID := uuid.New()
	body, _ := json.Marshal(map[string]any{
		"name":        "byo-loki",
		"output_type": "loki",
		"cluster_id":  clusterID.String(),
		"enabled":     true,
		"configuration": map[string]any{
			"host":         "loki.example.com",
			"bearer_token": "should-not-store",
		},
	})
	rec := httptest.NewRecorder()
	h.CreateOutput(rec, httptest.NewRequest(http.MethodPost, "/api/v1/logging/outputs/", bytes.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "should-not-store") {
		t.Fatalf("create response leaked bearer: %s", rec.Body.String())
	}
	for _, o := range q.outputs {
		if bytes.Contains(o.Configuration, []byte("should-not-store")) || bytes.Contains(o.Configuration, []byte("bearer")) {
			t.Fatalf("stored configuration contains bearer: %s", o.Configuration)
		}
		if o.IsSystem {
			t.Fatal("public create must not set is_system")
		}
	}
}

func TestApplySystemLokiInjectsBearerFromFernet(t *testing.T) {
	q := newLoggingFakeQuerier()
	h := NewLoggingHandler(q)
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	h.SetEncryptor(enc)
	requester := &loggingFakeRequester{}
	h.SetK8sRequester(requester)

	clusterID := uuid.New()
	sealed, err := enc.Encrypt("rotate-me-token")
	if err != nil {
		t.Fatal(err)
	}
	q.lokiTokens[clusterID] = sqlc.LokiIngestToken{ClusterID: clusterID, TokenEncrypted: sealed}
	out, err := h.upsertSystemLoggingOutput(context.Background(), systemLoggingOutputSpec{
		ClusterID: clusterID, Host: "loki-ingest.example.com", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.enqueueOutputApply(context.Background(), out, pgtype.UUID{}); err != nil {
		t.Fatal(err)
	}
	h.processPendingOperations(context.Background())

	foundBearer := false
	for _, c := range requester.calls {
		if !strings.Contains(string(c.body), "rotate-me-token") {
			continue
		}
		foundBearer = true
		if strings.Contains(string(c.body), "Secret") {
			t.Fatal("apply wrote a Kubernetes Secret")
		}
	}
	if !foundBearer {
		t.Fatal("member ConfigMap missing apply-time bearer_token")
	}
	if bytes.Contains(q.outputs[out.ID].Configuration, []byte("rotate-me-token")) {
		t.Fatal("bearer stored in configuration JSONB")
	}
	for _, op := range q.operations {
		if bytes.Contains(op.Payload, []byte("rotate-me-token")) {
			t.Fatal("bearer stored in logging_operations payload")
		}
	}
}

package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestManagementLoggingValuesOverlayLokiMissingStaysOff(t *testing.T) {
	if got := managementLoggingValuesOverlay(managementLoggingOverlayInput{}); got != nil {
		t.Fatalf("empty input overlay = %#v, want nil (chart default stays off)", got)
	}
	if got := managementLoggingValuesOverlay(managementLoggingOverlayInput{
		Status: "not_configured", IngestPublic: false, Host: "loki.example", TenantID: uuid.NewString(),
	}); got != nil {
		t.Fatalf("Loki missing overlay = %#v, want nil", got)
	}
	if got := managementLoggingValuesOverlay(managementLoggingOverlayInput{
		Status: "healthy", IngestPublic: false, Host: "loki.example", TenantID: uuid.NewString(),
	}); got != nil {
		t.Fatalf("healthy but ingestPublic=false overlay = %#v, want nil", got)
	}
}

func TestManagementLoggingValuesOverlayHealthyPopulatesBackendWithoutToken(t *testing.T) {
	tenant := uuid.MustParse(stackTestClusterID)
	got := managementLoggingValuesOverlay(managementLoggingOverlayInput{
		Status:       "healthy",
		IngestPublic: true,
		Host:         "loki-ingest.example.com",
		Port:         "443",
		TenantID:     tenant.String(),
	})
	if got == nil {
		t.Fatal("healthy+public overlay is nil")
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if strings.Contains(strings.ToLower(body), "token") && !strings.Contains(body, managementLoggingSecretName) {
		t.Fatalf("overlay leaked a token field: %s", body)
	}
	if strings.Contains(body, "bearer_token") || strings.Contains(body, `"token":"`) {
		t.Fatalf("overlay contained a plaintext token: %s", body)
	}
	ml, _ := got["managementLogging"].(map[string]any)
	if ml == nil {
		t.Fatalf("missing managementLogging: %s", body)
	}
	if ml["enabled"] != true {
		t.Fatalf("enabled = %v, want true", ml["enabled"])
	}
	if ml["backend"] != "loki" {
		t.Fatalf("backend = %v, want loki", ml["backend"])
	}
	if ml["endpoint"] != "https://loki-ingest.example.com" {
		t.Fatalf("endpoint = %v", ml["endpoint"])
	}
	loki, _ := ml["loki"].(map[string]any)
	if loki["tenantID"] != tenant.String() {
		t.Fatalf("tenantID = %v, want local cluster", loki["tenantID"])
	}
}

func TestReconcileManagementLoggingLokiMissingLeavesChartDefaultOff(t *testing.T) {
	h, _ := newStackLifecycleHandler(t)
	helm := &loggingHelmStub{}
	h.helm = helm
	h.requester = &loggingFakeRequester{}
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	h.SetEncryptor(enc)

	if err := h.ReconcileManagementLogging(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(helm.payloads) != 0 {
		raw, _ := json.Marshal(helm.payloads)
		t.Fatalf("Loki missing still helm-upgraded astronomer: %s", raw)
	}
}

func TestReconcileManagementLoggingHealthyOverlaysEndpointWithoutPrintingToken(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	helm := &loggingHelmStub{}
	h.helm = helm
	fake := &loggingFakeRequester{}
	h.requester = fake
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	h.SetEncryptor(enc)

	q.lokiTokens = []sqlc.ListLokiIngestTokenHashesRow{{
		ClusterID: uuid.MustParse(stackTestClusterID),
		TokenHash: "pre-existing-member-hash",
	}}
	persistHealthyLoki(t, h, q)

	if err := h.ReconcileManagementLogging(context.Background()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(helm.payloads) != 1 {
		t.Fatalf("helm upgrades = %d, want 1", len(helm.payloads))
	}
	if helm.msgTypes[0] != protocol.MsgHelmUpgrade {
		t.Fatalf("helm msg = %s, want upgrade", helm.msgTypes[0])
	}
	p := helm.payloads[0]
	if !p.ReuseValues {
		t.Fatal("overlay must reuse astronomer chart values")
	}
	if p.ReleaseName != astronomerDefaultRelease {
		t.Fatalf("release = %q, want %q", p.ReleaseName, astronomerDefaultRelease)
	}
	rawVals, _ := json.Marshal(p.Values)
	vals := string(rawVals)
	if !strings.Contains(vals, `"backend":"loki"`) {
		t.Fatalf("backend not populated: %s", vals)
	}
	if !strings.Contains(vals, "https://loki-ingest.example.com") {
		t.Fatalf("endpoint not populated: %s", vals)
	}
	if !strings.Contains(vals, stackTestClusterID) {
		t.Fatalf("tenant (local cluster) missing: %s", vals)
	}

	tok, ok := q.ingestTokens[uuid.MustParse(stackTestClusterID)]
	if !ok || tok.TokenEncrypted == "" {
		t.Fatal("management ingest token was not stored")
	}
	plain, err := enc.Decrypt(tok.TokenEncrypted)
	if err != nil {
		t.Fatalf("decrypt stored token: %v", err)
	}
	if plain == "" {
		t.Fatal("stored token was empty")
	}
	if strings.Contains(vals, plain) {
		t.Fatal("helm values leaked ingest token")
	}

	foundSecret := false
	for _, c := range fake.calls {
		body := string(c.body)
		if strings.Contains(body, managementLoggingSecretName) || strings.Contains(c.path, managementLoggingSecretName) {
			foundSecret = true
			if !strings.Contains(body, `"kind":"Secret"`) && !strings.Contains(body, `"kind": "Secret"`) {
				t.Fatalf("token applied outside Secret: %s", c.path)
			}
		}
		if strings.Contains(c.path, "configmaps") && strings.Contains(body, plain) {
			t.Fatal("plaintext token written to a ConfigMap")
		}
	}
	if !foundSecret {
		t.Fatal("management ingest Secret was not applied")
	}

	if err := h.ReconcileManagementLogging(context.Background()); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if len(helm.payloads) != 1 {
		t.Fatalf("second reconcile re-upgraded astronomer: %d", len(helm.payloads))
	}
}

func TestReconcileManagementLoggingDisablesWhenLokiUninstalled(t *testing.T) {
	h, q := newStackLifecycleHandler(t)
	helm := &loggingHelmStub{}
	h.helm = helm
	h.requester = &loggingFakeRequester{}
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}
	h.SetEncryptor(enc)
	q.lokiTokens = []sqlc.ListLokiIngestTokenHashesRow{{
		ClusterID: uuid.MustParse(stackTestClusterID),
		TokenHash: "abc",
	}}
	persistHealthyLoki(t, h, q)
	if err := h.ReconcileManagementLogging(context.Background()); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := h.updateSharedLokiMetadata(context.Background(), q.backend, SharedLokiRequest{
		ManagementClusterID: stackTestClusterID,
		Namespace:           "monitoring",
		ReleaseName:         sharedLokiDefaultRelease,
		ChartVersion:        sharedLokiDefaultChart,
		StorageConfigID:     stackTestStorageID,
		IngestHostname:      "loki-ingest.example.com",
	}, "uninstalled"); err != nil {
		t.Fatalf("uninstall persist: %v", err)
	}
	if err := h.ReconcileManagementLogging(context.Background()); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if len(helm.payloads) != 2 {
		t.Fatalf("helm upgrades = %d, want 2 (enable then disable)", len(helm.payloads))
	}
	disable := helm.payloads[1].Values
	raw, _ := json.Marshal(disable)
	if !strings.Contains(string(raw), `"enabled":false`) {
		t.Fatalf("uninstall overlay did not disable managementLogging: %s", raw)
	}
}

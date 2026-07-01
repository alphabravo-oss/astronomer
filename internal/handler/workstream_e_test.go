package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// --- Finding 1: ArgoCD sync_window_override is a documented no-op ---

func TestSyncWindowOverrideNoteIsHonest(t *testing.T) {
	if note := syncWindowOverrideNote(false); note != "" {
		t.Fatalf("expected empty note when override not requested, got %q", note)
	}
	note := syncWindowOverrideNote(true)
	if note == "" {
		t.Fatal("expected a note when override requested")
	}
	low := strings.ToLower(note)
	if !strings.Contains(low, "does not bypass") {
		t.Errorf("note should state Astronomer does not bypass sync windows: %q", note)
	}
	// The note must never imply the deny window WAS overridden.
	for _, bad := range []string{"overridden", "window bypassed", "bypassed the"} {
		if strings.Contains(low, bad) {
			t.Errorf("note misleadingly implies the override happened (%q): %q", bad, note)
		}
	}
}

// --- Finding 2: private HTTP Helm repo index auth ---

func TestApplyRepoIndexAuth(t *testing.T) {
	// basic auth
	req := httptest.NewRequest(http.MethodGet, "https://charts.example.com/index.yaml", nil)
	applyRepoIndexAuth(req, sqlc.HelmRepository{AuthType: "basic", AuthConfig: json.RawMessage(`{"username":"u","password":"p"}`)})
	user, pass, ok := req.BasicAuth()
	if !ok || user != "u" || pass != "p" {
		t.Fatalf("basic auth not applied: ok=%v user=%q pass=%q", ok, user, pass)
	}

	// bearer token
	req2 := httptest.NewRequest(http.MethodGet, "https://charts.example.com/index.yaml", nil)
	applyRepoIndexAuth(req2, sqlc.HelmRepository{AuthType: "bearer", AuthConfig: json.RawMessage(`{"token":"tok"}`)})
	if got := req2.Header.Get("Authorization"); got != "Bearer tok" {
		t.Fatalf("bearer auth header=%q want %q", got, "Bearer tok")
	}

	// no auth type → request left unauthenticated even if creds present
	req3 := httptest.NewRequest(http.MethodGet, "https://charts.example.com/index.yaml", nil)
	applyRepoIndexAuth(req3, sqlc.HelmRepository{AuthType: "", AuthConfig: json.RawMessage(`{"username":"u"}`)})
	if got := req3.Header.Get("Authorization"); got != "" {
		t.Fatalf("expected no Authorization header, got %q", got)
	}
}

// --- Finding 6: distribution values are deep-merged, not concatenated ---

func TestMergeValueLayersPreservesSiblingKeys(t *testing.T) {
	dist := "securityContext:\n  privileged: true\n  runAsUser: 0\n"
	user := "securityContext:\n  fsGroup: 1000\n"
	merged := mergeValueLayers(dist, "", user)

	var m map[string]any
	if err := yaml.Unmarshal([]byte(merged), &m); err != nil {
		t.Fatalf("merged yaml invalid: %v\n%s", err, merged)
	}
	sc, _ := m["securityContext"].(map[string]any)
	if sc == nil {
		t.Fatalf("securityContext missing in merged output:\n%s", merged)
	}
	if sc["privileged"] != true {
		t.Errorf("distribution privileged=true dropped by merge: %#v", sc)
	}
	if _, ok := sc["runAsUser"]; !ok {
		t.Errorf("distribution runAsUser dropped by merge: %#v", sc)
	}
	if _, ok := sc["fsGroup"]; !ok {
		t.Errorf("operator fsGroup dropped by merge: %#v", sc)
	}
}

func TestMergeValueLayersOperatorWinsOnConflict(t *testing.T) {
	out := mergeValueLayers("replicaCount: 1\n", "replicaCount: 3\n")
	var m map[string]any
	if err := yaml.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("bad yaml: %v", err)
	}
	if fmt.Sprint(m["replicaCount"]) != "3" {
		t.Errorf("later layer should win on conflict, got %v", m["replicaCount"])
	}
}

func TestMergeValueLayersFallsBackForNonMap(t *testing.T) {
	// A layer that is not a YAML mapping must not panic; it falls back to
	// concatenation so no content is silently lost.
	out := mergeValueLayers("just a scalar", "key: val\n")
	if !strings.Contains(out, "just a scalar") || !strings.Contains(out, "key: val") {
		t.Errorf("fallback should concatenate layers, got %q", out)
	}
}

// --- Findings 3 & 4: vault markers resolved at execution time, never persisted ---

type captureHelmStub struct {
	lastValues map[string]any
}

func (s *captureHelmStub) Do(_ context.Context, _ string, _ protocol.MessageType, p protocol.HelmRequestPayload) (*protocol.HelmResultPayload, error) {
	s.lastValues = p.Values
	return &protocol.HelmResultPayload{Success: true, Status: "deployed", Revision: 1}, nil
}

func (s *captureHelmStub) Status(_ context.Context, _, _, _ string) (*protocol.HelmResultPayload, error) {
	return nil, errors.New("release: not found")
}

var _ HelmRequester = (*captureHelmStub)(nil)

func TestCatalogSendHelmResolvesVaultAtExecution(t *testing.T) {
	stub := &captureHelmStub{}
	h := NewCatalogHandlerWithHelm(nil, stub)

	// Plain values still pass through unchanged.
	if _, err := h.sendHelm(context.Background(), "cid", protocol.MsgHelmInstall, catalogOperationEnvelope{ValuesOverride: "replicaCount: 3\n"}); err != nil {
		t.Fatalf("sendHelm(plain): %v", err)
	}
	if stub.lastValues["replicaCount"] == nil {
		t.Fatalf("plain values not forwarded to helm: %#v", stub.lastValues)
	}

	// A ${vault://...} marker with no resolver configured must fail loudly at
	// execution time — proving resolution now happens here — rather than
	// silently shipping the literal placeholder to Helm.
	if _, err := h.sendHelm(context.Background(), "cid", protocol.MsgHelmInstall, catalogOperationEnvelope{ValuesOverride: "password: ${vault://prod/secret/db#password}\n"}); err == nil {
		t.Fatal("expected error resolving vault marker with no resolver configured")
	}
}

func TestToolSendHelmRawResolvesVaultAtExecution(t *testing.T) {
	stub := &captureHelmStub{}
	h := NewToolHandlerWithHelm(nil, stub)

	if _, err := h.sendHelmRaw(context.Background(), toolOperationEnvelope{ClusterID: "cid", ValuesYAML: "server:\n  insecure: true\n"}, protocol.MsgHelmInstall); err != nil {
		t.Fatalf("sendHelmRaw(plain): %v", err)
	}
	if _, ok := stub.lastValues["server"].(map[string]any); !ok {
		t.Fatalf("plain values not forwarded to helm: %#v", stub.lastValues)
	}

	if _, err := h.sendHelmRaw(context.Background(), toolOperationEnvelope{ClusterID: "cid", ValuesYAML: "password: ${vault://prod/secret/db#password}\n"}, protocol.MsgHelmInstall); err == nil {
		t.Fatal("expected error resolving vault marker with no resolver configured")
	}
}

func TestCatalogInstallKeepsVaultMarkerInPayload(t *testing.T) {
	q, clusterID, versionID := newInstalledCatalogAuditQuerier()
	h := NewCatalogHandler(q)

	const marker = "${vault://prod/secret/db#password}"
	body, _ := json.Marshal(map[string]any{
		"cluster_id":       clusterID.String(),
		"chart_version_id": versionID.String(),
		"release_name":     "nginx",
		"namespace":        "apps",
		"values_override":  "password: " + marker + "\n",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/installed/", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.CreateInstalledChart(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}

	if len(q.operations) != 1 {
		t.Fatalf("operations=%d want 1", len(q.operations))
	}
	var env catalogOperationEnvelope
	if err := json.Unmarshal(q.operations[0].Payload, &env); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if !strings.Contains(env.ValuesOverride, marker) {
		t.Errorf("operation payload should keep the vault marker (no plaintext secret persisted), got %q", env.ValuesOverride)
	}
	for _, inst := range q.installations {
		if !strings.Contains(inst.ValuesOverride, marker) {
			t.Errorf("installed_charts row should keep the vault marker, got %q", inst.ValuesOverride)
		}
	}
}

// --- Finding 5: rollback accepts an explicit target revision ---

func TestRollbackAcceptsTargetRevision(t *testing.T) {
	q, clusterID, _ := newInstalledCatalogAuditQuerier()
	h := NewCatalogHandler(q)

	instID := uuid.New()
	q.installations[instID] = sqlc.InstalledChart{
		ID: instID, ClusterID: clusterID, ReleaseName: "nginx", Namespace: "apps",
		Revision: 4, Status: "installed",
	}

	// Explicit target revision 1 — roll back further than a single step.
	body, _ := json.Marshal(map[string]any{"revision": 1})
	req := httptest.NewRequest(http.MethodPost, "/rollback", bytes.NewReader(body))
	req = withChiParams(req, map[string]string{"id": instID.String()})
	rec := httptest.NewRecorder()
	h.RollbackInstalledChart(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("rollback status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := lastRollbackRevision(t, q); got != 1 {
		t.Fatalf("explicit rollback revision=%d want 1", got)
	}

	// No body → default to current.Revision-1 = 3 (prior behaviour preserved).
	req2 := httptest.NewRequest(http.MethodPost, "/rollback", nil)
	req2 = withChiParams(req2, map[string]string{"id": instID.String()})
	rec2 := httptest.NewRecorder()
	h.RollbackInstalledChart(rec2, req2)
	if rec2.Code != http.StatusAccepted {
		t.Fatalf("default rollback status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	if got := lastRollbackRevision(t, q); got != 3 {
		t.Fatalf("default rollback revision=%d want 3", got)
	}
}

func lastRollbackRevision(t *testing.T, q *installedCatalogAuditQuerier) int {
	t.Helper()
	if len(q.operations) == 0 {
		t.Fatal("no operations enqueued")
	}
	var env catalogOperationEnvelope
	if err := json.Unmarshal(q.operations[len(q.operations)-1].Payload, &env); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return env.RollbackRevision
}

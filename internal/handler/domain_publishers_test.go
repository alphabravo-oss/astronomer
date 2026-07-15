package handler

// P4.5 per-publisher tests: every domain publisher must emit its
// `<resource>.changed` event after a successful DB write, carrying the
// cluster_id for cluster-scoped domains (SEC-R07 drops events without it
// fail-closed for restricted users, so a publisher forgetting it silently
// breaks liveness).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
)

// pubSubscribe subscribes to bus and returns the receive channel.
func pubSubscribe(t *testing.T, bus *events.Bus) <-chan events.Event {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return bus.Subscribe(ctx)
}

// pubReceive returns the next event of the wanted type, failing after 2s.
func pubReceive(t *testing.T, ch <-chan events.Event, want events.Type) events.Event {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Type == want {
				return e
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q", want)
			return events.Event{}
		}
	}
}

// pubPayload marshals the event data back into a map for assertions.
func pubPayload(t *testing.T, e events.Event) map[string]any {
	t.Helper()
	raw, err := json.Marshal(e.Data)
	if err != nil {
		t.Fatalf("marshal event data: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal event data: %v", err)
	}
	return m
}

func pubAssertCluster(t *testing.T, payload map[string]any, wantCluster string) {
	t.Helper()
	if payload["cluster_id"] != wantCluster {
		t.Fatalf("cluster_id = %v, want %q (SEC-R07 drops events without it)", payload["cluster_id"], wantCluster)
	}
}

func TestPublisher_BackupDeleteEmitsChanged(t *testing.T) {
	q := newBackupAuditQuerier()
	clusterID := uuid.New()
	backup := sqlc.Backup{
		ID:        uuid.New(),
		Name:      "nightly",
		ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true},
	}
	q.backups[backup.ID] = backup

	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h := NewBackupHandler(q)
	h.SetEventBus(bus)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/backups/"+backup.ID.String()+"/", nil)
	req = withChiParam(req, "id", backup.ID.String())
	rec := httptest.NewRecorder()
	h.DeleteBackup(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", rec.Code, rec.Body.String())
	}

	e := pubReceive(t, ch, events.TypeBackupChanged)
	payload := pubPayload(t, e)
	pubAssertCluster(t, payload, clusterID.String())
	if payload["id"] != backup.ID.String() {
		t.Fatalf("id = %v, want %q", payload["id"], backup.ID.String())
	}
	if payload["kind"] != "backup" {
		t.Fatalf("kind = %v, want backup", payload["kind"])
	}
}

func TestPublisher_FleetOperationPauseFansOutPerTargetCluster(t *testing.T) {
	q := newFakeFleetOperationQuerier()
	opID := uuid.New()
	q.operations[opID] = sqlc.FleetOperation{ID: opID, Name: "drain", Status: "running"}
	clusterA := uuid.New()
	clusterB := uuid.New()
	q.targets[opID] = map[uuid.UUID]sqlc.FleetOperationTarget{}
	for _, cid := range []uuid.UUID{clusterA, clusterA, clusterB} {
		tid := uuid.New()
		q.targets[opID][tid] = sqlc.FleetOperationTarget{ID: tid, OperationID: opID, ClusterID: cid, Status: "running"}
	}

	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h := NewFleetOperationHandler(q)
	h.SetEventBus(bus)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/fleet-operations/"+opID.String()+"/pause/", nil)
	req = withChiParam(req, "id", opID.String())
	rec := httptest.NewRecorder()
	h.Pause(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("pause status=%d body=%s", rec.Code, rec.Body.String())
	}

	// One event per DISTINCT target cluster (duplicate clusterA coalesced).
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		e := pubReceive(t, ch, events.TypeFleetOperationChanged)
		payload := pubPayload(t, e)
		cid, _ := payload["cluster_id"].(string)
		if payload["id"] != opID.String() {
			t.Fatalf("id = %v, want operation id %q", payload["id"], opID.String())
		}
		seen[cid] = true
	}
	if !seen[clusterA.String()] || !seen[clusterB.String()] {
		t.Fatalf("expected one event per distinct target cluster, got %v", seen)
	}
	select {
	case e := <-ch:
		if e.Type == events.TypeFleetOperationChanged {
			t.Fatalf("duplicate cluster events must be coalesced, got extra %v", e.Data)
		}
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPublisher_TemplateBindingDetachEmitsChanged(t *testing.T) {
	q := newFakeClusterTemplateQuerier()
	clusterID := uuid.New()
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "member-1"}

	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h := NewClusterTemplateHandler(q)
	h.SetEventBus(bus)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/clusters/"+clusterID.String()+"/template/", nil)
	req = withChiParam(req, "cluster_id", clusterID.String())
	rec := httptest.NewRecorder()
	h.Detach(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("detach status=%d body=%s", rec.Code, rec.Body.String())
	}

	payload := pubPayload(t, pubReceive(t, ch, events.TypeTemplateBindingChanged))
	pubAssertCluster(t, payload, clusterID.String())
	if payload["status"] != "detached" {
		t.Fatalf("status = %v, want detached", payload["status"])
	}
}

func TestPublisher_SIEMForwarderCreateUnscoped(t *testing.T) {
	superID := uuid.New()
	q := newFakeSIEMQuerier(sqlc.User{ID: superID, IsSuperuser: true})
	h := newSIEMTestHandler(t, q)
	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h.SetEventBus(bus)

	body, _ := json.Marshal(map[string]any{
		"name":      "splunk-live",
		"transport": "splunk_hec",
		"endpoint":  "https://splunk.example.com:8088",
		"auth":      `{"token":"secret-token"}`,
	})
	rec := httptest.NewRecorder()
	h.Create(rec, authedSIEMRequest(http.MethodPost, "/api/v1/admin/siem-forwarders/", superID, body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}

	payload := pubPayload(t, pubReceive(t, ch, events.TypeSIEMForwarderChanged))
	if _, ok := payload["cluster_id"]; ok {
		t.Fatalf("siem_forwarder.changed must be unscoped (superuser-only via fail-closed drop), got cluster_id %v", payload["cluster_id"])
	}
	if payload["id"] == "" || payload["id"] == nil {
		t.Fatal("expected forwarder id in payload")
	}
}

func TestPublisher_ToolOperationCarriesEnvelopeClusterID(t *testing.T) {
	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h := &ToolHandler{}
	h.SetEventBus(bus)

	clusterID := uuid.New()
	payloadRaw, _ := json.Marshal(toolOperationEnvelope{ClusterID: clusterID.String(), ToolSlug: "velero"})
	op := sqlc.ToolOperation{ID: uuid.New(), Status: "running", Payload: payloadRaw}
	h.publishToolOperationChanged(op)

	payload := pubPayload(t, pubReceive(t, ch, events.TypeToolOperationChanged))
	pubAssertCluster(t, payload, clusterID.String())
	if payload["status"] != "running" {
		t.Fatalf("status = %v, want running", payload["status"])
	}
}

func TestPublisher_LoggingOperationCarriesEnvelopeClusterID(t *testing.T) {
	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h := &LoggingHandler{}
	h.SetEventBus(bus)

	clusterID := uuid.New()
	payloadRaw, _ := json.Marshal(loggingOperationEnvelope{ClusterID: clusterID.String()})
	op := sqlc.LoggingOperation{ID: uuid.New(), Status: "completed", Payload: payloadRaw}
	h.publishLoggingOperationChanged(op)

	payload := pubPayload(t, pubReceive(t, ch, events.TypeLoggingOperationChanged))
	pubAssertCluster(t, payload, clusterID.String())
	if payload["status"] != "completed" {
		t.Fatalf("status = %v, want completed", payload["status"])
	}
}

func TestPublisher_ArgoCDChangedCarriesScopeAndCluster(t *testing.T) {
	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h := &ArgoCDHandler{}
	h.SetEventBus(bus)

	clusterID := uuid.New()
	entityID := uuid.New().String()
	h.publishArgoCDChanged(clusterID, entityID, "instance")

	payload := pubPayload(t, pubReceive(t, ch, events.TypeArgoCDChanged))
	pubAssertCluster(t, payload, clusterID.String())
	if payload["scope"] != "instance" {
		t.Fatalf("scope = %v, want instance", payload["scope"])
	}
	if payload["id"] != entityID {
		t.Fatalf("id = %v, want %q", payload["id"], entityID)
	}
}

func TestPublisher_CISScanCarriesCluster(t *testing.T) {
	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h := &SecurityHandler{}
	h.SetEventBus(bus)

	clusterID := uuid.New()
	scanID := uuid.New()
	h.publishCISScanChanged(clusterID, scanID)

	payload := pubPayload(t, pubReceive(t, ch, events.TypeCISScanChanged))
	pubAssertCluster(t, payload, clusterID.String())
	if payload["id"] != scanID.String() {
		t.Fatalf("id = %v, want %q", payload["id"], scanID.String())
	}
}

func TestPublisher_AgentFleetCarriesCluster(t *testing.T) {
	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h := &AgentFleetHandler{}
	h.SetEventBus(bus)

	clusterID := uuid.New()
	opID := uuid.New().String()
	h.publishAgentFleetChanged(clusterID, opID)

	payload := pubPayload(t, pubReceive(t, ch, events.TypeAgentFleetChanged))
	pubAssertCluster(t, payload, clusterID.String())
	if payload["kind"] != "lifecycle_operation" {
		t.Fatalf("kind = %v, want lifecycle_operation", payload["kind"])
	}
}

func TestPublisher_SnapshotScheduleCarriesCluster(t *testing.T) {
	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h := NewClusterSnapshotsHandler(nil)
	h.SetEventBus(bus)

	clusterID := uuid.New()
	schedID := uuid.New()
	h.publishSnapshotChanged(clusterID, schedID, "schedule")

	payload := pubPayload(t, pubReceive(t, ch, events.TypeSnapshotChanged))
	pubAssertCluster(t, payload, clusterID.String())
	if payload["kind"] != "schedule" {
		t.Fatalf("kind = %v, want schedule", payload["kind"])
	}
}

// Publishers are wired lazily and must be nil-safe: a handler without a bus
// (tests, partial wiring) publishing is a no-op, never a panic.
func TestPublisher_NilBusIsNoOp(t *testing.T) {
	(&ToolHandler{}).publishToolOperationChanged(sqlc.ToolOperation{ID: uuid.New()})
	(&LoggingHandler{}).publishLoggingOperationChanged(sqlc.LoggingOperation{ID: uuid.New()})
	(&ArgoCDHandler{}).publishArgoCDChanged(uuid.New(), "x", "instance")
	(&SecurityHandler{}).publishCISScanChanged(uuid.New(), uuid.New())
	(&AgentFleetHandler{}).publishAgentFleetChanged(uuid.New(), "x")
	(&BackupHandler{}).publishBackupChanged(pgtype.UUID{}, uuid.New(), "backup")
	(&ClusterSnapshotsHandler{}).publishSnapshotChanged(uuid.New(), uuid.New(), "snapshot")
	(&ClusterTemplateHandler{}).publishTemplateBindingChanged(uuid.New(), "pending")
	// P4.9 publishers.
	(&AlertingHandler{}).publishAlertingChanged("rule", uuid.New().String(), uuid.New())
	(&SecurityHandler{}).publishSecurityPolicyChanged(uuid.New(), uuid.New())
	(&ApiserverAllowlistHandler{}).publishNetworkAccessChanged(uuid.New())
	(&CatalogHandler{}).publishCatalogReleaseChanged(uuid.New().String(), uuid.New().String())
}

// ── P4.9 coverage-completion publishers ─────────────────────────────────────

func TestPublisher_AlertingChangedCarriesKindAndCluster(t *testing.T) {
	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h := &AlertingHandler{}
	h.SetEventBus(bus)

	clusterID := uuid.New()
	ruleID := uuid.New()
	h.publishAlertingChanged("rule", clusterID.String(), ruleID)

	payload := pubPayload(t, pubReceive(t, ch, events.TypeAlertingChanged))
	pubAssertCluster(t, payload, clusterID.String())
	if payload["kind"] != "rule" {
		t.Fatalf("kind = %v, want rule", payload["kind"])
	}
	if payload["id"] != ruleID.String() {
		t.Fatalf("id = %v, want %q", payload["id"], ruleID.String())
	}
}

func TestPublisher_AlertingChangedGlobalEntityIsUnscoped(t *testing.T) {
	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h := &AlertingHandler{}
	h.SetEventBus(bus)

	// A global rule has no cluster: the event publishes unscoped and is
	// superuser-only via the SEC-R07 fail-closed drop.
	h.publishAlertingChanged("silence", "", uuid.New())

	payload := pubPayload(t, pubReceive(t, ch, events.TypeAlertingChanged))
	if _, ok := payload["cluster_id"]; ok {
		t.Fatalf("global alerting entity must publish unscoped, got cluster_id %v", payload["cluster_id"])
	}
	if payload["kind"] != "silence" {
		t.Fatalf("kind = %v, want silence", payload["kind"])
	}
}

func TestPublisher_SecurityPolicyChangedCarriesCluster(t *testing.T) {
	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h := &SecurityHandler{}
	h.SetEventBus(bus)

	clusterID := uuid.New()
	policyID := uuid.New()
	h.publishSecurityPolicyChanged(clusterID, policyID)

	payload := pubPayload(t, pubReceive(t, ch, events.TypeSecurityPolicyChanged))
	pubAssertCluster(t, payload, clusterID.String())
	if payload["id"] != policyID.String() {
		t.Fatalf("id = %v, want %q", payload["id"], policyID.String())
	}
}

func TestPublisher_SecurityPolicyChangedNilClusterIsUnscoped(t *testing.T) {
	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h := &SecurityHandler{}
	h.SetEventBus(bus)

	h.publishSecurityPolicyChanged(uuid.Nil, uuid.New())

	payload := pubPayload(t, pubReceive(t, ch, events.TypeSecurityPolicyChanged))
	if _, ok := payload["cluster_id"]; ok {
		t.Fatalf("Nil cluster must publish unscoped, not a zero UUID, got %v", payload["cluster_id"])
	}
}

// Every security_scan_results write must emit BOTH cis_scan.changed (CIS
// pages) and security_scan.changed (generic scans list) — same table, two
// read surfaces.
func TestPublisher_ScanWriteEmitsCISAndGenericTypes(t *testing.T) {
	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h := &SecurityHandler{}
	h.SetEventBus(bus)

	clusterID := uuid.New()
	scanID := uuid.New()
	h.publishCISScanChanged(clusterID, scanID)

	for _, want := range []events.Type{events.TypeCISScanChanged, events.TypeSecurityScanChanged} {
		payload := pubPayload(t, pubReceive(t, ch, want))
		pubAssertCluster(t, payload, clusterID.String())
		if payload["id"] != scanID.String() {
			t.Fatalf("%s id = %v, want %q", want, payload["id"], scanID.String())
		}
	}
}

func TestPublisher_NetworkAccessChangedOnAllowlistPut(t *testing.T) {
	clusterID := uuid.New()
	q := &fakeAllowlistQuerier{}

	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h := NewApiserverAllowlistHandler(q)
	h.SetEventBus(bus)

	body, _ := json.Marshal(AllowlistUpdateRequest{
		CIDRs: []string{"10.0.0.0/24"},
		Mode:  "monitor",
	})
	req := requestWithChiParams(t, http.MethodPut, "/api/v1/clusters/"+clusterID.String()+"/apiserver-allowlist/", body, map[string]string{"cluster_id": clusterID.String()})
	rec := httptest.NewRecorder()
	h.Update(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rec.Code, rec.Body.String())
	}

	payload := pubPayload(t, pubReceive(t, ch, events.TypeNetworkAccessChanged))
	pubAssertCluster(t, payload, clusterID.String())
}

func TestPublisher_CatalogReleaseChangedCarriesCluster(t *testing.T) {
	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h := &CatalogHandler{}
	h.SetEventBus(bus)

	clusterID := uuid.New()
	installationID := uuid.New()
	h.publishCatalogReleaseChanged(clusterID.String(), installationID.String())

	payload := pubPayload(t, pubReceive(t, ch, events.TypeCatalogReleaseChanged))
	pubAssertCluster(t, payload, clusterID.String())
	if payload["id"] != installationID.String() {
		t.Fatalf("id = %v, want %q", payload["id"], installationID.String())
	}
}

func TestPublisher_RegistryCreateEmitsChanged(t *testing.T) {
	clusterID := uuid.New()
	q := newFakeRegistryQuerier(clusterID)
	h := NewClusterRegistriesHandler(q)
	bus := events.NewBus()
	ch := pubSubscribe(t, bus)
	h.SetEventBus(bus)

	body, _ := json.Marshal(ClusterRegistryRequest{
		PrivateRegistryUrl: "https://registry.example.com",
		RegistryUsername:   "alice",
		RegistryPassword:   "s3cr3t",
		Namespaces:         []string{"default"},
	})
	req := requestWithChiParams(t, http.MethodPost, "/api/v1/clusters/"+clusterID.String()+"/registries/", body, map[string]string{"cluster_id": clusterID.String()})
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}

	payload := pubPayload(t, pubReceive(t, ch, events.TypeRegistryChanged))
	pubAssertCluster(t, payload, clusterID.String())
	if payload["id"] == nil || payload["id"] == "" {
		t.Fatal("expected registry id in payload")
	}
}

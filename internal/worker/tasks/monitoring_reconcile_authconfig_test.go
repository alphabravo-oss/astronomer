package tasks

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	imonitoring "github.com/alphabravocompany/astronomer-go/internal/monitoring"
)

const monitoringReconcileToken = "s3cr3t-thanos-token"

type monitoringReconcileQuerier struct {
	RuntimeQuerier
	upserts []sqlc.UpsertDefaultMonitoringBackendParams
}

func (q *monitoringReconcileQuerier) UpsertDefaultMonitoringBackend(_ context.Context, arg sqlc.UpsertDefaultMonitoringBackendParams) (sqlc.MonitoringBackend, error) {
	q.upserts = append(q.upserts, arg)
	return sqlc.MonitoringBackend{
		ID:                  uuid.New(),
		AuthConfig:          arg.AuthConfig,
		AuthConfigEncrypted: arg.AuthConfigEncrypted,
	}, nil
}

func newReconcileTestEncryptor(t *testing.T) *auth.Encryptor {
	t.Helper()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	return enc
}

func sealedReconcileBackend(t *testing.T, enc *auth.Encryptor, doc string) sqlc.MonitoringBackend {
	t.Helper()
	ciphertext, public, err := imonitoring.SealAuthConfig(json.RawMessage(doc), enc)
	if err != nil {
		t.Fatalf("SealAuthConfig: %v", err)
	}
	if ciphertext == "" {
		t.Fatalf("fixture %s carries nothing sealable", doc)
	}
	return sqlc.MonitoringBackend{
		ID:                  uuid.New(),
		Name:                "default",
		BackendType:         "thanos",
		AuthType:            "bearer",
		AuthConfig:          public,
		AuthConfigEncrypted: ciphertext,
	}
}

// RMW SITE 5 — reconcileMonitoringBackend (the monitoring:reconcile tick).
//
// This is the most dangerous of the five because it is unattended and runs on
// a timer: it stamps sharedThanos.status/updatedAt and writes the document
// back. Pre-fix it did decodeJSONMapLocal(backend.AuthConfig) → mutate →
// marshal, which on a sealed row re-marshals the STRIPPED projection. The
// first tick after upgrade would have deleted the credential, and the second
// tick would have reported the backend degraded — with the reconciler itself
// as both the cause and the only witness.
//
// QueryUrl is deliberately empty so the tick takes the "not_configured" path
// and makes no outbound request; the write is what is under test.
func TestReconcileMonitoringBackendPreservesCredential(t *testing.T) {
	defer resetRuntime()
	enc := newReconcileTestEncryptor(t)
	backend := sealedReconcileBackend(t, enc,
		`{"token":"`+monitoringReconcileToken+`","sharedThanos":{"namespace":"monitoring","status":"healthy"}}`)
	q := &monitoringReconcileQuerier{}
	ConfigureRuntime(RuntimeDependencies{Queries: q, MonitoringCipher: MonitoringCipherFor(enc)})

	if _, _, _, err := reconcileMonitoringBackend(context.Background(), backend); err != nil {
		t.Fatalf("reconcileMonitoringBackend: %v", err)
	}
	if len(q.upserts) != 1 {
		t.Fatalf("expected the status stamp to write once, got %d writes", len(q.upserts))
	}
	params := q.upserts[0]
	if params.AuthConfigEncrypted == "" {
		t.Fatalf("the reconcile tick wrote an empty envelope: the credential is gone (auth_config=%s)", params.AuthConfig)
	}
	if strings.Contains(string(params.AuthConfig), monitoringReconcileToken) {
		t.Fatalf("the reconcile tick put the credential back in the clear: %s", params.AuthConfig)
	}
	full, err := imonitoring.ResolveAuthConfig(params.AuthConfigEncrypted, params.AuthConfig, enc)
	if err != nil {
		t.Fatalf("ResolveAuthConfig on the written row: %v", err)
	}
	doc := imonitoring.DecodeAuthConfig(full)
	if doc["token"] != monitoringReconcileToken {
		t.Fatalf("the credential did not survive the status stamp: %v", doc)
	}
	shared, _ := doc["sharedThanos"].(map[string]any)
	if shared["status"] != "not_configured" {
		t.Fatalf("the status stamp this tick exists to make was lost: %v", doc)
	}
}

// A tick that cannot decrypt must skip THE WRITE and nothing else.
//
// Two halves, and they pull in opposite directions:
//
// Refusing the write is mandatory. Stamping the status from a document the
// tick could not read persists "this backend has no credential" and converts a
// recoverable key problem into permanent loss, on a 30-second timer, with
// nobody watching.
//
// Refusing the whole TICK is over-reach, and an earlier version of this change
// did exactly that: reconcileMonitoringBackend returned the resolve error and
// HandleMonitoringReconcile propagated it before listAllClustersPaged, so every
// per-cluster monitoring reconcile — none of which needs the monitoring
// credential at all — stopped converging for as long as the key was wrong. One
// unreadable row froze the whole fleet's monitoring config. So the error is not
// returned: it is logged, the backend is reported UNHEALTHY (a backend we
// cannot authenticate to is not healthy, however the unauthenticated probe went)
// and the caller carries on to the cluster fan-out.
func TestReconcileMonitoringBackendSkipsOnlyTheWriteWhenCredentialCannotBeDecrypted(t *testing.T) {
	defer resetRuntime()
	sealingEnc := newReconcileTestEncryptor(t)
	backend := sealedReconcileBackend(t, sealingEnc,
		`{"token":"`+monitoringReconcileToken+`","sharedThanos":{"namespace":"monitoring"}}`)
	q := &monitoringReconcileQuerier{}
	// A different key: the rotated-too-early case.
	ConfigureRuntime(RuntimeDependencies{Queries: q, MonitoringCipher: MonitoringCipherFor(newReconcileTestEncryptor(t))})

	got, _, healthy, err := reconcileMonitoringBackend(context.Background(), backend)
	if err != nil {
		t.Fatalf("an unreadable credential aborted the whole tick (%v); unrelated per-cluster reconciliation must still run", err)
	}
	if len(q.upserts) != 0 {
		t.Fatalf("wrote %d row(s) despite being unable to read the credential", len(q.upserts))
	}
	if healthy {
		t.Fatal("reported the backend healthy despite being unable to authenticate to it")
	}
	// The row handed to the cluster fan-out is the one read from the database,
	// untouched — the fan-out only needs its ID.
	if got.AuthConfigEncrypted != backend.AuthConfigEncrypted {
		t.Fatalf("the backend row was mutated by a tick that could not read it")
	}
}

// The whole-tick version of the same rule: HandleMonitoringReconcile's
// per-cluster work must not be gated on the monitoring credential. This pins
// the blast radius at the level the regression would actually be felt.
func TestMonitoringReconcileTickReconcilesClustersWhenTheCredentialIsUnreadable(t *testing.T) {
	defer resetRuntime()
	sealingEnc := newReconcileTestEncryptor(t)
	backend := sealedReconcileBackend(t, sealingEnc,
		`{"token":"`+monitoringReconcileToken+`","sharedThanos":{"namespace":"monitoring"}}`)
	// A query URL so the tick builds a client and reaches the fan-out.
	backend.QueryUrl = "http://127.0.0.1:1/"
	q := &monitoringReconcileTickQuerier{
		monitoringReconcileQuerier: monitoringReconcileQuerier{},
		backend:                    backend,
		clusters:                   []sqlc.Cluster{{ID: uuid.New(), Name: "c1"}},
	}
	ConfigureRuntime(RuntimeDependencies{Queries: q, MonitoringCipher: MonitoringCipherFor(newReconcileTestEncryptor(t))})

	_, client, healthy, err := reconcileMonitoringBackend(context.Background(), q.backend)
	if err != nil {
		t.Fatalf("reconcileMonitoringBackend: %v", err)
	}
	if client == nil {
		t.Fatal("no client was built, so HandleMonitoringReconcile would return before the cluster fan-out")
	}
	if healthy {
		t.Fatal("reported healthy despite an unreadable credential")
	}
	if err := reconcileClusterMonitoring(context.Background(), client, q.clusters[0], q.backend, healthy); err != nil {
		t.Fatalf("per-cluster reconcile failed: %v", err)
	}
	if len(q.clusterUpserts) != 1 {
		t.Fatalf("the cluster monitoring config did not converge: %d writes", len(q.clusterUpserts))
	}
	if q.clusterUpserts[0].Status != "degraded" {
		t.Fatalf("cluster status = %q, want \"degraded\"", q.clusterUpserts[0].Status)
	}
}

// monitoringReconcileTickQuerier adds the cluster-side reads/writes the tick
// makes after the backend step.
type monitoringReconcileTickQuerier struct {
	monitoringReconcileQuerier
	backend        sqlc.MonitoringBackend
	clusters       []sqlc.Cluster
	clusterUpserts []sqlc.UpsertClusterMonitoringConfigParams
}

func (q *monitoringReconcileTickQuerier) GetClusterMonitoringConfig(_ context.Context, clusterID uuid.UUID) (sqlc.ClusterMonitoringConfig, error) {
	return sqlc.ClusterMonitoringConfig{ClusterID: clusterID, Status: "healthy"}, nil
}

func (q *monitoringReconcileTickQuerier) UpsertClusterMonitoringConfig(_ context.Context, arg sqlc.UpsertClusterMonitoringConfigParams) (sqlc.ClusterMonitoringConfig, error) {
	q.clusterUpserts = append(q.clusterUpserts, arg)
	return sqlc.ClusterMonitoringConfig{ClusterID: arg.ClusterID, Status: arg.Status}, nil
}

// A pre-146 row (empty envelope, credential inline) must keep reconciling, and
// the tick is what seals it going forward.
func TestReconcileMonitoringBackendSealsAPreMigrationRow(t *testing.T) {
	defer resetRuntime()
	enc := newReconcileTestEncryptor(t)
	q := &monitoringReconcileQuerier{}
	ConfigureRuntime(RuntimeDependencies{Queries: q, MonitoringCipher: MonitoringCipherFor(enc)})

	backend := sqlc.MonitoringBackend{
		ID:         uuid.New(),
		AuthType:   "bearer",
		AuthConfig: json.RawMessage(`{"token":"` + monitoringReconcileToken + `","sharedThanos":{"namespace":"monitoring"}}`),
	}
	if _, _, _, err := reconcileMonitoringBackend(context.Background(), backend); err != nil {
		t.Fatalf("reconcileMonitoringBackend: %v", err)
	}
	params := q.upserts[0]
	if params.AuthConfigEncrypted == "" {
		t.Fatal("the tick rewrote a legacy row without sealing it")
	}
	if strings.Contains(string(params.AuthConfig), monitoringReconcileToken) {
		t.Fatalf("the legacy credential is still in the clear after the rewrite: %s", params.AuthConfig)
	}
}

package status

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/compatibility"
	"github.com/alphabravocompany/astronomer-go/internal/redaction"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

var (
	clusterID    = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	connectionID = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	deploymentID = uuid.MustParse("33333333-3333-4333-8333-333333333333")
)

type fakeRunner struct {
	tx     Transaction
	called bool
}

type fakeReadyReconciler struct {
	clusterID uuid.UUID
	err       error
}

func (f *fakeReadyReconciler) Reconcile(_ context.Context, clusterID uuid.UUID) error {
	f.clusterID = clusterID
	return f.err
}

type systemFakeTransaction struct {
	*fakeTransaction
	observeArg  *sqlc.ObserveDeliverySystemAssignmentParams
	observed    sqlc.ObserveDeliverySystemAssignmentRow
	systemEvent *sqlc.CreateDeliverySystemEventParams
}

func (f *systemFakeTransaction) ObserveDeliverySystemAssignment(_ context.Context, arg sqlc.ObserveDeliverySystemAssignmentParams) (sqlc.ObserveDeliverySystemAssignmentRow, error) {
	f.observeArg = &arg
	return f.observed, nil
}

func (f *systemFakeTransaction) CreateDeliverySystemEvent(_ context.Context, arg sqlc.CreateDeliverySystemEventParams) (sqlc.DeliverySystemEvent, error) {
	f.systemEvent = &arg
	return sqlc.DeliverySystemEvent{}, nil
}

func (r *fakeRunner) Run(ctx context.Context, work func(Transaction) error) error {
	r.called = true
	return work(r.tx)
}

type fakeTransaction struct {
	fenceErr     error
	current      sqlc.ClusterDeployment
	updated      *sqlc.UpdateClusterDeploymentObservedCASParams
	event        *sqlc.CreateClusterDeploymentEventParams
	advance      *sqlc.AdvanceDeliveryRolloutClusterFromStatusParams
	advanced     sqlc.AdvanceDeliveryRolloutClusterFromStatusRow
	rolloutEvent *sqlc.CreateDeliveryRolloutEventParams
	outbox       *sqlc.UpsertTaskOutboxParams
	ack          *sqlc.AcknowledgeDeliveryAssignmentSnapshotParams
	compat       string
	code         string
	finalized    uuid.UUID
}

func (f *fakeTransaction) FenceDeliveryAgentSession(context.Context, sqlc.FenceDeliveryAgentSessionParams) (uuid.UUID, error) {
	return connectionID, f.fenceErr
}

func (f *fakeTransaction) GetClusterDeploymentForDeliveryStatus(context.Context, sqlc.GetClusterDeploymentForDeliveryStatusParams) (sqlc.ClusterDeployment, error) {
	return f.current, nil
}

func (f *fakeTransaction) UpdateClusterDeploymentObservedCAS(_ context.Context, arg sqlc.UpdateClusterDeploymentObservedCASParams) (sqlc.ClusterDeployment, error) {
	f.updated = &arg
	result := f.current
	result.Phase = arg.Phase
	result.LastErrorCode = arg.LastErrorCode
	return result, nil
}

func (f *fakeTransaction) CreateClusterDeploymentEvent(_ context.Context, arg sqlc.CreateClusterDeploymentEventParams) (sqlc.ClusterDeploymentEvent, error) {
	f.event = &arg
	return sqlc.ClusterDeploymentEvent{}, nil
}

func (f *fakeTransaction) AdvanceDeliveryRolloutClusterFromStatus(_ context.Context, arg sqlc.AdvanceDeliveryRolloutClusterFromStatusParams) (sqlc.AdvanceDeliveryRolloutClusterFromStatusRow, error) {
	f.advance = &arg
	if f.advanced.RolloutID == uuid.Nil {
		return sqlc.AdvanceDeliveryRolloutClusterFromStatusRow{}, pgx.ErrNoRows
	}
	return f.advanced, nil
}

func (f *fakeTransaction) CreateDeliveryRolloutEvent(_ context.Context, arg sqlc.CreateDeliveryRolloutEventParams) (sqlc.DeliveryRolloutEvent, error) {
	f.rolloutEvent = &arg
	return sqlc.DeliveryRolloutEvent{}, nil
}

func (f *fakeTransaction) UpsertTaskOutbox(_ context.Context, arg sqlc.UpsertTaskOutboxParams) (sqlc.TaskOutbox, error) {
	f.outbox = &arg
	return sqlc.TaskOutbox{}, nil
}

func (f *fakeTransaction) UpsertDeliveryControllerInventory(_ context.Context, arg sqlc.UpsertDeliveryControllerInventoryParams) (sqlc.DeliveryControllerInventory, error) {
	f.compat = arg.CompatibilityStatus
	f.code = arg.ErrorCode
	return sqlc.DeliveryControllerInventory{}, nil
}

func (f *fakeTransaction) FinalizeDeliveryTargetDeletionIfComplete(_ context.Context, targetID uuid.UUID) (sqlc.DeliveryTarget, error) {
	f.finalized = targetID
	return sqlc.DeliveryTarget{}, nil
}

func (f *fakeTransaction) AcknowledgeDeliveryAssignmentSnapshot(_ context.Context, arg sqlc.AcknowledgeDeliveryAssignmentSnapshotParams) (sqlc.DeliveryAssignmentReceipt, error) {
	f.ack = &arg
	return sqlc.DeliveryAssignmentReceipt{}, nil
}

func TestIngestPersistsFencedSanitizedStatusAtomically(t *testing.T) {
	tx := &fakeTransaction{current: sqlc.ClusterDeployment{
		ID: deploymentID, ClusterID: clusterID, DesiredGeneration: 7,
		DesiredSpecDigest: "sha256:" + strings.Repeat("a", 64), Phase: "applying",
	}}
	runner := &fakeRunner{tx: tx}
	ingester := NewIngester(runner)
	payload := validStatus()
	payload.Deployments[0].Message = "authorization=top-secret"
	payload.Deployments[0].Conditions[0].Message = "bearer abc123"

	if err := ingester.Ingest(context.Background(), clusterID, connectionID, "session-new", payload); err != nil {
		t.Fatal(err)
	}
	if !runner.called || tx.updated == nil || tx.event == nil || tx.ack == nil {
		t.Fatalf("missing atomic writes: runner=%v update=%v event=%v ack=%v", runner.called, tx.updated, tx.event, tx.ack)
	}
	if strings.Contains(tx.updated.LastMessage, "top-secret") || !strings.Contains(tx.updated.LastMessage, redaction.Marker) {
		t.Fatalf("status message was not redacted: %q", tx.updated.LastMessage)
	}
	if strings.Contains(string(tx.updated.Conditions), "abc123") {
		t.Fatalf("condition leaked credential: %s", tx.updated.Conditions)
	}
	if tx.updated.AgentSessionID != "session-new" || tx.updated.AgentSequence != payload.SessionSequence {
		t.Fatalf("session fence missing from update: %#v", tx.updated)
	}
	if tx.compat != string(compatibility.Compatible) || tx.code != "" {
		t.Fatalf("compatibility = %s/%s", tx.compat, tx.code)
	}
}

func TestIngestTriggersPostCommitReadyReconciliationWithoutRejectingStatus(t *testing.T) {
	tx := &fakeTransaction{current: sqlc.ClusterDeployment{
		ID: deploymentID, ClusterID: clusterID, DesiredGeneration: 7,
		DesiredSpecDigest: "sha256:" + strings.Repeat("a", 64), Phase: "applying",
	}}
	ready := &fakeReadyReconciler{err: errors.New("retry on next heartbeat")}
	ingester := NewIngester(&fakeRunner{tx: tx})
	ingester.SetReadyReconciler(ready)
	if err := ingester.Ingest(context.Background(), clusterID, connectionID, "session", validStatus()); err != nil {
		t.Fatalf("committed status was rejected by post-commit work: %v", err)
	}
	if ready.clusterID != clusterID {
		t.Fatalf("ready reconciler cluster = %s, want %s", ready.clusterID, clusterID)
	}
}

func TestIngestRejectsPayloadIdentityBeforeTransaction(t *testing.T) {
	runner := &fakeRunner{tx: &fakeTransaction{}}
	payload := validStatus()
	payload.ClusterID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	err := NewIngester(runner).Ingest(context.Background(), clusterID, connectionID, "session", payload)
	if !errors.Is(err, ErrClusterIdentityMismatch) || runner.called {
		t.Fatalf("got err=%v called=%v", err, runner.called)
	}
}

func TestIngestAtomicallyAdvancesRolloutAndEnqueuesWake(t *testing.T) {
	rolloutID := uuid.New()
	tx := &fakeTransaction{
		current: sqlc.ClusterDeployment{ID: deploymentID, ClusterID: clusterID, DesiredGeneration: 7,
			DesiredSpecDigest: "sha256:" + strings.Repeat("a", 64), Phase: "applying"},
		advanced: sqlc.AdvanceDeliveryRolloutClusterFromStatusRow{
			ID: uuid.New(), RolloutID: rolloutID, ClusterID: clusterID, State: "ready",
			FromState: "reconciling", Fence: 4,
		},
	}
	if err := NewIngester(&fakeRunner{tx: tx}).Ingest(context.Background(), clusterID, connectionID, "session", validStatus()); err != nil {
		t.Fatal(err)
	}
	if tx.advance == nil || tx.rolloutEvent == nil || tx.outbox == nil {
		t.Fatalf("status advance writes missing: advance=%v event=%v outbox=%v", tx.advance, tx.rolloutEvent, tx.outbox)
	}
	if tx.rolloutEvent.RolloutID != rolloutID || tx.rolloutEvent.FromState != "reconciling" || tx.rolloutEvent.ToState != "ready" {
		t.Fatalf("rollout event = %+v", tx.rolloutEvent)
	}
	if tx.outbox.TaskType != "delivery:rollout_reconcile" || !strings.Contains(string(tx.outbox.Payload), rolloutID.String()) {
		t.Fatalf("rollout wake = %+v", tx.outbox)
	}
}

func TestIngestRejectsSupersededDatabaseSession(t *testing.T) {
	runner := &fakeRunner{tx: &fakeTransaction{fenceErr: pgx.ErrNoRows}}
	err := NewIngester(runner).Ingest(context.Background(), clusterID, connectionID, "old", validStatus())
	if !errors.Is(err, ErrSessionSuperseded) {
		t.Fatalf("got %v, want superseded", err)
	}
}

func TestIngestAdvancesSignedSystemReleaseFromAuthenticatedInventory(t *testing.T) {
	releaseID := uuid.New()
	tx := &systemFakeTransaction{
		fakeTransaction: &fakeTransaction{current: sqlc.ClusterDeployment{DesiredGeneration: 99}},
		observed: sqlc.ObserveDeliverySystemAssignmentRow{
			ClusterID: clusterID, DesiredReleaseID: releaseID, Generation: 3,
			PreviousPhase: "applying", Phase: "ready",
		},
	}
	payload := validStatus()
	if err := NewIngester(&fakeRunner{tx: tx}).Ingest(context.Background(), clusterID, connectionID, "session", payload); err != nil {
		t.Fatal(err)
	}
	if tx.observeArg == nil || tx.observeArg.ObservedAgentVersion != "v1.0.0" || tx.observeArg.ObservedDistributionDigest != payload.ControllerInventory.DistributionDigest {
		t.Fatalf("system inventory was not persisted: %#v", tx.observeArg)
	}
	if tx.systemEvent == nil || tx.systemEvent.ReleaseID != releaseID || tx.systemEvent.FromPhase != "applying" || tx.systemEvent.ToPhase != "ready" {
		t.Fatalf("system transition event was not appended: %#v", tx.systemEvent)
	}
}

func TestIngestIgnoresStaleGenerationButAcknowledgesSnapshot(t *testing.T) {
	tx := &fakeTransaction{current: sqlc.ClusterDeployment{
		ID: deploymentID, ClusterID: clusterID, DesiredGeneration: 8,
		DesiredSpecDigest: "sha256:" + strings.Repeat("b", 64), Phase: "applying",
	}}
	payload := validStatus()
	if err := NewIngester(&fakeRunner{tx: tx}).Ingest(context.Background(), clusterID, connectionID, "session", payload); err != nil {
		t.Fatal(err)
	}
	if tx.updated != nil || tx.event != nil || tx.ack == nil {
		t.Fatalf("stale generation handling update=%v event=%v ack=%v", tx.updated, tx.event, tx.ack)
	}
}

func validStatus() protocol.DeliveryStatusV2 {
	digest := "sha256:" + strings.Repeat("a", 64)
	distributionDigest, _ := fluxdistribution.ControllerSetDigest()
	return protocol.DeliveryStatusV2{
		ProtocolVersion: protocol.DeliveryProtocolVersion, ClusterID: clusterID.String(),
		SessionSequence: 9, SnapshotGeneration: 4, SnapshotETag: "sha256:" + strings.Repeat("c", 64),
		ControllerInventory: protocol.DeliveryControllerInventory{
			AgentVersion: "v1.0.0", FluxVersion: "v2.9.3", KubernetesVersion: "v1.34.2", Ready: true,
			DistributionDigest: distributionDigest,
			Components:         compatibility.RequiredComponentVersions(),
			APIVersions:        []string{"source.toolkit.fluxcd.io/v1", "kustomize.toolkit.fluxcd.io/v1", "helm.toolkit.fluxcd.io/v2"},
		},
		Deployments: []protocol.DeliveryDeploymentStatusV2{{
			DeploymentID: deploymentID.String(), Generation: 7, SpecDigest: digest,
			Phase: "ready", ObservedRevision: strings.Repeat("e", 40),
			SourceKind: "GitRepository", SourceName: "d-source",
			ReconcilerKind: "Kustomization", ReconcilerName: "d-reconciler",
			Conditions: []protocol.DeliveryCondition{{Type: "Ready", Status: "True", ObservedGeneration: 7}},
			Inventory:  protocol.DeliveryInventory{Entries: 2, Ready: 2}, ObservedAt: time.Now().UTC(),
		}},
	}
}

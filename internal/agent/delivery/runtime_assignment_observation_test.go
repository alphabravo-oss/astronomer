package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type countedAssignmentSource struct {
	*AssignmentCache
	snapshots atomic.Int64
	runs      atomic.Int64
}

func (s *countedAssignmentSource) SnapshotAssignments() map[string]AssignmentObservation {
	s.snapshots.Add(1)
	return s.AssignmentCache.SnapshotAssignments()
}
func (s *countedAssignmentSource) Run(ctx context.Context) error {
	s.runs.Add(1)
	return s.AssignmentCache.Run(ctx)
}
func useAssignmentFixture(t *testing.T, f *assignmentCacheFixture) (*Runtime, *memoryCheckpointStore, *countedAssignmentSource) {
	t.Helper()
	r, store := newRuntimeFixture(t)
	r.executor, _ = NewExecutor(f.client)
	for _, accepted := range f.assignments {
		r.checkpoint.Assignments[accepted.DeploymentID] = accepted
	}
	store.value = r.checkpoint
	source := &countedAssignmentSource{AssignmentCache: f.cache}
	r.config.AssignmentObservations = source
	r.config.ObservationFreshness = func() bool { return true }
	return r, store, source
}

func TestRuntimeAssignmentConsumerTiersAndLegacySelection(t *testing.T) {
	for _, n := range []int{1, 10, 100} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			f := newAssignmentCacheFixture(t, n)
			f.start(t)
			eventuallyAssignment(t, f.current)
			r, _, source := useAssignmentFixture(t, f)
			inventory, _, err := r.inspectInventory(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			f.client.ClearActions()
			var payload protocol.DeliveryStatusV2
			send := func(message *protocol.Message) error {
				payload = protocol.DeliveryStatusV2{}
				return json.Unmarshal(message.Payload, &payload)
			}
			for i := 0; i < 10; i++ {
				if err = r.sendStatusWithInventory(context.Background(), send, inventory); err != nil {
					t.Fatal(err)
				}
			}
			if source.snapshots.Load() != 10 || len(payload.Deployments) != n {
				t.Fatalf("batch reads=%d deployments=%d", source.snapshots.Load(), len(payload.Deployments))
			}
			for _, status := range payload.Deployments {
				if status.Phase != "ready" || status.Observation == nil || status.Observation.ObservedAt == nil {
					t.Fatalf("invalid modern status: %#v", status)
				}
			}
			for _, action := range f.client.Actions() {
				if action.GetVerb() == "get" {
					t.Fatal("modern consumer issued GET")
				}
			}
			// Contract marker wins even if negotiation callback currently says modern.
			inventory.Observation = nil
			if err = r.sendStatusWithInventory(context.Background(), send, inventory); err != nil {
				t.Fatal(err)
			}
			gets := 0
			for _, action := range f.client.Actions() {
				if action.GetVerb() == "get" {
					gets++
				}
			}
			if gets != 2*n || source.snapshots.Load() != 10 {
				t.Fatalf("legacy reads=%d snapshot reads=%d", gets, source.snapshots.Load())
			}
			if payload.Deployments[0].Observation != nil {
				t.Fatal("legacy payload leaked observation")
			}
		})
	}
}

func TestRuntimeAssignmentUnknownAndCheckpointFences(t *testing.T) {
	f := newAssignmentCacheFixture(t, 1)
	r, _, _ := useAssignmentFixture(t, f)
	id := f.assignments[0].DeploymentID
	accepted := f.assignments[0]
	at := time.Now().UTC().Add(-time.Minute)
	source, reconciler := f.objects[0].DeepCopy(), f.objects[1].DeepCopy()
	base := AssignmentObservation{Assignment: accepted, Source: AssignmentObjectObservation{source, protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: &at}}, Reconciler: AssignmentObjectObservation{reconciler, protocol.DeliveryObservation{State: protocol.ObservationCurrent, ObservedAt: &at}}}
	for _, state := range []protocol.ObservationState{protocol.ObservationDenied, protocol.ObservationUnsynced, protocol.ObservationStale, protocol.ObservationDisconnected, protocol.ObservationUnavailable, protocol.ObservationAbsent} {
		snapshot := base
		snapshot.Source.Observation.State = state
		if state == protocol.ObservationAbsent {
			snapshot.Source.Object = nil
		}
		status := r.cachedAssignmentStatus(accepted, snapshot, time.Now().UTC())
		if status.Phase != "unknown" || status.ErrorCode != "observation_"+string(state) || status.Inventory.Ready != 0 || len(status.Conditions) != 0 {
			t.Fatalf("noncurrent leaked health: %#v", status)
		}
		if !status.Observation.ObservedAt.Equal(at) {
			t.Fatal("source failure restamped verification")
		}
	}
	snapshot := base
	snapshot.Source.Observation = protocol.DeliveryObservation{State: protocol.ObservationAbsent, ObservedAt: &at}
	snapshot.Source.Object = nil
	snapshot.Reconciler.Observation = protocol.DeliveryObservation{State: protocol.ObservationUnsynced}
	status := r.cachedAssignmentStatus(accepted, snapshot, time.Now().UTC())
	if status.Observation.State != protocol.ObservationUnsynced || status.Observation.ObservedAt != nil {
		t.Fatal("partial observation fabricated source time")
	}
	changed := accepted
	changed.Generation++
	r.checkpoint.Assignments[id] = changed
	if status = r.cachedAssignmentStatus(changed, base, time.Now().UTC()); status.Phase != "unknown" || status.Generation != changed.Generation {
		t.Fatal("old generation crossed checkpoint fence")
	}
	r.config.AssignmentObservations = nil
	statuses := r.assignmentStatuses(context.Background(), true, time.Now().UTC())
	if len(statuses) != 1 || statuses[0].Observation.State != protocol.ObservationUnsynced {
		t.Fatal("missing source not explicit")
	}
	if len(f.client.Actions()) != 0 {
		t.Fatal("unknown source fell back to GET")
	}
	delete(r.checkpoint.Assignments, id)
	if len(r.assignmentStatuses(context.Background(), true, time.Now().UTC())) != 0 {
		t.Fatal("removed checkpoint assignment resurrected")
	}
}

func TestRuntimeAssignmentDirtyStatusDuringResponseWait(t *testing.T) {
	f := newAssignmentCacheFixture(t, 1)
	r, _, source := useAssignmentFixture(t, f)
	r.config.PollInterval = time.Hour
	r.config.StatusInterval = 500 * time.Millisecond
	var connected atomic.Bool
	connected.Store(true)
	r.config.Connected = connected.Load
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	statuses := make(chan protocol.DeliveryStatusV2, 20)
	var requests atomic.Int64
	send := func(message *protocol.Message) error {
		switch message.Type {
		case protocol.MsgDeliveryStateRequest:
			requests.Add(1)
		case protocol.MsgDeliveryStatus:
			var payload protocol.DeliveryStatusV2
			if err := json.Unmarshal(message.Payload, &payload); err != nil {
				return err
			}
			statuses <- payload
		}
		return nil
	}
	go func() { done <- r.Run(ctx, send) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("runtime/source shutdown blocked")
		}
	})
	eventuallyAssignment(t, func() bool { return requests.Load() == 1 && f.current() })
	select {
	case status := <-statuses:
		if len(status.Deployments) != 1 {
			t.Fatal("loaded checkpoint not subscribed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dirty status blocked behind desired response")
	}
	// Storm coalesces to bounded batch work, without another desired-state pull.
	for i := 0; i < 1000; i++ {
		f.cache.markKindDirty(deliveryResources[gitRepositoryGVK].resource)
	}
	connected.Store(false)
	time.Sleep(20 * time.Millisecond)
	connected.Store(true)
	time.Sleep(700 * time.Millisecond)
	if requests.Load() != 1 || source.runs.Load() != 1 || source.snapshots.Load() > 5 {
		t.Fatalf("unbounded/restarted work requests=%d runs=%d snapshots=%d", requests.Load(), source.runs.Load(), source.snapshots.Load())
	}
	for _, action := range f.client.Actions() {
		if action.GetVerb() == "get" || action.GetVerb() == "patch" || action.GetVerb() == "delete" {
			t.Fatalf("dirty status performed %s", action.GetVerb())
		}
	}
	cancel()
}

type blockingStatusProbe struct {
	staticCapabilityProbe
	calls    atomic.Int64
	deadline chan time.Time
}

func (p *blockingStatusProbe) Inspect(ctx context.Context) (protocol.DeliveryControllerInventory, Capabilities, error) {
	if p.calls.Add(1) == 1 {
		return p.staticCapabilityProbe.Inspect(ctx)
	}
	deadline, _ := ctx.Deadline()
	p.deadline <- deadline
	<-ctx.Done()
	return protocol.DeliveryControllerInventory{}, Capabilities{}, ctx.Err()
}

func TestRuntimeAssignmentResponseWaitBoundsStatusInspection(t *testing.T) {
	r, _ := newRuntimeFixture(t)
	probe := &blockingStatusProbe{staticCapabilityProbe: r.probe.(staticCapabilityProbe), deadline: make(chan time.Time, 1)}
	r.probe = probe
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	r.statusTicks = ticks
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- r.requestAndReconcile(ctx, func(*protocol.Message) error { return nil }) }()
	select {
	case deadline := <-probe.deadline:
		if deadline.IsZero() || deadline.After(start.Add(deliveryResponseTimeout+time.Second)) {
			t.Error("status inspection lacks response-wait deadline")
		}
	case <-time.After(time.Second):
		t.Fatal("scheduled inspection did not start during reply wait")
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("canceled reply wait=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked status inspection outlived wait cancellation")
	}
	if r.checkpoint.SnapshotGeneration != 0 || len(r.checkpoint.Assignments) != 0 {
		t.Fatal("timed-out observation changed checkpoint")
	}
}

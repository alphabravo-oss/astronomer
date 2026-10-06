package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type failingAckStore struct {
	fail      bool
	attempted checkpoint
}

func (s *failingAckStore) Load(context.Context) (checkpoint, error) { return emptyCheckpoint(), nil }
func (s *failingAckStore) Save(_ context.Context, c checkpoint) error {
	s.attempted = c
	if s.fail {
		return errors.New("summary write failed")
	}
	return nil
}

func TestRuntimeCheckpointAckWaitsForDurableSave(t *testing.T) {
	r, _ := newRuntimeFixture(t)
	ctx := context.Background()
	previous := canonicalSnapshot(t, 1, nil, nil)
	if err := r.processSnapshot(ctx, previous, testCapabilities()); err != nil {
		t.Fatal(err)
	}
	store := &failingAckStore{fail: true}
	r.store = store
	snapshot := canonicalSnapshot(t, 2, []protocol.DeliveryAssignmentV2{gitAssignment()}, nil)
	if err := r.processSnapshot(ctx, snapshot, testCapabilities()); err == nil {
		t.Fatal("expected save failure")
	}
	if len(r.checkpoint.Assignments) != 1 {
		t.Fatal("applied identity lost on failed persistence")
	}
	if store.attempted.SnapshotGeneration != 2 || store.attempted.CredentialEpoch != 2 {
		t.Fatal("store did not receive candidate headers")
	}
	assertHeaders := func(want protocol.DeliveryStateResponseV2) {
		t.Helper()
		if r.checkpoint.CredentialEpoch != want.CredentialEpoch {
			t.Fatal("credential epoch advanced without persistence")
		}
		r.lastStatusDigest = ""
		sentStatus := false
		if err := r.sendStatus(ctx, func(m *protocol.Message) error {
			var status protocol.DeliveryStatusV2
			if err := json.Unmarshal(m.Payload, &status); err != nil {
				return err
			}
			if status.SnapshotGeneration != want.SnapshotGeneration || status.SnapshotETag != want.ETag {
				t.Fatal("status advertised non-durable headers")
			}
			sentStatus = true
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if !sentStatus {
			t.Fatal("status not sent")
		}
		sentRequest := false
		stop := errors.New("captured request")
		err := r.requestAndReconcile(ctx, func(m *protocol.Message) error {
			var request protocol.DeliveryStateRequestV2
			if err := json.Unmarshal(m.Payload, &request); err != nil {
				return err
			}
			if request.AckedSnapshotGeneration != want.SnapshotGeneration || request.AckedETag != want.ETag {
				t.Fatal("state request advertised non-durable headers")
			}
			sentRequest = true
			return stop
		})
		if !errors.Is(err, stop) || !sentRequest {
			t.Fatalf("state request not captured: %v", err)
		}
	}
	assertHeaders(previous)
	store.fail = false
	// The runtime deliberately zeroes received credentials after each attempt.
	snapshot = canonicalSnapshot(t, 2, []protocol.DeliveryAssignmentV2{gitAssignment()}, nil)
	if err := r.processSnapshot(ctx, snapshot, testCapabilities()); err != nil {
		t.Fatal(err)
	}
	assertHeaders(snapshot)
}

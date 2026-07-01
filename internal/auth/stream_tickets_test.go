package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStreamTicketStore_OneUseAndScoped(t *testing.T) {
	store := NewStreamTicketStore(time.Minute)
	userID := uuid.New()
	clusterID := uuid.New()
	token, ticket, err := store.Issue(userID, StreamKindLogs, clusterID)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if token == "" || ticket.UserID != userID || ticket.Kind != StreamKindLogs || ticket.ClusterID != clusterID {
		t.Fatalf("unexpected ticket: token=%q ticket=%+v", token, ticket)
	}
	if _, err := store.Validate(token, StreamKindExec, clusterID); err != ErrStreamTicketScope {
		t.Fatalf("scope mismatch err = %v, want %v", err, ErrStreamTicketScope)
	}
	if _, err := store.Validate(token, StreamKindLogs, clusterID); err != ErrStreamTicketInvalid {
		t.Fatalf("ticket should be consumed after failed scope validation, got %v", err)
	}
}

func TestStreamTicketStore_ValidateOKConsumes(t *testing.T) {
	store := NewStreamTicketStore(time.Minute)
	userID := uuid.New()
	clusterID := uuid.New()
	token, _, err := store.Issue(userID, StreamKindRegistration, clusterID)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	got, err := store.Validate(token, StreamKindRegistration, clusterID)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got != userID {
		t.Fatalf("user id = %s, want %s", got, userID)
	}
	if _, err := store.Validate(token, StreamKindRegistration, clusterID); err != ErrStreamTicketInvalid {
		t.Fatalf("reused ticket err = %v, want %v", err, ErrStreamTicketInvalid)
	}
}

// TestStreamTicketStore_SharedBackendCrossInstance models the default
// 2-replica deployment (F01): a ticket minted on one pod's store must
// validate on a DIFFERENT pod's store that shares the same backend, and
// stay single-use cluster-wide. A shared in-memory backend stands in for
// Redis. Before the shared-backend refactor each store had a private map,
// so cross-instance validation was impossible.
func TestStreamTicketStore_SharedBackendCrossInstance(t *testing.T) {
	backend := newMemTicketBackend() // stand-in for a shared Redis
	minter := NewStreamTicketStoreWithBackend(time.Minute, backend)
	validator := NewStreamTicketStoreWithBackend(time.Minute, backend)

	userID := uuid.New()
	clusterID := uuid.New()
	token, _, err := minter.Issue(userID, StreamKindExec, clusterID)
	if err != nil {
		t.Fatalf("Issue on minter: %v", err)
	}

	// Validated on the OTHER instance — the WS pod is not the mint pod.
	got, err := validator.Validate(token, StreamKindExec, clusterID)
	if err != nil {
		t.Fatalf("Validate on separate instance: %v", err)
	}
	if got != userID {
		t.Fatalf("user id = %s, want %s", got, userID)
	}

	// Single-use cluster-wide: a second redeem on EITHER instance fails.
	if _, err := minter.Validate(token, StreamKindExec, clusterID); err != ErrStreamTicketInvalid {
		t.Fatalf("re-redeem on minter err = %v, want %v", err, ErrStreamTicketInvalid)
	}
	if _, err := validator.Validate(token, StreamKindExec, clusterID); err != ErrStreamTicketInvalid {
		t.Fatalf("re-redeem on validator err = %v, want %v", err, ErrStreamTicketInvalid)
	}
}

func TestStreamTicketStore_Expired(t *testing.T) {
	now := time.Now()
	store := NewStreamTicketStore(time.Minute)
	store.now = func() time.Time { return now }
	token, _, err := store.Issue(uuid.New(), StreamKindEvents, uuid.Nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := store.Validate(token, StreamKindEvents, uuid.Nil); err != ErrStreamTicketExpired {
		t.Fatalf("expired err = %v, want %v", err, ErrStreamTicketExpired)
	}
}

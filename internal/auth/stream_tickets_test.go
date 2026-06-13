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

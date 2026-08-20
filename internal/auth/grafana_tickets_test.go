package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGrafanaTicketStore_OneUseTake(t *testing.T) {
	store := NewGrafanaTicketStore(time.Minute)
	userID := uuid.New()
	token, ticket, err := store.Issue(userID, "viewer@example.com", "Viewer", false, false, time.Hour, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if token == "" || ticket.Email != "viewer@example.com" || ticket.Role != "Viewer" {
		t.Fatalf("unexpected ticket: token=%q ticket=%+v", token, ticket)
	}
	got, err := store.Take(token)
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	if got.UserID != userID || got.Email != "viewer@example.com" || got.CookieTTL != 3600 {
		t.Fatalf("got %+v", got)
	}
	if _, err := store.Take(token); err != ErrGrafanaTicketInvalid {
		t.Fatalf("reused ticket err = %v, want %v", err, ErrGrafanaTicketInvalid)
	}
}

func TestGrafanaTicketStore_Expired(t *testing.T) {
	now := time.Now()
	store := NewGrafanaTicketStore(time.Minute)
	store.now = func() time.Time { return now }
	token, _, err := store.Issue(uuid.New(), "a@example.com", "Viewer", false, false, time.Hour, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := store.Take(token); err != ErrGrafanaTicketExpired {
		t.Fatalf("expired err = %v, want %v", err, ErrGrafanaTicketExpired)
	}
}

func TestGrafanaTicketStore_SharedBackendCrossInstance(t *testing.T) {
	backend := newMemTicketBackend()
	minter := NewGrafanaTicketStoreWithBackend(time.Minute, backend)
	redeemer := NewGrafanaTicketStoreWithBackend(time.Minute, backend)
	token, _, err := minter.Issue(uuid.New(), "a@example.com", "Editor", true, false, time.Hour, nil)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := redeemer.Take(token); err != nil {
		t.Fatalf("Take on other instance: %v", err)
	}
	if _, err := minter.Take(token); err != ErrGrafanaTicketInvalid {
		t.Fatalf("re-take err = %v, want invalid", err)
	}
}

func TestGrafanaTicketStore_DoesNotUseStreamKinds(t *testing.T) {
	if got := NormalizeStreamKind("grafana"); got != "" {
		t.Fatalf("NormalizeStreamKind(grafana) = %q, must not be a stream kind", got)
	}
	if GrafanaTicketRedisPrefix() != "grafana-ticket:" {
		t.Fatalf("prefix = %q, want grafana-ticket:", GrafanaTicketRedisPrefix())
	}
	if strings.Contains(GrafanaTicketRedisPrefix(), "streamticket") {
		t.Fatal("grafana ticket prefix must not reuse streamticket:")
	}
}

func TestGrafanaTicketStore_ClusterIDsRoundTrip(t *testing.T) {
	store := NewGrafanaTicketStore(time.Minute)
	ids := []string{"11111111-1111-1111-1111-111111111111"}
	token, _, err := store.Issue(uuid.New(), "scoped@example.com", "Viewer", true, false, time.Hour, ids)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Take(token)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Explore || len(got.ClusterIDs) != 1 || got.ClusterIDs[0] != ids[0] {
		t.Fatalf("got %+v", got)
	}
}

func TestGrafanaTicketStore_MissingToken(t *testing.T) {
	store := NewGrafanaTicketStore(time.Minute)
	if _, err := store.Take(""); err != ErrGrafanaTicketInvalid {
		t.Fatalf("empty Take err = %v", err)
	}
	if _, err := store.Take("no-such-ticket"); err != ErrGrafanaTicketInvalid {
		t.Fatalf("unknown Take err = %v", err)
	}
}

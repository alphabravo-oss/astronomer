package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestExtensionTicket_IssueValidate_HappyPath(t *testing.T) {
	store := NewExtensionTicketStore(time.Minute)
	user := uuid.New()
	cluster := uuid.New()

	token, ticket, err := store.Issue(user, "cost-insights", "podCost", cluster)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if token == "" {
		t.Fatal("expected a non-empty opaque token")
	}
	if !ticket.ExpiresAt.After(time.Now()) {
		t.Fatal("expected a future expiry")
	}

	got, err := store.Validate(token, "cost-insights", "podCost", cluster)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got != user {
		t.Fatalf("validate returned %s, want %s", got, user)
	}
}

func TestExtensionTicket_SingleUse(t *testing.T) {
	store := NewExtensionTicketStore(time.Minute)
	user := uuid.New()
	cluster := uuid.New()
	token, _, err := store.Issue(user, "cost-insights", "podCost", cluster)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := store.Validate(token, "cost-insights", "podCost", cluster); err != nil {
		t.Fatalf("first validate: %v", err)
	}
	// A replay must fail — the ticket is deleted on first Validate.
	if _, err := store.Validate(token, "cost-insights", "podCost", cluster); !errors.Is(err, ErrExtensionTicketInvalid) {
		t.Fatalf("replay should be invalid, got %v", err)
	}
}

func TestExtensionTicket_TTLClampedTo60s(t *testing.T) {
	// A caller asking for a 10-minute TTL must be clamped to the ≤60s ceiling.
	store := NewExtensionTicketStore(10 * time.Minute)
	if store.ttl > extensionTicketMaxTTL {
		t.Fatalf("ttl %s exceeds the %s ceiling", store.ttl, extensionTicketMaxTTL)
	}
}

func TestExtensionTicket_Expired(t *testing.T) {
	store := NewExtensionTicketStore(time.Minute)
	user := uuid.New()
	cluster := uuid.New()
	now := time.Now()
	store.now = func() time.Time { return now }
	token, _, err := store.Issue(user, "cost-insights", "podCost", cluster)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Advance past the TTL.
	store.now = func() time.Time { return now.Add(2 * time.Minute) }
	if _, err := store.Validate(token, "cost-insights", "podCost", cluster); !errors.Is(err, ErrExtensionTicketExpired) {
		t.Fatalf("expected expired, got %v", err)
	}
}

func TestExtensionTicket_ScopeMismatch(t *testing.T) {
	store := NewExtensionTicketStore(time.Minute)
	user := uuid.New()
	cluster := uuid.New()
	other := uuid.New()

	cases := []struct {
		name                 string
		ext, dsID            string
		clusterID            uuid.UUID
	}{
		{"wrong extension", "other-ext", "podCost", cluster},
		{"wrong dataSource", "cost-insights", "otherSource", cluster},
		{"wrong cluster", "cost-insights", "podCost", other},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			token, _, err := store.Issue(user, "cost-insights", "podCost", cluster)
			if err != nil {
				t.Fatalf("issue: %v", err)
			}
			if _, err := store.Validate(token, tc.ext, tc.dsID, tc.clusterID); !errors.Is(err, ErrExtensionTicketScope) {
				t.Fatalf("expected scope mismatch, got %v", err)
			}
			// Single-use: even a scope-mismatched presentation burns the token.
			if _, err := store.Validate(token, "cost-insights", "podCost", cluster); !errors.Is(err, ErrExtensionTicketInvalid) {
				t.Fatalf("token must be burned after a mismatched presentation, got %v", err)
			}
		})
	}
}

func TestExtensionTicket_RejectsEmptyScope(t *testing.T) {
	store := NewExtensionTicketStore(time.Minute)
	if _, _, err := store.Issue(uuid.Nil, "cost-insights", "podCost", uuid.New()); !errors.Is(err, ErrExtensionTicketScope) {
		t.Fatalf("nil user must be rejected, got %v", err)
	}
	if _, _, err := store.Issue(uuid.New(), "", "podCost", uuid.New()); !errors.Is(err, ErrExtensionTicketScope) {
		t.Fatalf("empty extension must be rejected, got %v", err)
	}
	if _, _, err := store.Issue(uuid.New(), "cost-insights", "", uuid.New()); !errors.Is(err, ErrExtensionTicketScope) {
		t.Fatalf("empty dataSource must be rejected, got %v", err)
	}
}

func TestExtensionTicket_IssueToken(t *testing.T) {
	store := NewExtensionTicketStore(time.Minute)
	user := uuid.New()
	cluster := uuid.New()
	token, exp, err := store.IssueToken(user, "cost-insights", "podCost", cluster)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	if token == "" || !exp.After(time.Now()) {
		t.Fatalf("expected token + future expiry, got %q %s", token, exp)
	}
	if got, err := store.Validate(token, "cost-insights", "podCost", cluster); err != nil || got != user {
		t.Fatalf("validate after IssueToken: got %s err %v", got, err)
	}
}

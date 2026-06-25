package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ExtensionTicketStore mints and consumes the short-lived, narrowly-scoped
// bridge tickets a sandboxed Tier-2 extension (§BridgeProtocol) uses to reach
// §DataProxy. It is modeled on StreamTicketStore: the opaque token is
// sha256-hashed at rest, single-use (deleted on Validate), TTL-bounded (≤60s),
// and scope-checked against {UserID, Extension, DataSourceID, ClusterID}.
//
// The ticket grants NO permission of its own — it only proves "this user, for
// this extension+dataSource+cluster, briefly". §DataProxy re-derives RBAC from
// the bound UserID on every call, so a leaked ticket is good for one call, one
// dataSource, one user, ≤60s. The session JWT never crosses the bridge.
//
// TTL is hard-capped at extensionTicketMaxTTL so a misconfiguration can never
// widen the window past the threat-model bound.
const extensionTicketMaxTTL = 60 * time.Second

var (
	ErrExtensionTicketInvalid = errors.New("extension ticket is invalid")
	ErrExtensionTicketExpired = errors.New("extension ticket expired")
	ErrExtensionTicketScope   = errors.New("extension ticket scope mismatch")
)

// ExtensionTicket is the at-rest scope record. The opaque token that maps to it
// is never stored — only its sha256 hash is a map key.
type ExtensionTicket struct {
	UserID       uuid.UUID
	Extension    string
	DataSourceID string
	ClusterID    uuid.UUID
	ExpiresAt    time.Time
}

type ExtensionTicketStore struct {
	mu      sync.Mutex
	now     func() time.Time
	ttl     time.Duration
	tickets map[string]ExtensionTicket
}

// NewExtensionTicketStore returns a store whose tickets live for ttl, clamped to
// extensionTicketMaxTTL (and to a sane positive default when ttl<=0).
func NewExtensionTicketStore(ttl time.Duration) *ExtensionTicketStore {
	if ttl <= 0 || ttl > extensionTicketMaxTTL {
		ttl = extensionTicketMaxTTL
	}
	return &ExtensionTicketStore{
		now:     time.Now,
		ttl:     ttl,
		tickets: make(map[string]ExtensionTicket),
	}
}

// Issue mints a single-use ticket bound to the given scope and returns the
// opaque token (the only copy the caller ever sees) plus the stored record. The
// caller MUST have already RBAC-checked the user for this dataSource — the store
// enforces scope match and TTL, not authorization.
func (s *ExtensionTicketStore) Issue(userID uuid.UUID, extension, dataSourceID string, clusterID uuid.UUID) (string, ExtensionTicket, error) {
	if s == nil {
		return "", ExtensionTicket{}, ErrExtensionTicketInvalid
	}
	extension = strings.TrimSpace(extension)
	dataSourceID = strings.TrimSpace(dataSourceID)
	if userID == uuid.Nil || extension == "" || dataSourceID == "" {
		return "", ExtensionTicket{}, ErrExtensionTicketScope
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", ExtensionTicket{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	ticket := ExtensionTicket{
		UserID:       userID,
		Extension:    extension,
		DataSourceID: dataSourceID,
		ClusterID:    clusterID,
		ExpiresAt:    s.now().Add(s.ttl),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	s.tickets[hashExtensionTicket(token)] = ticket
	return token, ticket, nil
}

// IssueToken is the handler-facing convenience over Issue: it returns just the
// opaque token and its expiry, so the bridge handler need not depend on the
// ExtensionTicket struct.
func (s *ExtensionTicketStore) IssueToken(userID uuid.UUID, extension, dataSourceID string, clusterID uuid.UUID) (string, time.Time, error) {
	token, ticket, err := s.Issue(userID, extension, dataSourceID, clusterID)
	if err != nil {
		return "", time.Time{}, err
	}
	return token, ticket.ExpiresAt, nil
}

// Validate consumes a ticket: it is deleted before any scope/TTL check so a
// single token can never be replayed, and the scope must match the call
// exactly. It returns the bound user id; §DataProxy re-derives that user's RBAC
// on every call, so the ticket conveys identity, never permission.
func (s *ExtensionTicketStore) Validate(token, extension, dataSourceID string, clusterID uuid.UUID) (uuid.UUID, error) {
	if s == nil {
		return uuid.Nil, ErrExtensionTicketInvalid
	}
	token = strings.TrimSpace(token)
	extension = strings.TrimSpace(extension)
	dataSourceID = strings.TrimSpace(dataSourceID)
	if token == "" || extension == "" || dataSourceID == "" {
		return uuid.Nil, ErrExtensionTicketInvalid
	}
	now := s.now()
	key := hashExtensionTicket(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket, ok := s.tickets[key]
	if !ok {
		return uuid.Nil, ErrExtensionTicketInvalid
	}
	// Single-use: delete BEFORE the TTL/scope checks so even a scope-mismatched
	// or expired token is burned on first presentation.
	delete(s.tickets, key)
	if !ticket.ExpiresAt.After(now) {
		return uuid.Nil, ErrExtensionTicketExpired
	}
	if !extensionScopeEqual(ticket.Extension, extension) ||
		!extensionScopeEqual(ticket.DataSourceID, dataSourceID) ||
		ticket.ClusterID != clusterID {
		return uuid.Nil, ErrExtensionTicketScope
	}
	return ticket.UserID, nil
}

func (s *ExtensionTicketStore) pruneLocked(now time.Time) {
	for key, ticket := range s.tickets {
		if !ticket.ExpiresAt.After(now) {
			delete(s.tickets, key)
		}
	}
}

// extensionScopeEqual is a constant-time compare so the scope check leaks no
// timing about which field mismatched.
func extensionScopeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func hashExtensionTicket(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

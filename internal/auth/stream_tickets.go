package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	StreamKindEvents       = "events"
	StreamKindRegistration = "registration"
	StreamKindLogs         = "logs"
	StreamKindExec         = "exec"
	StreamKindShell        = "shell"
)

var (
	ErrStreamTicketInvalid = errors.New("stream ticket is invalid")
	ErrStreamTicketExpired = errors.New("stream ticket expired")
	ErrStreamTicketScope   = errors.New("stream ticket scope mismatch")
)

type StreamTicket struct {
	UserID    uuid.UUID
	Kind      string
	ClusterID uuid.UUID
	ExpiresAt time.Time
}

type StreamTicketStore struct {
	mu      sync.Mutex
	now     func() time.Time
	ttl     time.Duration
	tickets map[string]StreamTicket
}

func NewStreamTicketStore(ttl time.Duration) *StreamTicketStore {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &StreamTicketStore{
		now:     time.Now,
		ttl:     ttl,
		tickets: make(map[string]StreamTicket),
	}
}

func (s *StreamTicketStore) Issue(userID uuid.UUID, kind string, clusterID uuid.UUID) (string, StreamTicket, error) {
	if s == nil {
		return "", StreamTicket{}, ErrStreamTicketInvalid
	}
	kind = NormalizeStreamKind(kind)
	if kind == "" || userID == uuid.Nil {
		return "", StreamTicket{}, ErrStreamTicketScope
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", StreamTicket{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	ticket := StreamTicket{
		UserID:    userID,
		Kind:      kind,
		ClusterID: clusterID,
		ExpiresAt: s.now().Add(s.ttl),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.now())
	s.tickets[hashStreamTicket(token)] = ticket
	return token, ticket, nil
}

func (s *StreamTicketStore) Validate(token, kind string, clusterID uuid.UUID) (uuid.UUID, error) {
	if s == nil {
		return uuid.Nil, ErrStreamTicketInvalid
	}
	token = strings.TrimSpace(token)
	kind = NormalizeStreamKind(kind)
	if token == "" || kind == "" {
		return uuid.Nil, ErrStreamTicketInvalid
	}
	now := s.now()
	key := hashStreamTicket(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	ticket, ok := s.tickets[key]
	if !ok {
		return uuid.Nil, ErrStreamTicketInvalid
	}
	delete(s.tickets, key)
	if !ticket.ExpiresAt.After(now) {
		return uuid.Nil, ErrStreamTicketExpired
	}
	if ticket.Kind != kind || ticket.ClusterID != clusterID {
		return uuid.Nil, ErrStreamTicketScope
	}
	return ticket.UserID, nil
}

func NormalizeStreamKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case StreamKindEvents:
		return StreamKindEvents
	case StreamKindRegistration:
		return StreamKindRegistration
	case StreamKindLogs:
		return StreamKindLogs
	case StreamKindExec:
		return StreamKindExec
	case StreamKindShell:
		return StreamKindShell
	default:
		return ""
	}
}

func (s *StreamTicketStore) pruneLocked(now time.Time) {
	for key, ticket := range s.tickets {
		if !ticket.ExpiresAt.After(now) {
			delete(s.tickets, key)
		}
	}
}

func hashStreamTicket(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

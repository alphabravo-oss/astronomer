package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Grafana ticket store is dedicated. Do not add a StreamKind or call
// StreamTicketStore.Issue — those kinds require clusterID and
// NormalizeStreamKind ∈ {events,registration,logs,exec,shell}.

const (
	GrafanaTicketTTL        = 60 * time.Second
	grafanaTicketKeyPrefix  = "grafana-ticket:"
	grafanaTicketDefaultTTL = GrafanaTicketTTL
)

var (
	ErrGrafanaTicketInvalid = errors.New("grafana ticket is invalid")
	ErrGrafanaTicketExpired = errors.New("grafana ticket expired")
)

// GrafanaTicket is the at-rest payload. The plaintext token is never stored;
// only SHA-256(token) is a map key (prefixed grafana-ticket: on Redis).
type GrafanaTicket struct {
	UserID    uuid.UUID `json:"userID"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Explore   bool      `json:"explore"`
	Admin     bool      `json:"admin"`
	CookieTTL int       `json:"ttl"`
	ExpiresAt time.Time `json:"exp"`
}

// GrafanaTicketStore is a one-use Put/Take store on the Astronomer server.
// HA uses the same StreamTicketBackend interface as stream tickets, with a
// distinct key prefix. grafana-proxy never holds this store.
type GrafanaTicketStore struct {
	now     func() time.Time
	ttl     time.Duration
	backend StreamTicketBackend
}

func NewGrafanaTicketStore(ttl time.Duration) *GrafanaTicketStore {
	return NewGrafanaTicketStoreWithBackend(ttl, nil)
}

func NewGrafanaTicketStoreWithBackend(ttl time.Duration, backend StreamTicketBackend) *GrafanaTicketStore {
	if ttl <= 0 {
		ttl = grafanaTicketDefaultTTL
	}
	if backend == nil {
		backend = newMemTicketBackend()
	}
	return &GrafanaTicketStore{
		now:     time.Now,
		ttl:     ttl,
		backend: backend,
	}
}

// NewRedisGrafanaTicketBackend wraps Redis with prefix grafana-ticket:.
func NewRedisGrafanaTicketBackend(rdb *redis.Client) StreamTicketBackend {
	return &redisTicketBackend{rdb: rdb, prefix: grafanaTicketKeyPrefix}
}

// NewRedisGrafanaTicketBackendFromURL builds the HA backend from the same
// Redis URL stream tickets use. Distinct prefix; never streamticket:.
func NewRedisGrafanaTicketBackendFromURL(redisURL string) (StreamTicketBackend, error) {
	client, err := redisClientFromAsynqURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url for grafana tickets: %w", err)
	}
	return NewRedisGrafanaTicketBackend(client), nil
}

func (s *GrafanaTicketStore) Issue(userID uuid.UUID, email, role string, explore, admin bool, cookieTTL time.Duration) (string, GrafanaTicket, error) {
	if s == nil {
		return "", GrafanaTicket{}, ErrGrafanaTicketInvalid
	}
	email = strings.TrimSpace(email)
	role = strings.TrimSpace(role)
	if userID == uuid.Nil || email == "" || role == "" {
		return "", GrafanaTicket{}, ErrGrafanaTicketInvalid
	}
	if cookieTTL <= 0 {
		cookieTTL = s.ttl
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", GrafanaTicket{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	ticket := GrafanaTicket{
		UserID:    userID,
		Email:     email,
		Role:      role,
		Explore:   explore,
		Admin:     admin,
		CookieTTL: int(cookieTTL / time.Second),
		ExpiresAt: s.now().Add(s.ttl),
	}
	encoded, err := json.Marshal(ticket)
	if err != nil {
		return "", GrafanaTicket{}, err
	}
	if err := s.backend.Put(context.Background(), hashGrafanaTicket(token), string(encoded), s.ttl); err != nil {
		return "", GrafanaTicket{}, err
	}
	return token, ticket, nil
}

// Take atomically consumes the ticket. Missing, used, or expired → error.
func (s *GrafanaTicketStore) Take(token string) (GrafanaTicket, error) {
	if s == nil {
		return GrafanaTicket{}, ErrGrafanaTicketInvalid
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return GrafanaTicket{}, ErrGrafanaTicketInvalid
	}
	now := s.now()
	encoded, found, err := s.backend.Take(context.Background(), hashGrafanaTicket(token))
	if err != nil || !found {
		return GrafanaTicket{}, ErrGrafanaTicketInvalid
	}
	var ticket GrafanaTicket
	if err := json.Unmarshal([]byte(encoded), &ticket); err != nil {
		return GrafanaTicket{}, ErrGrafanaTicketInvalid
	}
	if !ticket.ExpiresAt.After(now) {
		return GrafanaTicket{}, ErrGrafanaTicketExpired
	}
	return ticket, nil
}

func hashGrafanaTicket(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// GrafanaTicketRedisPrefix is exported for tests that assert the dedicated
// prefix is not streamticket:.
func GrafanaTicketRedisPrefix() string { return grafanaTicketKeyPrefix }

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
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
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

// StreamTicketBackend is the pluggable single-use store behind
// StreamTicketStore. The in-memory backend keeps single-replica/dev
// behaviour; a Redis backend (NewRedisStreamTicketBackend) makes a
// ticket mintable on one replica and atomically validatable+consumed on
// ANY other replica. That is required under the default 2-replica server
// deployment: the pod that mints ?ticket= (POST /streams/tickets/) and
// the pod nginx pins the WebSocket to are load-balanced independently, so
// a per-pod in-memory map misses ~half of validations (F01).
//
// Take MUST be atomic (get-and-delete) so a ticket is single-use across
// the whole cluster even when two replicas race the same token.
type StreamTicketBackend interface {
	// Put stores value under key, expiring after ttl.
	Put(ctx context.Context, key, value string, ttl time.Duration) error
	// Take atomically returns and removes the value at key. A missing key
	// returns ("", false, nil).
	Take(ctx context.Context, key string) (value string, found bool, err error)
}

type StreamTicketStore struct {
	now     func() time.Time
	ttl     time.Duration
	backend StreamTicketBackend
}

// NewStreamTicketStore builds a store backed by a per-process in-memory
// map. Suitable for single-replica/dev. For the default multi-replica
// deployment use NewStreamTicketStoreWithBackend with a Redis backend so
// tickets validate cluster-wide (F01).
func NewStreamTicketStore(ttl time.Duration) *StreamTicketStore {
	return NewStreamTicketStoreWithBackend(ttl, nil)
}

// NewStreamTicketStoreWithBackend builds a store over the supplied
// backend. A nil backend falls back to the in-memory map so callers can
// pass a Redis backend only when one is configured. The return type is
// identical to NewStreamTicketStore, so swapping constructors is a
// drop-in for every consumer that holds a *StreamTicketStore.
func NewStreamTicketStoreWithBackend(ttl time.Duration, backend StreamTicketBackend) *StreamTicketStore {
	if ttl <= 0 {
		ttl = time.Minute
	}
	if backend == nil {
		backend = newMemTicketBackend()
	}
	return &StreamTicketStore{
		now:     time.Now,
		ttl:     ttl,
		backend: backend,
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
	encoded, err := json.Marshal(ticket)
	if err != nil {
		return "", StreamTicket{}, err
	}
	if err := s.backend.Put(context.Background(), hashStreamTicket(token), string(encoded), s.ttl); err != nil {
		return "", StreamTicket{}, err
	}
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
	// Take is atomic: the ticket is consumed here even if the scope/expiry
	// checks below reject it, preserving the "one attempt per ticket"
	// contract cluster-wide.
	encoded, found, err := s.backend.Take(context.Background(), hashStreamTicket(token))
	if err != nil || !found {
		// Fail closed: a backend error is treated as an invalid ticket so
		// a transient Redis blip can never authorize a stream.
		return uuid.Nil, ErrStreamTicketInvalid
	}
	var ticket StreamTicket
	if err := json.Unmarshal([]byte(encoded), &ticket); err != nil {
		return uuid.Nil, ErrStreamTicketInvalid
	}
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

func hashStreamTicket(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// memTicketBackend is the in-process default backend. It matches the
// original per-pod behaviour: fine for a single replica, but two replicas
// each get their own map (the F01 bug) — use a Redis backend in HA.
type memTicketBackend struct {
	mu      sync.Mutex
	entries map[string]memTicketEntry
}

type memTicketEntry struct {
	value    string
	expireAt time.Time // wall-clock, for memory hygiene only
}

func newMemTicketBackend() *memTicketBackend {
	return &memTicketBackend{entries: make(map[string]memTicketEntry)}
}

func (b *memTicketBackend) Put(_ context.Context, key, value string, ttl time.Duration) error {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(now)
	b.entries[key] = memTicketEntry{value: value, expireAt: now.Add(ttl)}
	return nil
}

func (b *memTicketBackend) Take(_ context.Context, key string) (string, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.entries[key]
	if !ok {
		return "", false, nil
	}
	delete(b.entries, key)
	return entry.value, true, nil
}

func (b *memTicketBackend) pruneLocked(now time.Time) {
	for key, entry := range b.entries {
		if !entry.expireAt.After(now) {
			delete(b.entries, key)
		}
	}
}

// redisTicketKeyPrefix namespaces stream-ticket keys so they never
// collide with the asynq queues or the tunnel locator that share the
// same Redis instance.
const redisTicketKeyPrefix = "streamticket:"

// redisTicketOpTimeout bounds each Redis round-trip. Issue/Validate sit
// on the request path (POST /streams/tickets and the WS upgrade), so a
// stalled Redis must fail fast rather than hang the handler.
const redisTicketOpTimeout = 5 * time.Second

// redisTicketBackend backs stream tickets with Redis so any replica can
// atomically validate+consume a ticket minted by any other replica.
type redisTicketBackend struct {
	rdb *redis.Client
}

// NewRedisStreamTicketBackend wraps a go-redis client as a shared,
// cluster-wide single-use ticket store. Single-use is guaranteed by
// GETDEL (atomic get-and-delete) in Take.
func NewRedisStreamTicketBackend(rdb *redis.Client) StreamTicketBackend {
	return &redisTicketBackend{rdb: rdb}
}

// NewRedisStreamTicketBackendFromURL builds a Redis backend from the same
// connection string the rest of the platform uses (asynq parses it),
// mirroring tunnel.NewLocatorFromAsynqRedisURL.
func NewRedisStreamTicketBackendFromURL(redisURL string) (StreamTicketBackend, error) {
	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url for stream tickets: %w", err)
	}
	client, ok := opt.MakeRedisClient().(*redis.Client)
	if !ok {
		return nil, errors.New("stream tickets: asynq redis backend is not a single-node go-redis client (cluster mode not supported)")
	}
	return NewRedisStreamTicketBackend(client), nil
}

func (b *redisTicketBackend) Put(ctx context.Context, key, value string, ttl time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, redisTicketOpTimeout)
	defer cancel()
	return b.rdb.Set(ctx, redisTicketKeyPrefix+key, value, ttl).Err()
}

func (b *redisTicketBackend) Take(ctx context.Context, key string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, redisTicketOpTimeout)
	defer cancel()
	value, err := b.rdb.GetDel(ctx, redisTicketKeyPrefix+key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

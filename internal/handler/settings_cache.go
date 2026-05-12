// Package handler — settings cache shared between the settings handler
// and the FeatureGate middleware.
//
// The cache exists because every authenticated API request that lands
// under a feature-gated route (/catalog/*, /projects/*, /monitoring/*,
// /argocd/*, /security/*, /backups/*) would otherwise hit the DB to
// check whether the feature is enabled. Settings change rarely —
// caching for 30s makes the gate effectively free in the hot path
// while keeping operator changes visible within a configuration round.
//
// The cache is per-process (not Redis). Mutation via the settings
// handler synchronously invalidates the local cache; remote replicas
// pick up the change on their own 30s TTL. That's acceptable for
// "toggle a tab" — strict cross-replica consistency is not a real
// requirement here.
package handler

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// SettingsReader is the DB surface FeatureGate needs. It's narrower
// than PlatformSettingsQuerier because the middleware only ever reads;
// it never mutates. Production wires *sqlc.Queries.
type SettingsReader interface {
	GetPlatformSetting(ctx context.Context, key string) (sqlc.PlatformSetting, error)
}

// SettingsCache is a per-process key/value cache for platform_settings
// rows. Each entry expires after `ttl`; mutations invalidate the entry
// so the next read pulls fresh.
type SettingsCache struct {
	reader  SettingsReader
	ttl     time.Duration
	mu      sync.RWMutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	value    json.RawMessage
	hasValue bool
	expires  time.Time
}

// NewSettingsCache returns a cache with the supplied DB reader and TTL.
// A nil reader is legal — the cache falls through to the registry
// default on every lookup, which is the right behaviour for tests and
// the bootstrap-pre-DB window.
func NewSettingsCache(reader SettingsReader, ttl time.Duration) *SettingsCache {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &SettingsCache{
		reader:  reader,
		ttl:     ttl,
		entries: make(map[string]cacheEntry),
	}
}

// Invalidate removes a single key. Called by PUT / DELETE on the
// settings handler.
func (c *SettingsCache) Invalidate(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// Flush drops every cached entry. Test-only convenience.
func (c *SettingsCache) Flush() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]cacheEntry)
}

// BoolValue returns the current boolean value for `key`, or the default
// if the registry doesn't list it, the DB has no row, or the cached
// value isn't a bool. Errors from the DB are NOT propagated — the
// caller (FeatureGate) treats them as "fall back to default" so a
// flaky DB doesn't 503 every API request.
func (c *SettingsCache) BoolValue(ctx context.Context, key string, fallback bool) bool {
	raw, ok := c.lookup(ctx, key)
	if !ok {
		return fallback
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return fallback
	}
	return v
}

// StringValue is the string companion to BoolValue. Same fallback
// semantics.
func (c *SettingsCache) StringValue(ctx context.Context, key string, fallback string) string {
	raw, ok := c.lookup(ctx, key)
	if !ok {
		return fallback
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return fallback
	}
	return v
}

// IntValue is the int companion. JSON Numbers are accepted; if the
// stored value is a float (e.g. operator put `60.5`) the integer
// truncation is intentional — the registry's MinInt/MaxInt validation
// on PUT keeps us out of that case in practice.
func (c *SettingsCache) IntValue(ctx context.Context, key string, fallback int) int {
	raw, ok := c.lookup(ctx, key)
	if !ok {
		return fallback
	}
	var v json.Number
	if err := json.Unmarshal(raw, &v); err != nil {
		return fallback
	}
	i, err := v.Int64()
	if err != nil {
		f, ferr := v.Float64()
		if ferr != nil {
			return fallback
		}
		return int(f)
	}
	return int(i)
}

// lookup is the shared "read with cache" path. Returns the cached
// JSON value when the entry is still valid; otherwise reads from the
// DB and refreshes. A DB row that's missing or returns ErrNoRows is
// cached as `hasValue=false` for the same TTL so a missing row
// doesn't generate per-request DB traffic either.
func (c *SettingsCache) lookup(ctx context.Context, key string) (json.RawMessage, bool) {
	if c == nil {
		return nil, false
	}
	now := time.Now()
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if ok && entry.expires.After(now) {
		if !entry.hasValue {
			return nil, false
		}
		return entry.value, true
	}
	// Either no entry or expired. Refresh under a write lock —
	// duplicate refreshes are tolerable (the DB read is cheap and the
	// alternative is a contended singleflight that isn't worth the
	// code for this cache's tiny footprint).
	if c.reader == nil {
		c.mu.Lock()
		c.entries[key] = cacheEntry{expires: now.Add(c.ttl)}
		c.mu.Unlock()
		return nil, false
	}
	row, err := c.reader.GetPlatformSetting(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.mu.Lock()
			c.entries[key] = cacheEntry{expires: now.Add(c.ttl)}
			c.mu.Unlock()
		}
		// Other errors: do NOT cache — next request will retry. The
		// caller is expected to use the fallback in this case.
		return nil, false
	}
	c.mu.Lock()
	c.entries[key] = cacheEntry{value: row.Value, hasValue: true, expires: now.Add(c.ttl)}
	c.mu.Unlock()
	return row.Value, true
}

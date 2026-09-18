package auth

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/cacheinvalidate"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// APITokenAuthentication is the non-secret authentication projection cached
// for a successfully resolved API-token hash. The plaintext credential is
// never retained. TokenHash is already a one-way SHA-256 digest and is used as
// the cache key rather than duplicated in this value.
type APITokenAuthentication struct {
	Token    sqlc.ApiToken
	Identity SessionIdentity
}

type apiTokenAuthenticationCacheEntry struct {
	authentication APITokenAuthentication
	expiresAt      time.Time
	activityAt     time.Time
}

// ResolveAPITokenAuthentication resolves a token and its active owner once per
// short validation window. Concurrent misses for the same token hash are
// coalesced so an expiry boundary cannot turn one hot automation credential
// into a PostgreSQL stampede. As with JWT verdicts, positive-cache hits are
// bypassed while distributed invalidation is unhealthy.
//
// The resolver runs independently of the first waiter's cancellation within a
// strict two-second bound; each waiter still observes its own cancellation. A
// concurrent local invalidation advances apiTokenEpoch and forces one fresh
// read before the flight may return, preventing a pre-revocation lookup from
// repopulating the cache after the committed invalidation.
func (m *JWTManager) ResolveAPITokenAuthentication(
	ctx context.Context,
	tokenHash string,
	resolve func(context.Context) (APITokenAuthentication, error),
) (APITokenAuthentication, error) {
	if m == nil || tokenHash == "" || resolve == nil {
		return APITokenAuthentication{}, errors.New("API token authentication cache is not configured")
	}
	if authentication, ok := m.cachedAPITokenAuthentication(tokenHash); ok {
		return authentication, nil
	}

	result := m.apiTokenGroup.DoChan(tokenHash, func() (any, error) {
		if authentication, ok := m.cachedAPITokenAuthentication(tokenHash); ok {
			return authentication, nil
		}
		resolveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()

		epoch := m.currentAPITokenEpoch()
		authentication, err := resolve(resolveCtx)
		if err != nil {
			return APITokenAuthentication{}, err
		}
		if err := validateAPITokenAuthentication(authentication); err != nil {
			return APITokenAuthentication{}, err
		}
		authentication.Token.TokenHash = ""
		if m.currentAPITokenEpoch() != epoch {
			// A revocation or user-state mutation committed while the first read
			// was in flight. Re-read against the post-invalidation database state
			// instead of admitting or caching the stale result.
			epoch = m.currentAPITokenEpoch()
			authentication, err = resolve(resolveCtx)
			if err != nil {
				return APITokenAuthentication{}, err
			}
			if err := validateAPITokenAuthentication(authentication); err != nil {
				return APITokenAuthentication{}, err
			}
			authentication.Token.TokenHash = ""
		}
		m.cacheAPITokenAuthentication(tokenHash, authentication, epoch)
		return authentication, nil
	})

	select {
	case <-ctx.Done():
		return APITokenAuthentication{}, ctx.Err()
	case resolved := <-result:
		if resolved.Err != nil {
			return APITokenAuthentication{}, resolved.Err
		}
		authentication, ok := resolved.Val.(APITokenAuthentication)
		if !ok {
			return APITokenAuthentication{}, errors.New("resolved API token authentication has invalid type")
		}
		return authentication, nil
	}
}

func validateAPITokenAuthentication(authentication APITokenAuthentication) error {
	if authentication.Token.ID == uuid.Nil || authentication.Token.UserID == uuid.Nil {
		return errors.New("resolved API token is missing identity")
	}
	if authentication.Token.IsRevoked {
		return errors.New("resolved API token is revoked")
	}
	if authentication.Identity.UserID == uuid.Nil || authentication.Identity.UserID != authentication.Token.UserID {
		return errors.New("resolved API token owner does not match token")
	}
	if !authentication.Identity.IsActive {
		return errors.New("resolved API token owner is inactive")
	}
	return nil
}

func (m *JWTManager) cachedAPITokenAuthentication(tokenHash string) (APITokenAuthentication, bool) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if m.cacheTTL <= 0 || m.cacheMaxEntries <= 0 {
		return APITokenAuthentication{}, false
	}
	if m.cacheCoordinator != nil && !m.cacheCoordinator.Healthy() {
		cacheinvalidate.RecordBypass("api_token", "coordinator_unhealthy")
		return APITokenAuthentication{}, false
	}
	entry, ok := m.apiTokenCache[tokenHash]
	if !ok {
		return APITokenAuthentication{}, false
	}
	if !entry.expiresAt.After(time.Now()) {
		delete(m.apiTokenCache, tokenHash)
		return APITokenAuthentication{}, false
	}
	return entry.authentication, true
}

func (m *JWTManager) cacheAPITokenAuthentication(tokenHash string, authentication APITokenAuthentication, epoch uint64) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if epoch != m.apiTokenEpoch || m.cacheTTL <= 0 || m.cacheMaxEntries <= 0 ||
		(m.cacheCoordinator != nil && !m.cacheCoordinator.Healthy()) {
		return
	}
	now := time.Now()
	if len(m.apiTokenCache) >= m.cacheMaxEntries {
		m.evictAPITokenAuthenticationLocked(now)
	}
	m.apiTokenCache[tokenHash] = apiTokenAuthenticationCacheEntry{
		authentication: authentication,
		expiresAt:      now.Add(m.cacheTTL),
	}
}

func (m *JWTManager) currentAPITokenEpoch() uint64 {
	m.cacheMu.RLock()
	defer m.cacheMu.RUnlock()
	return m.apiTokenEpoch
}

// ClaimAPITokenActivity returns true for exactly one successful request per
// cached validation window. The caller uses that claim to persist best-effort
// last_used_at and last_seen_remote_ip stamps without turning those
// bookkeeping writes into a per-request database cost. A missing cache entry
// fails open for telemetry:
// authentication has already succeeded, so retaining the historical write is
// safer than silently losing activity evidence.
func (m *JWTManager) ClaimAPITokenActivity(tokenHash string) bool {
	if m == nil || tokenHash == "" {
		return true
	}
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	entry, ok := m.apiTokenCache[tokenHash]
	if !ok || !entry.expiresAt.After(time.Now()) {
		return true
	}
	if !entry.activityAt.IsZero() {
		return false
	}
	entry.activityAt = time.Now()
	m.apiTokenCache[tokenHash] = entry
	return true
}

func (m *JWTManager) evictAPITokenAuthenticationLocked(now time.Time) {
	var earliestHash string
	var earliestExpiry time.Time
	for tokenHash, entry := range m.apiTokenCache {
		if !entry.expiresAt.After(now) {
			delete(m.apiTokenCache, tokenHash)
			continue
		}
		if earliestHash == "" || entry.expiresAt.Before(earliestExpiry) {
			earliestHash = tokenHash
			earliestExpiry = entry.expiresAt
		}
	}
	if len(m.apiTokenCache) >= m.cacheMaxEntries && earliestHash != "" {
		delete(m.apiTokenCache, earliestHash)
	}
}

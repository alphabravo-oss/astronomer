package auth

import "github.com/google/uuid"

// InvalidateJWTJTILocal removes one cached JWT verdict without broadcasting.
// Distributed coordinators call this after receiving a sibling's invalidation.
func (m *JWTManager) InvalidateJWTJTILocal(jti string) {
	if m == nil || jti == "" {
		return
	}
	m.cacheMu.Lock()
	delete(m.cache, jti)
	m.cacheMu.Unlock()
}

// InvalidateJWTUserLocal clears every cached authentication projection for a
// user. Advancing the API-token epoch also prevents an in-flight lookup from
// repopulating a pre-invalidation value.
func (m *JWTManager) InvalidateJWTUserLocal(userID string) {
	if m == nil || userID == "" {
		return
	}
	id, err := uuid.Parse(userID)
	if err != nil {
		return
	}
	m.cacheMu.Lock()
	for jti, entry := range m.cache {
		if entry.userID == id {
			delete(m.cache, jti)
		}
	}
	for tokenHash, entry := range m.apiTokenCache {
		if entry.authentication.Identity.UserID == id {
			delete(m.apiTokenCache, tokenHash)
		}
	}
	m.apiTokenEpoch++
	m.cacheMu.Unlock()
}

// InvalidateJWTAllLocal drops all positive authentication caches and advances
// the token epoch so no in-flight API-token lookup can restore stale state.
func (m *JWTManager) InvalidateJWTAllLocal() {
	if m == nil {
		return
	}
	m.cacheMu.Lock()
	m.cache = make(map[string]validationCacheEntry)
	m.apiTokenCache = make(map[string]apiTokenAuthenticationCacheEntry)
	m.apiTokenEpoch++
	m.cacheMu.Unlock()
}

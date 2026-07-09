package auth

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TokenType distinguishes access from refresh tokens
type TokenType string

const (
	AccessToken  TokenType = "access"
	RefreshToken TokenType = "refresh"
	// PurposeToken is the marker for a "purpose-bound" short-lived
	// challenge JWT — used today by the 2FA flow to keep the post-
	// bcrypt user identity around without yet issuing a full session.
	// The Purpose claim carries the specific use (e.g.
	// "totp_challenge"); validators check both that and the regular
	// signature/expiry.
	PurposeToken TokenType = "purpose"
)

// TOTPChallengeTTL bounds the lifetime of a /auth/login -> /auth/totp/verify
// handshake. Long enough that a user who fumbled their phone has time to
// retype; short enough that a stolen token from a network capture isn't
// useful by the time it's noticed.
const TOTPChallengeTTL = 5 * time.Minute

// Well-known Purpose values. Stringly-typed so the package boundary is
// the only thing callers need to import.
const (
	PurposeTOTPChallenge = "totp_challenge"
	// PurposeTOTPEnrollOnly is the "you must enroll before doing
	// anything else" challenge used when auth.totp.require=true and a
	// not-yet-enrolled user logs in. Holders can only POST to the
	// enrollment-start / enrollment-confirm endpoints — the regular
	// auth middleware does NOT accept it as a session token.
	PurposeTOTPEnrollOnly = "totp_enroll_only"
)

// Claims represents the JWT claims for Astronomer tokens
type Claims struct {
	jwt.RegisteredClaims
	UserID    uuid.UUID `json:"user_id"`
	TokenType TokenType `json:"token_type"`
	// Purpose narrows what a PurposeToken is allowed to do. Empty on
	// regular access / refresh tokens. The verify handler is the only
	// thing that should accept a non-empty Purpose; the regular auth
	// middleware rejects any token whose TokenType is PurposeToken so
	// the challenge can't be replayed as a session.
	Purpose string `json:"purpose,omitempty"`
}

// RevocationChecker is the optional dependency the JWTManager consults on
// every ValidateToken call to enforce the session-revocation deny list
// and the per-user invalidation cutoff. Both checks are skipped when the
// dependency is nil (e.g. the unit tests for JWTManager itself).
//
// IsJWTRevoked returns true when a specific JTI is on the deny list (set
// by Logout / force-logout). UserTokensInvalidatedAt returns the
// per-user "invalidate everything before this timestamp" cutoff and a
// boolean indicating whether the cutoff is set. When both the cutoff is
// set AND the token's iat is before it, the token is rejected.
type RevocationChecker interface {
	IsJWTRevoked(ctx context.Context, jti string) (bool, error)
	UserTokensInvalidatedAt(ctx context.Context, userID uuid.UUID) (time.Time, bool, error)
}

// JWTManager handles JWT token generation and validation. It supports
// multi-key rotation: the primary key signs new tokens; all configured
// keys can validate existing tokens. The single-string form keeps
// existing callers working unchanged.
type JWTManager struct {
	secretKeys           [][]byte // primary first
	accessTokenLifetime  time.Duration
	refreshTokenLifetime time.Duration

	// accessTTLProvider, when set, is consulted on every access-token mint
	// (DIR-05 / AUTH-R02) so platform setting session.timeout_minutes is
	// applied for password login, SSO, TOTP complete, and refresh without
	// each caller remembering to re-read settings. Returning <=0 keeps the
	// configured accessTokenLifetime.
	accessTTLMu       sync.RWMutex
	accessTTLProvider func(ctx context.Context) time.Duration

	// revocations is the deny-list backend. Optional — when nil, only
	// signature + expiry checks run. The HTTP layer attaches it via
	// SetRevocationChecker after construction so the auth package
	// doesn't need to depend on sqlc.
	revMu       sync.RWMutex
	revocations RevocationChecker

	// Validation result cache. ValidateToken is on the hot path (every
	// authenticated request) and would otherwise add a DB round-trip
	// per request. The cache stores positive verdicts (token still
	// valid) for a short TTL; negative verdicts are NEVER cached — a
	// freshly-revoked token must be rejected on its next use.
	cacheMu  sync.RWMutex
	cacheTTL time.Duration
	cache    map[string]validationCacheEntry
}

// JWTValidationCacheTTL is the default TTL for the "this JTI is still
// valid" cache. Kept short so a revocation that happens AFTER a positive
// cache hit becomes visible within seconds. The cache key is the JTI;
// negative outcomes are never cached so a revoke takes effect on the
// next request.
const JWTValidationCacheTTL = 30 * time.Second

type validationCacheEntry struct {
	expiresAt time.Time
}

// NewJWTManager creates a new JWT manager. secretKey is a comma-separated
// list of HMAC signing keys; the first is the primary (used to sign new
// tokens) and any additional entries are tried only on validation. This
// makes safe online key rotation possible (see docs/secret-rotation-runbook.md):
//
//  1. add the new key as primary (secretKey="<new>,<old>")
//  2. restart — new tokens are signed under <new>; existing tokens still
//     validate because <old> remains in the validator list
//  3. wait out the longest token lifetime (refresh = 7d) so every active
//     session has been re-issued under <new>
//  4. drop the old key from config on the next restart
func NewJWTManager(secretKey string, accessLifetimeMinutes int) *JWTManager {
	if accessLifetimeMinutes <= 0 {
		accessLifetimeMinutes = 60 // default 60 min
	}
	var keys [][]byte
	for _, raw := range strings.Split(secretKey, ",") {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		keys = append(keys, []byte(s))
	}
	if len(keys) == 0 {
		// Preserve the legacy zero-key behavior; callers that hand in an
		// empty string used to get a manager with []byte("") which signed
		// and validated tokens but produced trivially-forgeable JWTs in
		// dev. We keep the same shape so existing tests don't regress.
		keys = [][]byte{[]byte(secretKey)}
	}
	return &JWTManager{
		secretKeys:           keys,
		accessTokenLifetime:  time.Duration(accessLifetimeMinutes) * time.Minute,
		refreshTokenLifetime: 7 * 24 * time.Hour, // 7 days
		cacheTTL:             JWTValidationCacheTTL,
		cache:                make(map[string]validationCacheEntry),
	}
}

// SetRevocationChecker wires the JTI deny-list + per-user invalidation
// cutoff backend. The manager calls it on every ValidateToken; nil
// disables both checks (the default — useful for unit tests and the
// pre-DB bootstrap path).
func (m *JWTManager) SetRevocationChecker(c RevocationChecker) {
	if m == nil {
		return
	}
	m.revMu.Lock()
	m.revocations = c
	m.revMu.Unlock()
}

// SetValidationCacheTTL overrides the positive-result cache TTL. Pass 0
// to disable caching. Useful for tests that want to assert revocation
// is observed immediately without waiting out the default window.
func (m *JWTManager) SetValidationCacheTTL(d time.Duration) {
	if m == nil {
		return
	}
	m.cacheMu.Lock()
	m.cacheTTL = d
	m.cache = make(map[string]validationCacheEntry) // drop stale entries
	m.cacheMu.Unlock()
}

// SecretKey returns a defensive copy of the PRIMARY HMAC signing key so
// other auth helpers can bind short-lived browser state to the same
// application secret without sharing mutable backing storage. Callers
// that need to validate a token signed under a non-primary key should
// use ValidateToken instead of pinning the bytes directly.
func (m *JWTManager) SecretKey() []byte {
	if m == nil || len(m.secretKeys) == 0 || len(m.secretKeys[0]) == 0 {
		return nil
	}
	out := make([]byte, len(m.secretKeys[0]))
	copy(out, m.secretKeys[0])
	return out
}

// KeyCount reports how many JWT signing keys are loaded. Useful for
// /api/v1/admin diagnostics to confirm a rotation is mid-flight (>1) vs
// steady state (==1).
func (m *JWTManager) KeyCount() int {
	if m == nil {
		return 0
	}
	return len(m.secretKeys)
}

// GenerateTokenPair creates both access and refresh tokens for a user
func (m *JWTManager) GenerateTokenPair(userID uuid.UUID) (accessToken, refreshToken string, err error) {
	accessToken, err = m.GenerateAccessToken(userID)
	if err != nil {
		return "", "", fmt.Errorf("generating access token: %w", err)
	}

	refreshToken, err = m.GenerateRefreshToken(userID)
	if err != nil {
		return "", "", fmt.Errorf("generating refresh token: %w", err)
	}

	return accessToken, refreshToken, nil
}

// SetAccessTokenTTL updates the access-token lifetime used for subsequent
// mint/refresh. DIR-05: compliance baseline session.timeout_minutes is applied
// here at runtime without restarting the process.
func (m *JWTManager) SetAccessTokenTTL(d time.Duration) {
	if m == nil || d <= 0 {
		return
	}
	m.accessTTLMu.Lock()
	m.accessTokenLifetime = d
	m.accessTTLMu.Unlock()
}

// SetAccessTokenTTLProvider wires a runtime resolver (typically reading
// platform setting session.timeout_minutes). Consulted on every access-token
// mint so SSO/TOTP/password/refresh all honor the same absolute TTL.
func (m *JWTManager) SetAccessTokenTTLProvider(fn func(ctx context.Context) time.Duration) {
	if m == nil {
		return
	}
	m.accessTTLMu.Lock()
	m.accessTTLProvider = fn
	m.accessTTLMu.Unlock()
}

// AccessTokenTTL returns the current access-token lifetime (boot/default or
// last SetAccessTokenTTL value). Runtime provider overrides are applied only
// at mint time via effectiveAccessTTL.
func (m *JWTManager) AccessTokenTTL() time.Duration {
	if m == nil {
		return 0
	}
	m.accessTTLMu.RLock()
	defer m.accessTTLMu.RUnlock()
	return m.accessTokenLifetime
}

// effectiveAccessTTL returns the lifetime used for the next access token.
// Provider wins when it returns a positive duration (AUTH-R02).
func (m *JWTManager) effectiveAccessTTL(ctx context.Context) time.Duration {
	if m == nil {
		return 0
	}
	m.accessTTLMu.RLock()
	provider := m.accessTTLProvider
	base := m.accessTokenLifetime
	m.accessTTLMu.RUnlock()
	if provider != nil {
		if ctx == nil {
			ctx = context.Background()
		}
		if d := provider(ctx); d > 0 {
			return d
		}
	}
	return base
}

// GenerateAccessToken creates an access token
func (m *JWTManager) GenerateAccessToken(userID uuid.UUID) (string, error) {
	return m.generateToken(userID, AccessToken, m.effectiveAccessTTL(context.Background()))
}

// GenerateRefreshToken creates a refresh token
func (m *JWTManager) GenerateRefreshToken(userID uuid.UUID) (string, error) {
	return m.generateToken(userID, RefreshToken, m.refreshTokenLifetime)
}

// GeneratePurposeToken creates a short-lived JWT whose only legitimate use
// is the named `purpose` flow (e.g. PurposeTOTPChallenge). The validator
// in the consuming handler MUST check both signature validity AND the
// expected Purpose string — that's why the regular auth middleware refuses
// every PurposeToken regardless of signature: it stops a stolen challenge
// JWT from being replayed as a session token.
//
// `ttl` is bounded by the caller (typically TOTPChallengeTTL).
func (m *JWTManager) GeneratePurposeToken(userID uuid.UUID, purpose string, ttl time.Duration) (string, error) {
	if purpose == "" {
		return "", fmt.Errorf("purpose token requires a non-empty purpose")
	}
	if ttl <= 0 {
		ttl = TOTPChallengeTTL
	}
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.New().String(),
		},
		UserID:    userID,
		TokenType: PurposeToken,
		Purpose:   purpose,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secretKeys[0])
	if err != nil {
		return "", fmt.Errorf("signing purpose token: %w", err)
	}
	return signed, nil
}

// ValidateToken parses and validates a JWT token, returning the claims.
// When multiple keys are configured (rotation in flight), each is tried in
// order; the first that yields a valid signature wins.
//
// When a RevocationChecker is attached, ValidateToken additionally
// rejects:
//
//   - tokens whose JTI is on the deny list (logout / per-token revoke);
//   - tokens whose iat predates the user's tokens_invalidated_at cutoff
//     (admin force-logout).
//
// A short positive-result cache (default JWTValidationCacheTTL) covers
// the DB round-trips on the hot path. Negative verdicts are never
// cached so a fresh revocation takes effect on the next request.
func (m *JWTManager) ValidateToken(tokenString string) (*Claims, error) {
	return m.ValidateTokenContext(context.Background(), tokenString)
}

// ValidateTokenContext is the context-aware variant. The auth middleware
// passes the request context so DB round-trips inherit deadlines and
// cancellation. Existing callers of ValidateToken keep working —
// background context just means no cancellation.
func (m *JWTManager) ValidateTokenContext(ctx context.Context, tokenString string) (*Claims, error) {
	var lastErr error
	for _, key := range m.secretKeys {
		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return key, nil
		})
		if err != nil {
			lastErr = err
			continue
		}
		if !token.Valid {
			lastErr = fmt.Errorf("invalid token")
			continue
		}
		if claims.UserID == uuid.Nil {
			return nil, fmt.Errorf("invalid token: missing user_id claim")
		}
		if claims.TokenType == "" {
			return nil, fmt.Errorf("invalid token: missing token_type claim")
		}
		// Revocation checks — only run when a checker is attached AND
		// the cache says we haven't recently validated this JTI.
		if err := m.checkRevocations(ctx, claims); err != nil {
			return nil, err
		}
		return claims, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("invalid token")
	}
	return nil, fmt.Errorf("invalid token: %w", lastErr)
}

// checkRevocations enforces the JTI deny list + per-user invalidation
// cutoff. Short-circuited by the positive-result cache so repeated
// requests for the same JTI inside the TTL window only pay the DB
// cost once.
func (m *JWTManager) checkRevocations(ctx context.Context, claims *Claims) error {
	m.revMu.RLock()
	checker := m.revocations
	m.revMu.RUnlock()
	if checker == nil {
		return nil
	}
	jti := claims.ID

	// Positive-cache hit: we've recently confirmed this JTI is valid.
	if jti != "" && m.cacheHit(jti) {
		return nil
	}

	if jti != "" {
		revoked, err := checker.IsJWTRevoked(ctx, jti)
		if err != nil {
			// Failing closed (rejecting) on a DB error would lock
			// the entire fleet out the moment Postgres hiccups.
			// Failing open is the conventional auth-middleware
			// choice — the bcrypt/JWT signature gate already
			// guarantees the token was issued by us; the revoke
			// list is an additional layer that's acceptable to
			// briefly bypass.
			//
			// We do NOT cache this outcome — the next request will
			// retry.
			return nil
		}
		if revoked {
			return fmt.Errorf("invalid token: token revoked")
		}
	}

	cutoff, set, err := checker.UserTokensInvalidatedAt(ctx, claims.UserID)
	if err != nil {
		// Same fail-open rationale as above.
		return nil
	}
	if set && claims.IssuedAt != nil && !claims.IssuedAt.IsZero() {
		// iat predates the cutoff -> reject. Use !After so a token
		// issued at exactly the cutoff timestamp is rejected.
		if !claims.IssuedAt.After(cutoff) {
			return fmt.Errorf("invalid token: tokens invalidated for user")
		}
	}

	if jti != "" {
		m.cachePut(jti)
	}
	return nil
}

func (m *JWTManager) cacheHit(jti string) bool {
	m.cacheMu.RLock()
	entry, ok := m.cache[jti]
	ttl := m.cacheTTL
	m.cacheMu.RUnlock()
	if !ok || ttl <= 0 {
		return false
	}
	if time.Now().After(entry.expiresAt) {
		// Lazy eviction; the next put or invalidate will replace it.
		return false
	}
	return true
}

func (m *JWTManager) cachePut(jti string) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if m.cacheTTL <= 0 {
		return
	}
	m.cache[jti] = validationCacheEntry{expiresAt: time.Now().Add(m.cacheTTL)}
}

// InvalidateCache drops every entry from the positive-result cache.
// Called by the Logout / force-logout paths so an in-flight cached
// validation doesn't survive the revocation. Cheap — the cache is
// usually <1k entries.
func (m *JWTManager) InvalidateCache() {
	if m == nil {
		return
	}
	m.cacheMu.Lock()
	m.cache = make(map[string]validationCacheEntry)
	m.cacheMu.Unlock()
}

// generateToken is the internal token generation helper
func (m *JWTManager) generateToken(userID uuid.UUID, tokenType TokenType, lifetime time.Duration) (string, error) {
	now := time.Now()

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(lifetime)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.New().String(),
		},
		UserID:    userID,
		TokenType: tokenType,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString(m.secretKeys[0])
	if err != nil {
		return "", fmt.Errorf("signing token: %w", err)
	}

	return signedToken, nil
}

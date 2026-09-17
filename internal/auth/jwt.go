package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/alphabravocompany/astronomer-go/internal/cacheinvalidate"
	"github.com/alphabravocompany/astronomer-go/internal/sessionpolicy"
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
	UserID          uuid.UUID `json:"user_id"`
	TokenType       TokenType `json:"token_type"`
	SessionFamilyID uuid.UUID `json:"session_family_id,omitempty"`
	// BrowserSession marks cookie sessions whose family is checked against
	// durable storage. Non-browser/internal access JWTs are the explicit
	// exception; opaque API tokens use a separate authentication boundary.
	BrowserSession bool `json:"browser_session,omitempty"`
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
	IsSessionFamilyRevoked(ctx context.Context, familyID uuid.UUID) (bool, error)
	UserTokensInvalidatedAt(ctx context.Context, userID uuid.UUID) (time.Time, bool, error)
}

// ErrRevocationUnavailable identifies an authentication dependency failure.
// Callers must fail closed and may surface a retryable 503, but must not treat
// the token as authenticated while revocation state is unknown.
var ErrRevocationUnavailable = errors.New("token revocation state unavailable")

// JWTManager handles JWT token generation and validation. It supports
// multi-key rotation: the primary key signs new tokens; all configured
// keys can validate existing tokens. The single-string form keeps
// existing callers working unchanged.
type JWTManager struct {
	secretKeys           [][]byte // primary first
	issuer               string
	audience             string
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
	cacheMu          sync.RWMutex
	cacheTTL         time.Duration
	cacheMaxEntries  int
	cache            map[string]validationCacheEntry
	cacheCoordinator cacheinvalidate.Broadcaster
	validationGroup  singleflight.Group
	identityGroup    singleflight.Group
	apiTokenGroup    singleflight.Group
	apiTokenCache    map[string]apiTokenAuthenticationCacheEntry
	apiTokenEpoch    uint64
}

// JWTValidationCacheTTL is the default TTL for the "this JTI is still
// valid" cache. Kept short so a revocation that happens AFTER a positive
// cache hit becomes visible within seconds. The cache key is the JTI;
// negative outcomes are never cached so a revoke takes effect on the
// next request.
const (
	JWTValidationCacheTTL        = 30 * time.Second
	JWTValidationCacheMaxEntries = 10_000
	DefaultJWTIssuer             = "astronomer"
	DefaultJWTAudience           = "astronomer-browser"
)

// JWTConfig is the typed trust context for Astronomer-issued JWTs. Issuer and
// audience are mandatory in production so a token signed with shared key
// material cannot cross an application or deployment boundary accidentally.
type JWTConfig struct {
	SecretKey             string
	AccessLifetimeMinutes int
	Issuer                string
	Audience              string
}

type validationCacheEntry struct {
	expiresAt   time.Time
	userID      uuid.UUID
	identity    SessionIdentity
	hasIdentity bool
}

// SessionIdentity is the non-sensitive user projection authentication needs
// after a JWT has passed signature, expiry, and revocation validation. It is
// kept in the same short-lived, bounded, cross-replica-invalidated cache as the
// positive revocation verdict. This avoids rereading the users row on every
// request while preserving the existing immediate invalidation path for user
// deactivation, deletion, password reset, force logout, and SCIM suspension.
// Password hashes, lockout state, and other user fields never enter the cache.
type SessionIdentity struct {
	UserID             uuid.UUID
	Email              string
	Username           string
	FirstName          string
	LastName           string
	IsActive           bool
	IsStaff            bool
	IsSuperuser        bool
	MustChangePassword bool
	DateJoined         time.Time
	LastLogin          time.Time
	HasLastLogin       bool
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
//
// An empty secretKey is an error: a manager built from one used to sign with a
// zero-length HMAC key, which every reader of this repository can reproduce
// (dev-keys-default-and-silent).
func NewJWTManager(secretKey string, accessLifetimeMinutes int) (*JWTManager, error) {
	return NewJWTManagerWithConfig(JWTConfig{
		SecretKey: secretKey, AccessLifetimeMinutes: accessLifetimeMinutes,
		Issuer: DefaultJWTIssuer, Audience: DefaultJWTAudience,
	})
}

// NewJWTManagerWithConfig constructs a manager with an explicit token trust
// context. The compatibility constructor above uses the documented defaults;
// production composition passes typed configuration here directly.
func NewJWTManagerWithConfig(cfg JWTConfig) (*JWTManager, error) {
	accessLifetimeMinutes := cfg.AccessLifetimeMinutes
	if accessLifetimeMinutes < sessionpolicy.MinMinutes || accessLifetimeMinutes > sessionpolicy.MaxMinutes {
		accessLifetimeMinutes = sessionpolicy.DefaultMinutes
	}
	var keys [][]byte
	for _, raw := range strings.Split(cfg.SecretKey, ",") {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		keys = append(keys, []byte(s))
	}
	if len(keys) == 0 {
		return nil, errors.New("jwt: secret key is empty; set SECRET_KEY (chart: secrets.secretKey) to real signing material")
	}
	issuer := strings.TrimSpace(cfg.Issuer)
	if issuer == "" {
		return nil, errors.New("jwt: issuer is empty; set JWT_ISSUER")
	}
	audience := strings.TrimSpace(cfg.Audience)
	if audience == "" {
		return nil, errors.New("jwt: audience is empty; set JWT_AUDIENCE")
	}
	return &JWTManager{
		secretKeys:           keys,
		issuer:               issuer,
		audience:             audience,
		accessTokenLifetime:  time.Duration(accessLifetimeMinutes) * time.Minute,
		refreshTokenLifetime: 7 * 24 * time.Hour, // 7 days
		cacheTTL:             JWTValidationCacheTTL,
		cacheMaxEntries:      JWTValidationCacheMaxEntries,
		cache:                make(map[string]validationCacheEntry),
		apiTokenCache:        make(map[string]apiTokenAuthenticationCacheEntry),
	}, nil
}

// MustNewJWTManager is NewJWTManager for callers holding a compile-time signing
// key (tests, fixtures). It panics rather than returning an error.
func MustNewJWTManager(secretKey string, accessLifetimeMinutes int) *JWTManager {
	m, err := NewJWTManager(secretKey, accessLifetimeMinutes)
	if err != nil {
		panic(err)
	}
	return m
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

// HasRevocationChecker reports whether session validation has a durable
// revocation dependency. Production composition uses it to reject partial
// authentication wiring before serving requests.
func (m *JWTManager) HasRevocationChecker() bool {
	if m == nil {
		return false
	}
	m.revMu.RLock()
	defer m.revMu.RUnlock()
	return m.revocations != nil
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
	m.apiTokenCache = make(map[string]apiTokenAuthenticationCacheEntry)
	m.apiTokenEpoch++
	m.cacheMu.Unlock()
}

// SetValidationCacheMaxEntries bounds the positive-verdict cache. Values below
// one disable positive caching. Production uses JWTValidationCacheMaxEntries;
// the setter keeps capacity behavior deterministic in tests.
func (m *JWTManager) SetValidationCacheMaxEntries(maxEntries int) {
	if m == nil {
		return
	}
	m.cacheMu.Lock()
	m.cacheMaxEntries = maxEntries
	m.cache = make(map[string]validationCacheEntry)
	m.apiTokenCache = make(map[string]apiTokenAuthenticationCacheEntry)
	m.apiTokenEpoch++
	m.cacheMu.Unlock()
}

// SetCacheInvalidationCoordinator enables cross-replica invalidation and
// fail-closed positive-cache bypass while coordinator health is degraded.
func (m *JWTManager) SetCacheInvalidationCoordinator(c cacheinvalidate.Broadcaster) {
	if m == nil {
		return
	}
	m.cacheMu.Lock()
	m.cacheCoordinator = c
	m.cache = make(map[string]validationCacheEntry)
	m.apiTokenCache = make(map[string]apiTokenAuthenticationCacheEntry)
	m.apiTokenEpoch++
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
	return m.GenerateTokenPairContext(context.Background(), userID)
}

// GenerateTokenPairContext is the request-aware session mint used by every
// interactive authentication path. The context is propagated to the runtime
// access-TTL provider so settings reads inherit request cancellation and
// deadlines.
func (m *JWTManager) GenerateTokenPairContext(ctx context.Context, userID uuid.UUID) (accessToken, refreshToken string, err error) {
	pair, err := m.PrepareTokenPairContext(ctx)
	if err != nil {
		return "", "", fmt.Errorf("preparing token pair: %w", err)
	}
	accessToken, refreshToken, err = m.SignPreparedTokenPair(userID, pair)
	if err != nil {
		return "", "", fmt.Errorf("signing token pair: %w", err)
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
	return m.GenerateAccessTokenContext(context.Background(), userID)
}

// GenerateAccessTokenContext is the context-aware access-token mint. Session
// handlers use this through GenerateTokenPairContext; the context-free method
// remains for non-request callers and compatibility.
func (m *JWTManager) GenerateAccessTokenContext(ctx context.Context, userID uuid.UUID) (string, error) {
	return m.generateToken(userID, AccessToken, m.effectiveAccessTTL(ctx), uuid.New(), false)
}

// GenerateRefreshToken creates a refresh token
func (m *JWTManager) GenerateRefreshToken(userID uuid.UUID) (string, error) {
	return m.generateToken(userID, RefreshToken, m.refreshTokenLifetime, uuid.New(), true)
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
		RegisteredClaims: m.registeredClaims(userID, uuid.NewString(), now, now.Add(ttl)),
		UserID:           userID,
		TokenType:        PurposeToken,
		Purpose:          purpose,
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
			if token.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return key, nil
		}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithIssuer(m.issuer),
			jwt.WithAudience(m.audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
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
		if claims.Subject != claims.UserID.String() {
			return nil, fmt.Errorf("invalid token: subject does not match user_id")
		}
		if claims.ID == "" {
			return nil, fmt.Errorf("invalid token: missing jti claim")
		}
		switch claims.TokenType {
		case AccessToken, RefreshToken:
			if claims.SessionFamilyID == uuid.Nil {
				return nil, fmt.Errorf("invalid token: missing session family")
			}
			if claims.Purpose != "" {
				return nil, fmt.Errorf("invalid token: session token has purpose claim")
			}
			if claims.TokenType == RefreshToken && !claims.BrowserSession {
				return nil, fmt.Errorf("invalid token: refresh token is not a browser session")
			}
		case PurposeToken:
			if strings.TrimSpace(claims.Purpose) == "" {
				return nil, fmt.Errorf("invalid token: purpose token has no purpose")
			}
			if claims.SessionFamilyID != uuid.Nil {
				return nil, fmt.Errorf("invalid token: purpose token has session family")
			}
		default:
			return nil, fmt.Errorf("invalid token: unsupported token_type claim")
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
		result := m.validationGroup.DoChan(jti, func() (any, error) {
			// A request may have populated the cache while this call waited for
			// the per-JTI flight. Recheck before touching PostgreSQL.
			if m.cacheHit(jti) {
				return nil, nil
			}
			resolveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			return nil, m.checkRevocationsUncached(resolveCtx, claims, checker, jti)
		})
		select {
		case <-ctx.Done():
			return ctx.Err()
		case resolved := <-result:
			return resolved.Err
		}
	}
	return m.checkRevocationsUncached(ctx, claims, checker, jti)
}

func (m *JWTManager) checkRevocationsUncached(ctx context.Context, claims *Claims, checker RevocationChecker, jti string) error {
	if jti != "" {
		revoked, err := checker.IsJWTRevoked(ctx, jti)
		if err != nil {
			return fmt.Errorf("%w: token lookup", ErrRevocationUnavailable)
		}
		if revoked {
			return fmt.Errorf("invalid token: token revoked")
		}
	}
	if claims.BrowserSession {
		revoked, err := checker.IsSessionFamilyRevoked(ctx, claims.SessionFamilyID)
		if err != nil {
			return fmt.Errorf("%w: session family lookup", ErrRevocationUnavailable)
		}
		if revoked {
			return fmt.Errorf("invalid token: session family revoked")
		}
	}

	cutoff, set, err := checker.UserTokensInvalidatedAt(ctx, claims.UserID)
	if err != nil {
		return fmt.Errorf("%w: user cutoff lookup", ErrRevocationUnavailable)
	}
	if set && claims.IssuedAt != nil && !claims.IssuedAt.IsZero() {
		// iat predates the cutoff -> reject. Use !After so a token
		// issued at exactly the cutoff timestamp is rejected.
		if !claims.IssuedAt.After(cutoff) {
			return fmt.Errorf("invalid token: tokens invalidated for user")
		}
	}

	if jti != "" {
		m.cachePut(jti, claims.UserID)
	}
	return nil
}

func (m *JWTManager) cacheHit(jti string) bool {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	entry, ok := m.cache[jti]
	ttl := m.cacheTTL
	coordinator := m.cacheCoordinator
	if coordinator != nil && !coordinator.Healthy() {
		cacheinvalidate.RecordBypass("jwt", "coordinator_unhealthy")
		return false
	}
	if !ok || ttl <= 0 {
		return false
	}
	if time.Now().After(entry.expiresAt) {
		delete(m.cache, jti)
		return false
	}
	return true
}

func (m *JWTManager) cachePut(jti string, userID uuid.UUID) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if m.cacheTTL <= 0 || m.cacheMaxEntries <= 0 || (m.cacheCoordinator != nil && !m.cacheCoordinator.Healthy()) {
		return
	}
	now := time.Now()
	if len(m.cache) >= m.cacheMaxEntries {
		m.evictCacheEntryLocked(now)
	}
	m.cache[jti] = validationCacheEntry{expiresAt: now.Add(m.cacheTTL), userID: userID}
}

// CachedSessionIdentity returns the user projection previously resolved for a
// validated JTI. Cache hits are disabled whenever distributed invalidation is
// unhealthy, matching the revocation-verdict cache's fail-closed behavior.
func (m *JWTManager) CachedSessionIdentity(jti string) (SessionIdentity, bool) {
	if m == nil || jti == "" {
		return SessionIdentity{}, false
	}
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if m.cacheTTL <= 0 || m.cacheMaxEntries <= 0 {
		return SessionIdentity{}, false
	}
	if m.cacheCoordinator != nil && !m.cacheCoordinator.Healthy() {
		cacheinvalidate.RecordBypass("jwt", "coordinator_unhealthy")
		return SessionIdentity{}, false
	}
	entry, ok := m.cache[jti]
	if !ok {
		return SessionIdentity{}, false
	}
	if !entry.expiresAt.After(time.Now()) {
		delete(m.cache, jti)
		return SessionIdentity{}, false
	}
	if !entry.hasIdentity || entry.identity.UserID == uuid.Nil || entry.identity.UserID != entry.userID {
		return SessionIdentity{}, false
	}
	return entry.identity, true
}

// CacheSessionIdentity attaches an authoritative active-user lookup to the
// positive JTI cache. The entry is bounded by the normal validation TTL and
// participates in the same per-JTI, per-user, and global invalidation paths.
func (m *JWTManager) CacheSessionIdentity(jti string, identity SessionIdentity) {
	if m == nil || jti == "" || identity.UserID == uuid.Nil {
		return
	}
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if m.cacheTTL <= 0 || m.cacheMaxEntries <= 0 || (m.cacheCoordinator != nil && !m.cacheCoordinator.Healthy()) {
		return
	}
	now := time.Now()
	entry, ok := m.cache[jti]
	if ok && (!entry.expiresAt.After(now) || entry.userID != identity.UserID) {
		delete(m.cache, jti)
		ok = false
	}
	if !ok {
		if len(m.cache) >= m.cacheMaxEntries {
			m.evictCacheEntryLocked(now)
		}
		entry = validationCacheEntry{expiresAt: now.Add(m.cacheTTL), userID: identity.UserID}
	}
	entry.identity = identity
	entry.hasIdentity = true
	m.cache[jti] = entry
}

// ResolveSessionIdentity returns the cached identity for jti or coalesces
// concurrent cache misses into one authoritative lookup. The lookup outlives
// cancellation of the first waiter (within a strict two-second bound), while
// every waiting request still observes its own cancellation promptly.
func (m *JWTManager) ResolveSessionIdentity(ctx context.Context, jti string, userID uuid.UUID, resolve func(context.Context) (SessionIdentity, error)) (SessionIdentity, error) {
	if identity, ok := m.CachedSessionIdentity(jti); ok && identity.UserID == userID {
		return identity, nil
	}
	if resolve == nil {
		return SessionIdentity{}, errors.New("session identity resolver is not configured")
	}
	if jti == "" {
		return resolve(ctx)
	}
	result := m.identityGroup.DoChan(jti, func() (any, error) {
		if identity, ok := m.CachedSessionIdentity(jti); ok && identity.UserID == userID {
			return identity, nil
		}
		resolveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		identity, err := resolve(resolveCtx)
		if err != nil {
			return SessionIdentity{}, err
		}
		if identity.UserID == uuid.Nil || identity.UserID != userID {
			return SessionIdentity{}, errors.New("resolved session identity does not match token subject")
		}
		m.CacheSessionIdentity(jti, identity)
		return identity, nil
	})
	select {
	case <-ctx.Done():
		return SessionIdentity{}, ctx.Err()
	case resolved := <-result:
		if resolved.Err != nil {
			return SessionIdentity{}, resolved.Err
		}
		identity, ok := resolved.Val.(SessionIdentity)
		if !ok {
			return SessionIdentity{}, errors.New("resolved session identity has invalid type")
		}
		return identity, nil
	}
}

// evictCacheEntryLocked removes expired entries first, then the entry closest
// to expiry if the cache remains full. cacheMu must be held by the caller.
func (m *JWTManager) evictCacheEntryLocked(now time.Time) {
	var oldestJTI string
	var oldestExpiry time.Time
	for jti, entry := range m.cache {
		if !entry.expiresAt.After(now) {
			delete(m.cache, jti)
			continue
		}
		if oldestJTI == "" || entry.expiresAt.Before(oldestExpiry) {
			oldestJTI = jti
			oldestExpiry = entry.expiresAt
		}
	}
	if len(m.cache) >= m.cacheMaxEntries && oldestJTI != "" {
		delete(m.cache, oldestJTI)
	}
}

// InvalidateCache drops every entry from the positive-result cache.
// Called by the Logout / force-logout paths so an in-flight cached
// validation doesn't survive the revocation. Cheap — the cache is
// usually <1k entries.
func (m *JWTManager) InvalidateCache() {
	m.InvalidateJWTAllLocal()
}

// InvalidateJTI invalidates locally before broadcasting the committed JTI
// revocation to sibling replicas.
func (m *JWTManager) InvalidateJTI(ctx context.Context, jti string) {
	m.InvalidateJWTJTILocal(jti)
	if m == nil || jti == "" {
		return
	}
	m.cacheMu.RLock()
	coordinator := m.cacheCoordinator
	m.cacheMu.RUnlock()
	if coordinator != nil {
		_ = coordinator.Broadcast(ctx, cacheinvalidate.KindJWTJTI, jti)
	}
}

// InvalidateUser invalidates locally before broadcasting a committed user
// cutoff/password/deactivation mutation.
func (m *JWTManager) InvalidateUser(ctx context.Context, userID uuid.UUID) {
	m.InvalidateJWTUserLocal(userID.String())
	if m == nil || userID == uuid.Nil {
		return
	}
	m.cacheMu.RLock()
	coordinator := m.cacheCoordinator
	m.cacheMu.RUnlock()
	if coordinator != nil {
		_ = coordinator.Broadcast(ctx, cacheinvalidate.KindJWTUser, userID.String())
	}
}

// generateToken is the internal token generation helper
func (m *JWTManager) generateToken(userID uuid.UUID, tokenType TokenType, lifetime time.Duration, familyID uuid.UUID, browserSession bool) (string, error) {
	now := time.Now()

	claims := Claims{
		RegisteredClaims: m.registeredClaims(userID, uuid.NewString(), now, now.Add(lifetime)),
		UserID:           userID, TokenType: tokenType, SessionFamilyID: familyID, BrowserSession: browserSession,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signedToken, err := token.SignedString(m.secretKeys[0])
	if err != nil {
		return "", fmt.Errorf("signing token: %w", err)
	}

	return signedToken, nil
}

func (m *JWTManager) registeredClaims(userID uuid.UUID, id string, issuedAt, expiresAt time.Time) jwt.RegisteredClaims {
	return jwt.RegisteredClaims{
		Issuer: m.issuer, Subject: userID.String(), Audience: jwt.ClaimStrings{m.audience},
		ExpiresAt: jwt.NewNumericDate(expiresAt), NotBefore: jwt.NewNumericDate(issuedAt),
		IssuedAt: jwt.NewNumericDate(issuedAt), ID: id,
	}
}

package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/cacheinvalidate"
)

type fakeCacheCoordinator struct{ healthy bool }

func (f *fakeCacheCoordinator) Healthy() bool { return f.healthy }
func (f *fakeCacheCoordinator) Broadcast(context.Context, cacheinvalidate.Kind, string) error {
	return nil
}

// fakeRevocationChecker is the in-memory test backend for the
// JTI deny-list + per-user cutoff. Threadsafe so the cache-coherence
// tests can poke at it from sub-goroutines without racing.
type fakeRevocationChecker struct {
	mu          sync.Mutex
	revoked     map[string]bool
	families    map[uuid.UUID]bool
	cutoff      map[uuid.UUID]time.Time
	revErr      error
	familyErr   error
	cutoffErr   error
	revokeCalls int
}

func newFakeRevocationChecker() *fakeRevocationChecker {
	return &fakeRevocationChecker{
		revoked:  make(map[string]bool),
		families: make(map[uuid.UUID]bool),
		cutoff:   make(map[uuid.UUID]time.Time),
	}
}

func (f *fakeRevocationChecker) IsJWTRevoked(_ context.Context, jti string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revokeCalls++
	if f.revErr != nil {
		return false, f.revErr
	}
	return f.revoked[jti], nil
}

func (f *fakeRevocationChecker) IsSessionFamilyRevoked(_ context.Context, familyID uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.familyErr != nil {
		return false, f.familyErr
	}
	return f.families[familyID], nil
}

func (f *fakeRevocationChecker) UserTokensInvalidatedAt(_ context.Context, userID uuid.UUID) (time.Time, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cutoffErr != nil {
		return time.Time{}, false, f.cutoffErr
	}
	t, ok := f.cutoff[userID]
	return t, ok, nil
}

func (f *fakeRevocationChecker) revoke(jti string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked[jti] = true
}

func (f *fakeRevocationChecker) invalidate(userID uuid.UUID, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cutoff[userID] = at
}

func TestValidateTokenRejectsRevokedBrowserFamilyButExemptsNonBrowserAccess(t *testing.T) {
	mgr := MustNewJWTManager("family-revocation-secret", 15)
	mgr.SetValidationCacheTTL(0)
	checker := newFakeRevocationChecker()
	mgr.SetRevocationChecker(checker)
	userID := uuid.New()
	access, _, err := mgr.GenerateTokenPair(userID)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := mgr.ValidateToken(access)
	if err != nil {
		t.Fatal(err)
	}
	checker.mu.Lock()
	checker.families[claims.SessionFamilyID] = true
	checker.mu.Unlock()
	if _, err := mgr.ValidateToken(access); err == nil || !strings.Contains(err.Error(), "family revoked") {
		t.Fatalf("ValidateToken(revoked family) error = %v", err)
	}

	checker.mu.Lock()
	checker.familyErr = errors.New("family store unavailable")
	checker.mu.Unlock()
	serviceAccess, err := mgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatal(err)
	}
	if claims, err := mgr.ValidateToken(serviceAccess); err != nil || claims.BrowserSession {
		t.Fatalf("non-browser access validation = %#v, %v", claims, err)
	}
}

func TestValidateToken_RejectsRevokedJTI(t *testing.T) {
	mgr := MustNewJWTManager("test-secret", 60)
	mgr.SetValidationCacheTTL(0) // cache off so the second Validate sees the revocation immediately

	checker := newFakeRevocationChecker()
	mgr.SetRevocationChecker(checker)

	userID := uuid.New()
	token, err := mgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// First validate — token is fine.
	claims, err := mgr.ValidateToken(token)
	if err != nil {
		t.Fatalf("pre-revoke validate: %v", err)
	}
	if claims.UserID != userID {
		t.Fatalf("UserID = %v, want %v", claims.UserID, userID)
	}

	// Revoke this JTI.
	checker.revoke(claims.ID)

	// Validation should now fail with a "revoked" message.
	if _, err := mgr.ValidateToken(token); err == nil {
		t.Fatal("expected validate to fail post-revoke")
	}
}

func TestValidateToken_RejectsTokensIssuedBeforeInvalidation(t *testing.T) {
	mgr := MustNewJWTManager("test-secret", 60)
	mgr.SetValidationCacheTTL(0)
	checker := newFakeRevocationChecker()
	mgr.SetRevocationChecker(checker)

	userID := uuid.New()
	// Issue a token NOW, then set the invalidation cutoff to "after now".
	token, err := mgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	checker.invalidate(userID, time.Now().Add(time.Second)) // 1s in the future

	if _, err := mgr.ValidateToken(token); err == nil {
		t.Fatal("expected validate to fail for token issued before cutoff")
	}
}

func TestValidateToken_AcceptsNewTokensAfterInvalidation(t *testing.T) {
	mgr := MustNewJWTManager("test-secret", 60)
	mgr.SetValidationCacheTTL(0)
	checker := newFakeRevocationChecker()
	mgr.SetRevocationChecker(checker)

	userID := uuid.New()
	// Stamp the cutoff in the past — every token issued after this
	// must validate.
	checker.invalidate(userID, time.Now().Add(-time.Hour))

	token, err := mgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	claims, err := mgr.ValidateToken(token)
	if err != nil {
		t.Fatalf("validate post-cutoff: %v", err)
	}
	if claims.UserID != userID {
		t.Fatalf("UserID = %v, want %v", claims.UserID, userID)
	}
}

func TestValidateToken_NoCheckerLeavesValidationUntouched(t *testing.T) {
	// Backwards-compat: the manager without a RevocationChecker
	// must behave exactly as before — purely signature/expiry-based.
	mgr := MustNewJWTManager("test-secret", 60)
	token, err := mgr.GenerateAccessToken(uuid.New())
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := mgr.ValidateToken(token); err != nil {
		t.Fatalf("validate without checker: %v", err)
	}
}

func TestJWTValidationCacheEvictsExpiredEntriesAndStaysBounded(t *testing.T) {
	mgr := MustNewJWTManager("test-secret", 60)
	mgr.SetValidationCacheTTL(time.Minute)
	mgr.SetValidationCacheMaxEntries(2)

	mgr.cachePut("oldest", uuid.New())
	time.Sleep(time.Millisecond)
	mgr.cachePut("newer", uuid.New())
	mgr.cachePut("newest", uuid.New())

	mgr.cacheMu.RLock()
	if got := len(mgr.cache); got != 2 {
		t.Fatalf("cache size = %d, want 2", got)
	}
	_, hasOldest := mgr.cache["oldest"]
	mgr.cacheMu.RUnlock()
	if hasOldest {
		t.Fatal("oldest cache entry was not evicted at capacity")
	}

	mgr.cacheMu.Lock()
	mgr.cache["newer"] = validationCacheEntry{expiresAt: time.Now().Add(-time.Second)}
	mgr.cacheMu.Unlock()
	if mgr.cacheHit("newer") {
		t.Fatal("expired cache entry returned a hit")
	}
	mgr.cacheMu.RLock()
	_, stillCached := mgr.cache["newer"]
	mgr.cacheMu.RUnlock()
	if stillCached {
		t.Fatal("expired cache entry was not removed on lookup")
	}
}

func TestValidateTokenRevocationDBErrorAlwaysFailsClosed(t *testing.T) {
	for _, failure := range []string{"jti", "user_cutoff"} {
		t.Run(failure, func(t *testing.T) {
			mgr := MustNewJWTManager("test-secret", 60)
			checker := newFakeRevocationChecker()
			if failure == "jti" {
				checker.revErr = errors.New("db unavailable")
			} else {
				checker.cutoffErr = errors.New("db unavailable")
			}
			mgr.SetRevocationChecker(checker)
			coordinator := &fakeCacheCoordinator{healthy: true}
			mgr.SetCacheInvalidationCoordinator(coordinator)
			token, _ := mgr.GenerateAccessToken(uuid.New())
			if _, err := mgr.ValidateToken(token); !errors.Is(err, ErrRevocationUnavailable) {
				t.Fatalf("healthy coordinator revocation error = %v, want dependency failure", err)
			}

			coordinator.healthy = false
			if _, err := mgr.ValidateToken(token); !errors.Is(err, ErrRevocationUnavailable) {
				t.Fatalf("unhealthy coordinator revocation error = %v, want dependency failure", err)
			}
		})
	}
}

// PositiveCachePreventsRevocation: this is intentionally the OPPOSITE
// of what we want from a correctness standpoint, but it captures the
// behaviour of the positive cache so a future refactor doesn't
// silently change the semantics. The cache is supposed to mask
// revocations for up to its TTL — Logout calls InvalidateCache() to
// flush it, which we cover in the handler-side tests. Here we just
// pin the cache contract.
func TestValidateToken_PositiveCacheMasksRevocationUntilFlush(t *testing.T) {
	mgr := MustNewJWTManager("test-secret", 60)
	checker := newFakeRevocationChecker()
	mgr.SetRevocationChecker(checker)
	// Default TTL of 30s — that's effectively "never" for this test.

	userID := uuid.New()
	token, err := mgr.GenerateAccessToken(userID)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	claims, err := mgr.ValidateToken(token)
	if err != nil {
		t.Fatalf("pre-revoke validate: %v", err)
	}
	// Revoke; cache still says "good".
	checker.revoke(claims.ID)
	if _, err := mgr.ValidateToken(token); err != nil {
		t.Fatalf("cached validate should still succeed: %v", err)
	}
	// After flush, the next call must hit the DB and reject.
	mgr.InvalidateCache()
	if _, err := mgr.ValidateToken(token); err == nil {
		t.Fatal("expected validate to fail after InvalidateCache + revoke")
	}
}

// TokenIssuedBeforeCutoffWithCustomClaims double-checks that the iat
// path handles a hand-crafted token whose iat was set to a specific
// historical time (the realistic shape of a JWT the validator sees
// after a force-logout).
func TestValidateToken_CustomIssuedAtIsCompared(t *testing.T) {
	secret := "test-secret"
	mgr := MustNewJWTManager(secret, 60)
	mgr.SetValidationCacheTTL(0)
	checker := newFakeRevocationChecker()
	mgr.SetRevocationChecker(checker)

	userID := uuid.New()
	issued := time.Now().Add(-2 * time.Hour) // way back
	cutoff := time.Now().Add(-time.Hour)     // an hour after issuance

	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(issued),
			ID:        uuid.New().String(),
		},
		UserID:    userID,
		TokenType: AccessToken,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	checker.invalidate(userID, cutoff)
	if _, err := mgr.ValidateToken(signed); err == nil {
		t.Fatal("expected token issued before cutoff to be rejected")
	}
}

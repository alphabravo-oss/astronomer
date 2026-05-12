package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// recordingRevocationQuerier captures every revocation write made by
// the Logout / force-logout paths so tests can assert the right rows
// landed without standing up a real database.
type recordingRevocationQuerier struct {
	mu              sync.Mutex
	revoked         []sqlc.RevokeJWTParams
	invalidated     []sqlc.InvalidateAllTokensParams
	revErr          error
	invalidateErr   error
}

func newRecordingRevocationQuerier() *recordingRevocationQuerier {
	return &recordingRevocationQuerier{}
}

func (q *recordingRevocationQuerier) RevokeJWT(_ context.Context, arg sqlc.RevokeJWTParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.revErr != nil {
		return q.revErr
	}
	q.revoked = append(q.revoked, arg)
	return nil
}

func (q *recordingRevocationQuerier) InvalidateAllTokens(_ context.Context, arg sqlc.InvalidateAllTokensParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.invalidateErr != nil {
		return q.invalidateErr
	}
	q.invalidated = append(q.invalidated, arg)
	return nil
}

// TestLogout_RevokesJTI exercises the integration between Logout and the
// JTI deny list: an authenticated POST /auth/logout with a JWT in the
// header should write a row whose jti matches the token's claim.
func TestLogout_RevokesJTI(t *testing.T) {
	user := makeTestUser(t, true)
	q := newMockQuerier(user)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	rev := newRecordingRevocationQuerier()
	h := NewAuthHandler(q, jwtMgr)
	h.SetRevocationQuerier(rev)

	// Generate the JWT we'll log out.
	token, err := jwtMgr.GenerateAccessToken(user.ID)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	claims, err := jwtMgr.ValidateToken(token)
	if err != nil {
		t.Fatalf("validate own token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout/", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+token)
	req = setAuthUser(req, user.ID.String())
	rec := httptest.NewRecorder()

	h.Logout(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(rev.revoked) != 1 {
		t.Fatalf("revoked rows = %d, want 1", len(rev.revoked))
	}
	if rev.revoked[0].Jti != claims.ID {
		t.Fatalf("Jti = %q, want %q", rev.revoked[0].Jti, claims.ID)
	}
	if rev.revoked[0].UserID != user.ID {
		t.Fatalf("UserID = %v, want %v", rev.revoked[0].UserID, user.ID)
	}
	if rev.revoked[0].Reason != "user_logout" {
		t.Fatalf("Reason = %q, want user_logout", rev.revoked[0].Reason)
	}
}

func TestLogout_NoBearerSkipsRevocation(t *testing.T) {
	// Anonymous logout (no Authorization header) must NOT write a
	// row — there's nothing to revoke. The endpoint should still
	// return 200 to keep the frontend's logout flow happy.
	user := makeTestUser(t, true)
	q := newMockQuerier(user)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	rev := newRecordingRevocationQuerier()
	h := NewAuthHandler(q, jwtMgr)
	h.SetRevocationQuerier(rev)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout/", strings.NewReader(""))
	rec := httptest.NewRecorder()

	h.Logout(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(rev.revoked) != 0 {
		t.Fatalf("revoked rows = %d, want 0 (no bearer)", len(rev.revoked))
	}
}

// resourceQuerierForceLogout is a narrow ResourceQuerier shim that
// satisfies the ForceLogoutUser handler's dependencies. We only need
// GetUserByID + InvalidateAllTokens + UnlockUser for the tests in this
// file.
type resourceQuerierForceLogout struct {
	mu          sync.Mutex
	users       map[uuid.UUID]sqlc.User
	invalidated []sqlc.InvalidateAllTokensParams
	unlocked    []uuid.UUID
}

func (r *resourceQuerierForceLogout) GetPlatformConfig(_ context.Context) (sqlc.PlatformConfiguration, error) {
	return sqlc.PlatformConfiguration{}, nil
}
func (r *resourceQuerierForceLogout) UpsertPlatformConfig(_ context.Context, _ sqlc.UpsertPlatformConfigParams) (sqlc.PlatformConfiguration, error) {
	return sqlc.PlatformConfiguration{}, nil
}
func (r *resourceQuerierForceLogout) ListAuditLogV1(_ context.Context, _ sqlc.ListAuditLogsParams) ([]sqlc.AuditLog, error) {
	return nil, nil
}
func (r *resourceQuerierForceLogout) CountAuditLogV1(_ context.Context) (int64, error) {
	return 0, nil
}
func (r *resourceQuerierForceLogout) ListUsers(_ context.Context, _ sqlc.ListUsersParams) ([]sqlc.User, error) {
	return nil, nil
}
func (r *resourceQuerierForceLogout) CountUsers(_ context.Context) (int64, error) {
	return 0, nil
}
func (r *resourceQuerierForceLogout) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.users[id]
	if !ok {
		return sqlc.User{}, errNoRows
	}
	return u, nil
}
func (r *resourceQuerierForceLogout) GetUserByEmail(_ context.Context, _ string) (sqlc.User, error) {
	return sqlc.User{}, errNoRows
}
func (r *resourceQuerierForceLogout) GetUserByUsername(_ context.Context, _ string) (sqlc.User, error) {
	return sqlc.User{}, errNoRows
}
func (r *resourceQuerierForceLogout) CreateUser(_ context.Context, _ sqlc.CreateUserParams) (sqlc.User, error) {
	return sqlc.User{}, nil
}
func (r *resourceQuerierForceLogout) UpdateUser(_ context.Context, _ sqlc.UpdateUserParams) (sqlc.User, error) {
	return sqlc.User{}, nil
}
func (r *resourceQuerierForceLogout) DeleteUser(_ context.Context, _ uuid.UUID) error {
	return nil
}
func (r *resourceQuerierForceLogout) UpdateUserPassword(_ context.Context, _ sqlc.UpdateUserPasswordParams) error {
	return nil
}
func (r *resourceQuerierForceLogout) UnlockUser(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unlocked = append(r.unlocked, id)
	u := r.users[id]
	u.LockedUntil = pgtype.Timestamptz{}
	u.LockedReason = ""
	u.FailedLoginCount = 0
	r.users[id] = u
	return nil
}
func (r *resourceQuerierForceLogout) InvalidateAllTokens(_ context.Context, arg sqlc.InvalidateAllTokensParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.invalidated = append(r.invalidated, arg)
	return nil
}

func TestForceLogout_InvalidatesAllUserTokens(t *testing.T) {
	admin := makeTestUser(t, true)
	admin.IsSuperuser = true
	target := makeTestUser(t, true)
	target.ID = uuid.New()
	target.Email = "target@example.com"
	target.Username = "target"

	rq := &resourceQuerierForceLogout{
		users: map[uuid.UUID]sqlc.User{admin.ID: admin, target.ID: target},
	}
	h := NewResourceHandlerWithQueries(rq, nil)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	h.SetJWTManager(jwtMgr)

	r := chi.NewRouter()
	r.Post("/api/v1/admin/users/{id}/force-logout/", h.ForceLogoutUser)

	// Authenticate as the admin.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+target.ID.String()+"/force-logout/", nil)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: admin.ID.String(), AuthMethod: "jwt"}))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(rq.invalidated) != 1 {
		t.Fatalf("invalidate calls = %d, want 1", len(rq.invalidated))
	}
	if rq.invalidated[0].ID != target.ID {
		t.Fatalf("invalidated ID = %v, want %v", rq.invalidated[0].ID, target.ID)
	}
	if !rq.invalidated[0].TokensInvalidatedAt.Valid {
		t.Fatal("TokensInvalidatedAt should be valid")
	}
	if d := time.Since(rq.invalidated[0].TokensInvalidatedAt.Time); d > 5*time.Second {
		t.Fatalf("TokensInvalidatedAt = %s, want recent (within 5s)", d)
	}
}

func TestForceLogout_NonSuperuserRejected(t *testing.T) {
	caller := makeTestUser(t, true) // is_superuser=false
	target := makeTestUser(t, true)
	target.ID = uuid.New()
	target.Email = "target@example.com"

	rq := &resourceQuerierForceLogout{
		users: map[uuid.UUID]sqlc.User{caller.ID: caller, target.ID: target},
	}
	h := NewResourceHandlerWithQueries(rq, nil)

	r := chi.NewRouter()
	r.Post("/api/v1/admin/users/{id}/force-logout/", h.ForceLogoutUser)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+target.ID.String()+"/force-logout/", nil)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: caller.ID.String(), AuthMethod: "jwt"}))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if len(rq.invalidated) != 0 {
		t.Fatalf("invalidate calls = %d, want 0", len(rq.invalidated))
	}
}

func TestAdminUnlock_ClearsLockoutFields(t *testing.T) {
	admin := makeTestUser(t, true)
	admin.IsSuperuser = true
	target := makeTestUser(t, true)
	target.ID = uuid.New()
	target.Email = "target@example.com"
	target.Username = "target"
	target.LockedUntil = pgtype.Timestamptz{Time: time.Now().Add(10 * time.Minute), Valid: true}
	target.LockedReason = auth.LockoutReasonTooManyFailedAttempts
	target.FailedLoginCount = 7

	rq := &resourceQuerierForceLogout{
		users: map[uuid.UUID]sqlc.User{admin.ID: admin, target.ID: target},
	}
	h := NewResourceHandlerWithQueries(rq, nil)

	r := chi.NewRouter()
	r.Post("/api/v1/admin/users/{id}/unlock/", h.UnlockUser)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/"+target.ID.String()+"/unlock/", nil)
	req = req.WithContext(middleware.SetAuthenticatedUserForTest(req.Context(), &middleware.AuthenticatedUser{ID: admin.ID.String(), AuthMethod: "jwt"}))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(rq.unlocked) != 1 {
		t.Fatalf("unlock calls = %d, want 1", len(rq.unlocked))
	}
	if u := rq.users[target.ID]; u.LockedUntil.Valid {
		t.Fatal("expected LockedUntil to be cleared")
	}
}

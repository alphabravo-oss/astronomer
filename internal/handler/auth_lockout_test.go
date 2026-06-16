package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// recordingLockoutQuerier is a thread-safe in-memory LockoutQuerier that
// also satisfies UserQuerier so the auth handler can read back the
// mutated state on its next call. It carries no DB round-trips and is
// intended exclusively for the Login lockout test cases.
type recordingLockoutQuerier struct {
	mu     sync.Mutex
	users  map[uuid.UUID]sqlc.User
	emails map[string]uuid.UUID
	unames map[string]uuid.UUID
	// Captured call counts so tests can assert behaviour.
	incCalls    int
	lockCalls   int
	resetCalls  int
	unlockCalls int
}

func newRecordingLockoutQuerier(users ...sqlc.User) *recordingLockoutQuerier {
	q := &recordingLockoutQuerier{
		users:  make(map[uuid.UUID]sqlc.User),
		emails: make(map[string]uuid.UUID),
		unames: make(map[string]uuid.UUID),
	}
	for _, u := range users {
		q.users[u.ID] = u
		q.emails[u.Email] = u.ID
		q.unames[u.Username] = u.ID
	}
	return q
}

func (q *recordingLockoutQuerier) GetUserByEmail(_ context.Context, email string) (sqlc.User, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	id, ok := q.emails[email]
	if !ok {
		return sqlc.User{}, errNoRows
	}
	return q.users[id], nil
}

func (q *recordingLockoutQuerier) GetUserByUsername(_ context.Context, u string) (sqlc.User, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	id, ok := q.unames[u]
	if !ok {
		return sqlc.User{}, errNoRows
	}
	return q.users[id], nil
}

func (q *recordingLockoutQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	u, ok := q.users[id]
	if !ok {
		return sqlc.User{}, errNoRows
	}
	return u, nil
}

func (q *recordingLockoutQuerier) UpdateUserLastLogin(_ context.Context, _ uuid.UUID) error {
	return nil
}

func (q *recordingLockoutQuerier) IncrementFailedLoginCount(_ context.Context, arg sqlc.IncrementFailedLoginCountParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	u := q.users[arg.ID]
	u.FailedLoginCount = u.FailedLoginCount + 1
	u.FailedLoginAt = arg.FailedLoginAt
	q.users[arg.ID] = u
	q.incCalls++
	return nil
}

func (q *recordingLockoutQuerier) ResetFailedLoginCount(_ context.Context, id uuid.UUID) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	u := q.users[id]
	u.FailedLoginCount = 0
	u.FailedLoginAt = pgtype.Timestamptz{}
	u.LockedUntil = pgtype.Timestamptz{}
	u.LockedReason = ""
	q.users[id] = u
	q.resetCalls++
	return nil
}

func (q *recordingLockoutQuerier) LockUser(_ context.Context, arg sqlc.LockUserParams) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	u := q.users[arg.ID]
	u.LockedUntil = arg.LockedUntil
	u.LockedReason = arg.LockedReason
	q.users[arg.ID] = u
	q.lockCalls++
	return nil
}

func (q *recordingLockoutQuerier) UnlockUser(_ context.Context, id uuid.UUID) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	u := q.users[id]
	u.FailedLoginCount = 0
	u.LockedUntil = pgtype.Timestamptz{}
	u.LockedReason = ""
	q.users[id] = u
	q.unlockCalls++
	return nil
}

var errNoRows = &simpleErr{"no rows in result set"}

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }

func newLoginRequestBody(t *testing.T, email, password string) []byte {
	t.Helper()
	b, err := json.Marshal(LoginRequest{Email: email, Password: password})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return b
}

func doLogin(t *testing.T, h *AuthHandler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	return rec
}

func TestLogin_IncrementsOnFailure(t *testing.T) {
	user := makeTestUser(t, true)
	q := newRecordingLockoutQuerier(user)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	h := NewAuthHandler(q, jwtMgr)
	h.SetLockoutQuerier(q)
	h.SetLockoutPolicy(5, 15*time.Minute)

	body := newLoginRequestBody(t, user.Email, "wrong-password")
	rec := doLogin(t, h, body)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	if q.incCalls != 1 {
		t.Fatalf("IncrementFailedLoginCount calls = %d, want 1", q.incCalls)
	}
	if q.lockCalls != 0 {
		t.Fatalf("LockUser calls = %d, want 0 (under threshold)", q.lockCalls)
	}
	if got := q.users[user.ID].FailedLoginCount; got != 1 {
		t.Fatalf("FailedLoginCount = %d, want 1", got)
	}
}

func TestLogin_LocksAfterThreshold(t *testing.T) {
	user := makeTestUser(t, true)
	// Pre-seed with 4 prior failures so the next miss is the 5th
	// and trips the threshold.
	user.FailedLoginCount = 4
	q := newRecordingLockoutQuerier(user)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	h := NewAuthHandler(q, jwtMgr)
	h.SetLockoutQuerier(q)
	h.SetLockoutPolicy(5, 15*time.Minute)

	body := newLoginRequestBody(t, user.Email, "wrong-password")
	rec := doLogin(t, h, body)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	if q.lockCalls != 1 {
		t.Fatalf("LockUser calls = %d, want 1", q.lockCalls)
	}
	updated := q.users[user.ID]
	if !updated.LockedUntil.Valid {
		t.Fatal("expected LockedUntil to be set")
	}
	if updated.LockedReason != auth.LockoutReasonTooManyFailedAttempts {
		t.Fatalf("LockedReason = %q, want %q", updated.LockedReason, auth.LockoutReasonTooManyFailedAttempts)
	}
	// Lock window should be ~15 minutes from now.
	if d := time.Until(updated.LockedUntil.Time); d < 14*time.Minute || d > 16*time.Minute {
		t.Fatalf("LockedUntil window = %s, want ~15 min", d)
	}
}

func TestLogin_RejectsLockedAccount(t *testing.T) {
	user := makeTestUser(t, true)
	user.LockedUntil = pgtype.Timestamptz{Time: time.Now().Add(10 * time.Minute), Valid: true}
	user.LockedReason = auth.LockoutReasonTooManyFailedAttempts
	q := newRecordingLockoutQuerier(user)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	h := NewAuthHandler(q, jwtMgr)
	h.SetLockoutQuerier(q)
	auditWriter := &recordingAuthAuditWriter{}
	h.SetAuditWriter(auditWriter)

	// Even submitting the CORRECT password should bounce — the lockout
	// gate sits before bcrypt.
	body := newLoginRequestBody(t, user.Email, "testpassword")
	rec := doLogin(t, h, body)

	if rec.Code != http.StatusLocked {
		t.Fatalf("status = %d, want 423 Locked", rec.Code)
	}
	if q.incCalls != 0 {
		t.Fatalf("IncrementFailedLoginCount calls = %d, want 0 (gate is pre-bcrypt)", q.incCalls)
	}
	// Audit row should be the "locked" variant.
	if len(auditWriter.rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(auditWriter.rows))
	}
	if auditWriter.rows[0].Action != "auth.login_locked" {
		t.Fatalf("action = %q, want auth.login_locked", auditWriter.rows[0].Action)
	}
}

func TestLogin_ResetsCountOnSuccess(t *testing.T) {
	user := makeTestUser(t, true)
	user.FailedLoginCount = 3
	q := newRecordingLockoutQuerier(user)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	h := NewAuthHandler(q, jwtMgr)
	h.SetLockoutQuerier(q)

	body := newLoginRequestBody(t, user.Email, "testpassword")
	rec := doLogin(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	if q.resetCalls != 1 {
		t.Fatalf("ResetFailedLoginCount calls = %d, want 1", q.resetCalls)
	}
	if got := q.users[user.ID].FailedLoginCount; got != 0 {
		t.Fatalf("FailedLoginCount = %d, want 0 after success", got)
	}
}

func TestLogin_ExpiredLockProceeds(t *testing.T) {
	user := makeTestUser(t, true)
	// Lock that expired an hour ago — should fall through to normal
	// bcrypt verification and succeed.
	user.LockedUntil = pgtype.Timestamptz{Time: time.Now().Add(-time.Hour), Valid: true}
	user.LockedReason = auth.LockoutReasonTooManyFailedAttempts
	q := newRecordingLockoutQuerier(user)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	h := NewAuthHandler(q, jwtMgr)
	h.SetLockoutQuerier(q)

	body := newLoginRequestBody(t, user.Email, "testpassword")
	rec := doLogin(t, h, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (expired lock); body=%s", rec.Code, rec.Body.String())
	}
	if q.resetCalls != 1 {
		t.Fatalf("ResetFailedLoginCount calls = %d, want 1", q.resetCalls)
	}
}

// Sanity check on the new auth.login_locked audit action — make sure
// the row carries the expected detail keys so downstream consumers (the
// SOC dashboards) can filter on them.
func TestLogin_LockedAuditDetail(t *testing.T) {
	user := makeTestUser(t, true)
	user.FailedLoginCount = 4
	q := newRecordingLockoutQuerier(user)
	jwtMgr := auth.NewJWTManager("test-secret-key", 60)
	h := NewAuthHandler(q, jwtMgr)
	h.SetLockoutQuerier(q)
	auditWriter := &recordingAuthAuditWriter{}
	h.SetAuditWriter(auditWriter)

	rec := doLogin(t, h, newLoginRequestBody(t, user.Email, "wrong-password"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(auditWriter.rows) != 1 {
		t.Fatalf("audit rows = %d", len(auditWriter.rows))
	}
	if got := auditWriter.rows[0].Action; got != "auth.login_locked" {
		t.Fatalf("action = %q, want auth.login_locked", got)
	}
}

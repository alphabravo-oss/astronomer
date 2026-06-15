package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// fakeAuthQuerier implements UserQuerier (the minimal AuthHandler needs).
type fakeAuthQuerier struct {
	usersByEmail map[string]sqlc.User
	usersByID    map[uuid.UUID]sqlc.User
}

func (f *fakeAuthQuerier) GetUserByEmail(_ context.Context, email string) (sqlc.User, error) {
	u, ok := f.usersByEmail[email]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return u, nil
}
func (f *fakeAuthQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	u, ok := f.usersByID[id]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return u, nil
}
func (f *fakeAuthQuerier) GetUserByUsername(_ context.Context, _ string) (sqlc.User, error) {
	return sqlc.User{}, pgx.ErrNoRows
}
func (f *fakeAuthQuerier) UpdateUserLastLogin(_ context.Context, _ uuid.UUID) error { return nil }

// fakePasswordResetStore captures the writes the reset flow makes.
type fakePasswordResetStore struct {
	createCalls           atomic.Int32
	consumed              atomic.Int32
	updates               atomic.Int32
	deletes               atomic.Int32
	tokensByHash          map[string]sqlc.PasswordResetToken
	currentPasswordByUser map[uuid.UUID]string
}

func (f *fakePasswordResetStore) CreatePasswordResetToken(_ context.Context, arg sqlc.CreatePasswordResetTokenParams) (sqlc.PasswordResetToken, error) {
	f.createCalls.Add(1)
	row := sqlc.PasswordResetToken{
		ID:                  uuid.New(),
		UserID:              arg.UserID,
		TokenHash:           arg.TokenHash,
		PasswordHashAtIssue: arg.PasswordHashAtIssue,
		ExpiresAt:           arg.ExpiresAt,
	}
	if f.tokensByHash == nil {
		f.tokensByHash = map[string]sqlc.PasswordResetToken{}
	}
	f.tokensByHash[arg.TokenHash] = row
	return row, nil
}
func (f *fakePasswordResetStore) GetPasswordResetTokenByHash(_ context.Context, hash string) (sqlc.PasswordResetToken, error) {
	row, ok := f.tokensByHash[hash]
	if !ok {
		return sqlc.PasswordResetToken{}, pgx.ErrNoRows
	}
	return row, nil
}
func (f *fakePasswordResetStore) ConsumePasswordResetToken(_ context.Context, arg sqlc.ConsumePasswordResetTokenParams) (int64, error) {
	row, ok := f.tokensByHash[arg.TokenHash]
	if !ok || row.UsedAt.Valid {
		return 0, nil
	}
	row.UsedAt = arg.UsedAt
	f.tokensByHash[arg.TokenHash] = row
	f.consumed.Add(1)
	return 1, nil
}
func (f *fakePasswordResetStore) DeletePasswordResetTokensForUser(_ context.Context, _ uuid.UUID) error {
	f.deletes.Add(1)
	return nil
}
func (f *fakePasswordResetStore) UpdateUserPassword(_ context.Context, arg sqlc.UpdateUserPasswordParams) error {
	f.updates.Add(1)
	if f.currentPasswordByUser == nil {
		f.currentPasswordByUser = map[uuid.UUID]string{}
	}
	f.currentPasswordByUser[arg.ID] = arg.Password
	return nil
}

// recordingEmailNotifier counts calls.
type recordingEmailNotifier struct {
	calls atomic.Int32
	last  EmailNotifierRequest
}

func (r *recordingEmailNotifier) EnqueueAndLog(_ context.Context, req EmailNotifierRequest) {
	r.calls.Add(1)
	r.last = req
}

func newPasswordResetHandler(t *testing.T) (*AuthHandler, *fakeAuthQuerier, *fakePasswordResetStore, *recordingEmailNotifier) {
	t.Helper()
	q := &fakeAuthQuerier{
		usersByEmail: map[string]sqlc.User{},
		usersByID:    map[uuid.UUID]sqlc.User{},
	}
	jwt := auth.NewJWTManager("test-secret-key-32-bytes-min-yo!", 60)
	h := NewAuthHandlerWithTokens(q, nil, jwt)
	store := &fakePasswordResetStore{}
	notifier := &recordingEmailNotifier{}
	h.SetPasswordResetStore(store)
	h.SetEmailNotifier(notifier)
	return h, q, store, notifier
}

func seedUser(t *testing.T, q *fakeAuthQuerier, password string) sqlc.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	u := sqlc.User{
		ID:       uuid.New(),
		Email:    "alice@example.com",
		Username: "alice",
		Password: hash,
		IsActive: true,
	}
	q.usersByEmail[u.Email] = u
	q.usersByID[u.ID] = u
	return u
}

func TestPasswordReset_RequestEnqueuesEmail(t *testing.T) {
	h, q, store, notifier := newPasswordResetHandler(t)
	user := seedUser(t, q, "OriginalPass123")

	rec := httptest.NewRecorder()
	body, _ := json.Marshal(PasswordResetRequest{Email: user.Email})
	req := httptest.NewRequest(http.MethodPost, "/auth/password-reset/request/", bytes.NewReader(body))
	h.PasswordResetRequest(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if notifier.calls.Load() != 1 {
		t.Errorf("expected 1 email enqueued, got %d", notifier.calls.Load())
	}
	if store.createCalls.Load() != 1 {
		t.Errorf("expected 1 token written, got %d", store.createCalls.Load())
	}
	if notifier.last.Template != "password_reset" {
		t.Errorf("template should be password_reset, got %q", notifier.last.Template)
	}
	if notifier.last.UserID != user.ID {
		t.Errorf("user id mismatch: got %s want %s", notifier.last.UserID, user.ID)
	}
}

func TestPasswordReset_NoUserEnumeration(t *testing.T) {
	h, _, store, notifier := newPasswordResetHandler(t)
	// Email matches no user — must still return 202, no token, no email.
	rec := httptest.NewRecorder()
	body, _ := json.Marshal(PasswordResetRequest{Email: "ghost@example.com"})
	req := httptest.NewRequest(http.MethodPost, "/auth/password-reset/request/", bytes.NewReader(body))
	h.PasswordResetRequest(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if notifier.calls.Load() != 0 {
		t.Errorf("must not enqueue for unknown email, got %d", notifier.calls.Load())
	}
	if store.createCalls.Load() != 0 {
		t.Errorf("must not write a token for unknown email, got %d", store.createCalls.Load())
	}
	// Same response shape with empty body — caller can't infer success.
	bodyStr := rec.Body.String()
	if bodyStr != "" {
		t.Errorf("body should be empty, got %q", bodyStr)
	}
}

func TestPasswordReset_CompleteVerifiesToken(t *testing.T) {
	h, q, store, _ := newPasswordResetHandler(t)
	user := seedUser(t, q, "OriginalPass123")

	// Issue a reset.
	rec := httptest.NewRecorder()
	body, _ := json.Marshal(PasswordResetRequest{Email: user.Email})
	req := httptest.NewRequest(http.MethodPost, "/auth/password-reset/request/", bytes.NewReader(body))
	h.PasswordResetRequest(rec, req)

	// Find the plaintext token by re-deriving it: we have to read
	// the stored row and reverse the hash, which we can't do. Instead,
	// extract it from the recordingEmailNotifier captured Data map.
	// To do so cleanly, peek at the fakePasswordResetStore.
	if len(store.tokensByHash) != 1 {
		t.Fatalf("expected 1 token persisted")
	}
	var tokenRow sqlc.PasswordResetToken
	for _, row := range store.tokensByHash {
		tokenRow = row
	}
	// Verify the snapshot matches the user's current password hash.
	if tokenRow.PasswordHashAtIssue != user.Password {
		t.Errorf("snapshot password hash mismatch")
	}

	// The plaintext token is in the email — fish it out of the
	// notifier's last call. The Data map carries ResetURL=...?token=<hex>.
	notifier := h.emails.(*recordingEmailNotifier)
	data := notifier.last.Data.(map[string]any)
	resetURL := data["ResetURL"].(string)
	// extract token=<hex>
	const sentinel = "token="
	idx := -1
	for i := 0; i+len(sentinel) <= len(resetURL); i++ {
		if resetURL[i:i+len(sentinel)] == sentinel {
			idx = i + len(sentinel)
			break
		}
	}
	if idx < 0 {
		t.Fatalf("no token in reset URL: %s", resetURL)
	}
	tokenPlain := resetURL[idx:]
	if len(tokenPlain) != 64 {
		t.Errorf("token length should be 64 hex chars, got %d (%q)", len(tokenPlain), tokenPlain)
	}

	// Complete with the right token.
	rec = httptest.NewRecorder()
	body, _ = json.Marshal(PasswordResetComplete{Token: tokenPlain, NewPassword: "BrandNew123"})
	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	h.PasswordResetComplete(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.updates.Load() != 1 {
		t.Errorf("expected 1 password update, got %d", store.updates.Load())
	}

	// Replay the same token — must fail (single-use enforcement).
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	h.PasswordResetComplete(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("replay should fail, got %d", rec.Code)
	}
}

func TestPasswordReset_CompleteRejectsExpiredToken(t *testing.T) {
	h, q, store, _ := newPasswordResetHandler(t)
	user := seedUser(t, q, "OriginalPass123")
	// Manually inject an expired token row.
	tokenPlain := "deadbeefcafebabefeedfacecafef00dcafebabefeedfacedeadbeefcafef00d"
	store.tokensByHash = map[string]sqlc.PasswordResetToken{
		auth.HashOpaqueToken(tokenPlain): {
			ID:                  uuid.New(),
			UserID:              user.ID,
			TokenHash:           auth.HashOpaqueToken(tokenPlain),
			PasswordHashAtIssue: user.Password,
			ExpiresAt:           time.Now().Add(-time.Minute),
		},
	}
	rec := httptest.NewRecorder()
	body, _ := json.Marshal(PasswordResetComplete{Token: tokenPlain, NewPassword: "BrandNew123"})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	h.PasswordResetComplete(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expired token should be 400, got %d", rec.Code)
	}
}

func TestPasswordReset_CompleteRejectsTokenAfterPasswordChange(t *testing.T) {
	h, q, store, _ := newPasswordResetHandler(t)
	user := seedUser(t, q, "OriginalPass123")
	tokenPlain := "1111111111111111111111111111111111111111111111111111111111111111"
	store.tokensByHash = map[string]sqlc.PasswordResetToken{
		auth.HashOpaqueToken(tokenPlain): {
			ID:        uuid.New(),
			UserID:    user.ID,
			TokenHash: auth.HashOpaqueToken(tokenPlain),
			// Snapshot is the OLD password hash.
			PasswordHashAtIssue: user.Password,
			ExpiresAt:           time.Now().Add(time.Hour),
		},
	}
	// User changed their password since the token was issued.
	newHash, _ := auth.HashPassword("ChangedSinceIssue")
	user.Password = newHash
	q.usersByID[user.ID] = user

	rec := httptest.NewRecorder()
	body, _ := json.Marshal(PasswordResetComplete{Token: tokenPlain, NewPassword: "BrandNew123"})
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	h.PasswordResetComplete(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("token whose snapshot doesn't match current hash should be 400, got %d", rec.Code)
	}
}

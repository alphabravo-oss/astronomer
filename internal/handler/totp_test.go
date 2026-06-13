package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// fakeTOTPStore is a minimal in-memory TOTPQuerier for the handler
// tests. Thread-safe so concurrent verify-attempt tests don't race.
type fakeTOTPStore struct {
	mu          sync.Mutex
	enrollments map[uuid.UUID]sqlc.UserTotpEnrollment
	codes       []sqlc.UserTotpRecoveryCode
}

func newFakeTOTPStore() *fakeTOTPStore {
	return &fakeTOTPStore{enrollments: map[uuid.UUID]sqlc.UserTotpEnrollment{}}
}

func (s *fakeTOTPStore) GetUserTOTPEnrollment(_ context.Context, userID uuid.UUID) (sqlc.UserTotpEnrollment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.enrollments[userID]
	if !ok {
		return sqlc.UserTotpEnrollment{}, fmt.Errorf("no rows in result set")
	}
	return e, nil
}

func (s *fakeTOTPStore) UpsertUserTOTPEnrollment(_ context.Context, arg sqlc.UpsertUserTOTPEnrollmentParams) (sqlc.UserTotpEnrollment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := sqlc.UserTotpEnrollment{
		UserID:          arg.UserID,
		SecretEncrypted: arg.SecretEncrypted,
		Label:           arg.Label,
		ConfirmedAt:     arg.ConfirmedAt,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	s.enrollments[arg.UserID] = e
	return e, nil
}

func (s *fakeTOTPStore) DeleteUserTOTPEnrollment(_ context.Context, userID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.enrollments, userID)
	return nil
}

func (s *fakeTOTPStore) TouchUserTOTPLastUsed(_ context.Context, arg sqlc.TouchUserTOTPLastUsedParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.enrollments[arg.UserID]
	if !ok {
		return nil
	}
	e.LastUsedAt = arg.LastUsedAt
	s.enrollments[arg.UserID] = e
	return nil
}

func (s *fakeTOTPStore) InsertRecoveryCode(_ context.Context, arg sqlc.InsertRecoveryCodeParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes = append(s.codes, sqlc.UserTotpRecoveryCode{
		ID:        uuid.New(),
		UserID:    arg.UserID,
		CodeHash:  arg.CodeHash,
		CreatedAt: time.Now(),
	})
	return nil
}

func (s *fakeTOTPStore) ListUnusedRecoveryCodes(_ context.Context, userID uuid.UUID) ([]sqlc.UserTotpRecoveryCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []sqlc.UserTotpRecoveryCode
	for _, c := range s.codes {
		if c.UserID == userID && !c.UsedAt.Valid {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *fakeTOTPStore) CountUnusedRecoveryCodes(_ context.Context, userID uuid.UUID) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, c := range s.codes {
		if c.UserID == userID && !c.UsedAt.Valid {
			n++
		}
	}
	return n, nil
}

func (s *fakeTOTPStore) ConsumeRecoveryCode(_ context.Context, arg sqlc.ConsumeRecoveryCodeParams) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.codes {
		if c.UserID == arg.UserID && c.CodeHash == arg.CodeHash && !c.UsedAt.Valid {
			s.codes[i].UsedAt = arg.UsedAt
			return 1, nil
		}
	}
	return 0, nil
}

func (s *fakeTOTPStore) DeleteRecoveryCodesByUser(_ context.Context, userID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.codes[:0]
	for _, c := range s.codes {
		if c.UserID != userID {
			out = append(out, c)
		}
	}
	s.codes = out
	return nil
}

// setAuthUserFull returns a request with a fully-populated
// AuthenticatedUser so the TOTP handlers can derive label / username
// without a DB lookup.
func setAuthUserFull(r *http.Request, u sqlc.User) *http.Request {
	au := &middleware.AuthenticatedUser{
		ID:         u.ID.String(),
		Email:      u.Email,
		Username:   u.Username,
		AuthMethod: "jwt",
	}
	ctx := middleware.SetAuthenticatedUserForTest(r.Context(), au)
	return r.WithContext(ctx)
}

func mustEncryptor(t *testing.T) *auth.Encryptor {
	t.Helper()
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := auth.NewEncryptor(key)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	return enc
}

// --- Enroll flow ---

func TestEnrollFlow_StartThenConfirm(t *testing.T) {
	user := makeTestUser(t, true)
	jwtMgr := auth.NewJWTManager("test-secret", 60)
	store := newFakeTOTPStore()
	enc := mustEncryptor(t)

	h := NewTOTPHandler(store, newMockQuerier(user), enc, jwtMgr)
	h.SetIssuer("TestIssuer")

	// Start
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/enroll/start/", nil)
	req = setAuthUserFull(req, user)
	w := httptest.NewRecorder()
	h.EnrollStart(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("EnrollStart status = %d; body = %s", w.Code, w.Body.String())
	}
	var startBody map[string]any
	if err := json.NewDecoder(w.Body).Decode(&startBody); err != nil {
		t.Fatalf("decode start body: %v", err)
	}
	data := startBody["data"].(map[string]any)
	otpauthURL := data["otpauth_url"].(string)
	challenge := data["challenge"].(string)
	challengeToken := data["challenge_token"].(string)
	if !strings.Contains(otpauthURL, "TestIssuer") {
		t.Errorf("otpauth URL %q missing issuer", otpauthURL)
	}
	if challenge == "" || challengeToken == "" {
		t.Fatalf("missing challenge fields")
	}

	// Derive a TOTP code from the secret embedded in the challenge.
	rawSecret := decryptChallengeSecret(t, enc, challenge)
	code, err := totp.GenerateCode(rawSecret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}

	// Confirm
	confirmBody, _ := json.Marshal(map[string]string{
		"challenge_token": challengeToken,
		"challenge":       challenge,
		"code":            code,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/enroll/confirm/", bytes.NewReader(confirmBody))
	req.Header.Set("Content-Type", "application/json")
	req = setAuthUserFull(req, user)
	w = httptest.NewRecorder()
	h.EnrollConfirm(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("EnrollConfirm status = %d; body = %s", w.Code, w.Body.String())
	}
	var confirmResp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&confirmResp); err != nil {
		t.Fatalf("decode confirm body: %v", err)
	}
	confirmed := confirmResp["data"].(map[string]any)
	codes := confirmed["recovery_codes"].([]any)
	if len(codes) != auth.RecoveryCodeCount {
		t.Errorf("expected %d recovery codes, got %d", auth.RecoveryCodeCount, len(codes))
	}
	if !store.HasEnrollment(user.ID) {
		t.Error("expected enrollment row after confirm")
	}
}

func (s *fakeTOTPStore) HasEnrollment(uid uuid.UUID) bool {
	_, err := s.GetUserTOTPEnrollment(context.Background(), uid)
	return err == nil
}

// decryptChallengeSecret extracts the plaintext secret from the
// challenge blob the way the production confirm handler does — so the
// test exercises the same encrypt/decrypt path as the real flow.
func decryptChallengeSecret(t *testing.T, enc *auth.Encryptor, challenge string) string {
	t.Helper()
	payload, err := decodeTOTPChallenge(challenge)
	if err != nil {
		t.Fatalf("decodeTOTPChallenge: %v", err)
	}
	var c struct{ S, L string }
	if err := json.Unmarshal(payload, &c); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	secret, err := enc.Decrypt(c.S)
	if err != nil {
		t.Fatalf("decrypt secret: %v", err)
	}
	return secret
}

// --- Disable ---

func TestDisableRequiresPasswordAndCode(t *testing.T) {
	user := makeTestUser(t, true)
	jwtMgr := auth.NewJWTManager("test-secret", 60)
	store := newFakeTOTPStore()
	enc := mustEncryptor(t)
	// Pre-seed an enrollment so Disable has something to find.
	secret, _, _ := auth.GenerateSecret("alice", "TestIssuer")
	encSecret, _ := enc.Encrypt(secret)
	_, _ = store.UpsertUserTOTPEnrollment(context.Background(), sqlc.UpsertUserTOTPEnrollmentParams{
		UserID:          user.ID,
		SecretEncrypted: encSecret,
		Label:           "alice@TestIssuer",
		ConfirmedAt:     time.Now(),
	})

	h := NewTOTPHandler(store, newMockQuerier(user), enc, jwtMgr)

	// Wrong password
	body := mustJSON(t, map[string]string{"password": "wrong", "code": "000000"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/disable/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = setAuthUserFull(req, user)
	w := httptest.NewRecorder()
	h.Disable(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-password status = %d; want 401", w.Code)
	}
	if !store.HasEnrollment(user.ID) {
		t.Error("enrollment should still exist after wrong-password Disable")
	}

	// Right password, wrong code
	body = mustJSON(t, map[string]string{"password": "testpassword", "code": "000000"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/disable/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = setAuthUserFull(req, user)
	w = httptest.NewRecorder()
	h.Disable(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-code status = %d; want 401", w.Code)
	}
	if !store.HasEnrollment(user.ID) {
		t.Error("enrollment should still exist after wrong-code Disable")
	}

	// Right password + right code → success
	code, err := totp.GenerateCodeCustom(secret, time.Now(), totp.ValidateOpts{
		Period: auth.TOTPPeriod, Digits: auth.TOTPDigits, Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatalf("GenerateCodeCustom: %v", err)
	}
	body = mustJSON(t, map[string]string{"password": "testpassword", "code": code})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/disable/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = setAuthUserFull(req, user)
	w = httptest.NewRecorder()
	h.Disable(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("good-disable status = %d; body = %s", w.Code, w.Body.String())
	}
	if store.HasEnrollment(user.ID) {
		t.Error("enrollment should be gone after successful Disable")
	}
}

// --- Login gate ---

func TestLogin_TOTPRequiredAfterEnroll(t *testing.T) {
	user := makeTestUser(t, true)
	jwtMgr := auth.NewJWTManager("test-secret", 60)
	store := newFakeTOTPStore()
	enc := mustEncryptor(t)
	// Pre-seed enrollment.
	secret, _, _ := auth.GenerateSecret("alice", "TestIssuer")
	encSecret, _ := enc.Encrypt(secret)
	_, _ = store.UpsertUserTOTPEnrollment(context.Background(), sqlc.UpsertUserTOTPEnrollmentParams{
		UserID:          user.ID,
		SecretEncrypted: encSecret,
		ConfirmedAt:     time.Now(),
	})

	mock := newMockQuerier(user)
	authH := NewAuthHandler(mock, jwtMgr)
	totpH := NewTOTPHandler(store, mock, enc, jwtMgr)
	authH.SetTOTPGate(totpH)

	body := mustJSON(t, map[string]string{"email": user.Email, "password": "testpassword"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	authH.Login(w, req)

	if w.Code != http.StatusLocked {
		t.Fatalf("Login(enrolled user) status = %d; want 423 Locked", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["error"] != "totp_required" {
		t.Errorf("error = %v, want totp_required", resp["error"])
	}
	if _, ok := resp["challenge_token"].(string); !ok || resp["challenge_token"] == "" {
		t.Errorf("missing or empty challenge_token: %v", resp)
	}
}

func TestLogin_TOTPChallengeWithValidCode(t *testing.T) {
	user := makeTestUser(t, true)
	jwtMgr := auth.NewJWTManager("test-secret", 60)
	store := newFakeTOTPStore()
	enc := mustEncryptor(t)
	secret, _, _ := auth.GenerateSecret("alice", "TestIssuer")
	encSecret, _ := enc.Encrypt(secret)
	_, _ = store.UpsertUserTOTPEnrollment(context.Background(), sqlc.UpsertUserTOTPEnrollmentParams{
		UserID:          user.ID,
		SecretEncrypted: encSecret,
		ConfirmedAt:     time.Now(),
	})

	mock := newMockQuerier(user)
	authH := NewAuthHandler(mock, jwtMgr)
	totpH := NewTOTPHandler(store, mock, enc, jwtMgr)
	authH.SetTOTPGate(totpH)

	// Step 1: Login -> 423 + challenge.
	body := mustJSON(t, map[string]string{"email": user.Email, "password": "testpassword"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	authH.Login(w, req)
	if w.Code != http.StatusLocked {
		t.Fatalf("step1 status = %d; want 423", w.Code)
	}
	var loginResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&loginResp)
	challenge := loginResp["challenge_token"].(string)

	// Step 2: TOTP verify -> 200 + session pair.
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	body = mustJSON(t, map[string]any{"challenge_token": challenge, "code": code})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/verify/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	totpH.Verify(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("verify status = %d; body = %s", w.Code, w.Body.String())
	}
	var verifyResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&verifyResp)
	data := verifyResp["data"].(map[string]any)
	if data["token"] == nil || data["token"] == "" {
		t.Error("missing session token after verify")
	}
	if !responseHasCookie(w.Result(), middleware.SessionCookieName, true) {
		t.Fatalf("expected HttpOnly %s cookie after verify", middleware.SessionCookieName)
	}
	if !responseHasCookie(w.Result(), middleware.RefreshCookieName, true) {
		t.Fatalf("expected HttpOnly %s cookie after verify", middleware.RefreshCookieName)
	}
}

func TestLogin_TOTPChallengeWithRecoveryCode(t *testing.T) {
	user := makeTestUser(t, true)
	jwtMgr := auth.NewJWTManager("test-secret", 60)
	store := newFakeTOTPStore()
	enc := mustEncryptor(t)
	secret, _, _ := auth.GenerateSecret("alice", "TestIssuer")
	encSecret, _ := enc.Encrypt(secret)
	_, _ = store.UpsertUserTOTPEnrollment(context.Background(), sqlc.UpsertUserTOTPEnrollmentParams{
		UserID:          user.ID,
		SecretEncrypted: encSecret,
		ConfirmedAt:     time.Now(),
	})
	// Seed 1 recovery code.
	codes, hashes, _ := auth.GenerateRecoveryCodes(1)
	_ = store.InsertRecoveryCode(context.Background(), sqlc.InsertRecoveryCodeParams{
		UserID:   user.ID,
		CodeHash: hashes[0],
	})

	mock := newMockQuerier(user)
	authH := NewAuthHandler(mock, jwtMgr)
	totpH := NewTOTPHandler(store, mock, enc, jwtMgr)
	authH.SetTOTPGate(totpH)

	body := mustJSON(t, map[string]string{"email": user.Email, "password": "testpassword"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	authH.Login(w, req)
	var loginResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&loginResp)
	challenge := loginResp["challenge_token"].(string)

	body = mustJSON(t, map[string]any{"challenge_token": challenge, "code": codes[0], "use_recovery": true})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/verify/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	totpH.Verify(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("verify(recovery) status = %d; body = %s", w.Code, w.Body.String())
	}

	// Replay the same recovery code -> 401 (consumed).
	body = mustJSON(t, map[string]any{"challenge_token": challenge, "code": codes[0], "use_recovery": true})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/verify/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	totpH.Verify(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("replay status = %d; want 401 (recovery code already consumed)", w.Code)
	}
}

// TestLogin_TOTPLockoutAfterN exercises that repeated TOTP failures
// don't bypass the regular brute-force gate. We hit /auth/totp/verify
// with bad codes and assert each call gets a 401 — the rate limiter
// + the per-user lockout layered below handle the actual throttling
// in production (covered by middleware tests); this test pins the
// per-attempt 401 contract.
func TestLogin_TOTPLockoutAfterN(t *testing.T) {
	user := makeTestUser(t, true)
	jwtMgr := auth.NewJWTManager("test-secret", 60)
	store := newFakeTOTPStore()
	enc := mustEncryptor(t)
	secret, _, _ := auth.GenerateSecret("alice", "TestIssuer")
	encSecret, _ := enc.Encrypt(secret)
	_, _ = store.UpsertUserTOTPEnrollment(context.Background(), sqlc.UpsertUserTOTPEnrollmentParams{
		UserID:          user.ID,
		SecretEncrypted: encSecret,
		ConfirmedAt:     time.Now(),
	})

	mock := newMockQuerier(user)
	totpH := NewTOTPHandler(store, mock, enc, jwtMgr)
	challenge, _ := jwtMgr.GeneratePurposeToken(user.ID, auth.PurposeTOTPChallenge, auth.TOTPChallengeTTL)

	for i := 0; i < 5; i++ {
		body := mustJSON(t, map[string]any{"challenge_token": challenge, "code": "000000"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/verify/", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		totpH.Verify(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("attempt %d status = %d; want 401", i, w.Code)
		}
	}
}

// TestRequireMode_NewUserMustEnroll covers the auth.totp.require=true
// path: a brand-new user with the right password but no enrollment
// receives an enrollment-only challenge instead of a session.
func TestRequireMode_NewUserMustEnroll(t *testing.T) {
	user := makeTestUser(t, true)
	jwtMgr := auth.NewJWTManager("test-secret", 60)
	store := newFakeTOTPStore()
	enc := mustEncryptor(t)

	mock := newMockQuerier(user)
	authH := NewAuthHandler(mock, jwtMgr)
	totpH := NewTOTPHandler(store, mock, enc, jwtMgr)
	authH.SetTOTPGate(totpH)
	authH.SetTOTPRequireAll(true)

	body := mustJSON(t, map[string]string{"email": user.Email, "password": "testpassword"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	authH.Login(w, req)

	if w.Code != http.StatusLocked {
		t.Fatalf("status = %d; want 423 Locked (require=true + not enrolled)", w.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] != "totp_enrollment_required" {
		t.Errorf("error = %v; want totp_enrollment_required", resp["error"])
	}
	if _, ok := resp["challenge_token"].(string); !ok {
		t.Errorf("missing challenge_token")
	}
}

func TestAdminForceDisable_RequiresSuperuser(t *testing.T) {
	// Non-superuser → 403
	target := makeTestUser(t, true)
	nonAdmin := makeTestUser(t, true)
	nonAdmin.ID = uuid.New()
	nonAdmin.Email = "non-admin@example.com"
	nonAdmin.Username = "non-admin"
	nonAdmin.IsSuperuser = false

	store := newFakeTOTPStore()
	enc := mustEncryptor(t)
	encSecret, _ := enc.Encrypt("dummy")
	_, _ = store.UpsertUserTOTPEnrollment(context.Background(), sqlc.UpsertUserTOTPEnrollmentParams{
		UserID:          target.ID,
		SecretEncrypted: encSecret,
		ConfirmedAt:     time.Now(),
	})

	mock := newMockQuerier(target, nonAdmin)
	jwtMgr := auth.NewJWTManager("test-secret", 60)
	h := NewTOTPHandler(store, mock, enc, jwtMgr)

	req := newAdminTOTPRequest(http.MethodPost, target.ID, nonAdmin)
	w := httptest.NewRecorder()
	h.AdminForceDisable(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-superuser status = %d; want 403", w.Code)
	}
	if !store.HasEnrollment(target.ID) {
		t.Error("enrollment should remain after rejected admin force-disable")
	}

	// Superuser → 200 + enrollment gone.
	admin := makeTestUser(t, true)
	admin.ID = uuid.New()
	admin.Email = "root@example.com"
	admin.Username = "root"
	admin.IsSuperuser = true

	mock = newMockQuerier(target, admin)
	h = NewTOTPHandler(store, mock, enc, jwtMgr)
	req = newAdminTOTPRequest(http.MethodPost, target.ID, admin)
	w = httptest.NewRecorder()
	h.AdminForceDisable(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("superuser status = %d; want 200; body = %s", w.Code, w.Body.String())
	}
	if store.HasEnrollment(target.ID) {
		t.Error("enrollment should be gone after superuser force-disable")
	}
}

// newAdminTOTPRequest builds an *http.Request with the chi {id}
// URL param bound + the AuthenticatedUser context set. We use chi's
// routing context so chi.URLParam(r, "id") returns the target ID,
// matching the real route shape.
func newAdminTOTPRequest(method string, targetID uuid.UUID, actor sqlc.User) *http.Request {
	r := httptest.NewRequest(method, fmt.Sprintf("/api/v1/admin/users/%s/disable-totp/", targetID), nil)
	r = setAuthUserFull(r, actor)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", targetID.String())
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	return r.WithContext(ctx)
}

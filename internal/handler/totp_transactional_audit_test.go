package handler

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/cacheinvalidate"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pquerna/otp/totp"
)

// The staged store publishes neither credential changes nor audit rows until
// the callback and commit succeed. Existing HTTP tests use the same contract.
type totpTestTransaction struct {
	*fakeTOTPStore
	users      UserQuerier
	failAt     string
	audits     []sqlc.UpsertAuditOutboxParams
	lastLogins int
}

type totpTestRunner struct {
	store      *fakeTOTPStore
	users      UserQuerier
	failAt     string
	audits     []sqlc.UpsertAuditOutboxParams
	lastLogins int
	calls      int
}

func (s *totpTestRunner) run(ctx context.Context, fn func(TOTPMutationTx) error) error {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	s.calls++
	if s.failAt == "begin" {
		return errors.New("begin failed")
	}
	staged := &fakeTOTPStore{
		enrollments: maps.Clone(s.store.enrollments),
		codes:       append([]sqlc.UserTotpRecoveryCode(nil), s.store.codes...),
		failed:      maps.Clone(s.store.failed),
		challenges:  maps.Clone(s.store.challenges),
	}
	tx := &totpTestTransaction{fakeTOTPStore: staged, users: s.users, failAt: s.failAt}
	if err := fn(tx); err != nil {
		return err
	}
	if s.failAt == "commit" {
		return errors.New("commit failed")
	}
	s.store.enrollments, s.store.codes = staged.enrollments, staged.codes
	s.store.failed, s.store.challenges = staged.failed, staged.challenges
	s.audits = append(s.audits, tx.audits...)
	s.lastLogins += tx.lastLogins
	return nil
}

func newTestTOTPHandler(store *fakeTOTPStore, users UserQuerier, enc *auth.Encryptor, jwt *auth.JWTManager) *TOTPHandler {
	h := NewTOTPHandler(store, users, enc, jwt)
	runner := &totpTestRunner{store: store, users: users}
	h.SetRunTx(runner.run)
	return h
}

func (tx *totpTestTransaction) fail(operation string) error {
	if tx.failAt == operation {
		return errors.New(operation + " failed")
	}
	return nil
}

func (tx *totpTestTransaction) GetUserByIDForUpdate(ctx context.Context, id uuid.UUID) (sqlc.User, error) {
	if err := tx.fail("lock"); err != nil {
		return sqlc.User{}, err
	}
	return tx.users.GetUserByID(ctx, id)
}

func (tx *totpTestTransaction) UpsertUserTOTPEnrollment(ctx context.Context, arg sqlc.UpsertUserTOTPEnrollmentParams) (sqlc.UserTotpEnrollment, error) {
	if err := tx.fail("enrollment"); err != nil {
		return sqlc.UserTotpEnrollment{}, err
	}
	return tx.fakeTOTPStore.UpsertUserTOTPEnrollment(ctx, arg)
}

func (tx *totpTestTransaction) DeleteUserTOTPEnrollment(ctx context.Context, id uuid.UUID) error {
	if err := tx.fail("delete_enrollment"); err != nil {
		return err
	}
	return tx.fakeTOTPStore.DeleteUserTOTPEnrollment(ctx, id)
}

func (tx *totpTestTransaction) DeleteRecoveryCodesByUser(ctx context.Context, id uuid.UUID) error {
	if err := tx.fail("delete_codes"); err != nil {
		return err
	}
	return tx.fakeTOTPStore.DeleteRecoveryCodesByUser(ctx, id)
}

func (tx *totpTestTransaction) InsertRecoveryCode(ctx context.Context, arg sqlc.InsertRecoveryCodeParams) error {
	// Fail after one insertion to exercise rollback of partially replaced sets.
	if len(tx.codes) > 0 {
		if err := tx.fail("insert_code"); err != nil {
			return err
		}
	}
	return tx.fakeTOTPStore.InsertRecoveryCode(ctx, arg)
}

func (tx *totpTestTransaction) ConsumeRecoveryCode(ctx context.Context, arg sqlc.ConsumeRecoveryCodeParams) (int64, error) {
	if err := tx.fail("consume_code"); err != nil {
		return 0, err
	}
	return tx.fakeTOTPStore.ConsumeRecoveryCode(ctx, arg)
}

func (tx *totpTestTransaction) ConsumeJWTChallenge(ctx context.Context, arg sqlc.ConsumeJWTChallengeParams) (int64, error) {
	if err := tx.fail("challenge"); err != nil {
		return 0, err
	}
	return tx.fakeTOTPStore.ConsumeJWTChallenge(ctx, arg)
}

func (tx *totpTestTransaction) ResetFailedLoginCount(ctx context.Context, id uuid.UUID) error {
	if err := tx.fail("reset"); err != nil {
		return err
	}
	return tx.fakeTOTPStore.ResetFailedLoginCount(ctx, id)
}

func (tx *totpTestTransaction) RecordFailedLoginAttempt(ctx context.Context, arg sqlc.RecordFailedLoginAttemptParams) (sqlc.User, error) {
	if err := tx.fail("lockout"); err != nil {
		return sqlc.User{}, err
	}
	return tx.fakeTOTPStore.RecordFailedLoginAttempt(ctx, arg)
}

func (tx *totpTestTransaction) TouchUserTOTPLastUsed(ctx context.Context, arg sqlc.TouchUserTOTPLastUsedParams) error {
	if err := tx.fail("touch"); err != nil {
		return err
	}
	return tx.fakeTOTPStore.TouchUserTOTPLastUsed(ctx, arg)
}

func (tx *totpTestTransaction) UpdateUserLastLogin(context.Context, uuid.UUID) error {
	if err := tx.fail("last_login"); err != nil {
		return err
	}
	tx.lastLogins++
	return nil
}

func (tx *totpTestTransaction) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if err := tx.fail("audit"); err != nil {
		return sqlc.AuditOutbox{}, err
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestTOTPMutationsRequireTransactionRunner(t *testing.T) {
	h := NewTOTPHandler(nil, nil, nil, nil)
	for name, endpoint := range map[string]http.HandlerFunc{
		"start": h.EnrollStart, "confirm": h.EnrollConfirm,
		"disable": h.Disable, "regenerate": h.RegenerateRecoveryCodes,
		"verify": h.Verify, "admin_disable": h.AdminForceDisable,
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			endpoint(w, httptest.NewRequest(http.MethodPost, "/", nil))
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "runner_unwired") {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

type totpMutationFixture struct {
	handler                                      *TOTPHandler
	runner                                       *totpTestRunner
	user                                         sqlc.User
	secret, encrypted, code, recovery, challenge string
}

type totpInvalidations struct{ calls int }

func (*totpInvalidations) Healthy() bool { return true }
func (b *totpInvalidations) Broadcast(context.Context, cacheinvalidate.Kind, string) error {
	b.calls++
	return nil
}

func newTOTPMutationFixture(t *testing.T) *totpMutationFixture {
	t.Helper()
	user := makeTestUser(t, true)
	user.IsSuperuser = true
	enc := mustEncryptor(t)
	secret, _, err := auth.GenerateSecret(user.Username, "Astronomer")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := enc.Encrypt(secret)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	codes, hashes, err := auth.GenerateRecoveryCodes(1)
	if err != nil {
		t.Fatal(err)
	}
	store := newFakeTOTPStore()
	store.enrollments[user.ID] = sqlc.UserTotpEnrollment{UserID: user.ID, SecretEncrypted: encrypted, Label: "existing"}
	store.codes = []sqlc.UserTotpRecoveryCode{{ID: uuid.New(), UserID: user.ID, CodeHash: hashes[0]}}
	store.failed[user.ID] = 4
	users := newMockQuerier(user)
	manager := auth.MustNewJWTManager("test-secret", 60)
	challenge, err := manager.GeneratePurposeToken(user.ID, auth.PurposeTOTPChallenge, auth.TOTPChallengeTTL)
	if err != nil {
		t.Fatal(err)
	}
	h := NewTOTPHandler(store, users, enc, manager)
	runner := &totpTestRunner{store: store, users: users}
	h.SetRunTx(runner.run)
	return &totpMutationFixture{handler: h, runner: runner, user: user, secret: secret, encrypted: encrypted, code: code, recovery: codes[0], challenge: challenge}
}

func (f *totpMutationFixture) invoke(t *testing.T, operation string) *httptest.ResponseRecorder {
	t.Helper()
	var endpoint http.HandlerFunc
	var body map[string]any
	switch operation {
	case "enroll":
		payload := mustJSON(t, enrollChallengeClaims{Secret: f.encrypted, Label: "replacement"})
		body = map[string]any{"challenge_token": f.challenge, "challenge": encodeTOTPChallenge(payload), "code": f.code}
		endpoint = f.handler.EnrollConfirm
	case "disable":
		body = map[string]any{"password": "testpassword", "code": f.code}
		endpoint = f.handler.Disable
	case "regenerate":
		body = map[string]any{"code": f.code}
		endpoint = f.handler.RegenerateRecoveryCodes
	case "verify", "recovery", "failure":
		code := f.code
		if operation == "recovery" {
			code = f.recovery
		}
		if operation == "failure" {
			code = "invalid-code"
		}
		body = map[string]any{"challenge_token": f.challenge, "code": code, "use_recovery": operation != "verify"}
		endpoint = f.handler.Verify
	case "admin_disable":
		w := httptest.NewRecorder()
		f.handler.AdminForceDisable(w, newAdminTOTPRequest(http.MethodPost, f.user.ID, f.user))
		return w
	default:
		t.Fatalf("unknown operation %s", operation)
	}
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(mustJSON(t, body)))
	r = setAuthUserFull(r, f.user)
	if operation == "enroll" {
		r = r.WithContext(reqctx.WithTOTPEnrollOnly(r.Context()))
	}
	w := httptest.NewRecorder()
	endpoint(w, r)
	return w
}

func TestTOTPMutationsRollbackEveryFailure(t *testing.T) {
	operations := map[string][]string{
		"enroll":        {"lock", "challenge", "reset", "enrollment", "delete_codes", "insert_code", "last_login"},
		"disable":       {"lock", "delete_enrollment", "delete_codes"},
		"admin_disable": {"lock", "delete_enrollment", "delete_codes"},
		"regenerate":    {"lock", "delete_codes", "insert_code"},
		"verify":        {"lock", "challenge", "reset", "touch", "last_login"},
		"recovery":      {"lock", "consume_code", "challenge", "reset", "touch", "last_login"},
		"failure":       {"lock", "consume_code", "lockout"},
	}
	for operation, failures := range operations {
		for _, failure := range append(failures, "begin", "audit", "commit") {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				f := newTOTPMutationFixture(t)
				emails := &recordingEmailNotifier{}
				f.handler.SetEmailNotifier(emails)
				invalidations := &totpInvalidations{}
				f.handler.jwt.SetCacheInvalidationCoordinator(invalidations)
				store := f.runner.store
				enrollments, counts, challenges := maps.Clone(store.enrollments), maps.Clone(store.failed), maps.Clone(store.challenges)
				codes := append([]sqlc.UserTotpRecoveryCode(nil), store.codes...)
				f.runner.failAt = failure
				w := f.invoke(t, operation)
				if w.Code != http.StatusServiceUnavailable {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
				if !reflect.DeepEqual(store.enrollments, enrollments) || !reflect.DeepEqual(store.codes, codes) ||
					!reflect.DeepEqual(store.failed, counts) || !reflect.DeepEqual(store.challenges, challenges) {
					t.Fatal("credential or lockout state escaped rollback")
				}
				if len(f.runner.audits) != 0 || f.runner.lastLogins != 0 || len(w.Result().Cookies()) != 0 || emails.calls.Load() != 0 {
					t.Fatal("audit, session, email, or last-login state escaped rollback")
				}
				if invalidations.calls != 0 {
					t.Fatal("JWT cache invalidation escaped rollback")
				}
				if f.runner.calls != 1 {
					t.Fatalf("transactions=%d, want 1", f.runner.calls)
				}
				// A failure must leave the challenge and recovery code usable.
				f.runner.failAt = ""
				w = f.invoke(t, operation)
				want := http.StatusOK
				if operation == "failure" {
					want = http.StatusUnauthorized
				}
				if w.Code != want {
					t.Fatalf("retry status=%d body=%s", w.Code, w.Body.String())
				}
				if len(f.runner.audits) != 1 {
					t.Fatalf("committed audits=%d, want 1", len(f.runner.audits))
				}
			})
		}
	}
}

func TestTOTPRecoveryReplayDoesNotConsumeAnotherCode(t *testing.T) {
	f := newTOTPMutationFixture(t)
	claims, err := f.handler.jwt.ValidateToken(f.challenge)
	if err != nil {
		t.Fatal(err)
	}
	f.runner.store.challenges[claims.ID] = struct{}{}
	w := f.invoke(t, "recovery")
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "invalid_challenge") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if f.runner.store.codes[0].UsedAt.Valid || f.runner.store.failed[f.user.ID] != 4 || len(f.runner.audits) != 0 {
		t.Fatal("replayed challenge consumed recovery code or changed account state")
	}
}

func TestTOTPMutationsCommitStateWithAudit(t *testing.T) {
	for operation, action := range map[string]string{
		"enroll": "auth.totp.enrolled", "disable": "auth.totp.disabled",
		"admin_disable": "admin.user.totp_disabled", "regenerate": "auth.totp.recovery_codes_regenerated",
		"verify": "auth.totp.verified", "recovery": "auth.totp.recovery_code_consumed",
		"failure": "auth.totp.verify_failed",
	} {
		t.Run(operation, func(t *testing.T) {
			f := newTOTPMutationFixture(t)
			invalidations := &totpInvalidations{}
			f.handler.jwt.SetCacheInvalidationCoordinator(invalidations)
			w := f.invoke(t, operation)
			wantStatus := http.StatusOK
			if operation == "failure" {
				wantStatus = http.StatusUnauthorized
			}
			if w.Code != wantStatus {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if f.runner.calls != 1 || len(f.runner.audits) != 1 || f.runner.audits[0].Action != action {
				t.Fatalf("transactions=%d audits=%+v", f.runner.calls, f.runner.audits)
			}
			store := f.runner.store
			switch operation {
			case "enroll", "regenerate":
				if len(store.codes) != auth.RecoveryCodeCount {
					t.Fatalf("recovery count=%d", len(store.codes))
				}
				for _, code := range store.codes {
					if code.CodeHash == auth.HashRecoveryCode(f.recovery) {
						t.Fatal("old recovery code survived replacement")
					}
				}
			case "disable", "admin_disable":
				if len(store.enrollments) != 0 || len(store.codes) != 0 {
					t.Fatal("enrollment or codes survived disable")
				}
			case "recovery":
				if !store.codes[0].UsedAt.Valid {
					t.Fatal("recovery code was not consumed")
				}
			case "failure":
				if store.failed[f.user.ID] != 5 {
					t.Fatal("failed attempt did not commit with audit")
				}
			}
			if operation == "enroll" || operation == "verify" || operation == "recovery" {
				if len(store.challenges) != 1 || store.failed[f.user.ID] != 0 || f.runner.lastLogins != 1 || invalidations.calls != 1 {
					t.Fatal("challenge, safeguard reset, last login, or cache invalidation missing")
				}
				if len(w.Result().Cookies()) == 0 {
					t.Fatal("committed session did not produce cookies")
				}
			} else if len(w.Result().Cookies()) != 0 || invalidations.calls != 0 {
				t.Fatal("non-login mutation issued a session or invalidated the challenge")
			}
		})
	}
}

func TestTOTPSessionCompletionRechecksAccountUnderLock(t *testing.T) {
	for _, operation := range []string{"enroll", "verify", "recovery"} {
		for _, state := range []string{"disabled", "locked"} {
			t.Run(operation+"/"+state, func(t *testing.T) {
				f := newTOTPMutationFixture(t)
				current := f.user
				want := http.StatusUnauthorized
				if state == "locked" {
					current.LockedUntil = pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true}
					want = http.StatusLocked
				} else {
					current.IsActive = false
					if operation == "enroll" {
						want = http.StatusForbidden
					}
				}
				// The nontransactional user read still sees the active account.
				// Only the locked row reflects the concurrent security change.
				f.runner.users = newMockQuerier(current)
				w := f.invoke(t, operation)
				if w.Code != want {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
				store := f.runner.store
				if len(store.challenges) != 0 || store.codes[0].UsedAt.Valid || store.failed[f.user.ID] != 4 || f.runner.lastLogins != 0 || len(w.Result().Cookies()) != 0 {
					t.Fatal("account security change was bypassed during session completion")
				}
			})
		}
	}
}

func TestTOTPSessionPolicyResolvesBeforeTransaction(t *testing.T) {
	for _, operation := range []string{"enroll", "verify", "recovery"} {
		t.Run(operation, func(t *testing.T) {
			f := newTOTPMutationFixture(t)
			calls := 0
			f.handler.jwt.SetAccessTokenTTLProvider(func(context.Context) time.Duration {
				calls++
				if f.runner.calls != 0 {
					t.Error("session policy read acquired a pooled connection while TOTP transaction was open")
				}
				return 17 * time.Minute
			})
			w := f.invoke(t, operation)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if calls != 1 {
				t.Fatalf("policy lookups=%d, want 1", calls)
			}
		})
	}
}

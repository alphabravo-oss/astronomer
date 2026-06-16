package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/email"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// smtpFakeQuerier implements the narrow surface SMTPHandler needs.
type smtpFakeQuerier struct {
	row        *sqlc.SmtpSettings
	users      map[uuid.UUID]sqlc.User
	rows       []sqlc.EmailMessage
	upserts    int32
	countCalls atomic.Int32
}

func (f *smtpFakeQuerier) GetSMTPSettings(_ context.Context, _ uuid.UUID) (sqlc.SmtpSettings, error) {
	if f.row == nil {
		return sqlc.SmtpSettings{}, pgx.ErrNoRows
	}
	return *f.row, nil
}

func (f *smtpFakeQuerier) UpsertSMTPSettings(_ context.Context, arg sqlc.UpsertSMTPSettingsParams) (sqlc.SmtpSettings, error) {
	atomic.AddInt32(&f.upserts, 1)
	row := sqlc.SmtpSettings{
		ID:                arg.ID,
		Enabled:           arg.Enabled,
		Host:              arg.Host,
		Port:              arg.Port,
		Username:          arg.Username,
		PasswordEncrypted: arg.PasswordEncrypted,
		FromAddress:       arg.FromAddress,
		FromName:          arg.FromName,
		AuthMechanism:     arg.AuthMechanism,
		Encryption:        arg.Encryption,
		RequireTls:        arg.RequireTls,
		TimeoutSeconds:    arg.TimeoutSeconds,
	}
	f.row = &row
	return row, nil
}

func (f *smtpFakeQuerier) ListEmailMessages(_ context.Context, _ sqlc.ListEmailMessagesParams) ([]sqlc.EmailMessage, error) {
	return f.rows, nil
}

func (f *smtpFakeQuerier) CountEmailMessages(_ context.Context) (int64, error) {
	f.countCalls.Add(1)
	return int64(len(f.rows)), nil
}

func (f *smtpFakeQuerier) GetUserByID(_ context.Context, id uuid.UUID) (sqlc.User, error) {
	if u, ok := f.users[id]; ok {
		return u, nil
	}
	return sqlc.User{}, pgx.ErrNoRows
}

// T6.064 — baseline-guard stubs. Default behaviour returns ErrNoRows
// so existing tests run unchanged.
func (f *smtpFakeQuerier) GetActiveComplianceBaselineApplication(_ context.Context) (sqlc.ComplianceBaselineApplication, error) {
	return sqlc.ComplianceBaselineApplication{}, pgx.ErrNoRows
}

func (f *smtpFakeQuerier) GetComplianceBaseline(_ context.Context, _ uuid.UUID) (sqlc.ComplianceBaseline, error) {
	return sqlc.ComplianceBaseline{}, pgx.ErrNoRows
}

func buildSuperuserCtx(t *testing.T, q *smtpFakeQuerier) (context.Context, uuid.UUID) {
	t.Helper()
	id := uuid.New()
	q.users = map[uuid.UUID]sqlc.User{
		id: {ID: id, Username: "admin", IsSuperuser: true, IsActive: true},
	}
	ctx := middleware.SetAuthenticatedUserForTest(context.Background(), &middleware.AuthenticatedUser{
		ID:       id.String(),
		Username: "admin",
	})
	return ctx, id
}

func newEncryptor(t *testing.T) *auth.Encryptor {
	t.Helper()
	k, err := auth.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	enc, err := auth.NewEncryptor(k)
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	return enc
}

func TestSMTPHandler_GetSetTest(t *testing.T) {
	enc := newEncryptor(t)
	q := &smtpFakeQuerier{}
	ctx, _ := buildSuperuserCtx(t, q)
	h := NewSMTPHandler(q, enc, nil)

	// GET on an empty DB should return the safe defaults.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/smtp/", nil).WithContext(ctx)
	h.Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Get empty: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var firstGet struct {
		Data smtpSettingsResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &firstGet); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if firstGet.Data.Enabled || firstGet.Data.Port != 587 || firstGet.Data.AuthMechanism != "plain" {
		t.Errorf("default payload wrong: %+v", firstGet.Data)
	}
	if firstGet.Data.Password != PasswordSentinelEncrypted {
		t.Errorf("password should be sentinel, got %q", firstGet.Data.Password)
	}

	// PUT a fresh config.
	put := map[string]any{
		"enabled":         true,
		"host":            "smtp.example.com",
		"port":            587,
		"username":        "user",
		"password":        "topsecret",
		"from_address":    "noreply@example.com",
		"from_name":       "Test",
		"auth_mechanism":  "plain",
		"encryption":      "starttls",
		"require_tls":     true,
		"timeout_seconds": 30,
	}
	body, _ := json.Marshal(put)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/admin/smtp/", bytes.NewReader(body)).WithContext(ctx)
	h.Update(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Update: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Second GET — the ciphertext must NEVER come back to the
	// client. The sentinel + password_configured=true tell the
	// dashboard "there's a password but I'm not showing it."
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/smtp/", nil).WithContext(ctx)
	h.Get(rec, req)
	var second struct {
		Data smtpSettingsResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if second.Data.Password != PasswordSentinelEncrypted {
		t.Errorf("password leaked: %q", second.Data.Password)
	}
	if !second.Data.PasswordConfigured {
		t.Errorf("PasswordConfigured should be true after PUT")
	}

	// Second PUT keeps the password (sentinel) — verify the stored
	// ciphertext is unchanged.
	prevCipher := q.row.PasswordEncrypted
	put["password"] = PasswordSentinelEncrypted
	put["enabled"] = false
	body, _ = json.Marshal(put)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/admin/smtp/", bytes.NewReader(body)).WithContext(ctx)
	h.Update(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Update keep: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if q.row.PasswordEncrypted != prevCipher {
		t.Errorf("password ciphertext rewritten when sentinel was sent: prev=%q new=%q", prevCipher, q.row.PasswordEncrypted)
	}

	// Test the test-send. Inject a captureSender to avoid real SMTP.
	var captured atomic.Int32
	h.SetTestSenderFactory(func(cfg email.Settings) SMTPTestSender {
		return testSenderFn(func(_ context.Context, _ email.Message) error {
			captured.Add(1)
			return nil
		})
	})

	// Re-enable for the test send.
	q.row.Enabled = true
	rec = httptest.NewRecorder()
	body, _ = json.Marshal(map[string]string{"recipient": "ops@example.com"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/smtp/test/", bytes.NewReader(body)).WithContext(ctx)
	h.Test(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("Test: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if captured.Load() != 1 {
		t.Errorf("test sender should have been called once, got %d", captured.Load())
	}
}

func TestSMTPHandler_RejectsBadConfig(t *testing.T) {
	enc := newEncryptor(t)
	q := &smtpFakeQuerier{}
	ctx, _ := buildSuperuserCtx(t, q)
	h := NewSMTPHandler(q, enc, nil)

	for _, tc := range []struct {
		name string
		put  map[string]any
		want string
	}{
		{"missing host", map[string]any{"enabled": true, "from_address": "n@e.com", "port": 25}, "host"},
		{"missing from", map[string]any{"enabled": true, "host": "smtp", "port": 25}, "from_address"},
		{"bad port", map[string]any{"enabled": true, "host": "smtp", "from_address": "n@e.com", "port": 70000}, "port"},
		{"bad from", map[string]any{"enabled": true, "host": "smtp", "from_address": "<bad>>", "port": 25}, "from_address"},
		{"bad auth", map[string]any{"enabled": true, "host": "smtp", "from_address": "n@e.com", "port": 25, "auth_mechanism": "wat"}, "auth_mechanism"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.put)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body)).WithContext(ctx)
			h.Update(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("want body to mention %q, got %s", tc.want, rec.Body.String())
			}
		})
	}
}

func TestSMTPHandler_RequiresSuperuser(t *testing.T) {
	enc := newEncryptor(t)
	q := &smtpFakeQuerier{}
	id := uuid.New()
	q.users = map[uuid.UUID]sqlc.User{id: {ID: id, Username: "alice", IsSuperuser: false, IsActive: true}}
	ctx := middleware.SetAuthenticatedUserForTest(context.Background(), &middleware.AuthenticatedUser{
		ID:       id.String(),
		Username: "alice",
	})
	h := NewSMTPHandler(q, enc, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	h.Get(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("GET should 403 for non-superuser, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{}`)).WithContext(ctx)
	h.Update(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("PUT should 403 for non-superuser, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"recipient":"x@y.com"}`)).WithContext(ctx)
	h.Test(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("Test should 403 for non-superuser, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	h.List(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("List should 403 for non-superuser, got %d", rec.Code)
	}
}

// testSenderFn lets the tests inject a closure as the SMTP test sender.
type testSenderFn func(ctx context.Context, msg email.Message) error

func (f testSenderFn) Send(ctx context.Context, msg email.Message) error { return f(ctx, msg) }

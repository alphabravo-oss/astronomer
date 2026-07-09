package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// AUTH-R01: CreateUser with min_length override 16 rejects a 12-char password.
func TestCreateUser_PasswordPolicyMinLengthFromSettings(t *testing.T) {
	q := &createUserTrackingQuerier{}
	h := NewResourceHandlerWithQueries(q, nil)

	// Seed settings cache with password.min_length=16 (JSON number).
	reader := newFakeSettingsQuerier(sqlc.User{})
	reader.rows["password.min_length"] = sqlc.PlatformSetting{
		Key:   "password.min_length",
		Value: []byte(`16`),
	}
	cache := NewSettingsCache(reader, 0)
	h.SetSettingsCache(cache)

	body := `{"email":"a@example.com","username":"alice","password":"ValidPassw01"}` // 12 chars
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.CreateUser(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", rec.Code, rec.Body.String())
	}
	if q.createN != 0 {
		t.Fatal("CreateUser store must not be called when policy rejects password")
	}
}

// AUTH-R01: nil settings cache keeps default policy (12-char complex ok).
func TestCreateUser_DefaultPasswordPolicyWhenNoSettings(t *testing.T) {
	q := &createUserTrackingQuerier{}
	h := NewResourceHandlerWithQueries(q, nil)
	body := `{"email":"a@example.com","username":"alice","password":"ValidPassw01"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.CreateUser(rec, req)
	if rec.Code >= 400 {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if q.createN != 1 {
		t.Fatalf("CreateUser store calls=%d want 1", q.createN)
	}
}

// Ensure SettingsCache implements auth.PasswordPolicySettings at compile time
// via LoadPasswordPolicy usage above; this keeps the wiring honest.
var _ interface {
	IntValue(ctx context.Context, key string, fallback int) int
	BoolValue(ctx context.Context, key string, fallback bool) bool
} = (*SettingsCache)(nil)

package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type totpEnrollmentLookup struct {
	TOTPQuerier
	err   error
	calls int
}

func (q *totpEnrollmentLookup) GetUserTOTPEnrollment(context.Context, uuid.UUID) (sqlc.UserTotpEnrollment, error) {
	q.calls++
	return sqlc.UserTotpEnrollment{}, q.err
}

func TestTOTPEnrollmentLookupDistinguishesAbsenceFromOutage(t *testing.T) {
	for _, tc := range []struct {
		name              string
		store             TOTPQuerier
		enrolled, wantErr bool
	}{
		{name: "unwired", wantErr: true},
		{name: "enrolled", store: &totpEnrollmentLookup{}, enrolled: true},
		{name: "absent", store: &totpEnrollmentLookup{err: pgx.ErrNoRows}},
		{name: "wrapped absence", store: &totpEnrollmentLookup{err: fmt.Errorf("lookup: %w", pgx.ErrNoRows)}},
		{name: "outage", store: &totpEnrollmentLookup{err: errors.New("database unavailable")}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewTOTPHandler(tc.store, nil, nil, nil)
			enrolled, err := h.IsEnrolled(context.Background(), uuid.New())
			if enrolled != tc.enrolled || (err != nil) != tc.wantErr {
				t.Fatalf("enrolled=%v error=%v", enrolled, err)
			}
		})
	}
}

func TestTOTPEnrollmentReadOutageBlocksSessionMint(t *testing.T) {
	for _, flow := range []string{"login_optional", "login_required", "refresh_required"} {
		t.Run(flow, func(t *testing.T) {
			user := makeTestUser(t, true)
			users := newMockQuerier(user)
			jwt := auth.MustNewJWTManager("test-secret", 60)
			h := NewAuthHandler(users, jwt)
			wireAuthTestMutationTx(h, &authTestMutationTx{users: users})
			lookup := &totpEnrollmentLookup{err: errors.New("database unavailable")}
			h.SetTOTPGate(NewTOTPHandler(lookup, users, nil, jwt))
			h.SetTOTPRequireAll(flow != "login_optional")
			endpoint := h.Login
			body := map[string]string{"email": user.Email, "password": "testpassword"}
			if flow == "refresh_required" {
				_, refresh, err := jwt.GenerateTokenPair(user.ID)
				if err != nil {
					t.Fatal(err)
				}
				endpoint = h.Refresh
				body = nil
				r := httptest.NewRequest(http.MethodPost, "/", nil)
				r.AddCookie(&http.Cookie{Name: auth.RefreshCookieName, Value: refresh})
				r.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "csrf-token"})
				r.Header.Set("X-CSRF-Token", "csrf-token")
				w := httptest.NewRecorder()
				endpoint(w, r)
				if w.Code != http.StatusServiceUnavailable {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
				if len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "challenge_token") || strings.Contains(w.Body.String(), "\"token\"") {
					t.Fatal("enrollment outage issued an authentication credential")
				}
				if lookup.calls != 1 {
					t.Fatalf("enrollment lookups=%d, want 1", lookup.calls)
				}
				return
			}
			r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(mustJSON(t, body)))
			w := httptest.NewRecorder()
			endpoint(w, r)
			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "challenge_token") || strings.Contains(w.Body.String(), "\"token\"") {
				t.Fatal("enrollment outage issued an authentication credential")
			}
			if lookup.calls != 1 {
				t.Fatalf("enrollment lookups=%d, want 1", lookup.calls)
			}
		})
	}
}

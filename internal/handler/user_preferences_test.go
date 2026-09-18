package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type preferenceStoreFake struct {
	row    *sqlc.UserPreference
	audits []sqlc.UpsertAuditOutboxParams
}

func (f *preferenceStoreFake) GetUserPreferences(_ context.Context, _ uuid.UUID) (sqlc.UserPreference, error) {
	if f.row == nil {
		return sqlc.UserPreference{}, pgx.ErrNoRows
	}
	return *f.row, nil
}

type preferenceTxFake struct {
	row      sqlc.UserPreference
	audits   []sqlc.UpsertAuditOutboxParams
	auditErr error
}

func (f *preferenceTxFake) UpsertUserPreferences(_ context.Context, arg sqlc.UpsertUserPreferencesParams) (sqlc.UserPreference, error) {
	f.row = sqlc.UserPreference{
		UserID: arg.UserID, Theme: arg.Theme, TableDensity: arg.TableDensity,
		LandingRoute: arg.LandingRoute, TimeFormat: arg.TimeFormat,
		Favorites: arg.Favorites, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	return f.row, nil
}

func (f *preferenceTxFake) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	f.audits = append(f.audits, arg)
	return sqlc.AuditOutbox{}, f.auditErr
}

func preferenceRequest(method string, userID uuid.UUID, body any) *http.Request {
	var encoded []byte
	if body != nil {
		encoded, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, "/api/v1/auth/me/preferences/", bytes.NewReader(encoded))
	ctx := reqctx.WithUser(r.Context(), &reqctx.User{ID: userID.String(), AuthMethod: "jwt"})
	return r.WithContext(ctx)
}

func TestGetUserPreferencesReturnsCanonicalDefaults(t *testing.T) {
	store := &preferenceStoreFake{}
	h := NewAuthHandler(nil, nil)
	h.SetUserPreferences(store, nil)
	w := httptest.NewRecorder()
	h.GetUserPreferences(w, preferenceRequest(http.MethodGet, uuid.New(), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	for _, want := range []string{`"theme":"system"`, `"table_density":"comfortable"`, `"landing_route":"/dashboard"`, `"favorites":[]`} {
		if !bytes.Contains(w.Body.Bytes(), []byte(want)) {
			t.Errorf("response missing %s: %s", want, w.Body.String())
		}
	}
}

func TestPutUserPreferencesCommitsWithAuditIntent(t *testing.T) {
	store := &preferenceStoreFake{}
	tx := &preferenceTxFake{}
	h := NewAuthHandler(nil, nil)
	h.SetUserPreferences(store, func(_ context.Context, fn func(UserPreferencesMutationTx) error) error {
		if err := fn(tx); err != nil {
			return err
		}
		row := tx.row
		store.row = &row
		store.audits = append(store.audits, tx.audits...)
		return nil
	})
	body := map[string]any{
		"theme": "dark", "table_density": "compact",
		"landing_route": "/dashboard/clusters", "time_format": "24h",
		"favorites": []string{"/dashboard/clusters", "/dashboard/audit"},
	}
	w := httptest.NewRecorder()
	h.PutUserPreferences(w, preferenceRequest(http.MethodPut, uuid.New(), body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if store.row == nil || store.row.TableDensity != "compact" {
		t.Fatalf("stored row = %#v", store.row)
	}
	if len(store.audits) != 1 || store.audits[0].Action != "user.preferences.updated" {
		t.Fatalf("audit rows = %#v", store.audits)
	}
}

func TestPutUserPreferencesRejectsUnknownAndUnregisteredValues(t *testing.T) {
	for _, body := range []string{
		`{"theme":"sepia","table_density":"compact","landing_route":"/dashboard","time_format":"24h","favorites":[]}`,
		`{"theme":"dark","table_density":"compact","landing_route":"/dashboard","time_format":"24h","favorites":[],"extra":true}`,
	} {
		t.Run(body, func(t *testing.T) {
			store := &preferenceStoreFake{}
			h := NewAuthHandler(nil, nil)
			h.SetUserPreferences(store, func(_ context.Context, _ func(UserPreferencesMutationTx) error) error { return nil })
			r := httptest.NewRequest(http.MethodPut, "/api/v1/auth/me/preferences/", bytes.NewBufferString(body))
			ctx := reqctx.WithUser(r.Context(), &reqctx.User{ID: uuid.NewString()})
			w := httptest.NewRecorder()
			h.PutUserPreferences(w, r.WithContext(ctx))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestPutUserPreferencesRollsBackWhenAuditUnavailable(t *testing.T) {
	store := &preferenceStoreFake{}
	tx := &preferenceTxFake{auditErr: errors.New("database unavailable")}
	h := NewAuthHandler(nil, nil)
	h.SetUserPreferences(store, func(_ context.Context, fn func(UserPreferencesMutationTx) error) error {
		return fn(tx)
	})
	body := map[string]any{
		"theme": "dark", "table_density": "compact", "landing_route": "/dashboard",
		"time_format": "12h", "favorites": []string{},
	}
	w := httptest.NewRecorder()
	h.PutUserPreferences(w, preferenceRequest(http.MethodPut, uuid.New(), body))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if store.row != nil {
		t.Fatal("preference row committed without mandatory audit intent")
	}
}

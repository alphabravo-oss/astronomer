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
		Favorites: arg.Favorites, PinnedClusters: arg.PinnedClusters,
		RowsPerPage: arg.RowsPerPage, DateFormat: arg.DateFormat,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
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
	for _, want := range []string{`"theme":"system"`, `"table_density":"comfortable"`, `"landing_route":"/dashboard"`, `"favorites":[]`, `"pinned_clusters":[]`} {
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

func TestPutUserPreferencesRoundTripsPinnedClusters(t *testing.T) {
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
	pinned := []string{
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
	}
	body := map[string]any{
		"theme": "dark", "table_density": "compact",
		"landing_route": "/dashboard/clusters", "time_format": "24h",
		"favorites": []string{}, "pinned_clusters": pinned,
	}
	w := httptest.NewRecorder()
	h.PutUserPreferences(w, preferenceRequest(http.MethodPut, uuid.New(), body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	for _, id := range pinned {
		if !bytes.Contains(w.Body.Bytes(), []byte(id)) {
			t.Errorf("response missing pinned cluster %s: %s", id, w.Body.String())
		}
	}

	// A second GET (simulating a fresh session) must read back the same
	// pinned clusters from the stored row.
	getStore := &preferenceStoreFake{row: store.row}
	h2 := NewAuthHandler(nil, nil)
	h2.SetUserPreferences(getStore, nil)
	getW := httptest.NewRecorder()
	h2.GetUserPreferences(getW, preferenceRequest(http.MethodGet, uuid.New(), nil))
	if getW.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", getW.Code, getW.Body.String())
	}
	for _, id := range pinned {
		if !bytes.Contains(getW.Body.Bytes(), []byte(id)) {
			t.Errorf("GET response missing pinned cluster %s: %s", id, getW.Body.String())
		}
	}
}

func TestPutUserPreferencesDefaultsOmittedPinnedClusters(t *testing.T) {
	store := &preferenceStoreFake{}
	tx := &preferenceTxFake{}
	h := NewAuthHandler(nil, nil)
	h.SetUserPreferences(store, func(_ context.Context, fn func(UserPreferencesMutationTx) error) error {
		if err := fn(tx); err != nil {
			return err
		}
		row := tx.row
		store.row = &row
		return nil
	})
	body := map[string]any{
		"theme": "dark", "table_density": "compact",
		"landing_route": "/dashboard/clusters", "time_format": "24h",
		"favorites": []string{},
	}
	w := httptest.NewRecorder()
	h.PutUserPreferences(w, preferenceRequest(http.MethodPut, uuid.New(), body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"pinned_clusters":[]`)) {
		t.Fatalf("omitted pinned_clusters did not default to []: %s", w.Body.String())
	}
	if string(tx.row.PinnedClusters) != "[]" {
		t.Fatalf("stored pinned_clusters = %q, want [] (not null)", tx.row.PinnedClusters)
	}
}

func TestPutUserPreferencesRoundTripsRowsPerPageAndDateFormat(t *testing.T) {
	store := &preferenceStoreFake{}
	tx := &preferenceTxFake{}
	h := NewAuthHandler(nil, nil)
	h.SetUserPreferences(store, func(_ context.Context, fn func(UserPreferencesMutationTx) error) error {
		if err := fn(tx); err != nil {
			return err
		}
		row := tx.row
		store.row = &row
		return nil
	})
	body := map[string]any{
		"theme": "dark", "table_density": "compact",
		"landing_route": "/dashboard/clusters", "time_format": "24h",
		"favorites": []string{}, "rows_per_page": 50, "date_format": "iso",
	}
	w := httptest.NewRecorder()
	h.PutUserPreferences(w, preferenceRequest(http.MethodPut, uuid.New(), body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	for _, want := range []string{`"rows_per_page":50`, `"date_format":"iso"`} {
		if !bytes.Contains(w.Body.Bytes(), []byte(want)) {
			t.Errorf("response missing %s: %s", want, w.Body.String())
		}
	}

	getStore := &preferenceStoreFake{row: store.row}
	h2 := NewAuthHandler(nil, nil)
	h2.SetUserPreferences(getStore, nil)
	getW := httptest.NewRecorder()
	h2.GetUserPreferences(getW, preferenceRequest(http.MethodGet, uuid.New(), nil))
	if getW.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", getW.Code, getW.Body.String())
	}
	for _, want := range []string{`"rows_per_page":50`, `"date_format":"iso"`} {
		if !bytes.Contains(getW.Body.Bytes(), []byte(want)) {
			t.Errorf("GET response missing %s: %s", want, getW.Body.String())
		}
	}
}

func TestPutUserPreferencesDefaultsOmittedRowsPerPageAndDateFormat(t *testing.T) {
	store := &preferenceStoreFake{}
	tx := &preferenceTxFake{}
	h := NewAuthHandler(nil, nil)
	h.SetUserPreferences(store, func(_ context.Context, fn func(UserPreferencesMutationTx) error) error {
		if err := fn(tx); err != nil {
			return err
		}
		row := tx.row
		store.row = &row
		return nil
	})
	body := map[string]any{
		"theme": "dark", "table_density": "compact",
		"landing_route": "/dashboard/clusters", "time_format": "24h",
		"favorites": []string{},
	}
	w := httptest.NewRecorder()
	h.PutUserPreferences(w, preferenceRequest(http.MethodPut, uuid.New(), body))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"rows_per_page":25`)) {
		t.Fatalf("omitted rows_per_page did not default to 25: %s", w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"date_format":"locale"`)) {
		t.Fatalf("omitted date_format did not default to locale: %s", w.Body.String())
	}
}

func TestPutUserPreferencesRejectsUnknownAndUnregisteredValues(t *testing.T) {
	for _, body := range []string{
		`{"theme":"sepia","table_density":"compact","landing_route":"/dashboard","time_format":"24h","favorites":[]}`,
		`{"theme":"dark","table_density":"compact","landing_route":"/dashboard","time_format":"24h","favorites":[],"extra":true}`,
		`{"theme":"dark","table_density":"compact","landing_route":"/dashboard","time_format":"24h","favorites":[],"pinned_clusters":["not-a-uuid"]}`,
		`{"theme":"dark","table_density":"compact","landing_route":"/dashboard","time_format":"24h","favorites":[],"rows_per_page":15}`,
		`{"theme":"dark","table_density":"compact","landing_route":"/dashboard","time_format":"24h","favorites":[],"date_format":"epoch"}`,
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

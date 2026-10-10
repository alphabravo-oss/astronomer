package handler

// Source-only draft: not yet copied into a leased worktree or runtime qualified.
import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type preferenceIdentityRead struct{ ids []uuid.UUID }

func (q *preferenceIdentityRead) GetUserPreferences(_ context.Context, id uuid.UUID) (sqlc.UserPreference, error) {
	q.ids = append(q.ids, id)
	return sqlc.UserPreference{}, pgx.ErrNoRows
}

func TestUserPreferencesCharacterizationSelfIdentityBoundary(t *testing.T) {
	store := &preferenceIdentityRead{}
	h := NewAuthHandler(nil, nil)
	h.SetUserPreferences(store, nil)
	for _, user := range []*reqctx.User{nil, {ID: "invalid-uuid"}} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me/preferences/", nil)
		if user != nil {
			request = request.WithContext(reqctx.WithUser(request.Context(), user))
		}
		response := httptest.NewRecorder()
		h.GetUserPreferences(response, request)
		if response.Code != http.StatusUnauthorized || len(store.ids) != 0 {
			t.Fatalf("invalid identity status=%d, reads=%d", response.Code, len(store.ids))
		}
	}
	actor, foreign := uuid.New(), uuid.New()
	request := preferenceRequest(http.MethodGet, actor, nil)
	request.URL.RawQuery = "user_id=" + foreign.String()
	response := httptest.NewRecorder()
	h.GetUserPreferences(response, request)
	if response.Code != http.StatusOK || len(store.ids) != 1 || store.ids[0] != actor {
		t.Fatalf("self read status=%d, ids=%v", response.Code, store.ids)
	}
	// No mutation runner is configured: the missing row must remain a read
	// that returns complete defaults, rather than hidden initialization.
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	document := envelope.Data
	for _, key := range []string{"favorites", "pinned_clusters", "starred_types"} {
		value, ok := document[key].([]any)
		if !ok || len(value) != 0 {
			t.Fatalf("default %s=%v, want empty array", key, document[key])
		}
	}
}

func preferenceCharacterizationBody() map[string]any {
	return map[string]any{
		"theme": "dark", "table_density": "compact", "landing_route": "/dashboard",
		"time_format": "24h", "favorites": []string{},
	}
}

func TestUserPreferencesCharacterizationOptionalNormalization(t *testing.T) {
	for _, mode := range []string{"omitted", "null", "empty"} {
		t.Run(mode, func(t *testing.T) {
			body := preferenceCharacterizationBody()
			switch mode {
			case "null":
				body["pinned_clusters"], body["starred_types"] = nil, nil
			case "empty":
				body["pinned_clusters"], body["starred_types"] = []string{}, []string{}
				body["rows_per_page"], body["date_format"] = 0, ""
			}
			tx := &preferenceTxFake{}
			h := NewAuthHandler(nil, nil)
			calls := 0
			h.SetUserPreferences(&preferenceStoreFake{}, func(ctx context.Context, fn func(UserPreferencesMutationTx) error) error {
				calls++
				return fn(tx)
			})
			response := httptest.NewRecorder()
			h.PutUserPreferences(response, preferenceRequest(http.MethodPut, uuid.New(), body))
			if response.Code != http.StatusOK || calls != 1 || len(tx.audits) != 1 {
				t.Fatalf("replace status=%d, transactions=%d, audits=%d", response.Code, calls, len(tx.audits))
			}
			if string(tx.row.Favorites) != "[]" || string(tx.row.PinnedClusters) != "[]" || string(tx.row.StarredTypes) != "[]" || tx.row.RowsPerPage != 25 || tx.row.DateFormat != "locale" {
				t.Fatalf("optional normalization diverged: %+v", tx.row)
			}
		})
	}
}

func TestUserPreferencesCharacterizationAuditShapeAndRetryKey(t *testing.T) {
	actor := uuid.New()
	tx := &preferenceTxFake{}
	h := NewAuthHandler(nil, nil)
	h.SetUserPreferences(&preferenceStoreFake{}, func(ctx context.Context, fn func(UserPreferencesMutationTx) error) error { return fn(tx) })
	for _, requestID := range []string{"preference-request-first", "preference-request-retry"} {
		request := preferenceRequest(http.MethodPut, actor, preferenceCharacterizationBody())
		request = request.WithContext(reqctx.WithRequestID(request.Context(), requestID, false))
		request.Header.Set("Idempotency-Key", "preference-characterization-key")
		response := httptest.NewRecorder()
		h.PutUserPreferences(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("retry status=%d", response.Code)
		}
	}
	if len(tx.audits) != 2 || tx.audits[0].DedupeKey != tx.audits[1].DedupeKey {
		t.Fatal("same actor/path/idempotency key did not converge on one audit identity")
	}
	for _, event := range tx.audits {
		if !event.UserID.Valid || uuid.UUID(event.UserID.Bytes) != actor || event.ResourceID != actor.String() || event.Action != "user.preferences.updated" || event.HttpMethod != http.MethodPut || event.Path != "/api/v1/auth/me/preferences/" || event.ActorAuthMethod != "jwt" || event.Source != "service" || event.StatusCode != http.StatusOK {
			t.Fatalf("request-shaped mandatory audit changed: %+v", event)
		}
		var detail map[string]any
		if err := json.Unmarshal(event.Detail, &detail); err != nil {
			t.Fatal(err)
		}
		if len(detail) != 9 || detail["favorite_count"] != float64(0) || detail["pinned_cluster_count"] != float64(0) || detail["starred_type_count"] != float64(0) {
			t.Fatalf("audit detail shape changed: %v", detail)
		}
	}
}

func TestUserPreferencesCharacterizationCancelledTransactionContext(t *testing.T) {
	h := NewAuthHandler(nil, nil)
	called := false
	h.SetUserPreferences(&preferenceStoreFake{}, func(ctx context.Context, fn func(UserPreferencesMutationTx) error) error {
		called = true
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Error("transaction did not receive request cancellation")
		}
		return ctx.Err()
	})
	request := preferenceRequest(http.MethodPut, uuid.New(), preferenceCharacterizationBody())
	ctx, cancel := context.WithCancel(request.Context())
	cancel()
	response := httptest.NewRecorder()
	h.PutUserPreferences(response, request.WithContext(ctx))
	if !called || response.Code != statusClientClosedRequest || response.Body.Len() != 0 {
		t.Fatalf("cancelled replace status=%d, transaction invoked=%v", response.Code, called)
	}
}

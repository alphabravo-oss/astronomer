package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/userpreferences"
	"github.com/google/uuid"
)

type preferenceReplayFake struct {
	*preferenceTxFake
	*fakeOperationIdempotencyStore
	upserts int
}

func (q *preferenceReplayFake) ReserveOperationIdempotencyKey(ctx context.Context, arg sqlc.ReserveOperationIdempotencyKeyParams) (sqlc.OperationIdempotencyKey, error) {
	row, err := q.fakeOperationIdempotencyStore.ReserveOperationIdempotencyKey(ctx, arg)
	if !row.OperationID.Valid && row.OperationTable == "" && len(row.Response) == 0 {
		row.Response = json.RawMessage(`{}`)
	}
	return row, err
}

func (q *preferenceReplayFake) UpsertUserPreferences(ctx context.Context, arg sqlc.UpsertUserPreferencesParams) (sqlc.UserPreference, error) {
	q.upserts++
	return q.preferenceTxFake.UpsertUserPreferences(ctx, arg)
}

func TestPreferenceKeyReturnsCommittedDocumentWithoutRepeatingWrites(t *testing.T) {
	actor := uuid.New()
	q := &preferenceReplayFake{preferenceTxFake: &preferenceTxFake{}, fakeOperationIdempotencyStore: &fakeOperationIdempotencyStore{}}
	h := NewAuthHandler(nil, nil)
	h.SetUserPreferences(&preferenceStoreFake{}, func(_ context.Context, fn func(UserPreferencesMutationTx) error) error { return fn(q) })
	body := userpreferences.Defaults()
	body.Theme = userpreferences.ThemeDark
	send := func(prefs userpreferences.Preferences, key string) *httptest.ResponseRecorder {
		r := preferenceRequest(http.MethodPut, actor, prefs)
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		h.PutUserPreferences(w, r)
		return w
	}
	first := send(body, " internal spaces remain valid ")
	second := send(body, "internal spaces remain valid")
	if first.Code != http.StatusOK || second.Code != http.StatusOK || first.Body.String() != second.Body.String() || q.upserts != 1 || len(q.audits) != 1 {
		t.Fatal("same body did not return exact committed receipt without writes")
	}
	body.Theme = userpreferences.ThemeLight
	changed := send(body, "internal spaces remain valid")
	if changed.Code != http.StatusConflict || q.upserts != 1 || len(q.audits) != 1 || q.row.Theme != "dark" {
		t.Fatal("changed key body mutated preference or audit")
	}
}

func TestPreferenceServiceDigestMatchesCanonicalVersion1OperationIdentity(t *testing.T) {
	for _, mode := range []string{"defaults", "optional-nil", "populated"} {
		t.Run(mode, func(t *testing.T) {
			prefs := userpreferences.Defaults()
			if mode == "optional-nil" {
				prefs.PinnedClusters = nil
				prefs.StarredTypes = nil
				prefs.RowsPerPage = 0
				prefs.DateFormat = ""
			}
			if mode == "populated" {
				prefs.Theme = userpreferences.ThemeDark
				prefs.Favorites = []string{"/dashboard"}
				prefs.PinnedClusters = []string{uuid.NewString()}
			}
			command, err := userpreferences.Prepare(prefs)
			if err != nil {
				t.Fatal(err)
			}
			want, err := canonicalOperationRequestDigest(command.Document())
			if err != nil || command.Digest() != want {
				t.Fatal("domain digest changed canonical version 1 identity")
			}
		})
	}
}

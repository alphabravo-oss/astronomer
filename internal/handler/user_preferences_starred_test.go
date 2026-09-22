package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/userpreferences"
	"github.com/google/uuid"
)

func TestStarredTypesRoundTripAndAudit(t *testing.T) {
	for _, starred := range [][]string{nil, {"apps/deployments", "cert-manager.io/certificates"}} {
		tx := &preferenceTxFake{}
		h := NewAuthHandler(nil, nil)
		h.SetUserPreferences(&preferenceStoreFake{}, func(_ context.Context, fn func(UserPreferencesMutationTx) error) error { return fn(tx) })
		prefs := userpreferences.Defaults()
		prefs.StarredTypes = starred
		w := httptest.NewRecorder()
		h.PutUserPreferences(w, preferenceRequest(http.MethodPut, uuid.New(), prefs))
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		var response struct {
			Data userpreferences.Preferences `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Data.StarredTypes == nil || len(response.Data.StarredTypes) != len(starred) {
			t.Fatalf("response=%s", w.Body.String())
		}
		stored, err := preferencesFromRow(tx.row)
		if err != nil || len(stored.StarredTypes) != len(starred) {
			t.Fatalf("stored=%+v error=%v", stored, err)
		}
		if len(tx.audits) != 1 {
			t.Fatalf("audits=%d", len(tx.audits))
		}
	}
}

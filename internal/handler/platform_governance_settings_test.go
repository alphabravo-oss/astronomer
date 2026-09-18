package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/google/uuid"
)

func TestGovernanceSettingsValidateAndCommitWithAudit(t *testing.T) {
	for _, tc := range []struct {
		key, value string
		valid      bool
	}{
		{"users.inactive_retention_days", "1", true},
		{"users.inactive_retention_days", "3650", true},
		{"users.inactive_retention_days", "0", false},
		{"users.inactive_retention_days", "3651", false},
		{"audit.read_tier", `"incident"`, true},
		{"audit.read_tier", `"diagnostic"`, true},
		{"audit.read_tier", `"off"`, false},
	} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			caller := uuid.New()
			q := newFakeSettingsQuerier(sqlc.User{ID: caller, IsSuperuser: true})
			h := wirePlatformSettingsMutationFixture(NewPlatformSettingsHandler(q), q)
			h.SetRunTx(fakeSettingsRunTx(q))
			request := func() *http.Request {
				return withURLParam(authedRequest(http.MethodPut, "/api/v1/admin/settings/"+tc.key+"/", caller, []byte(`{"value":`+tc.value+`}`)), "key", tc.key)
			}
			w := httptest.NewRecorder()
			h.Update(w, request())
			if (w.Code == http.StatusOK) != tc.valid {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if !tc.valid {
				if len(q.rows) != 0 {
					t.Fatal("invalid setting persisted")
				}
				return
			}
			delete(q.rows, tc.key)
			q.outboxErr = errors.New("audit unavailable")
			w = httptest.NewRecorder()
			h.Update(w, request())
			if w.Code != http.StatusServiceUnavailable || len(q.rows) != 0 {
				t.Fatalf("audit failure status=%d rows=%v", w.Code, q.rows)
			}
		})
	}
}

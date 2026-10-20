//go:build integration

package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/internal/userpreferences"
)

//nolint:tparallel // subtests run sequentially against one shared per-test database fixture
func TestPreferencePostgresChangedBodySameRequestIDCannotRetainStaleAudit(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"absent-header", "invalid-header", "repeated-header"} {
		t.Run(mode, func(t *testing.T) {
			ctx, tx := paginationPostgresTx(t)
			actor := preferencePostgresUser(t, ctx, tx)
			state := &preferencePostgresBoundary{}
			h := preferencePostgresHandler(t, ctx, tx, state)
			invalidKey := strings.Repeat("x", 129)
			if validOperationIdempotencyKey(invalidKey) {
				t.Fatal("invalid-header fixture must fail canonical key syntax before requests")
			}
			send := func(theme userpreferences.Theme) *httptest.ResponseRecorder {
				t.Helper()
				body := userpreferences.Defaults()
				body.Theme = theme
				request := preferenceRequest(http.MethodPut, actor, body)
				request = request.WithContext(reqctx.WithRequestID(request.Context(), "same-request-fixture", false))
				switch mode {
				case "invalid-header":
					request.Header.Set("Idempotency-Key", invalidKey)
				case "repeated-header":
					request.Header.Add("Idempotency-Key", "first-key")
					request.Header.Add("Idempotency-Key", "second-key")
				}
				response := httptest.NewRecorder()
				h.PutUserPreferences(response, request)
				return response
			}
			for attempt := 0; attempt < 2; attempt++ {
				if send(userpreferences.Theme("dark")).Code != http.StatusOK {
					t.Fatal("ordinary same-body full replacement failed")
				}
			}
			if preferencePostgresAuditCount(t, ctx, tx, actor) != 1 {
				t.Fatal("same-body request replay created duplicate audit identities")
			}
			response := send(userpreferences.Theme("light"))
			row, err := sqlc.New(tx).GetUserPreferences(ctx, actor)
			if response.Code != http.StatusOK || err != nil || row.Theme != "light" {
				t.Fatal("ordinary changed-body full replacement changed")
			}
			var truthful int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit_outbox WHERE user_id = $1 AND action = 'user.preferences.updated' AND detail->>'theme' = 'light'`, actor).Scan(&truthful); err != nil {
				t.Fatal("cannot inspect actual changed-body audit identity")
			}
			if preferencePostgresAuditCount(t, ctx, tx, actor) == 1 {
				var retained string
				if err := tx.QueryRow(ctx, `SELECT detail->>'theme' FROM audit_outbox WHERE user_id = $1 AND action = 'user.preferences.updated'`, actor).Scan(&retained); err != nil || retained != "dark" {
					t.Fatal("request-ID baseline did not retain the actual first audit detail")
				}
			}
			if preferencePostgresAuditCount(t, ctx, tx, actor) != 2 || truthful != 1 {
				t.Fatal("same request ID without a valid idempotency key silently replaced preferences while retaining only the old audit detail")
			}
		})
	}
}

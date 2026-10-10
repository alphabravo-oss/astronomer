package handler

import (
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/internal/userpreferences"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// preferenceAuditIntent keeps the existing request-shaped event and nine
// bounded detail fields. Only this domain's dedupe identity includes the
// canonical body: ordinary replacements remain allowed without a valid key,
// while a reused request ID cannot conceal a changed preference document.
func preferenceAuditIntent(r *http.Request, actor uuid.UUID, prefs userpreferences.Preferences, digest string) audit.Intent {
	requestID := reqctx.RequestID(r.Context())
	if requestID == "" {
		requestID = uuid.NewString()
	}
	seed := audit.MutationDedupeKey(requestID, actor.String(), r.Method, r.URL.EscapedPath())
	if values := r.Header.Values("Idempotency-Key"); len(values) == 1 && validOperationIdempotencyKey(values[0]) {
		seed = audit.MutationDedupeKey(strings.TrimSpace(values[0]), actor.String(), r.Method, r.URL.EscapedPath())
	}
	seed = audit.MutationDedupeKey(seed, digest, "user_preferences", actor.String())
	return audit.Intent{
		Event: audit.NewHTTPRequestEvent(audit.HTTPRequestEvent{
			Request: r, Source: "service", CorrelationID: reqctx.CorrelationID(r.Context()),
			UserID: pgtype.UUID{Bytes: actor, Valid: true}, ActorAuthMethod: authMethodFromRequest(r),
			Action: "user.preferences.updated", ResourceType: "user_preferences", ResourceID: actor.String(), ResourceName: "Console preferences",
			StatusCode: http.StatusOK, RequestID: requestID, IPAddress: reqctx.ClientIP(r),
			Detail: map[string]any{
				"theme": prefs.Theme, "table_density": prefs.TableDensity, "landing_route": prefs.LandingRoute, "time_format": prefs.TimeFormat,
				"favorite_count": len(prefs.Favorites), "pinned_cluster_count": len(prefs.PinnedClusters), "starred_type_count": len(prefs.StarredTypes), "rows_per_page": prefs.RowsPerPage, "date_format": prefs.DateFormat,
			},
		}),
		DedupeKey: audit.MutationDedupeKey(seed, "user.preferences.updated", "user_preferences", actor.String()),
	}
}

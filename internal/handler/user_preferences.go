package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/userpreferences"
)

const maxUserPreferencesBodyBytes = 16 * 1024

type UserPreferencesQuerier interface {
	GetUserPreferences(context.Context, uuid.UUID) (sqlc.UserPreference, error)
}

type UserPreferencesMutationTx interface {
	audit.OutboxQuerier
	UpsertUserPreferences(context.Context, sqlc.UpsertUserPreferencesParams) (sqlc.UserPreference, error)
}

type userPreferencesRunTxFunc func(context.Context, func(UserPreferencesMutationTx) error) error

func preferenceUserID(r *http.Request) (uuid.UUID, bool) {
	user, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(user.ID)
	return id, err == nil
}

func preferencesFromRow(row sqlc.UserPreference) (userpreferences.Preferences, error) {
	prefs := userpreferences.Preferences{
		Theme:          userpreferences.Theme(row.Theme),
		TableDensity:   userpreferences.TableDensity(row.TableDensity),
		LandingRoute:   row.LandingRoute,
		TimeFormat:     userpreferences.TimeFormat(row.TimeFormat),
		Favorites:      []string{},
		PinnedClusters: []string{},
	}
	if err := json.Unmarshal(row.Favorites, &prefs.Favorites); err != nil {
		return userpreferences.Preferences{}, err
	}
	if err := json.Unmarshal(row.PinnedClusters, &prefs.PinnedClusters); err != nil {
		return userpreferences.Preferences{}, err
	}
	if err := prefs.Validate(); err != nil {
		return userpreferences.Preferences{}, err
	}
	return prefs, nil
}

// GetUserPreferences returns a complete preference document. Users without a
// stored row receive canonical defaults; reading preferences never creates
// hidden database state.
func (h *AuthHandler) GetUserPreferences(w http.ResponseWriter, r *http.Request) {
	userID, ok := preferenceUserID(r)
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	if h.preferences == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "User preferences are not configured")
		return
	}
	row, err := h.preferences.GetUserPreferences(r.Context(), userID)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondJSON(w, http.StatusOK, userpreferences.Defaults())
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to load user preferences")
		return
	}
	prefs, err := preferencesFromRow(row)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Stored user preferences are invalid")
		return
	}
	RespondJSON(w, http.StatusOK, prefs)
}

// PutUserPreferences replaces the complete preference document. Full replace
// semantics keep defaults and newly introduced keys deterministic across
// clients; unknown JSON keys are rejected rather than silently discarded.
func (h *AuthHandler) PutUserPreferences(w http.ResponseWriter, r *http.Request) {
	userID, ok := preferenceUserID(r)
	if !ok {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication required")
		return
	}
	if h.preferencesRunTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "User preferences are not configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUserPreferencesBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var prefs userpreferences.Preferences
	if err := decoder.Decode(&prefs); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid preference document")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Request body must contain one JSON object")
		return
	}
	if prefs.PinnedClusters == nil {
		// pinned_clusters is optional in the request (older clients may omit
		// it); normalize to an empty array so the stored value never becomes
		// the JSON scalar `null`, which would fail the column's
		// jsonb_typeof(...) = 'array' check.
		prefs.PinnedClusters = []string{}
	}
	if err := prefs.Validate(); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	favorites, err := json.Marshal(prefs.Favorites)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to encode user preferences")
		return
	}
	pinnedClusters, err := json.Marshal(prefs.PinnedClusters)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to encode user preferences")
		return
	}
	params := sqlc.UpsertUserPreferencesParams{
		UserID: userID, Theme: string(prefs.Theme),
		TableDensity: string(prefs.TableDensity), LandingRoute: prefs.LandingRoute,
		TimeFormat: string(prefs.TimeFormat), Favorites: favorites,
		PinnedClusters: pinnedClusters,
	}
	var stored sqlc.UserPreference
	err = h.preferencesRunTx(r.Context(), func(q UserPreferencesMutationTx) error {
		var mutationErr error
		stored, mutationErr = q.UpsertUserPreferences(r.Context(), params)
		if mutationErr != nil {
			return mutationErr
		}
		return recordAuditOutbox(r, q, "user.preferences.updated", "user_preferences", userID.String(), "Console preferences", http.StatusOK, map[string]any{
			"theme": prefs.Theme, "table_density": prefs.TableDensity,
			"landing_route": prefs.LandingRoute, "time_format": prefs.TimeFormat,
			"favorite_count": len(prefs.Favorites), "pinned_cluster_count": len(prefs.PinnedClusters),
		})
	})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.InternalError, "Failed to update user preferences")
		return
	}
	response, err := preferencesFromRow(stored)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Stored user preferences are invalid")
		return
	}
	RespondJSON(w, http.StatusOK, response)
}

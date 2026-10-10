package handler

import (
	"context"
	"encoding/json"
	"errors"
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
		StarredTypes:   []string{},
		RowsPerPage:    int(row.RowsPerPage),
		DateFormat:     row.DateFormat,
	}
	if err := json.Unmarshal(row.Favorites, &prefs.Favorites); err != nil {
		return userpreferences.Preferences{}, err
	}
	if err := json.Unmarshal(row.PinnedClusters, &prefs.PinnedClusters); err != nil {
		return userpreferences.Preferences{}, err
	}
	if len(row.StarredTypes) > 0 {
		if err := json.Unmarshal(row.StarredTypes, &prefs.StarredTypes); err != nil {
			return userpreferences.Preferences{}, err
		}
	}
	if prefs.StarredTypes == nil {
		prefs.StarredTypes = []string{}
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
	var prefs userpreferences.Preferences
	if err := decodeStrictJSONBody(r, &prefs); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Invalid preference document")
		return
	}
	prefs = prefs.NormalizeOptional()
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
	starredTypes, err := json.Marshal(prefs.StarredTypes)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to encode user preferences")
		return
	}
	params := sqlc.UpsertUserPreferencesParams{
		UserID: userID, Theme: string(prefs.Theme),
		TableDensity: string(prefs.TableDensity), LandingRoute: prefs.LandingRoute,
		TimeFormat: string(prefs.TimeFormat), Favorites: favorites,
		PinnedClusters: pinnedClusters,
		StarredTypes:   starredTypes,
		RowsPerPage:    int32(prefs.RowsPerPage),
		DateFormat:     prefs.DateFormat,
	}
	digest, err := canonicalOperationRequestDigest(prefs)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Failed to encode user preferences")
		return
	}
	intent := preferenceAuditIntent(r, userID, prefs, digest)
	ctx := r.Context()
	values := r.Header.Values("Idempotency-Key")
	keyed := len(values) == 1 && validOperationIdempotencyKey(values[0])
	if keyed {
		ctx = withOperationIdempotency(r, "user-preferences:v1")
	}
	var response userpreferences.Preferences
	err = h.preferencesRunTx(ctx, func(q UserPreferencesMutationTx) error {
		var receiptQueries resourceOperationIdempotencyQuerier
		if keyed {
			var supported bool
			receiptQueries, supported = q.(resourceOperationIdempotencyQuerier)
			if !supported {
				return errPreferenceReceiptUnavailable
			}
			prior, replay, claimErr := claimPreferenceReceipt(ctx, receiptQueries, r, userID, digest)
			if claimErr != nil {
				return claimErr
			}
			if replay {
				response = prior
				return nil
			}
		}
		stored, mutationErr := q.UpsertUserPreferences(ctx, params)
		if mutationErr != nil {
			return mutationErr
		}
		response, mutationErr = preferencesFromRow(stored)
		if mutationErr != nil {
			return mutationErr
		}
		storedDigest, encodeErr := canonicalOperationRequestDigest(response)
		if encodeErr != nil || storedDigest != digest {
			return errPreferenceReceiptInvalid
		}
		if mutationErr = audit.RecordIntent(ctx, q, intent); mutationErr != nil {
			return mutationErr
		}
		if keyed {
			return attachPreferenceReceipt(ctx, receiptQueries, r, userID, digest, response)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency key identifies a different preference document")
			return
		}
		if errors.Is(err, errPreferenceReceiptUnavailable) {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Preference replay storage is not configured")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.InternalError, "Failed to update user preferences")
		return
	}
	RespondJSON(w, http.StatusOK, response)
}

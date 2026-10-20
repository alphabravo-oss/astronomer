package handler

import (
	"errors"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/userpreferences"
)

const maxUserPreferencesBodyBytes = 16 * 1024

// Existing composition signatures remain aliases of the one domain contract.
type UserPreferencesQuerier = userpreferences.Reader
type UserPreferencesMutationTx = userpreferences.MutationTx
type userPreferencesRunTxFunc = userpreferences.RunTx

func preferenceUserID(r *http.Request) (uuid.UUID, bool) {
	user, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(user.ID)
	return id, err == nil
}

// GetUserPreferences returns a complete preference document. Users without a
// stored row receive canonical defaults; reading preferences never creates
// hidden database state.
func (h *AuthHandler) GetUserPreferences(w http.ResponseWriter, r *http.Request) {
	userID, ok := preferenceUserID(r)
	if !ok {
		apiutil.WriteError(w, r, http.StatusUnauthorized, apiutil.AuthenticationRequired, "Authentication required")
		return
	}
	if !h.preferenceService.CanRead() {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.NotConfigured, "User preferences are not configured")
		return
	}
	prefs, err := h.preferenceService.Get(r.Context(), userID)
	if err != nil {
		message := "Failed to load user preferences"
		if errors.Is(err, userpreferences.ErrInvalidStored) {
			message = "Stored user preferences are invalid"
		}
		apiutil.WriteError(w, r, http.StatusInternalServerError, apiutil.InternalError, message)
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
		apiutil.WriteError(w, r, http.StatusUnauthorized, apiutil.AuthenticationRequired, "Authentication required")
		return
	}
	if !h.preferenceService.CanReplace() {
		apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.NotConfigured, "User preferences are not configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUserPreferencesBodyBytes)
	var prefs userpreferences.Preferences
	if err := decodeStrictJSONBody(r, &prefs); err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, "Invalid preference document")
		return
	}
	command, err := userpreferences.Prepare(prefs)
	if err != nil {
		apiutil.WriteError(w, r, http.StatusBadRequest, apiutil.ValidationError, err.Error())
		return
	}
	prefs = command.Document()
	digest := command.Digest()
	intent := preferenceAuditIntent(r, userID, prefs, digest)
	ctx := r.Context()
	values := r.Header.Values("Idempotency-Key")
	keyed := len(values) == 1 && validOperationIdempotencyKey(values[0])
	if keyed {
		ctx = withOperationIdempotency(r, "user-preferences:v1")
	}
	var replay userpreferences.Replay
	if keyed {
		item, ok := operationIdempotencyFromContext(ctx)
		if !ok {
			apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.NotConfigured, "Preference replay storage is not configured")
			return
		}
		replay = userpreferences.Replay{Scope: item.scope, Key: item.key, Method: r.Method, Path: r.URL.EscapedPath()}
	}
	response, err := h.preferenceService.Replace(ctx, userID, command, replay, intent)
	if err != nil {
		if errors.Is(err, userpreferences.ErrConflict) {
			apiutil.WriteError(w, r, http.StatusConflict, apiutil.Conflict, "Idempotency key identifies a different preference document")
			return
		}
		if errors.Is(err, userpreferences.ErrReceiptUnavailable) {
			apiutil.WriteError(w, r, http.StatusServiceUnavailable, apiutil.NotConfigured, "Preference replay storage is not configured")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apiutil.InternalError, "Failed to update user preferences")
		return
	}
	RespondJSON(w, http.StatusOK, response)
}

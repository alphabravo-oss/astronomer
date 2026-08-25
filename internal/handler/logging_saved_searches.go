package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
)

const loggingSavedSearchMaxQueryBytes = 16 << 10

type loggingSavedSearchQuerier interface {
	ListLoggingSavedSearches(context.Context, sqlc.ListLoggingSavedSearchesParams) ([]sqlc.LoggingSavedSearch, error)
	GetLoggingSavedSearchForOwner(context.Context, sqlc.GetLoggingSavedSearchForOwnerParams) (sqlc.LoggingSavedSearch, error)
	CreateLoggingSavedSearch(context.Context, sqlc.CreateLoggingSavedSearchParams) (sqlc.LoggingSavedSearch, error)
	UpdateLoggingSavedSearch(context.Context, sqlc.UpdateLoggingSavedSearchParams) (sqlc.LoggingSavedSearch, error)
	DeleteLoggingSavedSearch(context.Context, sqlc.DeleteLoggingSavedSearchParams) (int64, error)
}

type loggingSavedSearchInput struct {
	Name       string   `json:"name"`
	Query      string   `json:"query"`
	Namespaces []string `json:"namespaces,omitempty"`
	Limit      int32    `json:"limit,omitempty"`
	Direction  string   `json:"direction,omitempty"`
	LiveTail   bool     `json:"live_tail,omitempty"`
}

// openapi:request CreateLoggingSavedSearchRequest
type createLoggingSavedSearchRequest struct {
	OutputID   string   `json:"output_id"`
	Name       string   `json:"name"`
	Query      string   `json:"query"`
	Namespaces []string `json:"namespaces,omitempty"`
	Limit      int32    `json:"limit,omitempty"`
	Direction  string   `json:"direction,omitempty"`
	LiveTail   bool     `json:"live_tail,omitempty"`
}

// openapi:request UpdateLoggingSavedSearchRequest
type updateLoggingSavedSearchRequest struct {
	Name       string   `json:"name"`
	Query      string   `json:"query"`
	Namespaces []string `json:"namespaces,omitempty"`
	Limit      int32    `json:"limit,omitempty"`
	Direction  string   `json:"direction,omitempty"`
	LiveTail   bool     `json:"live_tail,omitempty"`
}

func (req createLoggingSavedSearchRequest) input() loggingSavedSearchInput {
	return loggingSavedSearchInput{
		Name: req.Name, Query: req.Query, Namespaces: req.Namespaces, Limit: req.Limit,
		Direction: req.Direction, LiveTail: req.LiveTail,
	}
}

func (req updateLoggingSavedSearchRequest) input() loggingSavedSearchInput {
	return loggingSavedSearchInput(req)
}

type loggingSavedSearchResponse struct {
	ID         uuid.UUID `json:"id"`
	OutputID   uuid.UUID `json:"output_id"`
	Name       string    `json:"name"`
	Query      string    `json:"query"`
	Namespaces []string  `json:"namespaces"`
	Limit      int32     `json:"limit"`
	Direction  string    `json:"direction"`
	LiveTail   bool      `json:"live_tail"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func loggingSavedSearchDTO(row sqlc.LoggingSavedSearch) loggingSavedSearchResponse {
	return loggingSavedSearchResponse{
		ID: row.ID, OutputID: row.OutputID, Name: row.Name, Query: row.QueryText,
		Namespaces: row.Namespaces, Limit: row.ResultLimit, Direction: row.Direction,
		LiveTail: row.LiveTail, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func loggingSavedSearchDTOs(rows []sqlc.LoggingSavedSearch) []loggingSavedSearchResponse {
	items := make([]loggingSavedSearchResponse, len(rows))
	for i, row := range rows {
		items[i] = loggingSavedSearchDTO(row)
	}
	return items
}

func normalizeLoggingSavedSearch(req loggingSavedSearchInput) (loggingSavedSearchInput, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 120 {
		return req, errors.New("name must contain between 1 and 120 characters")
	}
	if len([]byte(req.Query)) > loggingSavedSearchMaxQueryBytes {
		return req, fmt.Errorf("query exceeds the %d-byte limit", loggingSavedSearchMaxQueryBytes)
	}
	if req.Limit == 0 {
		req.Limit = 100
	}
	if req.Limit < 1 || req.Limit > 1000 {
		return req, errors.New("limit must be between 1 and 1000")
	}
	req.Direction = strings.ToLower(strings.TrimSpace(req.Direction))
	if req.Direction == "" {
		req.Direction = "backward"
	}
	if req.Direction != "forward" && req.Direction != "backward" {
		return req, errors.New("direction must be forward or backward")
	}
	if len(req.Namespaces) > 20 {
		return req, errors.New("at most 20 namespaces may be saved")
	}
	seen := make(map[string]struct{}, len(req.Namespaces))
	namespaces := make([]string, 0, len(req.Namespaces))
	for _, namespace := range req.Namespaces {
		namespace = strings.TrimSpace(namespace)
		if !isSafeK8sName(namespace) {
			return req, fmt.Errorf("invalid namespace %q", namespace)
		}
		if _, ok := seen[namespace]; ok {
			continue
		}
		seen[namespace] = struct{}{}
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	req.Namespaces = namespaces
	return req, nil
}

func decodeLoggingSavedSearchJSON(r *http.Request, destination any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain exactly one JSON object")
		}
		return err
	}
	return nil
}

func (h *LoggingHandler) savedSearchQueries(w http.ResponseWriter, r *http.Request) (loggingSavedSearchQuerier, bool) {
	queries, ok := h.queries.(loggingSavedSearchQuerier)
	if !ok || queries == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Logging saved searches are not configured")
		return nil, false
	}
	return queries, true
}

func loggingSavedSearchOwner(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	current := currentUserUUID(r)
	if !current.Valid || current.Bytes == uuid.Nil {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.AuthenticationRequired, "Authentication is required")
		return uuid.Nil, false
	}
	return current.Bytes, true
}

func (h *LoggingHandler) authorizeSavedSearchOutput(w http.ResponseWriter, r *http.Request, outputID uuid.UUID) (sqlc.LoggingOutput, bool) {
	output, err := h.queries.GetLoggingOutputByID(r.Context(), outputID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging output not found")
		return sqlc.LoggingOutput{}, false
	}
	if !output.ClusterID.Valid {
		RespondRequestError(w, r, http.StatusUnprocessableEntity, apierror.InvalidRequest, "Saved searches require a cluster-owned logging output")
		return sqlc.LoggingOutput{}, false
	}
	if !h.authz.authorizeClusterAction(w, r, output.ClusterID.Bytes, rbac.ResourceLogging, rbac.VerbRead) {
		return sqlc.LoggingOutput{}, false
	}
	return output, true
}

// ListSavedSearches returns only the authenticated caller's searches for one
// already-authorized output. Requiring output_id keeps list authorization
// ahead of the database read and prevents a cross-cluster personal index.
func (h *LoggingHandler) ListSavedSearches(w http.ResponseWriter, r *http.Request) {
	queries, ok := h.savedSearchQueries(w, r)
	if !ok {
		return
	}
	ownerID, ok := loggingSavedSearchOwner(w, r)
	if !ok {
		return
	}
	outputID, err := uuid.Parse(strings.TrimSpace(r.URL.Query().Get("output_id")))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "A valid output_id is required")
		return
	}
	if _, ok := h.authorizeSavedSearchOutput(w, r, outputID); !ok {
		return
	}
	rows, err := queries.ListLoggingSavedSearches(r.Context(), sqlc.ListLoggingSavedSearchesParams{OwnerUserID: ownerID, OutputID: outputID})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list logging saved searches")
		return
	}
	RespondJSON(w, http.StatusOK, loggingSavedSearchDTOs(rows))
}

func (h *LoggingHandler) CreateSavedSearch(w http.ResponseWriter, r *http.Request) {
	queries, ok := h.savedSearchQueries(w, r)
	if !ok {
		return
	}
	ownerID, ok := loggingSavedSearchOwner(w, r)
	if !ok {
		return
	}
	var wire createLoggingSavedSearchRequest
	if err := decodeLoggingSavedSearchJSON(r, &wire); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	req, err := normalizeLoggingSavedSearch(wire.input())
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	outputID, err := uuid.Parse(strings.TrimSpace(wire.OutputID))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "A valid output_id is required")
		return
	}
	output, ok := h.authorizeSavedSearchOutput(w, r, outputID)
	if !ok {
		return
	}
	if req.LiveTail && !loggingCapabilitiesFor(output).Tail {
		RespondRequestError(w, r, http.StatusUnprocessableEntity, apierror.InvalidRequest, "This logging output does not support live tail")
		return
	}
	params := sqlc.CreateLoggingSavedSearchParams{
		OutputID: outputID, OwnerUserID: ownerID, Name: req.Name, QueryText: req.Query,
		Namespaces: req.Namespaces, ResultLimit: req.Limit, Direction: req.Direction, LiveTail: req.LiveTail,
	}
	row, err := executeLoggingMutation(r, h,
		func(q LoggingMutationTx) (sqlc.LoggingSavedSearch, error) {
			return q.CreateLoggingSavedSearch(r.Context(), params)
		},
		func() (sqlc.LoggingSavedSearch, error) { return queries.CreateLoggingSavedSearch(r.Context(), params) },
		loggingSavedSearchAuditEvent("logging.saved_search.create", http.StatusCreated),
	)
	if err != nil {
		if isUniqueViolation(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A saved search with this name already exists for the output")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create logging saved search")
		return
	}
	RespondJSON(w, http.StatusCreated, loggingSavedSearchDTO(row))
}

func (h *LoggingHandler) UpdateSavedSearch(w http.ResponseWriter, r *http.Request) {
	queries, ok := h.savedSearchQueries(w, r)
	if !ok {
		return
	}
	ownerID, ok := loggingSavedSearchOwner(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid saved search ID")
		return
	}
	existing, err := queries.GetLoggingSavedSearchForOwner(r.Context(), sqlc.GetLoggingSavedSearchForOwnerParams{ID: id, OwnerUserID: ownerID})
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging saved search not found")
		return
	}
	output, ok := h.authorizeSavedSearchOutput(w, r, existing.OutputID)
	if !ok {
		return
	}
	var wire updateLoggingSavedSearchRequest
	if err := decodeLoggingSavedSearchJSON(r, &wire); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	req, err := normalizeLoggingSavedSearch(wire.input())
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, err.Error())
		return
	}
	if req.LiveTail && !loggingCapabilitiesFor(output).Tail {
		RespondRequestError(w, r, http.StatusUnprocessableEntity, apierror.InvalidRequest, "This logging output does not support live tail")
		return
	}
	params := sqlc.UpdateLoggingSavedSearchParams{
		ID: id, OwnerUserID: ownerID, Name: req.Name, QueryText: req.Query,
		Namespaces: req.Namespaces, ResultLimit: req.Limit, Direction: req.Direction, LiveTail: req.LiveTail,
	}
	row, err := executeLoggingMutation(r, h,
		func(q LoggingMutationTx) (sqlc.LoggingSavedSearch, error) {
			return q.UpdateLoggingSavedSearch(r.Context(), params)
		},
		func() (sqlc.LoggingSavedSearch, error) { return queries.UpdateLoggingSavedSearch(r.Context(), params) },
		loggingSavedSearchAuditEvent("logging.saved_search.update", http.StatusOK),
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging saved search not found")
			return
		}
		if isUniqueViolation(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A saved search with this name already exists for the output")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update logging saved search")
		return
	}
	RespondJSON(w, http.StatusOK, loggingSavedSearchDTO(row))
}

func (h *LoggingHandler) DeleteSavedSearch(w http.ResponseWriter, r *http.Request) {
	queries, ok := h.savedSearchQueries(w, r)
	if !ok {
		return
	}
	ownerID, ok := loggingSavedSearchOwner(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid saved search ID")
		return
	}
	existing, err := queries.GetLoggingSavedSearchForOwner(r.Context(), sqlc.GetLoggingSavedSearchForOwnerParams{ID: id, OwnerUserID: ownerID})
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging saved search not found")
		return
	}
	if _, ok := h.authorizeSavedSearchOutput(w, r, existing.OutputID); !ok {
		return
	}
	params := sqlc.DeleteLoggingSavedSearchParams{ID: id, OwnerUserID: ownerID}
	_, err = executeLoggingMutation(r, h,
		func(q LoggingMutationTx) (sqlc.LoggingSavedSearch, error) {
			row, getErr := q.GetLoggingSavedSearchForOwner(r.Context(), sqlc.GetLoggingSavedSearchForOwnerParams{ID: id, OwnerUserID: ownerID})
			if getErr != nil {
				return sqlc.LoggingSavedSearch{}, getErr
			}
			deleted, deleteErr := q.DeleteLoggingSavedSearch(r.Context(), params)
			if deleteErr == nil && deleted != 1 {
				deleteErr = pgx.ErrNoRows
			}
			return row, deleteErr
		},
		func() (sqlc.LoggingSavedSearch, error) {
			deleted, deleteErr := queries.DeleteLoggingSavedSearch(r.Context(), params)
			if deleteErr == nil && deleted != 1 {
				deleteErr = pgx.ErrNoRows
			}
			return existing, deleteErr
		},
		loggingSavedSearchAuditEvent("logging.saved_search.delete", http.StatusNoContent),
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Logging saved search not found")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete logging saved search")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func loggingSavedSearchAuditEvent(action string, status int) func(sqlc.LoggingSavedSearch) clusterAuditEvent {
	return func(row sqlc.LoggingSavedSearch) clusterAuditEvent {
		digest := sha256.Sum256([]byte(row.QueryText))
		return clusterAuditEvent{
			action: action, resourceType: "logging_saved_search", resourceID: row.ID.String(), resourceName: row.Name, status: status,
			detail: map[string]any{
				"output_id": row.OutputID.String(), "query_sha256": fmt.Sprintf("%x", digest[:]),
				"namespace_count": len(row.Namespaces), "limit": row.ResultLimit,
				"direction": row.Direction, "live_tail": row.LiveTail,
			},
		}
	}
}

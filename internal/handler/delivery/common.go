package delivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

const (
	defaultPageLimit = 20
	maxPageLimit     = 200
	maxRequestBytes  = 1 << 20
)

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorEnvelope struct {
	Error apiError `json:"error"`
}

type dataEnvelope struct {
	Data any `json:"data"`
}

type pageEnvelope struct {
	Data       any     `json:"data"`
	Count      int64   `json:"count"`
	Next       *string `json:"next"`
	Previous   *string `json:"previous"`
	TotalKnown bool    `json:"total_known"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func respondData(w http.ResponseWriter, status int, value any) {
	writeJSON(w, status, dataEnvelope{Data: value})
}

func respondError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorEnvelope{Error: apiError{Code: code, Message: message}})
}

func respondDatabaseError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		respondError(w, http.StatusNotFound, "not_found", "delivery resource not found")
		return
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505":
			respondError(w, http.StatusConflict, "conflict", "a delivery resource with that identity already exists")
			return
		case "23503":
			respondError(w, http.StatusConflict, "resource_in_use", "delivery resource is still referenced")
			return
		case "23514", "22001", "22P02":
			respondError(w, http.StatusBadRequest, "validation_error", "delivery resource failed persistence validation")
			return
		}
	}
	respondError(w, http.StatusInternalServerError, "internal_error", "delivery operation failed")
}

// recordAudit emits a best-effort, metadata-only delivery audit event. The
// concrete production query set implements audit.Querier; narrow handler test
// fakes are intentionally allowed to omit it. Delivery callers must never put
// credentials, source URLs, rendered values/manifests, Secret names, or raw
// controller messages in detail.
func recordAudit(r *http.Request, queries any, action, resourceType, resourceID, resourceName string, detail map[string]any) {
	if r == nil || queries == nil {
		return
	}
	writer, ok := queries.(audit.Querier)
	if !ok || writer == nil {
		return
	}
	user, _ := middleware.GetAuthenticatedUser(r.Context())
	authMethod := ""
	if user != nil {
		authMethod = user.AuthMethod
	}
	audit.Record(r.Context(), writer, audit.NewHTTPRequestEvent(audit.HTTPRequestEvent{
		Request:         r,
		Source:          "service",
		CorrelationID:   middleware.GetCorrelationID(r.Context()),
		UserID:          middleware.AuthenticatedUserUUID(r.Context()),
		ActorAuthMethod: authMethod,
		Action:          action,
		ResourceType:    resourceType,
		ResourceID:      resourceID,
		ResourceName:    resourceName,
		RequestID:       middleware.GetRequestID(r.Context()),
		IPAddress:       middleware.RemoteIPAddr(r),
		Detail:          detail,
	}))
}

func decodeRequest(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return fmt.Errorf("request body exceeds %d bytes", maxRequestBytes)
		}
		return errors.New("request body must be one valid JSON object with only supported fields")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain exactly one JSON object")
	}
	return nil
}

func projectIDFromRequest(r *http.Request, bodyProjectID uuid.UUID) (uuid.UUID, error) {
	type candidate struct {
		name  string
		value string
	}
	candidates := make([]candidate, 0, 3)
	if values, ok := r.URL.Query()["project_id"]; ok {
		if len(values) != 1 {
			return uuid.Nil, errors.New("project_id query parameter must occur exactly once")
		}
		candidates = append(candidates, candidate{name: "project_id query parameter", value: values[0]})
	}
	if values := r.Header.Values("X-Project-ID"); len(values) != 0 {
		if len(values) != 1 {
			return uuid.Nil, errors.New("X-Project-ID header must occur exactly once")
		}
		candidates = append(candidates, candidate{name: "X-Project-ID header", value: values[0]})
	}
	if bodyProjectID != uuid.Nil {
		candidates = append(candidates, candidate{name: "project_id body field", value: bodyProjectID.String()})
	}
	if len(candidates) == 0 {
		return uuid.Nil, errors.New("project scope is required")
	}

	var projectID uuid.UUID
	for _, item := range candidates {
		parsed, err := uuid.Parse(item.value)
		if err != nil || parsed == uuid.Nil {
			return uuid.Nil, fmt.Errorf("%s must be a non-zero UUID", item.name)
		}
		if projectID != uuid.Nil && parsed != projectID {
			return uuid.Nil, errors.New("project scopes do not match")
		}
		projectID = parsed
	}
	return projectID, nil
}

func pathUUID(r *http.Request, names ...string) (uuid.UUID, error) {
	for _, name := range names {
		value := chi.URLParam(r, name)
		if value == "" {
			continue
		}
		parsed, err := uuid.Parse(value)
		if err != nil || parsed == uuid.Nil {
			return uuid.Nil, fmt.Errorf("%s must be a non-zero UUID", name)
		}
		return parsed, nil
	}
	return uuid.Nil, errors.New("resource identifier is required")
}

func parsePagination(r *http.Request) (limit, offset int32, err error) {
	limitValue, err := boundedQueryInteger(r.URL.Query(), "limit", defaultPageLimit, 1, maxPageLimit)
	if err != nil {
		return 0, 0, err
	}
	offsetValue, err := boundedQueryInteger(r.URL.Query(), "offset", 0, 0, int(^uint32(0)>>1))
	if err != nil {
		return 0, 0, err
	}
	return int32(limitValue), int32(offsetValue), nil
}

func boundedQueryInteger(values url.Values, name string, fallback, minimum, maximum int) (int, error) {
	rawValues, exists := values[name]
	if !exists {
		return fallback, nil
	}
	if len(rawValues) != 1 {
		return 0, fmt.Errorf("%s must occur exactly once", name)
	}
	parsed, err := strconv.Atoi(rawValues[0])
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be an integer from %d through %d", name, minimum, maximum)
	}
	return parsed, nil
}

func respondPage(w http.ResponseWriter, r *http.Request, items any, count int64, limit, offset int32, hasMore bool, totalKnown bool) {
	response := pageEnvelope{Data: items, Count: count, TotalKnown: totalKnown}
	if hasMore {
		next := pageLink(r, limit, offset+limit)
		response.Next = &next
	}
	if offset > 0 {
		previousOffset := offset - limit
		if previousOffset < 0 {
			previousOffset = 0
		}
		previous := pageLink(r, limit, previousOffset)
		response.Previous = &previous
	}
	writeJSON(w, http.StatusOK, response)
}

func pageLink(r *http.Request, limit, offset int32) string {
	query := r.URL.Query()
	query.Set("limit", strconv.FormatInt(int64(limit), 10))
	query.Set("offset", strconv.FormatInt(int64(offset), 10))
	return r.URL.Path + "?" + query.Encode()
}

func validateIdempotencyKey(r *http.Request) error {
	values := r.Header.Values("Idempotency-Key")
	if len(values) == 0 {
		return nil
	}
	if len(values) != 1 {
		return errors.New("Idempotency-Key must occur exactly once")
	}
	value := values[0]
	if value == "" || len(value) > 128 || !utf8.ValidString(value) || containsControl(value) {
		return errors.New("Idempotency-Key must be 1 through 128 printable UTF-8 bytes")
	}
	return nil
}

// requireIfMatch accepts one strong decimal entity tag. Delivery mutations use
// database resource/fencing generations as the tag value; weak tags and `*`
// are rejected because they cannot provide compare-and-swap semantics.
func requireIfMatch(r *http.Request) (int64, error) {
	values := r.Header.Values("If-Match")
	if len(values) != 1 {
		return 0, errors.New("If-Match must occur exactly once")
	}
	value := strings.TrimSpace(values[0])
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' || strings.HasPrefix(value, "W/") {
		return 0, errors.New("If-Match must be one strong quoted decimal entity tag")
	}
	parsed, err := strconv.ParseInt(value[1:len(value)-1], 10, 64)
	if err != nil || parsed < 0 {
		return 0, errors.New("If-Match must be one strong quoted non-negative decimal entity tag")
	}
	return parsed, nil
}

func setEntityTag(w http.ResponseWriter, version int64) {
	w.Header().Set("ETag", fmt.Sprintf("\"%d\"", version))
}

func validateDisplayFields(name, description string) error {
	if name == "" || name != strings.TrimSpace(name) || len(name) > 128 || !utf8.ValidString(name) || containsControl(name) {
		return errors.New("name must be 1 through 128 UTF-8 bytes without surrounding whitespace or control characters")
	}
	if len(description) > 4096 || !utf8.ValidString(description) || containsControl(description) {
		return errors.New("description must be at most 4096 UTF-8 bytes without control characters")
	}
	return nil
}

func containsControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

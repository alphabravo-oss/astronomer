// Package handler — admin CRUD for external SIEM forwarders + the
// per-forwarder status + test endpoints (migration 055).
//
// Route summary (all superuser-gated):
//
//	GET    /api/v1/admin/siem-forwarders/                 — list
//	POST   /api/v1/admin/siem-forwarders/                 — create
//	GET    /api/v1/admin/siem-forwarders/{id}/            — get
//	PUT    /api/v1/admin/siem-forwarders/{id}/            — update
//	DELETE /api/v1/admin/siem-forwarders/{id}/            — delete
//	POST   /api/v1/admin/siem-forwarders/{id}/test/       — synthetic event through the pipeline
//	GET    /api/v1/admin/siem-forwarders/{id}/status/     — current queue_depth + dropped + last_error

package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/siem"
)

// SIEMAuthSentinel is the placeholder returned in GET responses instead
// of the raw ciphertext. The dashboard PUT path echoes this value back
// when the admin didn't change the auth blob, and the handler treats
// it as "keep existing" so a fresh PUT doesn't accidentally blank the
// auth.
const SIEMAuthSentinel = "<encrypted>"

// validSIEMTransports mirrors the CHECK constraint on the migration.
var validSIEMTransports = map[string]bool{
	siem.TransportSyslogUDP:   true,
	siem.TransportSyslogTCP:   true,
	siem.TransportSyslogTLS:   true,
	siem.TransportSplunkHEC:   true,
	siem.TransportNDJSONHTTPS: true,
}

// validSIEMFormats includes the empty string ("auto-derive from
// transport") plus every named format.
var validSIEMFormats = map[string]bool{
	"":                   true,
	siem.FormatRFC5424ID: true,
	siem.FormatRFC3164ID: true,
	siem.FormatCEFID:     true,
	siem.FormatNDJSONID:  true,
}

// SIEMQuerier is the database surface SIEMHandler needs. *sqlc.Queries
// satisfies it directly.
type SIEMQuerier interface {
	ListSIEMForwarders(ctx context.Context) ([]sqlc.SiemForwarder, error)
	GetSIEMForwarder(ctx context.Context, id uuid.UUID) (sqlc.SiemForwarder, error)
	GetSIEMForwarderByName(ctx context.Context, name string) (sqlc.SiemForwarder, error)
	CreateSIEMForwarder(ctx context.Context, arg sqlc.CreateSIEMForwarderParams) (sqlc.SiemForwarder, error)
	UpdateSIEMForwarder(ctx context.Context, arg sqlc.UpdateSIEMForwarderParams) (sqlc.SiemForwarder, error)
	DeleteSIEMForwarder(ctx context.Context, id uuid.UUID) error
	EnqueueSIEMEvent(ctx context.Context, arg sqlc.EnqueueSIEMEventParams) (sqlc.SiemForwardQueue, error)
	GetSIEMForwarderStatus(ctx context.Context, forwarderID uuid.UUID) (sqlc.SiemForwarderStatus, error)
	CountSIEMQueueByForwarder(ctx context.Context, forwarderID uuid.UUID) (int64, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
}

type SIEMMutationTx interface {
	audit.OutboxQuerier
	CreateSIEMForwarder(context.Context, sqlc.CreateSIEMForwarderParams) (sqlc.SiemForwarder, error)
	UpdateSIEMForwarder(context.Context, sqlc.UpdateSIEMForwarderParams) (sqlc.SiemForwarder, error)
	DeleteSIEMForwarder(context.Context, uuid.UUID) error
	EnqueueSIEMEvent(context.Context, sqlc.EnqueueSIEMEventParams) (sqlc.SiemForwardQueue, error)
}

type siemTestOperationQueries interface {
	CreateSIEMTestOperationAndQueue(context.Context, sqlc.CreateSIEMTestOperationAndQueueParams) (sqlc.SIEMTestOperation, error)
	GetSIEMTestOperation(context.Context, sqlc.GetSIEMTestOperationParams) (sqlc.SIEMTestOperation, error)
}

type siemRunTxFunc func(context.Context, func(SIEMMutationTx) error) error

// SIEMTapInvalidator is the cache hook on the bus tap. Wired by the
// server so a CRUD operation reflects on the next event. Optional.
type SIEMTapInvalidator interface {
	Invalidate()
}

// SIEMHandler owns /api/v1/admin/siem-forwarders/*. Superuser-gated
// inside each handler so non-admins get a clean 403.
type SIEMHandler struct {
	queries   SIEMQuerier
	encryptor *auth.Encryptor
	log       *slog.Logger
	audit     AuthAuditWriter
	tap       SIEMTapInvalidator
	bus       *events.Bus
	runTx     siemRunTxFunc
}

// SetEventBus wires the SSE bus for siem_forwarder.changed liveness events
// (P4.5). Deliberately unscoped (no cluster_id): the SEC-R07 fail-closed
// drop makes it superuser-only, matching the endpoints (D9).
func (h *SIEMHandler) SetEventBus(bus *events.Bus) {
	if h == nil {
		return
	}
	h.bus = bus
}

// NewSIEMHandler builds a usable handler.
func NewSIEMHandler(queries SIEMQuerier, encryptor *auth.Encryptor, log *slog.Logger) *SIEMHandler {
	if log == nil {
		log = slog.Default()
	}
	return &SIEMHandler{
		queries:   queries,
		encryptor: encryptor,
		log:       log,
	}
}

// SetAuditWriter wires the audit log writer.
func (h *SIEMHandler) SetAuditWriter(a AuthAuditWriter) { h.audit = a }

func (h *SIEMHandler) SetRunTx(runTx siemRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *SIEMHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

func executeSIEMMutation[T any](r *http.Request, h *SIEMHandler, mutate func(SIEMMutationTx) (T, error), fallback func() (T, error), describe func(T) clusterAuditEvent) (T, error) {
	var zero T
	if h == nil {
		return zero, errors.New("SIEM handler is nil")
	}
	if h.runTx != nil {
		var result T
		err := h.runTx(r.Context(), func(q SIEMMutationTx) error {
			var mutationErr error
			result, mutationErr = mutate(q)
			if mutationErr != nil {
				return mutationErr
			}
			event := describe(result)
			return recordAuditOutbox(r, q, event.action, event.resourceType, event.resourceID, event.resourceName, event.status, event.detail)
		})
		return result, err
	}
	result, err := fallback()
	if err != nil {
		return zero, err
	}
	event := describe(result)
	recordAudit(r, h.audit, event.action, event.resourceType, event.resourceID, event.resourceName, event.detail)
	return result, nil
}

// SetTap wires the bus-tap cache invalidator.
func (h *SIEMHandler) SetTap(t SIEMTapInvalidator) { h.tap = t }

// siemForwarderResponse is the wire shape. auth is always the sentinel
// — we NEVER leak the ciphertext.
type siemForwarderResponse struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Transport        string   `json:"transport"`
	Endpoint         string   `json:"endpoint"`
	Auth             string   `json:"auth"`
	AuthConfigured   bool     `json:"auth_configured"`
	EventFilters     []string `json:"event_filters"`
	Format           string   `json:"format"`
	TLSSkipVerify    bool     `json:"tls_skip_verify"`
	CACertConfigured bool     `json:"ca_cert_configured"`
	BatchSize        int      `json:"batch_size"`
	FlushIntervalMs  int      `json:"flush_interval_ms"`
	TimeoutSeconds   int      `json:"timeout_seconds"`
	Enabled          bool     `json:"enabled"`
	CreatedBy        string   `json:"created_by,omitempty"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
}

// siemForwarderRequest is the POST/PUT body. Every field is a pointer
// so PUT can do partial updates while POST sets explicit defaults at
// the helper layer.
// openapi:request SIEMForwarderRequest
type siemForwarderRequest struct {
	Name            *string   `json:"name"`
	Transport       *string   `json:"transport"`
	Endpoint        *string   `json:"endpoint"`
	Auth            *string   `json:"auth"`
	EventFilters    *[]string `json:"event_filters"`
	Format          *string   `json:"format"`
	TLSSkipVerify   *bool     `json:"tls_skip_verify"`
	CACertPEM       *string   `json:"ca_cert_pem"`
	BatchSize       *int      `json:"batch_size"`
	FlushIntervalMs *int      `json:"flush_interval_ms"`
	TimeoutSeconds  *int      `json:"timeout_seconds"`
	Enabled         *bool     `json:"enabled"`
}

// List handles GET /api/v1/admin/siem-forwarders/.
func (h *SIEMHandler) List(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	rows, err := h.queries.ListSIEMForwarders(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to list SIEM forwarders")
		return
	}
	items := make([]siemForwarderResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, toSIEMForwarderResponse(row))
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}

// Create handles POST /api/v1/admin/siem-forwarders/.
func (h *SIEMHandler) Create(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	var req siemForwarderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if req.Name == nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "name is required")
		return
	}
	if req.Transport == nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "transport is required")
		return
	}
	if req.Endpoint == nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "endpoint is required")
		return
	}
	merged, vErr := h.mergeSIEMForCreate(req)
	if vErr != "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, vErr)
		return
	}
	// Uniqueness check (the unique index would catch this too).
	if existing, err := h.queries.GetSIEMForwarderByName(r.Context(), merged.Name); err == nil && existing.ID != uuid.Nil {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A SIEM forwarder with this name already exists")
		return
	}

	authEnc := ""
	if req.Auth != nil && strings.TrimSpace(*req.Auth) != "" && *req.Auth != SIEMAuthSentinel {
		if h.encryptor == nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Encryptor is not configured; cannot store SIEM auth blob")
			return
		}
		enc, err := h.encryptor.Encrypt(*req.Auth)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt SIEM auth blob")
			return
		}
		authEnc = enc
	}
	filtersJSON, _ := json.Marshal(merged.EventFilters)

	params := sqlc.CreateSIEMForwarderParams{
		Name:            merged.Name,
		Transport:       merged.Transport,
		Endpoint:        merged.Endpoint,
		AuthEncrypted:   authEnc,
		EventFilters:    filtersJSON,
		Format:          merged.Format,
		TlsSkipVerify:   merged.TLSSkipVerify,
		CaCertPem:       merged.CACertPEM,
		BatchSize:       int32(merged.BatchSize),
		FlushIntervalMs: int32(merged.FlushIntervalMs),
		TimeoutSeconds:  int32(merged.TimeoutSeconds),
		Enabled:         merged.Enabled,
		CreatedBy:       currentUserUUID(r),
	}
	saved, err := executeSIEMMutation(r, h,
		func(q SIEMMutationTx) (sqlc.SiemForwarder, error) { return q.CreateSIEMForwarder(r.Context(), params) },
		func() (sqlc.SiemForwarder, error) { return h.queries.CreateSIEMForwarder(r.Context(), params) },
		func(saved sqlc.SiemForwarder) clusterAuditEvent {
			return clusterAuditEvent{
				action: "admin.siem_forwarder.created", resourceType: "siem_forwarder", resourceID: saved.ID.String(), resourceName: saved.Name,
				status: http.StatusCreated,
				detail: map[string]any{"transport": saved.Transport, "endpoint": saved.Endpoint, "event_filters": merged.EventFilters, "enabled": saved.Enabled},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.WriteError, "Failed to create SIEM forwarder")
		return
	}
	if h.tap != nil {
		h.tap.Invalidate()
	}
	if merged.TLSSkipVerify {
		h.log.Warn("siem forwarder created with tls_skip_verify=true",
			"forwarder", saved.Name, "endpoint", saved.Endpoint)
	}
	events.PublishChanged(h.bus, "siem_forwarder", "", saved.ID.String(), nil)
	RespondJSON(w, http.StatusCreated, toSIEMForwarderResponse(saved))
}

// Get handles GET /api/v1/admin/siem-forwarders/{id}/.
func (h *SIEMHandler) Get(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	id, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid forwarder id")
		return
	}
	row, err := h.queries.GetSIEMForwarder(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "SIEM forwarder not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to read SIEM forwarder")
		return
	}
	RespondJSON(w, http.StatusOK, toSIEMForwarderResponse(row))
}

// Update handles PUT /api/v1/admin/siem-forwarders/{id}/.
func (h *SIEMHandler) Update(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	id, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid forwarder id")
		return
	}
	existing, err := h.queries.GetSIEMForwarder(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "SIEM forwarder not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to read SIEM forwarder")
		return
	}
	var req siemForwarderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	merged, vErr := h.mergeSIEMForUpdate(existing, req)
	if vErr != "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, vErr)
		return
	}
	// Auth handling: SIEMAuthSentinel means "keep existing". Any other
	// non-empty value is re-encrypted.
	encryptedAuth := existing.AuthEncrypted
	if req.Auth != nil && *req.Auth != SIEMAuthSentinel {
		if strings.TrimSpace(*req.Auth) == "" {
			// Operator explicitly cleared the auth blob — accept that.
			encryptedAuth = ""
		} else {
			if h.encryptor == nil {
				RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Encryptor unavailable")
				return
			}
			enc, encErr := h.encryptor.Encrypt(*req.Auth)
			if encErr != nil {
				RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt SIEM auth blob")
				return
			}
			encryptedAuth = enc
		}
	}
	filtersJSON, _ := json.Marshal(merged.EventFilters)

	params := sqlc.UpdateSIEMForwarderParams{
		ID:              id,
		Name:            merged.Name,
		Transport:       merged.Transport,
		Endpoint:        merged.Endpoint,
		AuthEncrypted:   encryptedAuth,
		EventFilters:    filtersJSON,
		Format:          merged.Format,
		TlsSkipVerify:   merged.TLSSkipVerify,
		CaCertPem:       merged.CACertPEM,
		BatchSize:       int32(merged.BatchSize),
		FlushIntervalMs: int32(merged.FlushIntervalMs),
		TimeoutSeconds:  int32(merged.TimeoutSeconds),
		Enabled:         merged.Enabled,
	}
	saved, err := executeSIEMMutation(r, h,
		func(q SIEMMutationTx) (sqlc.SiemForwarder, error) { return q.UpdateSIEMForwarder(r.Context(), params) },
		func() (sqlc.SiemForwarder, error) { return h.queries.UpdateSIEMForwarder(r.Context(), params) },
		func(saved sqlc.SiemForwarder) clusterAuditEvent {
			return clusterAuditEvent{
				action: "admin.siem_forwarder.updated", resourceType: "siem_forwarder", resourceID: saved.ID.String(), resourceName: saved.Name,
				status: http.StatusOK,
				detail: map[string]any{"transport": saved.Transport, "endpoint": saved.Endpoint, "event_filters": merged.EventFilters, "enabled": saved.Enabled},
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.WriteError, "Failed to update SIEM forwarder")
		return
	}
	if h.tap != nil {
		h.tap.Invalidate()
	}
	if merged.TLSSkipVerify && !existing.TlsSkipVerify {
		h.log.Warn("siem forwarder enabled tls_skip_verify=true",
			"forwarder", saved.Name, "endpoint", saved.Endpoint)
	}
	events.PublishChanged(h.bus, "siem_forwarder", "", saved.ID.String(), nil)
	RespondJSON(w, http.StatusOK, toSIEMForwarderResponse(saved))
}

// Delete handles DELETE /api/v1/admin/siem-forwarders/{id}/. CASCADE on
// siem_forward_queue + siem_forwarder_status wipes the queue + status
// row automatically.
func (h *SIEMHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	id, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid forwarder id")
		return
	}
	existing, err := h.queries.GetSIEMForwarder(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "SIEM forwarder not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to read SIEM forwarder")
		return
	}
	_, err = executeSIEMMutation(r, h,
		func(q SIEMMutationTx) (sqlc.SiemForwarder, error) {
			return existing, q.DeleteSIEMForwarder(r.Context(), id)
		},
		func() (sqlc.SiemForwarder, error) { return existing, h.queries.DeleteSIEMForwarder(r.Context(), id) },
		func(existing sqlc.SiemForwarder) clusterAuditEvent {
			return clusterAuditEvent{
				action: "admin.siem_forwarder.deleted", resourceType: "siem_forwarder", resourceID: id.String(), resourceName: existing.Name,
				status: http.StatusNoContent,
			}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.WriteError, "Failed to delete SIEM forwarder")
		return
	}
	if h.tap != nil {
		h.tap.Invalidate()
	}
	events.PublishChanged(h.bus, "siem_forwarder", "", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// Test handles POST /api/v1/admin/siem-forwarders/{id}/test/. Enqueues
// a synthetic event onto the forwarder's queue so it travels the same
// pipeline a real event would; the dispatcher picks it up on the next
// tick (within 2s).
func (h *SIEMHandler) Test(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	id, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid forwarder id")
		return
	}
	fwd, err := h.queries.GetSIEMForwarder(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "SIEM forwarder not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to read SIEM forwarder")
		return
	}
	actor := currentUserUUID(r)
	if !actor.Valid || uuid.UUID(actor.Bytes) == uuid.Nil || h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Durable SIEM test operations are unavailable")
		return
	}
	actorID := uuid.UUID(actor.Bytes)
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	scope := "siem-test:actor:" + actorID.String() + ":forwarder:" + fwd.ID.String()
	operationID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(scope+":"+key))
	digestBytes := sha256.Sum256([]byte("siem-forwarder-test:" + fwd.ID.String()))
	requestDigest := fmt.Sprintf("%x", digestBytes[:])
	now := time.Now().UTC()
	payload, _ := json.Marshal(map[string]any{
		"event_name": "siem.test_ping",
		"event_id":   operationID.String(),
		"timestamp":  now,
		"detail": map[string]any{
			"message":      "synthetic SIEM test ping from astronomer admin",
			"triggered_by": callerUsername(r),
		},
	})
	var operation sqlc.SIEMTestOperation
	err = h.runTx(r.Context(), func(q SIEMMutationTx) error {
		operations, ok := q.(siemTestOperationQueries)
		if !ok {
			return errors.New("SIEM test operation persistence is unavailable")
		}
		var createErr error
		operation, createErr = operations.CreateSIEMTestOperationAndQueue(r.Context(), sqlc.CreateSIEMTestOperationAndQueueParams{
			ID: operationID, ForwarderID: fwd.ID, IdempotencyScope: scope,
			IdempotencyKey: key, RequestDigest: requestDigest, RequestedBy: actorID, Payload: payload,
		})
		if createErr != nil || !operation.Created {
			return createErr
		}
		return recordAuditOutbox(r, q, "admin.siem_forwarder.test_queued", "siem_test_operation", operation.ID.String(), fwd.Name,
			http.StatusAccepted, map[string]any{"queue_id": operation.QueueID, "forwarder_id": fwd.ID.String(), "transport": fwd.Transport})
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different SIEM test")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.WriteError, "Failed to enqueue test event")
		return
	}
	statusURL := "/api/v1/admin/siem-forwarders/" + fwd.ID.String() + "/test-operations/" + operation.ID.String() + "/"
	w.Header().Set("Location", statusURL)
	w.Header().Set("Retry-After", "2")
	RespondJSON(w, http.StatusAccepted, makeSIEMTestOperationResponse(operation, statusURL))
}

type siemTestOperationResponse struct {
	OperationID uuid.UUID `json:"operation_id"`
	ForwarderID uuid.UUID `json:"forwarder_id"`
	Status      string    `json:"status"`
	StatusURL   string    `json:"status_url"`
	ErrorCode   string    `json:"error_code,omitempty"`
	CreatedAt   string    `json:"created_at"`
	CompletedAt string    `json:"completed_at,omitempty"`
}

func makeSIEMTestOperationResponse(row sqlc.SIEMTestOperation, statusURL string) siemTestOperationResponse {
	response := siemTestOperationResponse{OperationID: row.ID, ForwarderID: row.ForwarderID, Status: row.Status,
		StatusURL: statusURL, ErrorCode: row.ErrorCode, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339)}
	if !row.CompletedAt.IsZero() {
		response.CompletedAt = row.CompletedAt.UTC().Format(time.RFC3339)
	}
	return response
}

func (h *SIEMHandler) GetTestOperation(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	forwarderID, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid forwarder id")
		return
	}
	operationID, ok := parseUUIDParam(r, "operation_id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid SIEM test operation id")
		return
	}
	queries, ok := h.queries.(siemTestOperationQueries)
	if !ok {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.InternalError, "Durable SIEM test operations are unavailable")
		return
	}
	row, err := queries.GetSIEMTestOperation(r.Context(), sqlc.GetSIEMTestOperationParams{ID: operationID, ForwarderID: forwarderID})
	if errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "SIEM test operation not found")
		return
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to read SIEM test operation")
		return
	}
	statusURL := "/api/v1/admin/siem-forwarders/" + forwarderID.String() + "/test-operations/" + operationID.String() + "/"
	RespondJSON(w, http.StatusOK, makeSIEMTestOperationResponse(row, statusURL))
}

// siemStatusResponse is the wire shape for the per-forwarder status
// endpoint.
type siemStatusResponse struct {
	ForwarderID     string  `json:"forwarder_id"`
	LastSentAt      *string `json:"last_sent_at"`
	LastError       string  `json:"last_error"`
	QueueDepth      int     `json:"queue_depth"`
	DroppedTotal    int64   `json:"dropped_total"`
	DispatchedTotal int64   `json:"dispatched_total"`
	UpdatedAt       string  `json:"updated_at"`
}

// Status handles GET /api/v1/admin/siem-forwarders/{id}/status/. Reads
// the live status row + the current queue_depth so the UI doesn't have
// to call two endpoints to render the per-forwarder health card.
func (h *SIEMHandler) Status(w http.ResponseWriter, r *http.Request) {
	if err := h.requireSuperuser(r); err != nil {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, err.Error())
		return
	}
	id, ok := parseUUIDParam(r, "id")
	if !ok {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid forwarder id")
		return
	}
	if _, err := h.queries.GetSIEMForwarder(r.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "SIEM forwarder not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to read SIEM forwarder")
		return
	}
	status, err := h.queries.GetSIEMForwarderStatus(r.Context(), id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ReadError, "Failed to read SIEM forwarder status")
		return
	}
	depth, _ := h.queries.CountSIEMQueueByForwarder(r.Context(), id)
	resp := siemStatusResponse{
		ForwarderID:     id.String(),
		LastError:       status.LastError,
		QueueDepth:      int(depth),
		DroppedTotal:    status.DroppedTotal,
		DispatchedTotal: status.DispatchedTotal,
		UpdatedAt:       status.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if status.LastSentAt.Valid {
		s := status.LastSentAt.Time.UTC().Format(time.RFC3339)
		resp.LastSentAt = &s
	}
	RespondJSONUnwrapped(w, http.StatusOK, resp)
}

// mergedSIEMSettings is the validated, all-pointers-resolved view used
// by both the create + update paths.
type mergedSIEMSettings struct {
	Name            string
	Transport       string
	Endpoint        string
	EventFilters    []string
	Format          string
	TLSSkipVerify   bool
	CACertPEM       string
	BatchSize       int
	FlushIntervalMs int
	TimeoutSeconds  int
	Enabled         bool
}

func (h *SIEMHandler) mergeSIEMForCreate(req siemForwarderRequest) (mergedSIEMSettings, string) {
	out := mergedSIEMSettings{
		EventFilters:    []string{},
		BatchSize:       100,
		FlushIntervalMs: 2000,
		TimeoutSeconds:  10,
		Enabled:         true,
	}
	if req.Name != nil {
		out.Name = strings.TrimSpace(*req.Name)
	}
	if req.Transport != nil {
		out.Transport = strings.TrimSpace(*req.Transport)
	}
	if req.Endpoint != nil {
		out.Endpoint = strings.TrimSpace(*req.Endpoint)
	}
	if req.EventFilters != nil {
		out.EventFilters = *req.EventFilters
	}
	if req.Format != nil {
		out.Format = strings.TrimSpace(*req.Format)
	}
	if req.TLSSkipVerify != nil {
		out.TLSSkipVerify = *req.TLSSkipVerify
	}
	if req.CACertPEM != nil {
		out.CACertPEM = *req.CACertPEM
	}
	if req.BatchSize != nil {
		out.BatchSize = *req.BatchSize
	}
	if req.FlushIntervalMs != nil {
		out.FlushIntervalMs = *req.FlushIntervalMs
	}
	if req.TimeoutSeconds != nil {
		out.TimeoutSeconds = *req.TimeoutSeconds
	}
	if req.Enabled != nil {
		out.Enabled = *req.Enabled
	}
	return out, validateMergedSIEM(out)
}

func (h *SIEMHandler) mergeSIEMForUpdate(existing sqlc.SiemForwarder, req siemForwarderRequest) (mergedSIEMSettings, string) {
	out := mergedSIEMSettings{
		Name:            existing.Name,
		Transport:       existing.Transport,
		Endpoint:        existing.Endpoint,
		Format:          existing.Format,
		TLSSkipVerify:   existing.TlsSkipVerify,
		CACertPEM:       existing.CaCertPem,
		BatchSize:       int(existing.BatchSize),
		FlushIntervalMs: int(existing.FlushIntervalMs),
		TimeoutSeconds:  int(existing.TimeoutSeconds),
		Enabled:         existing.Enabled,
	}
	out.EventFilters = []string{}
	if len(existing.EventFilters) > 0 {
		_ = json.Unmarshal(existing.EventFilters, &out.EventFilters)
	}
	if req.Name != nil {
		out.Name = strings.TrimSpace(*req.Name)
	}
	if req.Transport != nil {
		out.Transport = strings.TrimSpace(*req.Transport)
	}
	if req.Endpoint != nil {
		out.Endpoint = strings.TrimSpace(*req.Endpoint)
	}
	if req.EventFilters != nil {
		out.EventFilters = *req.EventFilters
	}
	if req.Format != nil {
		out.Format = strings.TrimSpace(*req.Format)
	}
	if req.TLSSkipVerify != nil {
		out.TLSSkipVerify = *req.TLSSkipVerify
	}
	if req.CACertPEM != nil {
		out.CACertPEM = *req.CACertPEM
	}
	if req.BatchSize != nil {
		out.BatchSize = *req.BatchSize
	}
	if req.FlushIntervalMs != nil {
		out.FlushIntervalMs = *req.FlushIntervalMs
	}
	if req.TimeoutSeconds != nil {
		out.TimeoutSeconds = *req.TimeoutSeconds
	}
	if req.Enabled != nil {
		out.Enabled = *req.Enabled
	}
	return out, validateMergedSIEM(out)
}

func validateMergedSIEM(s mergedSIEMSettings) string {
	if s.Name == "" {
		return "name is required"
	}
	if len(s.Name) > 128 {
		return "name must be 128 chars or fewer"
	}
	if !validSIEMTransports[s.Transport] {
		return "transport must be one of: syslog_udp, syslog_tcp, syslog_tls, splunk_hec, ndjson_https"
	}
	if s.Endpoint == "" {
		return "endpoint is required"
	}
	if !validSIEMFormats[s.Format] {
		return "format must be one of: rfc5424, rfc3164, cef, ndjson (or empty for auto)"
	}
	if s.BatchSize < 1 || s.BatchSize > 10000 {
		return "batch_size must be 1..10000"
	}
	if s.FlushIntervalMs < 100 || s.FlushIntervalMs > 600000 {
		return "flush_interval_ms must be 100..600000"
	}
	if s.TimeoutSeconds < 1 || s.TimeoutSeconds > 300 {
		return "timeout_seconds must be 1..300"
	}
	// HTTPS sinks require an https:// URL; the syslog transports take
	// host:port. We keep the check loose enough that operators can use
	// IP literals without scheme parsing yelling.
	switch s.Transport {
	case siem.TransportSplunkHEC, siem.TransportNDJSONHTTPS:
		if !strings.HasPrefix(s.Endpoint, "http://") && !strings.HasPrefix(s.Endpoint, "https://") {
			return "endpoint for HTTPS transports must start with http:// or https://"
		}
	}
	return ""
}

// toSIEMForwarderResponse renders one row into the wire shape. We
// always set auth to the sentinel — never leak the ciphertext.
func toSIEMForwarderResponse(row sqlc.SiemForwarder) siemForwarderResponse {
	filters := []string{}
	if len(row.EventFilters) > 0 {
		_ = json.Unmarshal(row.EventFilters, &filters)
	}
	createdBy := ""
	if row.CreatedBy.Valid {
		createdBy = uuid.UUID(row.CreatedBy.Bytes).String()
	}
	return siemForwarderResponse{
		ID:               row.ID.String(),
		Name:             row.Name,
		Transport:        row.Transport,
		Endpoint:         row.Endpoint,
		Auth:             SIEMAuthSentinel,
		AuthConfigured:   row.AuthEncrypted != "",
		EventFilters:     filters,
		Format:           row.Format,
		TLSSkipVerify:    row.TlsSkipVerify,
		CACertConfigured: row.CaCertPem != "",
		BatchSize:        int(row.BatchSize),
		FlushIntervalMs:  int(row.FlushIntervalMs),
		TimeoutSeconds:   int(row.TimeoutSeconds),
		Enabled:          row.Enabled,
		CreatedBy:        createdBy,
		CreatedAt:        row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:        row.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func (h *SIEMHandler) requireSuperuser(r *http.Request) error {
	return requireSuperuserFromContext(r, h.queries)
}

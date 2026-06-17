// Package handler — migration 067: Vault connections.
//
// Superuser-gated CRUD over vault_connections plus a project-level
// "default vault connection" picker. The HTTP surface is:
//
//   Superuser (gated inside each handler method — same pattern as
//   admin_drill.go / admin_queues.go):
//     GET    /api/v1/admin/vault-connections/
//     POST   /api/v1/admin/vault-connections/
//     GET    /api/v1/admin/vault-connections/{id}/
//     PUT    /api/v1/admin/vault-connections/{id}/
//     DELETE /api/v1/admin/vault-connections/{id}/
//     POST   /api/v1/admin/vault-connections/{id}/test/   — auth + kv probe
//     POST   /api/v1/admin/vault-connections/{id}/health/ — auth-only probe
//
//   Project (gated via the standard project RBAC middleware in
//   routes.go — superuser gating is wrong here because operators with
//   Project Edit on a project should be allowed to pick which Vault
//   connection their installs default to):
//     GET    /api/v1/projects/{id}/default-vault-connection/
//     PUT    /api/v1/projects/{id}/default-vault-connection/
//
// Encryption:
//   - auth_encrypted is a Fernet-encrypted JSON blob whose shape depends
//     on auth_method (see internal/vault/resolver.go DecodeAuthBlob).
//   - GETs decrypt then redact: the response shows method-specific
//     redaction (e.g. token: "<encrypted>") so the UI can render the
//     form without ever shipping the cleartext over the wire.
//   - PUT accepts the SentinelEncrypted value (constant in
//     internal/vault) as "preserve the stored auth blob" so a natural
//     GET → edit → PUT loop doesn't blank the credentials.
//
// Audit + metrics:
//   - admin.vault_connection.{created,updated,deleted,tested,healthchecked}
//     audit rows are written best-effort.
//   - The /test/ and /health/ endpoints update last_health_* on the row
//     and emit astronomer_vault_connection_health{connection}.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	avault "github.com/alphabravocompany/astronomer-go/internal/vault"
)

// VaultConnectionQuerier is the slice of *sqlc.Queries the handler needs.
// Defined as an interface so tests can pass narrow fakes.
type VaultConnectionQuerier interface {
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	GetProjectByID(ctx context.Context, id uuid.UUID) (sqlc.Project, error)

	ListVaultConnections(ctx context.Context) ([]sqlc.VaultConnection, error)
	GetVaultConnectionByID(ctx context.Context, id uuid.UUID) (sqlc.VaultConnection, error)
	GetVaultConnectionByName(ctx context.Context, name string) (sqlc.VaultConnection, error)
	CreateVaultConnection(ctx context.Context, arg sqlc.CreateVaultConnectionParams) (sqlc.VaultConnection, error)
	UpdateVaultConnection(ctx context.Context, arg sqlc.UpdateVaultConnectionParams) (sqlc.VaultConnection, error)
	DeleteVaultConnection(ctx context.Context, id uuid.UUID) error
	UpdateVaultConnectionHealth(ctx context.Context, arg sqlc.UpdateVaultConnectionHealthParams) error

	SetProjectDefaultVaultConnection(ctx context.Context, arg sqlc.SetProjectDefaultVaultConnectionParams) error
	GetProjectDefaultVaultConnection(ctx context.Context, projectID uuid.UUID) (pgtype.UUID, error)
}

// VaultProbe is the surface used by /test/ + /health/ endpoints. The
// production implementation builds a transient avault.Client via the
// resolver's factory; tests pass a fake to skip the network.
type VaultProbe interface {
	// Health authenticates against the connection. Returns nil on
	// success; otherwise the wrapped error.
	Health(ctx context.Context, conn sqlc.VaultConnection, authBlob string) error
	// Test authenticates AND performs a kv GET against the provided
	// probe path. The path must exist (or 404 cleanly); the value
	// of the secret is NOT returned to the caller.
	Test(ctx context.Context, conn sqlc.VaultConnection, authBlob, probePath string) (TestResult, error)
}

// TestResult is the JSON shape of POST /test/. Latency is measured by
// the probe; Reachable is true when the connection auth + probe both
// succeeded. The secret value never appears here — only timing + a
// short human-readable message.
type TestResult struct {
	OK        bool   `json:"ok"`
	Reachable bool   `json:"reachable"`
	AuthOK    bool   `json:"auth_ok"`
	LatencyMS int64  `json:"latency_ms"`
	Message   string `json:"message"`
	ProbePath string `json:"probe_path,omitempty"`
}

// VaultHandler owns the admin + project-default endpoints.
type VaultHandler struct {
	queries   VaultConnectionQuerier
	auditor   any // auditWriterV1 surface; same pattern as cloud_credentials
	encryptor *auth.Encryptor
	probe     VaultProbe
	resolver  *avault.Resolver // cached resolver for ClearCache calls on mutate/delete
}

// NewVaultHandler wires the handler. The encryptor is required for
// POST/PUT (writes refuse with 503 not_configured when nil).
func NewVaultHandler(queries VaultConnectionQuerier) *VaultHandler {
	return &VaultHandler{queries: queries}
}

func (h *VaultHandler) SetAuditor(a any) {
	if h != nil {
		h.auditor = a
	}
}
func (h *VaultHandler) SetEncryptor(e *auth.Encryptor) {
	if h != nil {
		h.encryptor = e
	}
}
func (h *VaultHandler) SetProbe(p VaultProbe) {
	if h != nil {
		h.probe = p
	}
}
func (h *VaultHandler) SetResolver(r *avault.Resolver) {
	if h != nil {
		h.resolver = r
	}
}

// --- Wire DTOs ---------------------------------------------------------

// VaultConnectionResponse is the wire shape on every GET / List / write
// echo. The auth blob is method-specific redacted — token values become
// "<encrypted>", approle's role_id is kept visible (it's not the secret
// part) and secret_id is redacted, kubernetes role + jwt_path are kept
// visible (no secret).
type VaultConnectionResponse struct {
	ID            uuid.UUID         `json:"id"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Addr          string            `json:"addr"`
	AuthMethod    string            `json:"auth_method"`
	Auth          map[string]string `json:"auth"`
	Namespace     string            `json:"namespace"`
	TLSSkipVerify bool              `json:"tls_skip_verify"`
	CACertPEM     string            `json:"ca_cert_pem"`
	DefaultMount  string            `json:"default_mount"`
	Enabled       bool              `json:"enabled"`
	LastHealthAt  string            `json:"last_health_at,omitempty"`
	LastHealthOK  bool              `json:"last_health_ok"`
	LastError     string            `json:"last_error,omitempty"`
	CreatedAt     string            `json:"created_at"`
	UpdatedAt     string            `json:"updated_at"`
}

// VaultConnectionRequest is the POST / PUT body. Auth is method-specific:
//   - token:      {"token": "..."}
//   - approle:    {"role_id": "...", "secret_id": "..."}
//   - kubernetes: {"role": "...", "jwt_path": "/var/run/secrets/..."}
//
// Any secret field that arrives equal to avault.SentinelEncrypted on PUT
// is preserved from the stored blob — so a GET → edit → PUT loop
// doesn't wipe the credentials.
type VaultConnectionRequest struct {
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Addr          string            `json:"addr"`
	AuthMethod    string            `json:"auth_method"`
	Auth          map[string]string `json:"auth"`
	Namespace     string            `json:"namespace"`
	TLSSkipVerify bool              `json:"tls_skip_verify"`
	CACertPEM     string            `json:"ca_cert_pem"`
	DefaultMount  string            `json:"default_mount"`
	Enabled       *bool             `json:"enabled,omitempty"`
}

// --- Common gating -----------------------------------------------------

// gateSuperuser is the same pattern as admin_drill / admin_queues. 401
// on no auth, 403 on non-superuser, 500 on internal lookup failure.
func (h *VaultHandler) gateSuperuser(w http.ResponseWriter, r *http.Request) (sqlc.User, bool) {
	return requireSuperuser(w, r, h.queries, superuserGateConfig{
		StoreUnavailableMessage: "Admin store not configured",
		ForbiddenMessage:        "Vault connection management requires superuser privileges",
	})
}

// --- Admin CRUD --------------------------------------------------------

// List handles GET /api/v1/admin/vault-connections/.
func (h *VaultHandler) List(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.gateSuperuser(w, r); !ok {
		return
	}
	rows, err := h.queries.ListVaultConnections(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list vault connections")
		return
	}
	out := make([]VaultConnectionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, h.toResponse(row, true))
	}
	RespondJSON(w, http.StatusOK, map[string]any{"items": out})
}

// Get handles GET /api/v1/admin/vault-connections/{id}/.
func (h *VaultHandler) Get(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.gateSuperuser(w, r); !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid connection ID")
		return
	}
	row, err := h.queries.GetVaultConnectionByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Vault connection not found")
		return
	}
	RespondJSON(w, http.StatusOK, h.toResponse(row, true))
}

// Create handles POST /api/v1/admin/vault-connections/.
func (h *VaultHandler) Create(w http.ResponseWriter, r *http.Request) {
	caller, ok := h.gateSuperuser(w, r)
	if !ok {
		return
	}
	if h.encryptor == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Encryption key not configured; cannot store vault auth")
		return
	}
	var req VaultConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if err := validateConnectionName(req.Name); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidName, err.Error())
		return
	}
	if err := validateAddr(req.Addr, req.TLSSkipVerify); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidAddr, err.Error())
		return
	}
	authBlob, err := avault.EncodeAuthBlob(req.AuthMethod, req.Auth)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.AuthenticationRequired, err.Error())
		return
	}
	encrypted, err := h.encryptor.Encrypt(authBlob)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt auth blob")
		return
	}
	mount := req.DefaultMount
	if mount == "" {
		mount = "secret"
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	createdBy := pgtype.UUID{}
	if u, err := uuid.Parse(caller.ID.String()); err == nil {
		createdBy = pgtype.UUID{Bytes: u, Valid: true}
	}

	row, err := h.queries.CreateVaultConnection(r.Context(), sqlc.CreateVaultConnectionParams{
		Name:          req.Name,
		Description:   req.Description,
		Addr:          req.Addr,
		AuthMethod:    req.AuthMethod,
		AuthEncrypted: encrypted,
		Namespace:     req.Namespace,
		TlsSkipVerify: req.TLSSkipVerify,
		CaCertPem:     req.CACertPEM,
		DefaultMount:  mount,
		Enabled:       enabled,
		CreatedBy:     createdBy,
	})
	if err != nil {
		if isUniqueViolation(err) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "A vault connection with that name already exists")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create vault connection")
		return
	}
	recordAudit(r, h.queries, "admin.vault_connection.created", "vault_connection", row.ID.String(), row.Name, map[string]any{
		"addr":        row.Addr,
		"auth_method": row.AuthMethod,
		"namespace":   row.Namespace,
	})
	w.Header().Set("Location", "/api/v1/admin/vault-connections/"+row.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, h.toResponse(row, true))
}

// Update handles PUT /api/v1/admin/vault-connections/{id}/.
func (h *VaultHandler) Update(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.gateSuperuser(w, r); !ok {
		return
	}
	if h.encryptor == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Encryption key not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid connection ID")
		return
	}
	existing, err := h.queries.GetVaultConnectionByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Vault connection not found")
		return
	}
	var req VaultConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if req.AuthMethod == "" {
		req.AuthMethod = existing.AuthMethod
	}
	if err := validateAddr(req.Addr, req.TLSSkipVerify); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidAddr, err.Error())
		return
	}

	// Auth-preservation: any incoming field that equals the sentinel is
	// replaced with the stored value before we re-encode the blob.
	mergedAuth, err := h.mergeAuth(existing, req.AuthMethod, req.Auth)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.AuthenticationRequired, err.Error())
		return
	}
	authBlob, err := avault.EncodeAuthBlob(req.AuthMethod, mergedAuth)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.AuthenticationRequired, err.Error())
		return
	}
	encrypted, err := h.encryptor.Encrypt(authBlob)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt auth blob")
		return
	}
	mount := req.DefaultMount
	if mount == "" {
		mount = existing.DefaultMount
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	row, err := h.queries.UpdateVaultConnection(r.Context(), sqlc.UpdateVaultConnectionParams{
		ID:            id,
		Description:   req.Description,
		Addr:          req.Addr,
		AuthMethod:    req.AuthMethod,
		AuthEncrypted: encrypted,
		Namespace:     req.Namespace,
		TlsSkipVerify: req.TLSSkipVerify,
		CaCertPem:     req.CACertPEM,
		DefaultMount:  mount,
		Enabled:       enabled,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update vault connection")
		return
	}
	if h.resolver != nil {
		h.resolver.ClearCache(row.ID)
	}
	recordAudit(r, h.queries, "admin.vault_connection.updated", "vault_connection", row.ID.String(), row.Name, map[string]any{
		"addr":        row.Addr,
		"auth_method": row.AuthMethod,
	})
	RespondJSON(w, http.StatusOK, h.toResponse(row, true))
}

// Delete handles DELETE /api/v1/admin/vault-connections/{id}/.
func (h *VaultHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.gateSuperuser(w, r); !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid connection ID")
		return
	}
	existing, err := h.queries.GetVaultConnectionByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Vault connection not found")
		return
	}
	if err := h.queries.DeleteVaultConnection(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete vault connection")
		return
	}
	if h.resolver != nil {
		h.resolver.ClearCache(id)
	}
	recordAudit(r, h.queries, "admin.vault_connection.deleted", "vault_connection", id.String(), existing.Name, map[string]any{})
	w.WriteHeader(http.StatusNoContent)
}

// Test handles POST /api/v1/admin/vault-connections/{id}/test/.
// Body: {"probe_path": "secret/data/_health"} — optional; defaults to a
// known-empty path under the connection's default_mount.
func (h *VaultHandler) Test(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.gateSuperuser(w, r); !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid connection ID")
		return
	}
	conn, err := h.queries.GetVaultConnectionByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Vault connection not found")
		return
	}
	if h.probe == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Vault probe not configured")
		return
	}
	var body struct {
		ProbePath string `json:"probe_path"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.ProbePath == "" {
		body.ProbePath = "_health"
	}

	authBlob, decErr := h.decryptAuth(conn)
	if decErr != nil {
		_ = h.queries.UpdateVaultConnectionHealth(r.Context(), sqlc.UpdateVaultConnectionHealthParams{ID: conn.ID, LastHealthOk: false, LastError: decErr.Error()})
		RespondJSON(w, http.StatusOK, TestResult{OK: false, Message: decErr.Error()})
		return
	}

	res, err := h.probe.Test(r.Context(), conn, authBlob, body.ProbePath)
	if err != nil {
		// The probe consolidates auth + kv-probe; an err here means we
		// couldn't even authenticate. Stamp health row + audit.
		_ = h.queries.UpdateVaultConnectionHealth(r.Context(), sqlc.UpdateVaultConnectionHealthParams{
			ID: conn.ID, LastHealthOk: false, LastError: err.Error(),
		})
		observability.RecordVaultHealth(conn.Name, false)
		recordAudit(r, h.queries, "admin.vault_connection.tested", "vault_connection", conn.ID.String(), conn.Name, map[string]any{
			"ok":         false,
			"probe_path": body.ProbePath,
			"error":      err.Error(),
		})
		RespondJSON(w, http.StatusOK, TestResult{OK: false, Message: err.Error(), ProbePath: body.ProbePath})
		return
	}
	_ = h.queries.UpdateVaultConnectionHealth(r.Context(), sqlc.UpdateVaultConnectionHealthParams{
		ID: conn.ID, LastHealthOk: res.OK, LastError: "",
	})
	observability.RecordVaultHealth(conn.Name, res.OK)
	recordAudit(r, h.queries, "admin.vault_connection.tested", "vault_connection", conn.ID.String(), conn.Name, map[string]any{
		"ok":         res.OK,
		"latency_ms": res.LatencyMS,
		"probe_path": body.ProbePath,
		// IMPORTANT: never include any field from the Vault response;
		// only timing + reachability.
	})
	RespondJSON(w, http.StatusOK, res)
}

// Health handles POST /api/v1/admin/vault-connections/{id}/health/.
// Auth-only — no kv GET. Used by the UI's "ping" button.
func (h *VaultHandler) Health(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.gateSuperuser(w, r); !ok {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid connection ID")
		return
	}
	conn, err := h.queries.GetVaultConnectionByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Vault connection not found")
		return
	}
	if h.probe == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Vault probe not configured")
		return
	}
	authBlob, decErr := h.decryptAuth(conn)
	if decErr != nil {
		_ = h.queries.UpdateVaultConnectionHealth(r.Context(), sqlc.UpdateVaultConnectionHealthParams{ID: conn.ID, LastHealthOk: false, LastError: decErr.Error()})
		observability.RecordVaultHealth(conn.Name, false)
		RespondJSON(w, http.StatusOK, map[string]any{"ok": false, "message": decErr.Error()})
		return
	}
	start := time.Now()
	herr := h.probe.Health(r.Context(), conn, authBlob)
	latency := time.Since(start)
	ok := herr == nil
	msg := "ok"
	errStr := ""
	if !ok {
		msg = herr.Error()
		errStr = msg
	}
	_ = h.queries.UpdateVaultConnectionHealth(r.Context(), sqlc.UpdateVaultConnectionHealthParams{
		ID: conn.ID, LastHealthOk: ok, LastError: errStr,
	})
	observability.RecordVaultHealth(conn.Name, ok)
	recordAudit(r, h.queries, "admin.vault_connection.healthchecked", "vault_connection", conn.ID.String(), conn.Name, map[string]any{
		"ok":         ok,
		"latency_ms": latency.Milliseconds(),
	})
	RespondJSON(w, http.StatusOK, map[string]any{
		"ok":         ok,
		"latency_ms": latency.Milliseconds(),
		"message":    msg,
	})
}

// --- Project default ---------------------------------------------------

// GetProjectDefault handles GET /api/v1/projects/{id}/default-vault-connection/.
//
// Project RBAC is enforced by the route's requirePermission middleware
// in routes.go; this handler just returns the row (or null).
func (h *VaultHandler) GetProjectDefault(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		// Some route conventions use "project_id" instead of "id"; try
		// the alternate path param too.
		projectID, err = uuid.Parse(chi.URLParam(r, "project_id"))
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
			return
		}
	}
	if _, err := h.queries.GetProjectByID(r.Context(), projectID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
		return
	}
	ptr, err := h.queries.GetProjectDefaultVaultConnection(r.Context(), projectID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Failed to look up project default vault connection")
		return
	}
	if !ptr.Valid {
		RespondJSON(w, http.StatusOK, map[string]any{"connection_id": nil, "connection": nil})
		return
	}
	conn, err := h.queries.GetVaultConnectionByID(r.Context(), ptr.Bytes)
	if err != nil {
		RespondJSON(w, http.StatusOK, map[string]any{"connection_id": ptr.Bytes, "connection": nil})
		return
	}
	resp := h.toResponse(conn, true)
	RespondJSON(w, http.StatusOK, map[string]any{"connection_id": ptr.Bytes, "connection": resp})
}

// PutProjectDefault handles PUT /api/v1/projects/{id}/default-vault-connection/.
// Body: {"connection_id": "<uuid>" | null}
func (h *VaultHandler) PutProjectDefault(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		projectID, err = uuid.Parse(chi.URLParam(r, "project_id"))
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
			return
		}
	}
	if _, err := h.queries.GetProjectByID(r.Context(), projectID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
		return
	}
	var body struct {
		ConnectionID *string `json:"connection_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	var ptr pgtype.UUID
	if body.ConnectionID != nil && *body.ConnectionID != "" {
		id, err := uuid.Parse(*body.ConnectionID)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid connection_id")
			return
		}
		if _, err := h.queries.GetVaultConnectionByID(r.Context(), id); err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Vault connection not found")
			return
		}
		ptr = pgtype.UUID{Bytes: id, Valid: true}
	}
	if err := h.queries.SetProjectDefaultVaultConnection(r.Context(), sqlc.SetProjectDefaultVaultConnectionParams{
		ID:                       projectID,
		DefaultVaultConnectionID: ptr,
	}); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update project default vault connection")
		return
	}
	recordAudit(r, h.queries, "project.default_vault_connection.set", "project", projectID.String(), "", map[string]any{
		"connection_id": stringOrNull(body.ConnectionID),
	})
	RespondJSON(w, http.StatusOK, map[string]any{"connection_id": stringOrNull(body.ConnectionID)})
}

// --- Helpers -----------------------------------------------------------

// toResponse converts a row to the wire shape. When redact=true the
// secret fields of the auth blob become the sentinel marker; never
// returns the cleartext value to the wire.
func (h *VaultHandler) toResponse(row sqlc.VaultConnection, redact bool) VaultConnectionResponse {
	resp := VaultConnectionResponse{
		ID:            row.ID,
		Name:          row.Name,
		Description:   row.Description,
		Addr:          row.Addr,
		AuthMethod:    row.AuthMethod,
		Auth:          map[string]string{},
		Namespace:     row.Namespace,
		TLSSkipVerify: row.TlsSkipVerify,
		CACertPEM:     row.CaCertPem,
		DefaultMount:  row.DefaultMount,
		Enabled:       row.Enabled,
		LastHealthOK:  row.LastHealthOk,
		LastError:     row.LastError,
		CreatedAt:     row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     row.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if row.LastHealthAt.Valid {
		resp.LastHealthAt = row.LastHealthAt.Time.UTC().Format(time.RFC3339)
	}
	switch row.AuthMethod {
	case "token":
		resp.Auth["token"] = avault.SentinelEncrypted
	case "approle":
		// role_id is not the secret part — it's safe to round-trip in
		// the response so the UI can render it. secret_id is the
		// secret part; redact.
		auth, _ := h.decryptAuthMap(row)
		resp.Auth["role_id"] = auth["role_id"]
		resp.Auth["secret_id"] = avault.SentinelEncrypted
	case "kubernetes":
		// kubernetes auth has no secret part stored in our DB — the
		// secret is the in-cluster SA JWT, read from disk at login
		// time. Round-trip everything.
		auth, _ := h.decryptAuthMap(row)
		resp.Auth["role"] = auth["role"]
		resp.Auth["jwt_path"] = auth["jwt_path"]
	}
	if !redact {
		// Path used only by internal probes — never wired to the
		// HTTP layer. We keep the toggle for symmetry with the
		// cloudcreds pattern.
		auth, _ := h.decryptAuthMap(row)
		for k, v := range auth {
			resp.Auth[k] = v
		}
	}
	return resp
}

// decryptAuth returns the cleartext JSON blob for a connection.
func (h *VaultHandler) decryptAuth(conn sqlc.VaultConnection) (string, error) {
	if h.encryptor == nil {
		return conn.AuthEncrypted, nil
	}
	if conn.AuthEncrypted == "" {
		return "{}", nil
	}
	return h.encryptor.Decrypt(conn.AuthEncrypted)
}

func (h *VaultHandler) decryptAuthMap(conn sqlc.VaultConnection) (map[string]string, error) {
	blob, err := h.decryptAuth(conn)
	if err != nil {
		return map[string]string{}, err
	}
	return avault.DecodeAuthBlob(conn.AuthMethod, blob)
}

// mergeAuth folds the incoming auth map into the stored one, treating
// SentinelEncrypted values as "preserve existing".
func (h *VaultHandler) mergeAuth(existing sqlc.VaultConnection, newMethod string, incoming map[string]string) (map[string]string, error) {
	out := map[string]string{}
	stored, _ := h.decryptAuthMap(existing)
	if newMethod == existing.AuthMethod {
		for k, v := range stored {
			out[k] = v
		}
	}
	for k, v := range incoming {
		if v == avault.SentinelEncrypted {
			continue
		}
		out[k] = v
	}
	return out, nil
}

func validateConnectionName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name is required")
	}
	if len(name) > 128 {
		return errors.New("name must be <= 128 chars")
	}
	return nil
}

func validateAddr(addr string, allowInsecure bool) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return errors.New("addr is required")
	}
	switch {
	case strings.HasPrefix(addr, "https://"):
		return nil
	case strings.HasPrefix(addr, "http://"):
		if allowInsecure {
			return nil
		}
		return errors.New("http:// addr requires tls_skip_verify=true (dev only)")
	}
	return errors.New("addr must start with http:// or https://")
}

func stringOrNull(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// --- Probe wrapper -----------------------------------------------------

// LiveVaultProbe is the production VaultProbe. It builds a transient
// avault.Client from the connection + decrypted auth blob, runs an
// auth and an optional kv read, then discards the client. We never
// reuse the cached resolver clients here because /test/ should
// validate the operator's most recent input regardless of cache.
type LiveVaultProbe struct{}

// Health performs an auth-only probe.
func (LiveVaultProbe) Health(ctx context.Context, conn sqlc.VaultConnection, authBlob string) error {
	c, err := avault.DefaultClientFactory(conn, authBlob)
	if err != nil {
		return err
	}
	// We use a "definitely-doesn't-exist" path under the default mount
	// to exercise auth without depending on any particular operator-
	// owned secret. A successful auth followed by a 404 from the read
	// proves the token works.
	_, err = c.FetchSecret(ctx, conn.DefaultMount, "__astronomer_health_probe__")
	if err == nil {
		return nil
	}
	// 404 / "not found" means auth worked but the path doesn't exist —
	// which is exactly what we want for an auth-only probe.
	if isNotFoundError(err) {
		return nil
	}
	return err
}

// Test runs auth + an explicit kv GET. The value is never returned to
// the caller — we only report timing + reachability.
func (LiveVaultProbe) Test(ctx context.Context, conn sqlc.VaultConnection, authBlob, probePath string) (TestResult, error) {
	c, err := avault.DefaultClientFactory(conn, authBlob)
	if err != nil {
		return TestResult{OK: false, Message: err.Error()}, err
	}
	start := time.Now()
	_, ferr := c.FetchSecret(ctx, conn.DefaultMount, probePath)
	latency := time.Since(start)
	res := TestResult{
		ProbePath: probePath,
		LatencyMS: latency.Milliseconds(),
	}
	if ferr == nil {
		res.OK = true
		res.Reachable = true
		res.AuthOK = true
		res.Message = "kv probe succeeded"
		return res, nil
	}
	if isNotFoundError(ferr) {
		// Auth worked, kv path doesn't exist — that's a healthy
		// outcome for a generic probe path.
		res.OK = true
		res.Reachable = true
		res.AuthOK = true
		res.Message = fmt.Sprintf("auth ok; %q does not exist (expected)", probePath)
		return res, nil
	}
	res.Reachable = true // we got a response from Vault at all
	res.Message = ferr.Error()
	return res, ferr
}

func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(strings.ToLower(msg), "not found") ||
		strings.Contains(msg, "404")
}

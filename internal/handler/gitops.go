// Package handler — admin CRUD over gitops_registration_sources (migration 060).
//
// Route summary (all superuser-gated):
//
//	GET    /api/v1/admin/gitops-sources/                 — list
//	POST   /api/v1/admin/gitops-sources/                 — create
//	GET    /api/v1/admin/gitops-sources/{id}/            — get
//	PUT    /api/v1/admin/gitops-sources/{id}/            — update
//	DELETE /api/v1/admin/gitops-sources/{id}/            — delete
//	POST   /api/v1/admin/gitops-sources/{id}/sync/       — manual trigger
//	GET    /api/v1/admin/gitops-sources/{id}/preview/    — dry-run diff
//	GET    /api/v1/admin/gitops-sources/{id}/clusters/   — managed clusters
//
// Auth_encrypted is NEVER returned in GET responses — we substitute the
// sentinel "<encrypted>" so a PUT that echoes that value back means
// "keep existing". The sync_mode / on_delete validation here mirrors
// the CHECK constraints on the schema so 400s come back with a clean
// message instead of a 500 from the DB.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
)

// GitOpsAuthSentinel is the placeholder returned in GET responses for
// the auth_encrypted column. PUT requests that echo this value back are
// interpreted as "keep existing auth blob".
const GitOpsAuthSentinel = "<encrypted>"

var validGitOpsAuthModes = map[string]bool{"none": true, "https_token": true, "ssh_key": true}
var validGitOpsSyncModes = map[string]bool{"manual": true, "interval": true}
var validGitOpsOnDelete = map[string]bool{"log": true, "tombstone": true, "decommission": true}

// GitOpsQuerier is the database surface the handler needs.
type GitOpsQuerier interface {
	ListGitOpsSources(ctx context.Context) ([]sqlc.GitopsRegistrationSource, error)
	GetGitOpsSource(ctx context.Context, id uuid.UUID) (sqlc.GitopsRegistrationSource, error)
	GetGitOpsSourceByName(ctx context.Context, name string) (sqlc.GitopsRegistrationSource, error)
	CreateGitOpsSource(ctx context.Context, arg sqlc.CreateGitOpsSourceParams) (sqlc.GitopsRegistrationSource, error)
	UpdateGitOpsSource(ctx context.Context, arg sqlc.UpdateGitOpsSourceParams) (sqlc.GitopsRegistrationSource, error)
	DeleteGitOpsSource(ctx context.Context, id uuid.UUID) error
	ListGitOpsRegisteredClustersBySource(ctx context.Context, sourceID uuid.UUID) ([]sqlc.GitopsRegisteredCluster, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
}

// GitOpsSyncRunner is the worker contract for manual sync + preview.
type GitOpsSyncRunner interface {
	SyncSource(ctx context.Context, sourceID uuid.UUID) error
	PreviewSource(ctx context.Context, sourceID uuid.UUID) (tasks.PreviewResult, error)
}

// defaultSyncRunner adapts the package-level tasks functions to the
// GitOpsSyncRunner interface so production wiring is a one-liner.
type defaultSyncRunner struct{}

func (defaultSyncRunner) SyncSource(ctx context.Context, id uuid.UUID) error {
	return tasks.SyncSource(ctx, id)
}
func (defaultSyncRunner) PreviewSource(ctx context.Context, id uuid.UUID) (tasks.PreviewResult, error) {
	return tasks.PreviewSource(ctx, id)
}

// DefaultGitOpsSyncRunner returns the production runner that calls into
// the worker-task package's SyncSource / PreviewSource entry points.
func DefaultGitOpsSyncRunner() GitOpsSyncRunner { return defaultSyncRunner{} }

// GitOpsHandler owns /api/v1/admin/gitops-sources/*. Superuser-gated.
type GitOpsHandler struct {
	queries   GitOpsQuerier
	runner    GitOpsSyncRunner
	log       *slog.Logger
	audit     AuthAuditWriter
	encryptor *auth.Encryptor
}

// SetEncryptor wires the Fernet encryptor for gitops auth blobs
// (T6 item 060). When nil, auth_encrypted is stored in plaintext —
// the column name is still appropriate because operators can layer
// at-rest encryption at the storage tier, but if a Fernet key is
// available we layer application-level encryption on top.
func (h *GitOpsHandler) SetEncryptor(e *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = e
}

// NewGitOpsHandler builds a handler. runner may be nil — when nil, the
// manual sync / preview endpoints return 503 service_unavailable.
func NewGitOpsHandler(q GitOpsQuerier, runner GitOpsSyncRunner, log *slog.Logger) *GitOpsHandler {
	if log == nil {
		log = slog.Default()
	}
	return &GitOpsHandler{queries: q, runner: runner, log: log}
}

// SetAuditWriter wires the audit log writer.
func (h *GitOpsHandler) SetAuditWriter(a AuthAuditWriter) { h.audit = a }

// gitopsSourceResponse is the wire shape returned by every handler. The
// auth_encrypted column is replaced with a sentinel.
type gitopsSourceResponse struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	RepoURL             string `json:"repo_url"`
	Branch              string `json:"branch"`
	PathPrefix          string `json:"path_prefix"`
	AuthMode            string `json:"auth_mode"`
	Auth                string `json:"auth"` // sentinel
	AuthConfigured      bool   `json:"auth_configured"`
	SyncMode            string `json:"sync_mode"`
	SyncIntervalSeconds int32  `json:"sync_interval_seconds"`
	OnDelete            string `json:"on_delete"`
	LastSyncedAt        string `json:"last_synced_at,omitempty"`
	LastSyncedSHA       string `json:"last_synced_sha,omitempty"`
	LastError           string `json:"last_error,omitempty"`
	Enabled             bool   `json:"enabled"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
}

func toGitOpsSourceResponse(row sqlc.GitopsRegistrationSource) gitopsSourceResponse {
	resp := gitopsSourceResponse{
		ID:                  row.ID.String(),
		Name:                row.Name,
		RepoURL:             row.RepoUrl,
		Branch:              row.Branch,
		PathPrefix:          row.PathPrefix,
		AuthMode:            row.AuthMode,
		Auth:                "",
		AuthConfigured:      row.AuthEncrypted != "",
		SyncMode:            row.SyncMode,
		SyncIntervalSeconds: row.SyncIntervalSeconds,
		OnDelete:            row.OnDelete,
		LastSyncedSHA:       row.LastSyncedSha,
		LastError:           row.LastError,
		Enabled:             row.Enabled,
		CreatedAt:           row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:           row.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if resp.AuthConfigured {
		resp.Auth = GitOpsAuthSentinel
	}
	if row.LastSyncedAt.Valid {
		resp.LastSyncedAt = row.LastSyncedAt.Time.UTC().Format(time.RFC3339)
	}
	return resp
}

// Request body for Create / Update -----------------------------------

type gitopsSourceRequest struct {
	Name                string `json:"name"`
	RepoURL             string `json:"repo_url"`
	Branch              string `json:"branch"`
	PathPrefix          string `json:"path_prefix"`
	AuthMode            string `json:"auth_mode"`
	Auth                string `json:"auth"`
	SyncMode            string `json:"sync_mode"`
	SyncIntervalSeconds int32  `json:"sync_interval_seconds"`
	OnDelete            string `json:"on_delete"`
	Enabled             *bool  `json:"enabled,omitempty"`
}

// gate enforces superuser. Same shape as the SIEM / admin handlers.
func (h *GitOpsHandler) gate(w http.ResponseWriter, r *http.Request) bool {
	_, ok := requireSuperuser(w, r, h.queries, superuserGateConfig{
		StoreUnavailableMessage: "GitOps store not configured",
		ForbiddenMessage:        "GitOps administration requires superuser privileges",
	})
	return ok
}

// List handles GET /api/v1/admin/gitops-sources/.
func (h *GitOpsHandler) List(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	rows, err := h.queries.ListGitOpsSources(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list gitops sources")
		return
	}
	out := make([]gitopsSourceResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toGitOpsSourceResponse(row))
	}
	RespondJSON(w, http.StatusOK, map[string]any{"sources": out})
}

// Create handles POST /api/v1/admin/gitops-sources/.
func (h *GitOpsHandler) Create(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	var req gitopsSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if err := validateGitOpsRequest(&req, true); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	// auth blob: when a Fernet encryptor is wired (the production path),
	// we wrap the raw token before writing it. The column stays named
	// auth_encrypted; only the contents flip from plaintext to a
	// Fernet token. Decrypt happens lazily inside the sync worker.
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	authBlob := req.Auth
	if h.encryptor != nil && authBlob != "" {
		ct, encErr := h.encryptor.Encrypt(authBlob)
		if encErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt gitops auth blob")
			return
		}
		authBlob = ct
	}
	row, err := h.queries.CreateGitOpsSource(r.Context(), sqlc.CreateGitOpsSourceParams{
		Name:                req.Name,
		RepoUrl:             req.RepoURL,
		Branch:              gitopsDefaultString(req.Branch, "main"),
		PathPrefix:          req.PathPrefix,
		AuthMode:            gitopsDefaultString(req.AuthMode, "none"),
		AuthEncrypted:       authBlob,
		SyncMode:            gitopsDefaultString(req.SyncMode, "interval"),
		SyncIntervalSeconds: defaultIntervalSeconds(req.SyncIntervalSeconds),
		OnDelete:            gitopsDefaultString(req.OnDelete, "log"),
		Enabled:             enabled,
		CreatedBy:           currentUserUUID(r),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create gitops source")
		return
	}
	h.warnLargeBlastRadius(r.Context(), row)
	recordAudit(r, h.queries, "admin.gitops_source.created", "gitops_source", row.ID.String(), row.Name, map[string]any{
		"repo_url":  row.RepoUrl,
		"branch":    row.Branch,
		"on_delete": row.OnDelete,
	})
	RespondJSON(w, http.StatusCreated, toGitOpsSourceResponse(row))
}

// Get handles GET /api/v1/admin/gitops-sources/{id}/.
func (h *GitOpsHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid source ID")
		return
	}
	row, err := h.queries.GetGitOpsSource(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "GitOps source not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.GetError, "Failed to load gitops source")
		return
	}
	RespondJSON(w, http.StatusOK, toGitOpsSourceResponse(row))
}

// Update handles PUT /api/v1/admin/gitops-sources/{id}/.
func (h *GitOpsHandler) Update(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid source ID")
		return
	}
	existing, err := h.queries.GetGitOpsSource(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "GitOps source not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.GetError, "Failed to load gitops source")
		return
	}
	var req gitopsSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if err := validateGitOpsRequest(&req, false); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	auth := req.Auth
	if auth == GitOpsAuthSentinel || auth == "" {
		auth = existing.AuthEncrypted
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	row, err := h.queries.UpdateGitOpsSource(r.Context(), sqlc.UpdateGitOpsSourceParams{
		ID:                  id,
		Name:                gitopsDefaultString(req.Name, existing.Name),
		RepoUrl:             gitopsDefaultString(req.RepoURL, existing.RepoUrl),
		Branch:              gitopsDefaultString(req.Branch, existing.Branch),
		PathPrefix:          req.PathPrefix,
		AuthMode:            gitopsDefaultString(req.AuthMode, existing.AuthMode),
		AuthEncrypted:       auth,
		SyncMode:            gitopsDefaultString(req.SyncMode, existing.SyncMode),
		SyncIntervalSeconds: defaultIntervalSecondsOr(req.SyncIntervalSeconds, existing.SyncIntervalSeconds),
		OnDelete:            gitopsDefaultString(req.OnDelete, existing.OnDelete),
		Enabled:             enabled,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update gitops source")
		return
	}
	h.warnLargeBlastRadius(r.Context(), row)
	recordAudit(r, h.queries, "admin.gitops_source.updated", "gitops_source", row.ID.String(), row.Name, map[string]any{
		"repo_url":  row.RepoUrl,
		"branch":    row.Branch,
		"on_delete": row.OnDelete,
	})
	RespondJSON(w, http.StatusOK, toGitOpsSourceResponse(row))
}

// Delete handles DELETE /api/v1/admin/gitops-sources/{id}/.
func (h *GitOpsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid source ID")
		return
	}
	existing, err := h.queries.GetGitOpsSource(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "GitOps source not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.GetError, "Failed to load gitops source")
		return
	}
	if err := h.queries.DeleteGitOpsSource(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete gitops source")
		return
	}
	recordAudit(r, h.queries, "admin.gitops_source.deleted", "gitops_source", id.String(), existing.Name, map[string]any{
		"repo_url": existing.RepoUrl,
	})
	w.WriteHeader(http.StatusNoContent)
}

// Sync handles POST /api/v1/admin/gitops-sources/{id}/sync/.
func (h *GitOpsHandler) Sync(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid source ID")
		return
	}
	if h.runner == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "GitOps sync runner not configured")
		return
	}
	row, err := h.queries.GetGitOpsSource(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "GitOps source not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.GetError, "Failed to load gitops source")
		return
	}
	if err := h.runner.SyncSource(r.Context(), id); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SyncError, err.Error())
		return
	}
	recordAudit(r, h.queries, "admin.gitops_source.synced", "gitops_source", id.String(), row.Name, map[string]any{
		"trigger": "manual",
	})
	RespondJSON(w, http.StatusOK, map[string]any{"status": "synced"})
}

// Preview handles GET /api/v1/admin/gitops-sources/{id}/preview/.
func (h *GitOpsHandler) Preview(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid source ID")
		return
	}
	if h.runner == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "GitOps sync runner not configured")
		return
	}
	res, err := h.runner.PreviewSource(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.PreviewError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, res)
}

// ListClusters handles GET /api/v1/admin/gitops-sources/{id}/clusters/.
func (h *GitOpsHandler) ListClusters(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid source ID")
		return
	}
	rows, err := h.queries.ListGitOpsRegisteredClustersBySource(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list clusters")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, link := range rows {
		entry := map[string]any{
			"cluster_id":      link.ClusterID.String(),
			"repo_path":       link.RepoPath,
			"last_yaml_sha":   link.LastYamlSha,
			"last_applied_at": link.LastAppliedAt.UTC().Format(time.RFC3339),
			"status":          link.Status,
		}
		if link.TombstonedAt.Valid {
			entry["tombstoned_at"] = link.TombstonedAt.Time.UTC().Format(time.RFC3339)
		}
		cluster, err := h.queries.GetClusterByID(r.Context(), link.ClusterID)
		if err == nil {
			entry["cluster_name"] = cluster.Name
			entry["display_name"] = cluster.DisplayName
		}
		out = append(out, entry)
	}
	RespondJSON(w, http.StatusOK, map[string]any{"clusters": out})
}

// Helpers -------------------------------------------------------------

func validateGitOpsRequest(req *gitopsSourceRequest, requireFields bool) error {
	if requireFields {
		if req.Name == "" {
			return errors.New("name is required")
		}
		if req.RepoURL == "" {
			return errors.New("repo_url is required")
		}
	}
	if req.AuthMode != "" && !validGitOpsAuthModes[req.AuthMode] {
		return errors.New("auth_mode must be one of: none, https_token, ssh_key")
	}
	if req.SyncMode != "" && !validGitOpsSyncModes[req.SyncMode] {
		return errors.New("sync_mode must be 'manual' or 'interval'")
	}
	if req.OnDelete != "" && !validGitOpsOnDelete[req.OnDelete] {
		return errors.New("on_delete must be one of: log, tombstone, decommission")
	}
	if req.SyncIntervalSeconds < 0 {
		return errors.New("sync_interval_seconds must be >= 0")
	}
	return nil
}

func gitopsDefaultString(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func defaultIntervalSeconds(v int32) int32 {
	if v <= 0 {
		return 60
	}
	return v
}

func defaultIntervalSecondsOr(v, fallback int32) int32 {
	if v <= 0 {
		return fallback
	}
	return v
}

// warnLargeBlastRadius logs at WARN when on_delete=decommission AND
// path_prefix is empty. That combo means a single accidental rm anywhere
// in the entire repo could trigger a decom; the spec requires we surface
// this at create / update time.
func (h *GitOpsHandler) warnLargeBlastRadius(ctx context.Context, src sqlc.GitopsRegistrationSource) {
	if src.OnDelete == "decommission" && src.PathPrefix == "" {
		h.log.WarnContext(ctx, "gitops source has on_delete=decommission with empty path_prefix; entire repo is monitored",
			"source", src.Name, "source_id", src.ID.String(), "repo_url", src.RepoUrl)
	}
}

// Used to silence unused-import warnings during stubbed audit tests.
var _ = pgtype.UUID{}

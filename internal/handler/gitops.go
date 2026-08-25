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
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
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

type GitOpsMutationTx interface {
	GitOpsQuerier
	audit.OutboxQuerier
	tasks.TaskOutboxWriter
}

type gitOpsRunTxFunc func(context.Context, func(GitOpsMutationTx) error) error

func executeGitOpsMutation[T any](r *http.Request, h *GitOpsHandler, mutate func(GitOpsMutationTx) (T, error), fallback func() (T, error), describe func(T) clusterAuditEvent) (T, error) {
	var zero T
	if h == nil {
		return zero, errors.New("gitops handler is nil")
	}
	if h.runTx != nil {
		var result T
		err := h.runTx(r.Context(), func(q GitOpsMutationTx) error {
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
	auditor := any(h.audit)
	if h.audit == nil {
		auditor = h.queries
	}
	recordAudit(r, auditor, event.action, event.resourceType, event.resourceID, event.resourceName, event.detail)
	return result, nil
}

// GitOpsSyncRunner is retained as the preview-only worker adapter name for
// compatibility. Mutating sync requests must use the durable task outbox and
// cannot call the worker implementation inline.
type GitOpsSyncRunner interface {
	PreviewSource(ctx context.Context, sourceID uuid.UUID) (tasks.PreviewResult, error)
}

// DefaultGitOpsSyncRunner returns the explicitly composed preview runtime. The
// API and worker processes own independent values, so preview cannot overwrite
// the worker's dependencies in a shared package global.
func DefaultGitOpsSyncRunner(runtime tasks.GitOpsRuntime) GitOpsSyncRunner { return runtime }

// GitOpsHandler owns /api/v1/admin/gitops-sources/*. Superuser-gated.
type GitOpsHandler struct {
	queries       GitOpsQuerier
	runTx         gitOpsRunTxFunc
	taskOutbox    tasks.TaskOutboxWriter
	runner        GitOpsSyncRunner
	log           *slog.Logger
	audit         AuthAuditWriter
	encryptor     *auth.Encryptor
	webhookSecret string
}

func (h *GitOpsHandler) SetRunTx(runTx gitOpsRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *GitOpsHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

func (h *GitOpsHandler) SetTaskOutbox(writer tasks.TaskOutboxWriter) {
	if h != nil {
		h.taskOutbox = writer
	}
}

// SetEncryptor wires the Fernet encryptor for GitOps auth blobs. Credentialed
// writes fail closed when it is absent; plaintext is never accepted for the
// auth_encrypted column.
func (h *GitOpsHandler) SetEncryptor(e *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = e
}

// NewGitOpsHandler builds a handler. runner may be nil; only preview requires
// it. Manual/webhook sync requires the durable task-outbox transaction path.
func NewGitOpsHandler(q GitOpsQuerier, runner GitOpsSyncRunner, log *slog.Logger) *GitOpsHandler {
	if log == nil {
		log = slog.Default()
	}
	return &GitOpsHandler{queries: q, runner: runner, log: log}
}

// SetAuditWriter wires the audit log writer.
func (h *GitOpsHandler) SetAuditWriter(a AuthAuditWriter) { h.audit = a }

// SetWebhookSecret wires the shared secret that inbound git-provider push
// webhooks must present. When empty the Webhook endpoint is disabled (503)
// so it can never be triggered unauthenticated.
func (h *GitOpsHandler) SetWebhookSecret(secret string) {
	if h == nil {
		return
	}
	h.webhookSecret = secret
}

// gitopsSourceResponse is the wire shape returned by every handler. The
// auth_encrypted column is replaced with a sentinel.
type gitopsSourceResponse struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	RepoURL               string `json:"repo_url"`
	Branch                string `json:"branch"`
	PathPrefix            string `json:"path_prefix"`
	AuthMode              string `json:"auth_mode"`
	Auth                  string `json:"auth"` // sentinel
	AuthConfigured        bool   `json:"auth_configured"`
	SyncMode              string `json:"sync_mode"`
	SyncIntervalSeconds   int32  `json:"sync_interval_seconds"`
	OnDelete              string `json:"on_delete"`
	LastSyncedAt          string `json:"last_synced_at,omitempty"`
	LastSyncedSHA         string `json:"last_synced_sha,omitempty"`
	LastError             string `json:"last_error,omitempty"`
	Enabled               bool   `json:"enabled"`
	AllowMassDecommission bool   `json:"allow_mass_decommission"`
	CreatedAt             string `json:"created_at"`
	UpdatedAt             string `json:"updated_at"`
}

func toGitOpsSourceResponse(row sqlc.GitopsRegistrationSource) gitopsSourceResponse {
	resp := gitopsSourceResponse{
		ID:                    row.ID.String(),
		Name:                  row.Name,
		RepoURL:               row.RepoUrl,
		Branch:                row.Branch,
		PathPrefix:            row.PathPrefix,
		AuthMode:              row.AuthMode,
		Auth:                  "",
		AuthConfigured:        row.AuthEncrypted != "",
		SyncMode:              row.SyncMode,
		SyncIntervalSeconds:   row.SyncIntervalSeconds,
		OnDelete:              row.OnDelete,
		LastSyncedSHA:         row.LastSyncedSha,
		LastError:             row.LastError,
		Enabled:               row.Enabled,
		AllowMassDecommission: row.AllowMassDecommission,
		CreatedAt:             row.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:             row.UpdatedAt.UTC().Format(time.RFC3339),
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

// openapi:request GitOpsSourceRequest
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
	// AllowMassDecommission arms the one-shot mass-decommission override
	// (E3/H10). *bool (mirroring Enabled) so an unrelated PUT preserves
	// the armed state. The worker consumes/disarms it on the next sync.
	AllowMassDecommission *bool `json:"allow_mass_decommission,omitempty"`
}

type gitOpsUpdateMutationResult struct {
	row           sqlc.GitopsRegistrationSource
	overrideArmed bool
}

type gitOpsSyncMutationResult struct {
	row  sqlc.GitopsRegistrationSource
	task sqlc.TaskOutbox
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
	page, pagination := pageWindow(r, out)
	RespondList(w, page, pagination)
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
	// Auth material is Fernet-sealed before opening the mutation transaction.
	// Decrypt happens only inside the sync worker; an unwired encryptor fails
	// closed instead of writing a misleading plaintext auth_encrypted value.
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	authBlob := req.Auth
	if authBlob != "" {
		if h.encryptor == nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "GitOps credential encryption is not configured")
			return
		}
		ct, encErr := h.encryptor.Encrypt(authBlob)
		if encErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt gitops auth blob")
			return
		}
		authBlob = ct
	}
	params := sqlc.CreateGitOpsSourceParams{
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
	}
	row, err := executeGitOpsMutation(r, h,
		func(q GitOpsMutationTx) (sqlc.GitopsRegistrationSource, error) {
			return q.CreateGitOpsSource(r.Context(), params)
		},
		func() (sqlc.GitopsRegistrationSource, error) {
			return h.queries.CreateGitOpsSource(r.Context(), params)
		},
		func(row sqlc.GitopsRegistrationSource) clusterAuditEvent {
			return clusterAuditEvent{
				action: "admin.gitops_source.created", resourceType: "gitops_source",
				resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusCreated,
				detail: gitOpsSourceAuditDetail(row),
			}
		})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create gitops source")
		return
	}
	h.warnLargeBlastRadius(r.Context(), row)
	w.Header().Set("Location", "/api/v1/admin/gitops-sources/"+row.ID.String()+"/")
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
	var req gitopsSourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if err := validateGitOpsRequest(&req, false); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	// auth blob: a sentinel or empty value means "keep the stored blob".
	// A fresh, non-sentinel value must be encrypted at rest exactly like
	// Create — otherwise a PUT that rotates the git PAT / SSH key would
	// silently persist the new credential in PLAINTEXT in auth_encrypted
	// (the sync worker's decryptGitAuth() falls back to the raw value on a
	// Fernet-decrypt miss, so the leak is invisible at runtime).
	replaceAuth := req.Auth != GitOpsAuthSentinel && req.Auth != ""
	var replacementAuth string
	if replaceAuth {
		if h.encryptor == nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "GitOps credential encryption is not configured")
			return
		}
		ct, encErr := h.encryptor.Encrypt(req.Auth)
		if encErr != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncryptError, "Failed to encrypt gitops auth blob")
			return
		}
		replacementAuth = ct
	}
	updateSource := func(q GitOpsQuerier) (gitOpsUpdateMutationResult, error) {
		existing, getErr := q.GetGitOpsSource(r.Context(), id)
		if getErr != nil {
			return gitOpsUpdateMutationResult{}, getErr
		}
		authBlob := existing.AuthEncrypted
		if replaceAuth {
			authBlob = replacementAuth
		}
		enabled := existing.Enabled
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		allowMass := existing.AllowMassDecommission
		if req.AllowMassDecommission != nil {
			allowMass = *req.AllowMassDecommission
		}
		row, updateErr := q.UpdateGitOpsSource(r.Context(), sqlc.UpdateGitOpsSourceParams{
			ID:                    id,
			Name:                  gitopsDefaultString(req.Name, existing.Name),
			RepoUrl:               gitopsDefaultString(req.RepoURL, existing.RepoUrl),
			Branch:                gitopsDefaultString(req.Branch, existing.Branch),
			PathPrefix:            req.PathPrefix,
			AuthMode:              gitopsDefaultString(req.AuthMode, existing.AuthMode),
			AuthEncrypted:         authBlob,
			SyncMode:              gitopsDefaultString(req.SyncMode, existing.SyncMode),
			SyncIntervalSeconds:   defaultIntervalSecondsOr(req.SyncIntervalSeconds, existing.SyncIntervalSeconds),
			OnDelete:              gitopsDefaultString(req.OnDelete, existing.OnDelete),
			Enabled:               enabled,
			AllowMassDecommission: allowMass,
		})
		return gitOpsUpdateMutationResult{
			row: row, overrideArmed: req.AllowMassDecommission != nil &&
				*req.AllowMassDecommission && !existing.AllowMassDecommission,
		}, updateErr
	}
	result, err := executeGitOpsMutation(r, h,
		func(q GitOpsMutationTx) (gitOpsUpdateMutationResult, error) { return updateSource(q) },
		func() (gitOpsUpdateMutationResult, error) { return updateSource(h.queries) },
		func(result gitOpsUpdateMutationResult) clusterAuditEvent {
			detail := gitOpsSourceAuditDetail(result.row)
			detail["mass_decommission_override_armed"] = result.overrideArmed
			return clusterAuditEvent{
				action: "admin.gitops_source.updated", resourceType: "gitops_source",
				resourceID: result.row.ID.String(), resourceName: result.row.Name,
				status: http.StatusOK, detail: detail,
			}
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "GitOps source not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to update gitops source")
		return
	}
	row := result.row
	h.warnLargeBlastRadius(r.Context(), row)
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
	deleteSource := func(q GitOpsQuerier) (sqlc.GitopsRegistrationSource, error) {
		existing, getErr := q.GetGitOpsSource(r.Context(), id)
		if getErr != nil {
			return sqlc.GitopsRegistrationSource{}, getErr
		}
		if deleteErr := q.DeleteGitOpsSource(r.Context(), id); deleteErr != nil {
			return sqlc.GitopsRegistrationSource{}, deleteErr
		}
		return existing, nil
	}
	_, err = executeGitOpsMutation(r, h,
		func(q GitOpsMutationTx) (sqlc.GitopsRegistrationSource, error) { return deleteSource(q) },
		func() (sqlc.GitopsRegistrationSource, error) { return deleteSource(h.queries) },
		func(existing sqlc.GitopsRegistrationSource) clusterAuditEvent {
			return clusterAuditEvent{
				action: "admin.gitops_source.deleted", resourceType: "gitops_source",
				resourceID: existing.ID.String(), resourceName: existing.Name,
				status: http.StatusNoContent, detail: gitOpsSourceAuditDetail(existing),
			}
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "GitOps source not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete gitops source")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Sync handles POST /api/v1/admin/gitops-sources/{id}/sync/.
func (h *GitOpsHandler) Sync(w http.ResponseWriter, r *http.Request) {
	if !h.gate(w, r) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid source ID")
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "GitOps transaction runner not configured")
		return
	}
	r = r.WithContext(withOperationIdempotency(r, "admin_gitops_source_sync"))
	digest, err := canonicalOperationRequestDigest(struct {
		Action   string `json:"action"`
		SourceID string `json:"source_id"`
	}{Action: "sync", SourceID: id.String()})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode GitOps sync request")
		return
	}
	var receipt GitOpsSyncReceipt
	err = h.runTx(r.Context(), func(q GitOpsMutationTx) error {
		idemQ, ok := q.(resourceOperationIdempotencyQuerier)
		if !ok {
			return errors.New("GitOps sync idempotency store is not configured")
		}
		_, stored, replay, claimErr := claimOperationReceipt[GitOpsSyncReceipt](r.Context(), idemQ, "gitops_source_syncs", digest)
		if claimErr != nil {
			return claimErr
		}
		if replay {
			receipt = stored
			return nil
		}
		result, enqueueErr := enqueueGitOpsSourceSync(r, q, q, id)
		if enqueueErr != nil {
			return enqueueErr
		}
		receipt = GitOpsSyncReceipt{SourceID: result.row.ID.String(), TaskID: result.task.ID.String(), Status: "queued"}
		if auditErr := recordAuditOutbox(r, q, "admin.gitops_source.sync_requested", "gitops_source", result.row.ID.String(), result.row.Name, http.StatusAccepted, map[string]any{
			"trigger": "manual", "task_id": result.task.ID.String(),
		}); auditErr != nil {
			return auditErr
		}
		return attachOperationReceipt(r.Context(), idemQ, "gitops_source_syncs", result.task.ID, digest, receipt)
	})
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different GitOps source sync")
			return
		}
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "GitOps source not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SyncError, "Failed to queue GitOps source sync")
		return
	}
	RespondAcceptedOperation(w, "/api/v1/admin/gitops-sources/"+receipt.SourceID+"/", receipt)
}

type GitOpsSyncReceipt struct {
	SourceID string `json:"source_id"`
	TaskID   string `json:"task_id"`
	Status   string `json:"status"`
}

// Webhook handles POST /api/v1/gitops/sources/{id}/webhook/. It lets a git
// provider (GitHub/GitLab push hook, or a CI job) durably queue an immediate
// source sync so merge-to-deploy no longer waits for the interval tick.
//
// Unlike the /admin/ routes this is NOT superuser-JWT gated — the caller is
// an external system, not a console user — so it authenticates on a shared
// secret presented in the X-Astronomer-Webhook-Secret header (or an
// Authorization: Bearer token), constant-time compared. When no secret is
// configured the endpoint is closed (503) rather than open.
func (h *GitOpsHandler) Webhook(w http.ResponseWriter, r *http.Request) {
	if h.webhookSecret == "" {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "GitOps webhook secret not configured")
		return
	}
	provided := r.Header.Get("X-Astronomer-Webhook-Secret")
	if provided == "" {
		provided = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	if subtle.ConstantTimeCompare([]byte(provided), []byte(h.webhookSecret)) != 1 {
		RespondRequestError(w, r, http.StatusUnauthorized, apierror.InvalidToken, "Invalid webhook secret")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid source ID")
		return
	}
	if h.runTx == nil && h.taskOutbox == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "GitOps task outbox not configured")
		return
	}
	result, err := executeGitOpsMutation(r, h,
		func(q GitOpsMutationTx) (gitOpsSyncMutationResult, error) {
			return enqueueGitOpsSourceSync(r, q, q, id)
		},
		func() (gitOpsSyncMutationResult, error) {
			return enqueueGitOpsSourceSync(r, h.queries, h.taskOutbox, id)
		},
		func(result gitOpsSyncMutationResult) clusterAuditEvent {
			return clusterAuditEvent{
				action: "admin.gitops_source.sync_requested", resourceType: "gitops_source",
				resourceID: result.row.ID.String(), resourceName: result.row.Name,
				status: http.StatusAccepted, detail: map[string]any{"trigger": "webhook", "task_id": result.task.ID.String()},
			}
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "GitOps source not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SyncError, "Failed to queue GitOps source sync")
		return
	}
	RespondJSON(w, http.StatusAccepted, map[string]any{"status": "queued", "task_id": result.task.ID.String()})
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
	// Memoize the cluster lookup per request so a source with the same
	// cluster registered under multiple repo paths resolves the name once
	// instead of a GetClusterByID per row (N+1). Mirrors the instance→cluster
	// cache in delivery status views and the clusterName cache in projects.
	out := make([]map[string]any, 0, len(rows))
	clusterCache := make(map[uuid.UUID]sqlc.Cluster, len(rows))
	clusterMissing := make(map[uuid.UUID]struct{})
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
		cluster, ok := clusterCache[link.ClusterID]
		if !ok {
			if _, missed := clusterMissing[link.ClusterID]; !missed {
				c, cerr := h.queries.GetClusterByID(r.Context(), link.ClusterID)
				if cerr == nil {
					cluster, ok = c, true
					clusterCache[link.ClusterID] = c
				} else {
					clusterMissing[link.ClusterID] = struct{}{}
				}
			}
		}
		if ok {
			entry["cluster_name"] = cluster.Name
			entry["display_name"] = cluster.DisplayName
		}
		out = append(out, entry)
	}
	page, pagination := pageWindow(r, out)
	RespondList(w, page, pagination)
}

// Helpers -------------------------------------------------------------

func enqueueGitOpsSourceSync(r *http.Request, q GitOpsQuerier, outbox tasks.TaskOutboxWriter, sourceID uuid.UUID) (gitOpsSyncMutationResult, error) {
	if q == nil || outbox == nil {
		return gitOpsSyncMutationResult{}, errors.New("GitOps task outbox is unavailable")
	}
	row, err := q.GetGitOpsSource(r.Context(), sourceID)
	if err != nil {
		return gitOpsSyncMutationResult{}, err
	}
	task, err := tasks.NewGitOpsSourceSyncTask(sourceID)
	if err != nil {
		return gitOpsSyncMutationResult{}, err
	}
	requestID := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if requestID == "" {
		requestID = middleware.GetRequestID(r.Context())
	}
	intent, err := tasks.EnqueueTaskOutbox(r.Context(), outbox, task, tasks.TaskOutboxOptions{
		DedupeKey: "gitops:sync:" + audit.MutationDedupeKey(requestID, "sync", "gitops_source", sourceID.String()),
		QueueName: "default", MaxRetry: 10, Timeout: 30 * time.Minute,
		Unique: 30 * time.Second,
	})
	if err != nil {
		return gitOpsSyncMutationResult{}, err
	}
	return gitOpsSyncMutationResult{row: row, task: intent}, nil
}

func gitOpsSourceAuditDetail(row sqlc.GitopsRegistrationSource) map[string]any {
	return map[string]any{
		"branch":                      row.Branch,
		"on_delete":                   row.OnDelete,
		"auth_mode":                   row.AuthMode,
		"auth_configured":             row.AuthEncrypted != "",
		"enabled":                     row.Enabled,
		"allow_mass_decommission":     row.AllowMassDecommission,
		"sync_interval_seconds":       row.SyncIntervalSeconds,
		"repository_location_omitted": true,
	}
}

func validateGitOpsRepositoryURL(raw string) (string, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return "", errors.New("repo_url is required")
	}
	if strings.HasPrefix(clean, "git@") {
		hostAndPath := strings.TrimPrefix(clean, "git@")
		if strings.ContainsAny(hostAndPath, "?#") || !strings.Contains(hostAndPath, ":") || strings.HasPrefix(hostAndPath, ":") {
			return "", errors.New("repo_url is invalid")
		}
		return clean, nil
	}
	parsed, err := url.Parse(clean)
	if err != nil || parsed.Host == "" {
		return "", errors.New("repo_url is invalid")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("repo_url must not contain query parameters or fragments")
	}
	switch parsed.Scheme {
	case "http", "https":
		if parsed.User != nil {
			return "", errors.New("repo_url must not contain credentials; use the auth field")
		}
	case "ssh":
		if parsed.User != nil {
			_, hasPassword := parsed.User.Password()
			if hasPassword || parsed.User.Username() != "git" {
				return "", errors.New("SSH repo_url may contain only the fixed git username; use the auth field for credentials")
			}
		}
	default:
		return "", errors.New("repo_url scheme must be http, https, or ssh")
	}
	return clean, nil
}

func validateGitOpsRequest(req *gitopsSourceRequest, requireFields bool) error {
	if requireFields {
		if req.Name == "" {
			return errors.New("name is required")
		}
		if req.RepoURL == "" {
			return errors.New("repo_url is required")
		}
	}
	if req.RepoURL != "" {
		cleanURL, err := validateGitOpsRepositoryURL(req.RepoURL)
		if err != nil {
			return err
		}
		req.RepoURL = cleanURL
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

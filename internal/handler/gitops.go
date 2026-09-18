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
	"errors"
	"log/slog"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
)

// GitOpsAuthSentinel is the placeholder returned in GET responses for
// the auth_encrypted column. PUT requests that echo this value back are
// interpreted as "keep existing auth blob".
const GitOpsAuthSentinel = "<encrypted>"

var validGitOpsAuthModes = map[string]bool{"none": true, "https_token": true, "ssh_key": true}
var validGitOpsSyncModes = map[string]bool{"manual": true, "interval": true}
var validGitOpsOnDelete = map[string]bool{"log": true, "tombstone": true, "decommission": true}
var validGitOpsWebhookProviders = map[string]bool{"": true, "github": true}

var (
	errGitOpsWebhookSecretRequired = errors.New("webhook_secret of at least 32 characters is required when webhook_provider is enabled")
	errGitOpsWebhookInvalid        = errors.New("invalid webhook signature")
	errGitOpsWebhookReplay         = errors.New("webhook delivery already processed")
)

const gitOpsWebhookBodyLimit = 1 << 20

// GitOpsQuerier is the database surface the handler needs.
type GitOpsQuerier interface {
	ListGitOpsSourcesPage(ctx context.Context, arg sqlc.ListGitOpsSourcesPageParams) ([]sqlc.ListGitOpsSourcesPageRow, error)
	CountGitOpsSources(ctx context.Context) (int64, error)
	GetGitOpsSource(ctx context.Context, id uuid.UUID) (sqlc.GitopsRegistrationSource, error)
	GetGitOpsSourceByName(ctx context.Context, name string) (sqlc.GitopsRegistrationSource, error)
	CreateGitOpsSource(ctx context.Context, arg sqlc.CreateGitOpsSourceParams) (sqlc.GitopsRegistrationSource, error)
	UpdateGitOpsSource(ctx context.Context, arg sqlc.UpdateGitOpsSourceParams) (sqlc.GitopsRegistrationSource, error)
	DeleteGitOpsSource(ctx context.Context, id uuid.UUID) error
	ListGitOpsRegisteredClustersBySourcePage(ctx context.Context, arg sqlc.ListGitOpsRegisteredClustersBySourcePageParams) ([]sqlc.ListGitOpsRegisteredClustersBySourcePageRow, error)
	CountGitOpsRegisteredClustersBySource(ctx context.Context, sourceID uuid.UUID) (int64, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	CreateGitOpsWebhookReceipt(ctx context.Context, arg sqlc.CreateGitOpsWebhookReceiptParams) (time.Time, error)
}

type GitOpsMutationTx interface {
	GitOpsQuerier
	audit.OutboxQuerier
	tasks.TaskOutboxWriter
}

type gitOpsRunTxFunc func(context.Context, func(GitOpsMutationTx) error) error

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
	queries    GitOpsQuerier
	runTx      gitOpsRunTxFunc
	taskOutbox tasks.TaskOutboxWriter
	runner     GitOpsSyncRunner
	log        *slog.Logger
	audit      AuthAuditWriter
	encryptor  *auth.Encryptor
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
	WebhookProvider       string `json:"webhook_provider"`
	WebhookConfigured     bool   `json:"webhook_configured"`
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
		WebhookProvider:       row.WebhookProvider,
		WebhookConfigured:     row.WebhookProvider != "" && row.WebhookSecretEncrypted != "",
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

// toGitOpsSourceListResponse maps the credential-free list projection. List
// requests never load either encrypted credential column from PostgreSQL.
func toGitOpsSourceListResponse(row sqlc.ListGitOpsSourcesPageRow) gitopsSourceResponse {
	resp := gitopsSourceResponse{
		ID:                    row.ID.String(),
		Name:                  row.Name,
		RepoURL:               row.RepoUrl,
		Branch:                row.Branch,
		PathPrefix:            row.PathPrefix,
		AuthMode:              row.AuthMode,
		AuthConfigured:        row.AuthConfigured,
		SyncMode:              row.SyncMode,
		SyncIntervalSeconds:   row.SyncIntervalSeconds,
		OnDelete:              row.OnDelete,
		LastSyncedSHA:         row.LastSyncedSha,
		LastError:             row.LastError,
		Enabled:               row.Enabled,
		AllowMassDecommission: row.AllowMassDecommission,
		WebhookProvider:       row.WebhookProvider,
		WebhookConfigured:     row.WebhookProvider != "" && row.WebhookConfigured,
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
	// WebhookProvider is nil on update to preserve the existing setting.
	// An explicit empty string disables inbound webhooks and clears the secret.
	WebhookProvider *string `json:"webhook_provider,omitempty"`
	// WebhookSecret is accepted only on create/rotation and is always Fernet
	// encrypted before the source mutation transaction opens.
	WebhookSecret string `json:"webhook_secret,omitempty"`
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

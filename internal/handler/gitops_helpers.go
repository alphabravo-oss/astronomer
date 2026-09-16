package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	gitopsdomain "github.com/alphabravocompany/astronomer-go/internal/gitops"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

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
		requestID = reqctx.RequestID(r.Context())
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
		"webhook_provider":            row.WebhookProvider,
		"webhook_configured":          row.WebhookProvider != "" && row.WebhookSecretEncrypted != "",
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
	if err := gitopsdomain.ValidatePathPrefix(req.PathPrefix); err != nil {
		return err
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
	if req.WebhookProvider != nil {
		provider := strings.TrimSpace(*req.WebhookProvider)
		*req.WebhookProvider = provider
		if !validGitOpsWebhookProviders[provider] {
			return errors.New("webhook_provider must be empty or 'github'")
		}
		if provider == "" && req.WebhookSecret != "" {
			return errors.New("webhook_secret requires webhook_provider")
		}
		if requireFields && provider != "" && len(req.WebhookSecret) < 32 {
			return errGitOpsWebhookSecretRequired
		}
	} else if requireFields && req.WebhookSecret != "" {
		return errors.New("webhook_secret requires webhook_provider")
	}
	if req.WebhookSecret != "" && len(req.WebhookSecret) < 32 {
		return errGitOpsWebhookSecretRequired
	}
	if req.SyncIntervalSeconds < 0 {
		return errors.New("sync_interval_seconds must be >= 0")
	}
	return nil
}

func (h *GitOpsHandler) encryptWebhookSecret(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if len(plaintext) < 32 {
		return "", errGitOpsWebhookSecretRequired
	}
	if h == nil || h.encryptor == nil {
		return "", errors.New("GitOps webhook encryption is not configured")
	}
	ciphertext, err := h.encryptor.Encrypt(plaintext)
	if err != nil {
		return "", fmt.Errorf("encrypt GitOps webhook secret: %w", err)
	}
	return ciphertext, nil
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

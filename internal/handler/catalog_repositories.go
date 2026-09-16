package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"sigs.k8s.io/yaml"
)

// --- Helm Repositories ---

// ListRepos handles GET /api/v1/catalog/repositories/.
//
// Default behavior (admin view, no query params): excludes project-owned
// (private) catalogs — operators expect /admin/ to show only the
// operator-curated global set. Migration 061 added two new query params:
//
//   - ?include_project_owned=true → admin sees every helm_repositories row
//     including private ones (used by the superuser "all catalogs"
//     screen).
//   - ?project_id=<uuid> → switches to project-scoped browse (globals +
//     own + subscribed for that project).
//
// The two params are mutually exclusive: project_id always wins. If
// neither is set, the legacy "global list" behaviour is preserved
// verbatim — no semantic change for existing callers.
func (h *CatalogHandler) ListRepos(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryOffset(r))

	if pidRaw := r.URL.Query().Get("project_id"); pidRaw != "" {
		pid, err := uuid.Parse(pidRaw)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project_id query param")
			return
		}
		rows, err := h.queries.ListCatalogsForProject(r.Context(), pid)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list project catalogs")
			return
		}
		// ListCatalogsForProject returns the full authorized set.
		paging.Write(w, helmRepositoriesToResponse(h.redactHelmRepositories(rows), h.chartCountsFor(r.Context(), rows)), paging.Exact(len(rows), len(rows), 0, len(rows)))
		return
	}

	if r.URL.Query().Get("include_project_owned") == "true" {
		rows, err := h.queries.ListAdminCatalogsIncludingProjectOwned(r.Context(), sqlc.ListAdminCatalogsIncludingProjectOwnedParams{
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list catalogs")
			return
		}
		total, err := h.queries.CountHelmRepositories(r.Context())
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count repositories")
			return
		}
		paging.Write(w, helmRepositoriesToResponse(h.redactHelmRepositories(rows), h.chartCountsFor(r.Context(), rows)), paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(rows)))
		return
	}

	// Admin default view hides project-owned (private) catalogs. Filter
	// and paginate on owner_project_id IS NULL at the DB layer: the old
	// over-fetch-plus-in-Go-filter path emitted empty trailing pages once
	// private rows were removed and silently dropped globals that fell
	// past the fixed over-fetch slack window.
	repos, err := h.queries.ListGlobalHelmRepositories(r.Context(), sqlc.ListGlobalHelmRepositoriesParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list repositories")
		return
	}

	total, err := h.queries.CountGlobalHelmRepositories(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count repositories")
		return
	}

	paging.Write(w, helmRepositoriesToResponse(h.redactHelmRepositories(repos), h.chartCountsFor(r.Context(), repos)), paging.Exact(total, queryLimit(r, 20), queryOffset(r), len(repos)))
}

// CreateRepoRequest represents the request body for creating a helm repository.
// openapi:request-operation postCatalogRepositories
// openapi:request-allow username decoded solely to reject misplaced top-level credentials with a clear 400 response
// openapi:request-allow password decoded solely to reject misplaced top-level credentials with a clear 400 response
// openapi:request-allow token decoded solely to reject misplaced top-level credentials with a clear 400 response
type CreateRepoRequest struct {
	Name        string          `json:"name" validate:"required"`
	URL         string          `json:"url" validate:"required"`
	RepoType    string          `json:"repo_type"`
	Description string          `json:"description"`
	IsDefault   bool            `json:"is_default"`
	AuthType    string          `json:"auth_type"`
	AuthConfig  json.RawMessage `json:"auth_config"`
	// Enabled defaults to TRUE when the key is absent. It used to be a bare
	// bool, so a body that omitted it created a DISABLED repository: the
	// scheduled sweep reads ListEnabledHelmRepositories, so the repo was
	// never synced and never showed a chart. Nobody asks for a repository
	// they do not want synced. Mirrors CreateProjectCatalogRequest.Enabled.
	Enabled *bool `json:"enabled,omitempty"`

	// misplacedCredentialFields are decoded ONLY so they can be rejected.
	//
	// Credentials belong in auth_config. The UI posted them here — flat
	// `username`/`password` — for as long as this endpoint existed, and
	// encoding/json discards unknown fields silently, so the repository was
	// created with no credential at all and the first sign of trouble was a
	// 401 from the registry that reads as a wrong password rather than a
	// dropped one. Answering 400 costs one branch and makes that class of
	// mistake impossible to make quietly, for this UI or any other client.
	misplacedCredentialFields
}

// misplacedCredentialFields captures the top-level credential keys this API
// has never accepted, so create and update can both refuse them by name
// instead of ignoring them.
type misplacedCredentialFields struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token"`
}

// rejectMisplacedCredentials answers 400 when credentials arrive at the top
// level of the body instead of inside auth_config. Reports whether the request
// may continue.
func rejectMisplacedCredentials(w http.ResponseWriter, r *http.Request, f misplacedCredentialFields) bool {
	if f.Username == "" && f.Password == "" && f.Token == "" {
		return true
	}
	RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError,
		`Credentials belong in auth_config, not at the top level of the request: `+
			`{"auth_type":"basic","auth_config":{"username":"...","password":"..."}}`)
	return false
}

// CreateRepo handles POST /api/v1/catalog/repositories/.
func (h *CatalogHandler) CreateRepo(w http.ResponseWriter, r *http.Request) {
	var req CreateRepoRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if !rejectMisplacedCredentials(w, r, req.misplacedCredentialFields) {
		return
	}

	params, err := catalog.NewRepositoryService().PrepareCreate(catalog.RepositoryCreateInput{
		Name: req.Name, URL: req.URL, RepoType: req.RepoType, Description: req.Description,
		IsDefault: req.IsDefault, AuthType: req.AuthType, AuthConfig: req.AuthConfig, Enabled: req.Enabled,
	}, h.sealer())
	if err != nil {
		switch {
		case errors.Is(err, catalog.ErrRepositoryCredentialEncryptionUnavailable):
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.CryptoError, "Repository credential encryption is unavailable")
		case catalog.IsValidationError(err):
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		default:
			h.log.Error("prepare chart repository credential", "repository", req.Name, "error", err)
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to secure repository credentials")
		}
		return
	}
	repo, err := executeMutation(r, h.runTx,
		func(q CatalogMutationTx) (sqlc.HelmRepository, error) {
			return q.CreateHelmRepository(r.Context(), params)
		},
		func(row sqlc.HelmRepository) mutationAuditEvent {
			return mutationAuditEvent{action: "catalog.repo.create", resourceType: "helm_repository", resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusCreated, detail: map[string]any{
				"repo_type": row.RepoType, "auth_type": row.AuthType,
			}}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create repository")
		return
	}

	w.Header().Set("Location", "/api/v1/catalog/repositories/"+repo.ID.String()+"/")
	// A repository has ingested nothing at the instant it is created, so the
	// count is 0 by construction rather than by query.
	RespondJSON(w, http.StatusCreated, helmRepositoryToResponse(h.redactHelmRepository(repo), 0))
}

// GetRepo handles GET /api/v1/catalog/repositories/{id}/.
func (h *CatalogHandler) GetRepo(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid repository ID")
		return
	}

	repo, err := h.queries.GetHelmRepositoryByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Repository not found")
		return
	}

	// SEC-01: never return live registry passwords/tokens on GET.
	RespondJSON(w, http.StatusOK, helmRepositoryToResponse(h.redactHelmRepository(repo),
		h.chartCountsFor(r.Context(), []sqlc.HelmRepository{repo})[repo.ID]))
}

// UpdateRepoRequest represents the request body for updating a helm repository.
//
// Every field is a pointer and every field means the same thing: ABSENT
// LEAVES THE STORED VALUE ALONE. UpdateHelmRepository writes all nine columns
// on every call, so with plain scalars a body that mentioned only `name`
// blanked the URL, disabled the repository, and — because auth_config was
// normalised from nil to `{}` before the merge — erased the credential.
// The credential case is the dangerous one: the operator renames a repo and
// discovers days later that the nightly sync has been 401ing ever since.
// openapi:request-operation putCatalogRepositoriesById
// openapi:request-allow username decoded solely to reject misplaced top-level credentials with a clear 400 response
// openapi:request-allow password decoded solely to reject misplaced top-level credentials with a clear 400 response
// openapi:request-allow token decoded solely to reject misplaced top-level credentials with a clear 400 response
type UpdateRepoRequest struct {
	Name        *string          `json:"name"`
	URL         *string          `json:"url"`
	RepoType    *string          `json:"repo_type"`
	Description *string          `json:"description"`
	IsDefault   *bool            `json:"is_default"`
	AuthType    *string          `json:"auth_type"`
	AuthConfig  *json.RawMessage `json:"auth_config"`
	Enabled     *bool            `json:"enabled"`

	// Rejected, not ignored — see CreateRepoRequest.
	misplacedCredentialFields
}

// UpdateRepo handles PUT /api/v1/catalog/repositories/{id}/.
func (h *CatalogHandler) UpdateRepo(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid repository ID")
		return
	}

	var req UpdateRepoRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if !rejectMisplacedCredentials(w, r, req.misplacedCredentialFields) {
		return
	}

	// The stored row is now required, not best-effort: it is the base every
	// omitted field falls back to.
	existing, err := h.queries.GetHelmRepositoryByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Repository not found")
		return
	}

	params, prepareErr := catalog.NewRepositoryService().PrepareUpdate(existing, catalog.RepositoryUpdateInput{
		Name: req.Name, URL: req.URL, RepoType: req.RepoType, Description: req.Description,
		IsDefault: req.IsDefault, AuthType: req.AuthType, AuthConfig: req.AuthConfig,
		Enabled: req.Enabled, RedactedSecretValue: SecretSentinel,
	}, h.sealer(), h.decryptor())
	if prepareErr != nil {
		switch {
		case errors.Is(prepareErr, catalog.ErrRepositoryCredentialEncryptionUnavailable):
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.CryptoError, "Repository credential encryption is unavailable")
		case catalog.IsValidationError(prepareErr):
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, prepareErr.Error())
		case errors.Is(prepareErr, catalog.ErrAuthConfigUnavailable):
			h.log.Error("decrypt chart repository credential for update", "repository", existing.Name, "error", prepareErr)
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError,
				"Failed to read existing repository credentials; check the platform encryption key")
		default:
			h.log.Error("prepare chart repository credential", "repository", existing.Name, "error", prepareErr)
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to secure repository credentials")
		}
		return
	}
	repo, err := executeMutation(r, h.runTx,
		func(q CatalogMutationTx) (sqlc.HelmRepository, error) {
			return q.UpdateHelmRepository(r.Context(), params)
		},
		func(row sqlc.HelmRepository) mutationAuditEvent {
			return mutationAuditEvent{action: "catalog.repo.update", resourceType: "helm_repository", resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusOK, detail: map[string]any{
				"enabled": row.Enabled, "auth_type": row.AuthType,
			}}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.UpdateError, "Failed to update repository")
		return
	}

	RespondJSON(w, http.StatusOK, helmRepositoryToResponse(h.redactHelmRepository(repo),
		h.chartCountsFor(r.Context(), []sqlc.HelmRepository{repo})[repo.ID]))
}

// DeleteRepo handles DELETE /api/v1/catalog/repositories/{id}/.
func (h *CatalogHandler) DeleteRepo(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid repository ID")
		return
	}

	repoName := ""
	if existing, lookupErr := h.queries.GetHelmRepositoryByID(r.Context(), id); lookupErr == nil {
		repoName = existing.Name
	}
	_, err = executeMutation(r, h.runTx,
		func(q CatalogMutationTx) (uuid.UUID, error) { return id, q.DeleteHelmRepository(r.Context(), id) },
		func(rowID uuid.UUID) mutationAuditEvent {
			return mutationAuditEvent{action: "catalog.repo.delete", resourceType: "helm_repository", resourceID: rowID.String(), resourceName: repoName, status: http.StatusNoContent}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete repository")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// SyncRepo handles POST /api/v1/catalog/repositories/{id}/sync/.
//
// Fetches the repository's index.yaml, parses the standard Helm schema, and
// upserts HelmChart + HelmChartVersion rows. The previous implementation only
// stamped last_synced_at, which left the chart catalog empty on a fresh
// install. Errors from the network or DB bubble up as a 502 — last_synced_at
// is only stamped on successful ingest.
func (h *CatalogHandler) SyncRepo(w http.ResponseWriter, r *http.Request) {
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid repository ID")
		return
	}
	repo, err := h.queries.GetHelmRepositoryByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Repository not found")
		return
	}
	r = r.WithContext(withOperationIdempotency(r, "catalog_repository_sync"))
	digest, err := canonicalOperationRequestDigest(struct {
		Action       string `json:"action"`
		RepositoryID string `json:"repository_id"`
	}{Action: "sync", RepositoryID: id.String()})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EncodeError, "Failed to encode repository sync request")
		return
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "Catalog sync transaction runner is not configured")
		return
	}
	task, taskErr := tasks.NewCatalogSyncTask(tasks.CatalogSyncPayload{RepositoryID: repo.ID.String()})
	if taskErr != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.EnqueueError, "Failed to build repository sync request")
		return
	}
	requestKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	dedupeKey := "catalog-sync:" + audit.MutationDedupeKey(requestKey, "catalog.repo.sync_requested", "helm_repository", repo.ID.String())
	var outbox sqlc.TaskOutbox
	var receipt CatalogRepositorySyncReceipt
	err = h.runTx(r.Context(), func(q CatalogMutationTx) error {
		idemQ, ok := q.(resourceOperationIdempotencyQuerier)
		if !ok {
			return errors.New("catalog sync idempotency store is not configured")
		}
		_, stored, replay, claimErr := claimOperationReceipt[CatalogRepositorySyncReceipt](r.Context(), idemQ, "catalog_repository_syncs", digest)
		if claimErr != nil {
			return claimErr
		}
		if replay {
			receipt = stored
			return nil
		}
		var mutationErr error
		outbox, mutationErr = tasks.EnqueueTaskOutbox(r.Context(), q, task, tasks.TaskOutboxOptions{
			DedupeKey: dedupeKey, QueueName: "default", MaxRetry: 25,
			Timeout: 30 * time.Minute, Unique: 10 * time.Minute, MaxDeliveryAttempts: 20,
		})
		if mutationErr != nil {
			return mutationErr
		}
		receipt = CatalogRepositorySyncReceipt{RepositoryID: repo.ID.String(), TaskID: outbox.ID.String(), Status: outbox.Status}
		if auditErr := recordAuditOutbox(r, q, "catalog.repo.sync_requested", "helm_repository", repo.ID.String(), repo.Name, http.StatusAccepted, map[string]any{
			"repo_type": repo.RepoType, "task_outbox_id": outbox.ID.String(),
		}); auditErr != nil {
			return auditErr
		}
		return attachOperationReceipt(r.Context(), idemQ, "catalog_repository_syncs", outbox.ID, digest, receipt)
	})
	if err != nil {
		if errors.Is(err, errOperationIdempotencyConflict) {
			RespondRequestError(w, r, http.StatusConflict, apierror.Conflict, "Idempotency-Key already identifies a different catalog repository sync")
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.EnqueueError, "Failed to enqueue repository sync")
		return
	}
	RespondAcceptedOperation(w, "/api/v1/catalog/repositories/"+receipt.RepositoryID+"/", receipt)
}

type CatalogRepositorySyncReceipt struct {
	RepositoryID string `json:"repository_id"`
	TaskID       string `json:"task_id"`
	Status       string `json:"status"`
}

// helmIndexFile mirrors the relevant fields of a Helm repo index.yaml. We use
// our own minimal struct rather than helm.sh/helm/v3/pkg/repo to keep this
// handler decoupled from the worker package.
type helmIndexFile struct {
	APIVersion string                         `json:"apiVersion"`
	Entries    map[string][]helmIndexChartVer `json:"entries"`
}

type helmIndexChartVer struct {
	Name        string                `json:"name"`
	Version     string                `json:"version"`
	AppVersion  string                `json:"appVersion"`
	Description string                `json:"description"`
	Icon        string                `json:"icon"`
	Home        string                `json:"home"`
	Digest      string                `json:"digest"`
	URLs        []string              `json:"urls"`
	Keywords    []string              `json:"keywords"`
	Maintainers []helmIndexChartMaint `json:"maintainers"`
	Created     time.Time             `json:"created"`
}

type helmIndexChartMaint struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	URL   string `json:"url"`
}

// chartVersionIngestRow is one element of the JSON payload handed to
// BulkCreateHelmChartVersions (parsed server-side via jsonb_to_recordset).
// The JSON keys must match the recordset column names exactly. A nil
// CreatedAtUpstream serialises to JSON null → SQL NULL, preserving the
// previous per-row behaviour for charts without an upstream publish date.
type chartVersionIngestRow struct {
	Version           string          `json:"version"`
	AppVersion        string          `json:"app_version"`
	Digest            string          `json:"digest"`
	URLs              json.RawMessage `json:"urls"`
	CreatedAtUpstream *time.Time      `json:"created_at_upstream"`
}

func (h *CatalogHandler) fetchAndIngestRepoIndex(ctx context.Context, repo sqlc.HelmRepository) (chartCount, versionCount int, err error) {
	indexURL := strings.TrimRight(repo.Url, "/") + "/index.yaml"
	// SEC-02: same SSRF posture as the worker catalog_sync path.
	if err := httpclient.GuardPublicHost(indexURL); err != nil {
		return 0, 0, fmt.Errorf("catalog repository host is not a permitted public address")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("build index request: %w", err)
	}
	h.applyRepoIndexAuth(req, repo)
	client := httpclient.SafeClientWithLimit(30*time.Second, catalog.MaxIndexBytes)
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("fetch index: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= http.StatusBadRequest {
		return 0, 0, fmt.Errorf("repository returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, catalog.MaxIndexBytes+1))
	if err != nil {
		return 0, 0, fmt.Errorf("read index body: %w", err)
	}
	if int64(len(body)) > catalog.MaxIndexBytes {
		return 0, 0, fmt.Errorf("repository index exceeds %d bytes", catalog.MaxIndexBytes)
	}
	var index helmIndexFile
	if err := yaml.Unmarshal(body, &index); err != nil {
		return 0, 0, fmt.Errorf("parse index yaml: %w", err)
	}
	for chartName, versions := range index.Entries {
		if chartName == "" || len(versions) == 0 {
			continue
		}
		// Apply the SAME last-N cap the scheduled sweep applies, on the same
		// ordering. index.yaml entry order is conventionally newest-first but
		// nothing guarantees it, so sort before truncating (the worker gets
		// this from helm's IndexFile.SortEntries).
		//
		// Without this the two ingests disagreed destructively: Sync inserted
		// every version in the index and the next sweep's GC deleted
		// everything outside its own top-3. No chart archive is downloaded
		// here, so capping costs nothing — it is purely a slice truncation.
		slices.SortStableFunc(versions, func(a, b helmIndexChartVer) int {
			return catalog.CompareVersionsDesc(a.Version, b.Version)
		})
		if len(versions) > catalog.MaxIndexVersionsPerChart {
			versions = versions[:catalog.MaxIndexVersionsPerChart]
		}
		// Pick the first non-empty descriptive fields across all versions —
		// some repos only set icon/home on the latest version.
		first := versions[0]
		description, icon, home := first.Description, first.Icon, first.Home
		var keywords []string
		var maintainers []helmIndexChartMaint
		for _, v := range versions {
			if description == "" && v.Description != "" {
				description = v.Description
			}
			if icon == "" && v.Icon != "" {
				icon = v.Icon
			}
			if home == "" && v.Home != "" {
				home = v.Home
			}
			if len(keywords) == 0 && len(v.Keywords) > 0 {
				keywords = v.Keywords
			}
			if len(maintainers) == 0 && len(v.Maintainers) > 0 {
				maintainers = v.Maintainers
			}
		}
		chart, err := h.queries.GetHelmChartByRepoAndName(ctx, sqlc.GetHelmChartByRepoAndNameParams{
			RepositoryID: repo.ID,
			Name:         chartName,
		})
		if err != nil {
			keywordsJSON, _ := json.Marshal(keywords)
			if len(keywordsJSON) == 0 {
				keywordsJSON = []byte(`[]`)
			}
			maintList := make([]map[string]string, 0, len(maintainers))
			for _, m := range maintainers {
				maintList = append(maintList, map[string]string{"name": m.Name, "email": m.Email, "url": m.URL})
			}
			maintainersJSON, _ := json.Marshal(maintList)
			if len(maintainersJSON) == 0 {
				maintainersJSON = []byte(`[]`)
			}
			chart, err = h.queries.CreateHelmChart(ctx, sqlc.CreateHelmChartParams{
				RepositoryID: repo.ID,
				Name:         chartName,
				DisplayName:  chartName,
				Description:  description,
				IconUrl:      icon,
				HomeUrl:      home,
				Category:     "",
				Keywords:     keywordsJSON,
				Maintainers:  maintainersJSON,
				Deprecated:   false,
			})
			if err != nil {
				return chartCount, versionCount, fmt.Errorf("create chart %s: %w", chartName, err)
			}
		}
		chartCount++

		// Bulk-load the versions already known for this chart in one query
		// instead of a SELECT probe per version, then multi-row INSERT the
		// new ones (ON CONFLICT DO NOTHING) in a single round trip. This
		// turns tens of thousands of serial round-trips on a large repo
		// into two queries per chart.
		existingVersions, err := h.queries.ListChartVersionStrings(ctx, chart.ID)
		if err != nil {
			return chartCount, versionCount, fmt.Errorf("load existing versions for %s: %w", chartName, err)
		}
		known := make(map[string]struct{}, len(existingVersions))
		for _, ver := range existingVersions {
			known[ver] = struct{}{}
		}

		rows := make([]chartVersionIngestRow, 0, len(versions))
		for _, v := range versions {
			if v.Version == "" {
				continue
			}
			// Skip versions we already have and de-dup repeats within the
			// index entry so the multi-row insert never carries the same
			// (chart_id, version) pair twice.
			if _, ok := known[v.Version]; ok {
				continue
			}
			known[v.Version] = struct{}{}
			urlsJSON, _ := json.Marshal(v.URLs)
			if len(urlsJSON) == 0 {
				urlsJSON = []byte(`[]`)
			}
			row := chartVersionIngestRow{
				Version:    v.Version,
				AppVersion: v.AppVersion,
				Digest:     v.Digest,
				URLs:       json.RawMessage(urlsJSON),
			}
			if !v.Created.IsZero() {
				created := v.Created
				row.CreatedAtUpstream = &created
			}
			rows = append(rows, row)
		}
		if len(rows) == 0 {
			continue
		}
		rowsJSON, err := json.Marshal(rows)
		if err != nil {
			return chartCount, versionCount, fmt.Errorf("marshal chart versions for %s: %w", chartName, err)
		}
		inserted, err := h.queries.BulkCreateHelmChartVersions(ctx, sqlc.BulkCreateHelmChartVersionsParams{
			ChartID: chart.ID,
			Rows:    rowsJSON,
		})
		if err != nil {
			return chartCount, versionCount, fmt.Errorf("create chart versions for %s: %w", chartName, err)
		}
		versionCount += len(inserted)
	}
	return chartCount, versionCount, nil
}

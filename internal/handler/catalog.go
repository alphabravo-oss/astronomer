package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"github.com/alphabravocompany/astronomer-go/internal/maintenance"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	avault "github.com/alphabravocompany/astronomer-go/internal/vault"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// CatalogQuerier abstracts the catalog-related database queries needed by CatalogHandler.
type CatalogQuerier interface {
	// Cluster lookup — used by the migration-057 maintenance gate to
	// resolve the target cluster's labels for selector matching.
	GetClusterByID(ctx context.Context, id uuid.UUID) (sqlc.Cluster, error)
	// Repositories
	GetHelmRepositoryByID(ctx context.Context, id uuid.UUID) (sqlc.HelmRepository, error)
	ListHelmRepositories(ctx context.Context, arg sqlc.ListHelmRepositoriesParams) ([]sqlc.HelmRepository, error)
	// ListGlobalHelmRepositories + CountGlobalHelmRepositories back the
	// admin default view (owner_project_id IS NULL) with DB-layer filter +
	// pagination.
	ListGlobalHelmRepositories(ctx context.Context, arg sqlc.ListGlobalHelmRepositoriesParams) ([]sqlc.HelmRepository, error)
	CountGlobalHelmRepositories(ctx context.Context) (int64, error)
	CreateHelmRepository(ctx context.Context, arg sqlc.CreateHelmRepositoryParams) (sqlc.HelmRepository, error)
	UpdateHelmRepository(ctx context.Context, arg sqlc.UpdateHelmRepositoryParams) (sqlc.HelmRepository, error)
	DeleteHelmRepository(ctx context.Context, id uuid.UUID) error
	CountHelmRepositories(ctx context.Context) (int64, error)
	// Charts
	ListHelmCharts(ctx context.Context, arg sqlc.ListHelmChartsParams) ([]sqlc.HelmChart, error)
	// ListHelmChartsByTag + CountHelmChartsByTag drive the ?tag= filter
	// (migration 071). The handler falls back to the unfiltered list
	// when these aren't wired so the CatalogQuerier surface stays
	// optional for older test harnesses.
	ListHelmChartsByTag(ctx context.Context, arg sqlc.ListHelmChartsByTagParams) ([]sqlc.HelmChart, error)
	CountHelmChartsByTag(ctx context.Context, tag string) (int64, error)
	ListChartVersions(ctx context.Context, arg sqlc.ListChartVersionsParams) ([]sqlc.HelmChartVersion, error)
	ListChartsByRepository(ctx context.Context, arg sqlc.ListChartsByRepositoryParams) ([]sqlc.HelmChart, error)
	// Project-scoped catalog browse across a set of repo ids in a single
	// query with real LIMIT/OFFSET + COUNT (replaces the per-catalog fan-out).
	ListChartsByRepositoryIDs(ctx context.Context, arg sqlc.ListChartsByRepositoryIDsParams) ([]sqlc.HelmChart, error)
	CountChartsByRepositoryIDs(ctx context.Context, repositoryIds []uuid.UUID) (int64, error)
	// CountChartsPerRepository backs the chart_count field on the catalog
	// Repositories table: one grouped aggregate for the whole page.
	CountChartsPerRepository(ctx context.Context, repositoryIds []uuid.UUID) ([]sqlc.CountChartsPerRepositoryRow, error)
	GetHelmChartByID(ctx context.Context, id uuid.UUID) (sqlc.HelmChart, error)
	GetHelmChartByRepoAndName(ctx context.Context, arg sqlc.GetHelmChartByRepoAndNameParams) (sqlc.HelmChart, error)
	CreateHelmChart(ctx context.Context, arg sqlc.CreateHelmChartParams) (sqlc.HelmChart, error)
	CountHelmCharts(ctx context.Context) (int64, error)
	// Chart Versions
	GetHelmChartVersionByID(ctx context.Context, id uuid.UUID) (sqlc.HelmChartVersion, error)
	GetLatestChartVersion(ctx context.Context, chartID uuid.UUID) (sqlc.HelmChartVersion, error)
	GetHelmChartVersion(ctx context.Context, arg sqlc.GetHelmChartVersionParams) (sqlc.HelmChartVersion, error)
	CreateHelmChartVersion(ctx context.Context, arg sqlc.CreateHelmChartVersionParams) (sqlc.HelmChartVersion, error)
	// Repo-index ingest fast path: bulk-load known versions once, then
	// multi-row insert new ones (ON CONFLICT DO NOTHING) per chart.
	ListChartVersionStrings(ctx context.Context, chartID uuid.UUID) ([]string, error)
	BulkCreateHelmChartVersions(ctx context.Context, arg sqlc.BulkCreateHelmChartVersionsParams) ([]string, error)
	// Sprint 082 — lazy hydration writeback for default_values + readme.
	UpdateHelmChartVersionContent(ctx context.Context, arg sqlc.UpdateHelmChartVersionContentParams) error
	// Sprint 082 — joined Apps tab listing (chart name/icon/version + repo).
	ListInstalledChartsWithMetadataByCluster(ctx context.Context, arg sqlc.ListInstalledChartsWithMetadataByClusterParams) ([]sqlc.InstalledChartWithMetadata, error)
	// Installed Charts
	ListInstalledCharts(ctx context.Context, arg sqlc.ListInstalledChartsParams) ([]sqlc.InstalledChart, error)
	ListInstalledChartsByCluster(ctx context.Context, arg sqlc.ListInstalledChartsByClusterParams) ([]sqlc.InstalledChart, error)
	GetInstalledChartByID(ctx context.Context, id uuid.UUID) (sqlc.InstalledChart, error)
	CreateInstalledChart(ctx context.Context, arg sqlc.CreateInstalledChartParams) (sqlc.InstalledChart, error)
	UpdateHelmRepositoryLastSynced(ctx context.Context, id uuid.UUID) error
	UpdateInstalledChartStatus(ctx context.Context, arg sqlc.UpdateInstalledChartStatusParams) error
	UpdateInstalledChartValues(ctx context.Context, arg sqlc.UpdateInstalledChartValuesParams) (sqlc.InstalledChart, error)
	DeleteInstalledChart(ctx context.Context, id uuid.UUID) error
	// Rancher-style bulk-delete of stuck releases; used by the Apps tab.
	DeleteFailedInstallationsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	CountInstalledCharts(ctx context.Context) (int64, error)
	CountInstalledChartsByCluster(ctx context.Context, clusterID uuid.UUID) (int64, error)
	CreateCatalogOperation(ctx context.Context, arg sqlc.CreateCatalogOperationParams) (sqlc.CatalogOperation, error)
	GetCatalogOperation(ctx context.Context, id uuid.UUID) (sqlc.CatalogOperation, error)
	ListCatalogOperations(ctx context.Context, arg sqlc.ListCatalogOperationsParams) ([]sqlc.CatalogOperation, error)
	ListPendingCatalogOperations(ctx context.Context, limit int32) ([]sqlc.CatalogOperation, error)
	MarkCatalogOperationRunning(ctx context.Context, id uuid.UUID) (sqlc.CatalogOperation, error)
	MarkCatalogOperationCompleted(ctx context.Context, id uuid.UUID) (sqlc.CatalogOperation, error)
	MarkCatalogOperationFailed(ctx context.Context, arg sqlc.MarkCatalogOperationFailedParams) (sqlc.CatalogOperation, error)
	MarkCatalogOperationSuperseded(ctx context.Context, arg sqlc.MarkCatalogOperationSupersededParams) (sqlc.CatalogOperation, error)
	RequeueCatalogOperation(ctx context.Context, id uuid.UUID) (sqlc.CatalogOperation, error)
	CreateCatalogOperationEvent(ctx context.Context, arg sqlc.CreateCatalogOperationEventParams) (sqlc.CatalogOperationEvent, error)
	ListCatalogOperationEvents(ctx context.Context, operationID uuid.UUID) ([]sqlc.CatalogOperationEvent, error)
	// Migration 061 — project-scoped catalog browse + admin all-rows view.
	// Optional on the interface: callers that don't use the project_id
	// query param never reach these methods, so tests that omit them
	// still satisfy the interface as long as they embed *sqlc.Queries.
	ListCatalogsForProject(ctx context.Context, projectID uuid.UUID) ([]sqlc.HelmRepositoryWithOwner, error)
	ListAdminCatalogsIncludingProjectOwned(ctx context.Context, arg sqlc.ListAdminCatalogsIncludingProjectOwnedParams) ([]sqlc.HelmRepositoryWithOwner, error)
	GetHelmRepositoryWithOwner(ctx context.Context, id uuid.UUID) (sqlc.HelmRepositoryWithOwner, error)
	GetCatalogVisibilityForProject(ctx context.Context, projectID, catalogID uuid.UUID) (sqlc.CatalogVisibility, error)
	GetProjectByID(ctx context.Context, id uuid.UUID) (sqlc.Project, error)
}

type installedChartScopedPager interface {
	ListInstalledChartsForScopes(ctx context.Context, arg sqlc.ListInstalledChartsForScopesParams) ([]sqlc.InstalledChart, error)
	CountInstalledChartsForScopes(ctx context.Context, clusterIDs []uuid.UUID) (int64, error)
}

type catalogOperationPager interface {
	CountCatalogOperations(ctx context.Context, arg sqlc.CountCatalogOperationsParams) (int64, error)
	ListCatalogOperationsForScopes(ctx context.Context, arg sqlc.ListCatalogOperationsForScopesParams) ([]sqlc.CatalogOperation, error)
	CountCatalogOperationsForScopes(ctx context.Context, arg sqlc.CountCatalogOperationsForScopesParams) (int64, error)
}

// CatalogMutationTx is the transaction-bound write surface for repository and
// installed-chart lifecycle changes. Production supplies sqlc.New(tx), so the
// domain row, durable operation intent, and mandatory audit intent share one
// commit decision.
type CatalogMutationTx interface {
	audit.OutboxQuerier
	tasks.TaskOutboxWriter
	CreateHelmRepository(context.Context, sqlc.CreateHelmRepositoryParams) (sqlc.HelmRepository, error)
	UpdateHelmRepository(context.Context, sqlc.UpdateHelmRepositoryParams) (sqlc.HelmRepository, error)
	DeleteHelmRepository(context.Context, uuid.UUID) error
	CreateInstalledChart(context.Context, sqlc.CreateInstalledChartParams) (sqlc.InstalledChart, error)
	UpdateInstalledChartStatus(context.Context, sqlc.UpdateInstalledChartStatusParams) error
	UpdateInstalledChartValues(context.Context, sqlc.UpdateInstalledChartValuesParams) (sqlc.InstalledChart, error)
	CreateCatalogOperation(context.Context, sqlc.CreateCatalogOperationParams) (sqlc.CatalogOperation, error)
	CreateCatalogOperationIdempotent(context.Context, sqlc.CreateCatalogOperationIdempotentParams) (sqlc.CatalogOperation, error)
	CreateCatalogOperationIdempotentWithDisposition(context.Context, sqlc.CreateCatalogOperationIdempotentWithDispositionParams) (sqlc.CreateCatalogOperationIdempotentWithDispositionRow, error)
	RequeueCatalogOperation(context.Context, uuid.UUID) (sqlc.CatalogOperation, error)
}

type catalogRunTxFunc func(context.Context, func(CatalogMutationTx) error) error

type catalogMutationResult[T any] struct {
	row T
	op  sqlc.CatalogOperation
}

// CatalogHandler handles catalog endpoints (helm repositories, charts, installations).
type CatalogHandler struct {
	queries CatalogQuerier
	helm    HelmRequester
	log     *slog.Logger
	authz   authorizationSupport
	mu      sync.Mutex
	trigger chan struct{}
	// helmConcurrency caps the number of executeOperation goroutines
	// dispatched per reconciler tick. Zero falls back to the package
	// default (see effectiveHelmConcurrency).
	helmConcurrency int
	// maintenanceGate is the migration-057 hook on helm.{install,
	// uninstall}. Optional + nil-safe.
	maintenanceGate *MaintenanceGate
	// vaultResolver substitutes ${vault://...} markers in the values
	// blob right before the install task is enqueued. Migration 067.
	// Nil-safe: when no vault refs appear in the blob the handler
	// proceeds unchanged; when refs appear and the resolver is nil
	// the install fails with a clear error.
	vaultResolver *avault.Resolver
	bus           *events.Bus
	// encryptor seals/unseals helm_repositories.auth_config (migration 145).
	// Legacy plaintext rows remain readable for migration, but every new
	// credential-bearing write fails closed when this dependency is absent.
	encryptor *auth.Encryptor
	runTx     catalogRunTxFunc
}

// SetRunTx wires the production transaction boundary used for catalog state,
// operation intent, and mandatory audit intent.
func (h *CatalogHandler) SetRunTx(runTx catalogRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *CatalogHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

func executeCatalogMutation[T any](r *http.Request, h *CatalogHandler, mutate func(CatalogMutationTx) (T, error), fallback func() (T, error), describe func(T) clusterAuditEvent) (T, error) {
	var zero T
	if h == nil {
		return zero, errors.New("catalog handler is nil")
	}
	if h.runTx != nil {
		var result T
		err := h.runTx(r.Context(), func(q CatalogMutationTx) error {
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
	recordAudit(r, h.queries, event.action, event.resourceType, event.resourceID, event.resourceName, event.detail)
	return result, nil
}

// SetEncryptor wires the Fernet encryptor used for chart-repository
// credentials at rest (migration 145).
func (h *CatalogHandler) SetEncryptor(encryptor *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = encryptor
}

// decryptor returns h.encryptor as a catalog.Decryptor, or a genuinely nil
// interface when none is wired. Returning h.encryptor directly would hand back
// a non-nil interface holding a nil *auth.Encryptor, and the nil check in
// catalog.ResolveAuthConfig would pass straight into a nil-receiver Decrypt.
func (h *CatalogHandler) decryptor() catalog.Decryptor {
	if h == nil || h.encryptor == nil {
		return nil
	}
	return h.encryptor
}

// sealer mirrors decryptor for the write path.
func (h *CatalogHandler) sealer() catalog.Encryptor {
	if h == nil || h.encryptor == nil {
		return nil
	}
	return h.encryptor
}

// SetEventBus wires the SSE bus for catalog_release.changed liveness events
// (P4.9). Optional: publishers are fire-and-forget and nil-safe.
func (h *CatalogHandler) SetEventBus(bus *events.Bus) {
	if h == nil {
		return
	}
	h.bus = bus
}

// publishCatalogReleaseChanged emits the metadata-only catalog_release.changed
// event after an installed-chart row write (pending_* staging in the HTTP
// handlers and the reconciler's terminal status writes). Server-initiated
// release changes therefore surface even when the agent's Helm-Secret
// informer stream lags.
func (h *CatalogHandler) publishCatalogReleaseChanged(clusterID, installationID string) {
	if h == nil {
		return
	}
	events.PublishChanged(h.bus, "catalog_release", clusterID, installationID, nil)
}

// SetMaintenanceGate wires the migration-057 gate that refuses or
// defers helm install / uninstall during an active window.
func (h *CatalogHandler) SetMaintenanceGate(g *MaintenanceGate) {
	if h == nil {
		return
	}
	h.maintenanceGate = g
}

// SetVaultResolver wires the Vault resolver used to substitute
// ${vault://...} markers in operator-supplied values blobs at install
// time. See internal/vault/resolver.go for the reference grammar.
func (h *CatalogHandler) SetVaultResolver(r *avault.Resolver) {
	if h == nil {
		return
	}
	h.vaultResolver = r
}

// NewCatalogHandler creates a new catalog handler.
func NewCatalogHandler(queries CatalogQuerier) *CatalogHandler {
	return &CatalogHandler{
		queries: queries,
		log:     slog.Default(),
		trigger: make(chan struct{}, 1),
	}
}

func NewCatalogHandlerWithHelm(queries CatalogQuerier, helm HelmRequester) *CatalogHandler {
	return &CatalogHandler{
		queries: queries,
		helm:    helm,
		log:     slog.Default(),
		trigger: make(chan struct{}, 1),
	}
}

func (h *CatalogHandler) SetLogger(log *slog.Logger) {
	if log != nil {
		h.log = log
	}
}

func (h *CatalogHandler) SetAuthorization(engine *rbac.Engine, querier middleware.RBACQuerier) {
	h.authz.SetAuthorization(engine, querier)
}

func (h *CatalogHandler) StartReconciler(ctx context.Context) {
	if h == nil || h.queries == nil {
		return
	}
	go h.runReconciler(ctx)
}

func (h *CatalogHandler) TriggerReconcile() {
	if h == nil || h.trigger == nil {
		return
	}
	select {
	case h.trigger <- struct{}{}:
	default:
	}
}

func (h *CatalogHandler) runReconciler(ctx context.Context) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	h.processPendingOperations(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.processPendingOperations(ctx)
		case <-h.trigger:
			h.processPendingOperations(ctx)
		}
	}
}

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
	offset := int32(queryInt(r, "offset", 0))

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
		RespondList(w, helmRepositoriesToResponse(h.redactHelmRepositories(rows), h.chartCountsFor(r.Context(), rows)),
			NewPagination(len(rows), len(rows), 0, len(rows)))
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
		RespondPaginated(w, r, helmRepositoriesToResponse(h.redactHelmRepositories(rows), h.chartCountsFor(r.Context(), rows)), total)
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

	RespondPaginated(w, r, helmRepositoriesToResponse(h.redactHelmRepositories(repos), h.chartCountsFor(r.Context(), repos)), total)
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

// valueOr dereferences an optional request field, falling back to the stored
// value when the client omitted the key. This is what makes "absent leaves the
// stored value alone" a rule rather than a per-field decision.
func valueOr[T any](p *T, stored T) T {
	if p == nil {
		return stored
	}
	return *p
}

// validateCatalogRepositoryURL prevents credentials from bypassing the
// encrypted auth_config field and leaking into operation/task payloads, audit
// detail, or logs. Repository URLs are identifiers, not secret containers.
func validateCatalogRepositoryURL(raw string) (string, error) {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return "", errors.New("repository URL is required")
	}
	// Git's conventional SCP-like transport is not RFC 3986, but contains no
	// credential beyond the fixed transport username.
	if strings.HasPrefix(clean, "git@") && !strings.ContainsAny(clean, "?#") {
		return clean, nil
	}
	parsed, err := url.Parse(clean)
	if err != nil {
		return "", errors.New("repository URL is invalid")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("repository URL must not contain credentials, query parameters, or fragments; use auth_config")
	}
	return clean, nil
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

	if req.AuthConfig == nil {
		req.AuthConfig = json.RawMessage(`{}`)
	}
	cleanURL, err := validateCatalogRepositoryURL(req.URL)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, err.Error())
		return
	}
	req.URL = cleanURL

	// Auto-detect OCI URLs so the UI can render the correct icon and the
	// reconciler can dispatch to the OCI ingest path even when the operator
	// forgets to pass repo_type explicitly.
	if req.RepoType == "" && IsOCIRepo(req.URL) {
		req.RepoType = "oci"
	}
	// Same treatment for the other half of the credential: an auth_config
	// carrying a username/password with no auth_type would be stored intact
	// and then never sent, because ApplyIndexAuth short-circuits on an empty
	// auth_type. A stated auth_type always wins.
	if req.AuthType == "" {
		req.AuthType = catalog.InferAuthType(req.AuthConfig)
	}
	// DIR-07: accept git-sourced chart repos (clone/index path lands in worker).
	if strings.EqualFold(req.RepoType, "git") {
		req.RepoType = "git"
		if strings.TrimSpace(req.URL) == "" {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "git repository URL is required")
			return
		}
	}

	// Migration 145: the credential goes to the database as a Fernet envelope;
	// only the non-secret projection stays in the JSONB column.
	if h.sealer() == nil && catalog.HasAuthConfigSecret(req.AuthConfig) {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.CryptoError, "Repository credential encryption is unavailable")
		return
	}
	sealed, publicCfg, err := catalog.SealAuthConfig(req.AuthConfig, h.sealer())
	if err != nil {
		h.log.Error("encrypt chart repository credential", "repository", req.Name, "error", err)
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to secure repository credentials")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	params := sqlc.CreateHelmRepositoryParams{
		Name:                req.Name,
		Url:                 req.URL,
		RepoType:            req.RepoType,
		Description:         req.Description,
		IsDefault:           req.IsDefault,
		AuthType:            req.AuthType,
		AuthConfig:          publicCfg,
		AuthConfigEncrypted: sealed,
		Enabled:             enabled,
	}
	repo, err := executeCatalogMutation(r, h,
		func(q CatalogMutationTx) (sqlc.HelmRepository, error) {
			return q.CreateHelmRepository(r.Context(), params)
		},
		func() (sqlc.HelmRepository, error) { return h.queries.CreateHelmRepository(r.Context(), params) },
		func(row sqlc.HelmRepository) clusterAuditEvent {
			return clusterAuditEvent{action: "catalog.repo.create", resourceType: "helm_repository", resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusCreated, detail: map[string]any{
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

	// SEC-01: when the client echoes the redaction sentinel, keep existing
	// secrets. The stored secrets live in the Fernet envelope since migration
	// 145, so the merge base has to be the DECRYPTED document — merging
	// against the stripped JSONB projection would resolve every echoed
	// sentinel to "absent" and quietly delete the credential the operator
	// meant to leave alone.
	existingCfg, resolveErr := catalog.ResolveAuthConfig(existing, h.decryptor())
	if resolveErr != nil {
		// Fail the write rather than merge against a document we could not
		// read: silently dropping a credential the operator asked us to
		// preserve is worse than an error they can act on.
		h.log.Error("decrypt chart repository credential for update", "repository", existing.Name, "error", resolveErr)
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError,
			"Failed to read existing repository credentials; check the platform encryption key")
		return
	}

	// An absent auth_config means "do not touch the credential"; a present one
	// replaces the document, with sentinel/empty secret values still resolving
	// to the stored value so a redacted GET can be edited and PUT straight back.
	authConfig := existingCfg
	if req.AuthConfig != nil {
		authConfig = mergeAuthConfigPreservingSentinel(existingCfg, *req.AuthConfig)
	}

	name := valueOr(req.Name, existing.Name)
	authType := valueOr(req.AuthType, existing.AuthType)
	// auth_type and auth_config are one credential: a document that gained a
	// username/password while auth_type stayed empty would be stored and then
	// never sent. Same rule as CreateRepo — a stated auth_type always wins.
	if authType == "" {
		authType = catalog.InferAuthType(authConfig)
	}
	if h.sealer() == nil && catalog.HasAuthConfigSecret(authConfig) {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.CryptoError, "Repository credential encryption is unavailable")
		return
	}

	sealed, publicCfg, err := catalog.SealAuthConfig(authConfig, h.sealer())
	if err != nil {
		h.log.Error("encrypt chart repository credential", "repository", name, "error", err)
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.UpdateError, "Failed to secure repository credentials")
		return
	}

	params := sqlc.UpdateHelmRepositoryParams{
		ID:                  id,
		Name:                name,
		Url:                 valueOr(req.URL, existing.Url),
		RepoType:            valueOr(req.RepoType, existing.RepoType),
		Description:         valueOr(req.Description, existing.Description),
		IsDefault:           valueOr(req.IsDefault, existing.IsDefault),
		AuthType:            authType,
		AuthConfig:          publicCfg,
		AuthConfigEncrypted: sealed,
		Enabled:             valueOr(req.Enabled, existing.Enabled),
	}
	cleanURL, urlErr := validateCatalogRepositoryURL(params.Url)
	if urlErr != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, urlErr.Error())
		return
	}
	params.Url = cleanURL
	repo, err := executeCatalogMutation(r, h,
		func(q CatalogMutationTx) (sqlc.HelmRepository, error) {
			return q.UpdateHelmRepository(r.Context(), params)
		},
		func() (sqlc.HelmRepository, error) { return h.queries.UpdateHelmRepository(r.Context(), params) },
		func(row sqlc.HelmRepository) clusterAuditEvent {
			return clusterAuditEvent{action: "catalog.repo.update", resourceType: "helm_repository", resourceID: row.ID.String(), resourceName: row.Name, status: http.StatusOK, detail: map[string]any{
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
	_, err = executeCatalogMutation(r, h,
		func(q CatalogMutationTx) (uuid.UUID, error) { return id, q.DeleteHelmRepository(r.Context(), id) },
		func() (uuid.UUID, error) { return id, h.queries.DeleteHelmRepository(r.Context(), id) },
		func(rowID uuid.UUID) clusterAuditEvent {
			return clusterAuditEvent{action: "catalog.repo.delete", resourceType: "helm_repository", resourceID: rowID.String(), resourceName: repoName, status: http.StatusNoContent}
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
	if h.runTx != nil {
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
		return
	}
	RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "Catalog sync transaction runner is not configured")
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

// --- Helm Charts ---

func catalogProjectQuery(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if raw == "" {
		return uuid.Nil, false, true
	}
	projectID, err := uuid.Parse(raw)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project_id query param")
		return uuid.Nil, false, false
	}
	return projectID, true, true
}

func catalogVisibilityAllowsRead(visibility sqlc.CatalogVisibility) bool {
	switch visibility {
	case sqlc.CatalogVisibilityOwn, sqlc.CatalogVisibilitySubscribedPublic, sqlc.CatalogVisibilityPublic:
		return true
	default:
		return false
	}
}

func (h *CatalogHandler) authorizeChartRead(w http.ResponseWriter, r *http.Request, chart sqlc.HelmChart) bool {
	projectID, projectScoped, ok := catalogProjectQuery(w, r)
	if !ok {
		return false
	}
	repository, err := h.queries.GetHelmRepositoryByID(r.Context(), chart.RepositoryID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart repository not found")
		return false
	}
	if !projectScoped {
		if repository.OwnerProjectID.Valid {
			// A private chart is intentionally indistinguishable from a missing
			// chart unless the caller selects and is authorized for its project.
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart not found")
			return false
		}
		return h.authz.authorizeGlobalAction(w, r, rbac.ResourceCatalog, rbac.VerbRead)
	}
	if !h.authz.authorizeProjectAction(w, r, projectID, rbac.ResourceCatalog, rbac.VerbRead) {
		return false
	}
	visibility, err := h.queries.GetCatalogVisibilityForProject(r.Context(), projectID, repository.ID)
	if err != nil || !catalogVisibilityAllowsRead(visibility) {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart not found")
		return false
	}
	return true
}

func catalogVisibleToProject(ctx context.Context, queries CatalogQuerier, projectID, repositoryID uuid.UUID) bool {
	visibility, err := queries.GetCatalogVisibilityForProject(ctx, projectID, repositoryID)
	return err == nil && catalogVisibilityAllowsRead(visibility)
}

// ListCharts handles GET /api/v1/catalog/charts/.
//
// Migration 061: when ?project_id=<uuid> is present, the visible catalog
// set is narrowed from "every helm_repositories row" to the project-scoped
// union (globals + own + subscribed). Without project_id the behaviour
// is unchanged for the admin view.
// Migration 071: also accepts ?tag= to filter on helm_chart_tags (used by
// the service-mesh tab "Install" deep-link).
func (h *CatalogHandler) ListCharts(w http.ResponseWriter, r *http.Request) {
	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))
	pid, projectScoped, ok := catalogProjectQuery(w, r)
	if !ok {
		return
	}
	if projectScoped && !h.authz.authorizeProjectAction(w, r, pid, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}
	if !projectScoped && !h.authz.authorizeGlobalAction(w, r, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}

	if tag != "" {
		if projectScoped {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidRequest, "tag filtering is available only on the global catalog")
			return
		}
		charts, err := h.queries.ListHelmChartsByTag(r.Context(), sqlc.ListHelmChartsByTagParams{
			Tag:    tag,
			Limit:  limit,
			Offset: offset,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list charts by tag")
			return
		}
		total, err := h.queries.CountHelmChartsByTag(r.Context(), tag)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count charts by tag")
			return
		}
		RespondPaginated(w, r, charts, total)
		return
	}

	if projectScoped {
		visibleCatalogs, err := h.queries.ListCatalogsForProject(r.Context(), pid)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to resolve project catalogs")
			return
		}
		// Single IN-list query over the project's visible catalog set with
		// real LIMIT/OFFSET + COUNT. The old path fanned out a Limit:1000
		// query per catalog and sliced in Go, which silently truncated any
		// catalog holding more than 1000 charts.
		repoIDs := make([]uuid.UUID, 0, len(visibleCatalogs))
		for _, cat := range visibleCatalogs {
			repoIDs = append(repoIDs, cat.ID)
		}
		if len(repoIDs) == 0 {
			RespondPaginated(w, r, []sqlc.HelmChart{}, 0)
			return
		}
		charts, err := h.queries.ListChartsByRepositoryIDs(r.Context(), sqlc.ListChartsByRepositoryIDsParams{
			RepositoryIds: repoIDs,
			QueryLimit:    limit,
			QueryOffset:   offset,
		})
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list project charts")
			return
		}
		total, err := h.queries.CountChartsByRepositoryIDs(r.Context(), repoIDs)
		if err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count project charts")
			return
		}
		RespondPaginated(w, r, charts, total)
		return
	}

	charts, err := h.queries.ListHelmCharts(r.Context(), sqlc.ListHelmChartsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list charts")
		return
	}

	total, err := h.queries.CountHelmCharts(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count charts")
		return
	}

	RespondPaginated(w, r, charts, total)
}

// GetChart handles GET /api/v1/catalog/charts/{id}/.
func (h *CatalogHandler) GetChart(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid chart ID")
		return
	}

	chart, err := h.queries.GetHelmChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart not found")
		return
	}
	if !h.authorizeChartRead(w, r, chart) {
		return
	}

	RespondJSON(w, http.StatusOK, chart)
}

// ListChartVersions handles GET /api/v1/catalog/charts/{id}/versions/.
func (h *CatalogHandler) ListChartVersions(w http.ResponseWriter, r *http.Request) {
	chartID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid chart ID")
		return
	}
	chart, err := h.queries.GetHelmChartByID(r.Context(), chartID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart not found")
		return
	}
	if !h.authorizeChartRead(w, r, chart) {
		return
	}
	limit := queryLimit(r, 50)
	offset := queryInt(r, "offset", 0)
	versions, err := h.queries.ListChartVersions(r.Context(), sqlc.ListChartVersionsParams{
		ChartID: chartID,
		Limit:   int32(limit),
		Offset:  int32(offset),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list chart versions")
		return
	}
	// No exact total is available; infer has_more from a full SQL page.
	RespondList(w, versions, NewPaginationFromPage(limit, offset, len(versions)))
}

// --- Installed Charts (Installations) ---

// ListInstallations handles GET /api/v1/clusters/{cluster_id}/installations/.
func (h *CatalogHandler) ListInstallations(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	// The per-cluster installation list (and ?cluster_id= on the fleet list,
	// which delegates here) carries values_override — gate it on the cluster's
	// own catalog:read so a caller without a grant there can't read it.
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}

	limit := int32(queryLimit(r, 20))
	offset := int32(queryInt(r, "offset", 0))

	installations, err := h.queries.ListInstalledChartsByCluster(r.Context(), sqlc.ListInstalledChartsByClusterParams{
		ClusterID: clusterID,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list installations")
		return
	}

	total, err := h.queries.CountInstalledChartsByCluster(r.Context(), clusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CountError, "Failed to count installations")
		return
	}

	RespondPaginated(w, r, installations, total)
}

// CreateInstallationRequest represents the request body for creating an installation.
type CreateInstallationRequest struct {
	ChartVersionID string `json:"chart_version_id" validate:"required,uuid"`
	ProjectID      string `json:"project_id" validate:"required,uuid"`
	ReleaseName    string `json:"release_name" validate:"required"`
	Namespace      string `json:"namespace" validate:"required"`
	ValuesOverride string `json:"values_override"`
	Notes          string `json:"notes"`
	ToolSlug       string `json:"tool_slug"`
	PresetUsed     string `json:"preset_used"`
}

type catalogOperationEnvelope struct {
	InstalledChartID string `json:"installedChartId"`
	ClusterID        string `json:"clusterId"`
	ReleaseName      string `json:"releaseName"`
	Namespace        string `json:"namespace"`
	ChartVersionID   string `json:"chartVersionId,omitempty"`
	ChartName        string `json:"chartName,omitempty"`
	RepoURL          string `json:"repoUrl,omitempty"`
	Version          string `json:"version,omitempty"`
	ValuesOverride   string `json:"valuesOverride,omitempty"`
	Notes            string `json:"notes,omitempty"`
	RollbackRevision int    `json:"rollbackRevision,omitempty"`
}

// CreateInstallation handles POST /api/v1/clusters/{cluster_id}/installations/.
func (h *CatalogHandler) CreateInstallation(w http.ResponseWriter, r *http.Request) {
	clusterID, err := uuid.Parse(chi.URLParam(r, "cluster_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbCreate) {
		return
	}

	// Migration 057: maintenance window gate.
	if blocked := h.checkCatalogMaintenanceWindow(w, r, clusterID, "helm.install"); blocked {
		return
	}

	var req CreateInstallationRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	params := sqlc.CreateInstalledChartParams{
		ClusterID:      clusterID,
		ReleaseName:    req.ReleaseName,
		Namespace:      req.Namespace,
		ValuesOverride: req.ValuesOverride,
		Status:         "pending_install",
		Revision:       1,
		Notes:          req.Notes,
		InstalledByID:  currentUserUUID(r),
	}

	var version sqlc.HelmChartVersion
	var chart sqlc.HelmChart
	var repo sqlc.HelmRepository
	if req.ChartVersionID != "" {
		projectID, parseErr := uuid.Parse(req.ProjectID)
		if parseErr != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "A valid project_id is required for catalog installation")
			return
		}
		project, projectErr := h.queries.GetProjectByID(r.Context(), projectID)
		if projectErr != nil || project.ClusterID != clusterID {
			RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Project is not assigned to the target cluster")
			return
		}
		if !h.authz.authorizeProjectAction(w, r, projectID, rbac.ResourceCatalog, rbac.VerbCreate) {
			return
		}
		cvID, err := uuid.Parse(req.ChartVersionID)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid chart version ID")
			return
		}
		params.ChartVersionID = pgtype.UUID{Bytes: cvID, Valid: true}
		version, err = h.queries.GetHelmChartVersionByID(r.Context(), cvID)
		if err != nil {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart version not found")
			return
		}
		chart, err = h.queries.GetHelmChartByID(r.Context(), version.ChartID)
		if err != nil {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart not found")
			return
		}
		repo, err = h.queries.GetHelmRepositoryByID(r.Context(), chart.RepositoryID)
		if err != nil {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Repository not found")
			return
		}
		if !catalogVisibleToProject(r.Context(), h.queries, projectID, repo.ID) {
			RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Chart repository is not visible to this project")
			return
		}
	}

	if req.ToolSlug != "" {
		params.ToolSlug = pgtype.Text{String: req.ToolSlug, Valid: true}
	}
	if req.PresetUsed != "" {
		params.PresetUsed = pgtype.Text{String: req.PresetUsed, Valid: true}
	}

	// Migration 067 — the values blob keeps its ${vault://...} markers in
	// both the installed_charts row AND the enqueued operation payload.
	// Resolution happens at execution time inside the reconciler
	// (sendHelm), so the resolved plaintext secret is never persisted to
	// catalog_operations.payload — it only ever exists in-memory on the
	// wire to the cluster. Catalog installs are cluster-scoped and not
	// tied to a single project, so unqualified references (no <connection>
	// segment) require the operator to use the explicit
	// "${vault://<connection>/...}" form; the reconciler fails the
	// operation clearly when a reference is unresolvable.
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	opCtx := withOperationIdempotency(r, "catalog")
	result, err := executeCatalogMutation(r, h,
		func(q CatalogMutationTx) (catalogMutationResult[sqlc.InstalledChart], error) {
			installation, mutationErr := q.CreateInstalledChart(r.Context(), params)
			if mutationErr != nil {
				return catalogMutationResult[sqlc.InstalledChart]{}, mutationErr
			}
			op, mutationErr := createCatalogOperation(opCtx, q, "installed_chart", installation.ID.String(), "install", catalogOperationEnvelope{
				InstalledChartID: installation.ID.String(), ClusterID: clusterID.String(), ReleaseName: installation.ReleaseName,
				Namespace: installation.Namespace, ChartVersionID: req.ChartVersionID, ChartName: chart.Name, RepoURL: repo.Url,
				Version: version.Version, ValuesOverride: installation.ValuesOverride, Notes: installation.Notes,
			}, currentUserUUID(r))
			return catalogMutationResult[sqlc.InstalledChart]{row: installation, op: op}, mutationErr
		},
		func() (catalogMutationResult[sqlc.InstalledChart], error) {
			installation, mutationErr := h.queries.CreateInstalledChart(r.Context(), params)
			if mutationErr != nil {
				return catalogMutationResult[sqlc.InstalledChart]{}, mutationErr
			}
			op, mutationErr := h.enqueueOperation(opCtx, "installed_chart", installation.ID.String(), "install", catalogOperationEnvelope{
				InstalledChartID: installation.ID.String(), ClusterID: clusterID.String(), ReleaseName: installation.ReleaseName,
				Namespace: installation.Namespace, ChartVersionID: req.ChartVersionID, ChartName: chart.Name, RepoURL: repo.Url,
				Version: version.Version, ValuesOverride: installation.ValuesOverride, Notes: installation.Notes,
			}, currentUserUUID(r))
			return catalogMutationResult[sqlc.InstalledChart]{row: installation, op: op}, mutationErr
		},
		func(m catalogMutationResult[sqlc.InstalledChart]) clusterAuditEvent {
			return clusterAuditEvent{action: "catalog.installation.create", resourceType: "installed_chart", resourceID: m.row.ID.String(), resourceName: m.row.ReleaseName, status: http.StatusAccepted, detail: map[string]any{
				"cluster_id": m.row.ClusterID.String(), "namespace": m.row.Namespace, "chart_version_id": req.ChartVersionID,
				"chart_name": chart.Name, "repository_id": repo.ID.String(), "version": version.Version, "operation_id": m.op.ID.String(),
			}}
		})
	if err != nil {
		respondCatalogMutationError(w, r, err, apierror.EnqueueError, "Failed to create and enqueue installation")
		return
	}
	installation, op := result.row, result.op
	if h.runTx != nil {
		h.TriggerReconcile()
	}
	h.publishCatalogReleaseChanged(clusterID.String(), installation.ID.String())
	RespondAcceptedOperation(w, "/api/v1/catalog/operations/"+op.ID.String()+"/", map[string]any{
		"installation": installation,
		"operation":    catalogOperationResponse(op),
	})
}

// DeleteInstallation handles DELETE /api/v1/clusters/{cluster_id}/installations/{id}/.
func (h *CatalogHandler) DeleteInstallation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid installation ID")
		return
	}
	installation, err := h.queries.GetInstalledChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Installation not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, installation.ClusterID, rbac.ResourceCatalog, rbac.VerbDelete) {
		return
	}
	// Migration 057: maintenance window gate.
	if blocked := h.checkCatalogMaintenanceWindow(w, r, installation.ClusterID, "helm.uninstall"); blocked {
		return
	}
	statusParams := sqlc.UpdateInstalledChartStatusParams{
		ID:       installation.ID,
		Status:   "pending_uninstall",
		Revision: installation.Revision,
	}
	envelope := catalogOperationEnvelope{InstalledChartID: installation.ID.String(), ClusterID: installation.ClusterID.String(), ReleaseName: installation.ReleaseName, Namespace: installation.Namespace}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	opCtx := withOperationIdempotency(r, "catalog")
	result, err := executeCatalogMutation(r, h,
		func(q CatalogMutationTx) (catalogMutationResult[sqlc.InstalledChart], error) {
			if mutationErr := q.UpdateInstalledChartStatus(r.Context(), statusParams); mutationErr != nil {
				return catalogMutationResult[sqlc.InstalledChart]{}, mutationErr
			}
			op, mutationErr := createCatalogOperation(opCtx, q, "installed_chart", installation.ID.String(), "uninstall", envelope, currentUserUUID(r))
			return catalogMutationResult[sqlc.InstalledChart]{row: installation, op: op}, mutationErr
		},
		func() (catalogMutationResult[sqlc.InstalledChart], error) {
			if mutationErr := h.queries.UpdateInstalledChartStatus(r.Context(), statusParams); mutationErr != nil {
				return catalogMutationResult[sqlc.InstalledChart]{}, mutationErr
			}
			op, mutationErr := h.enqueueOperation(opCtx, "installed_chart", installation.ID.String(), "uninstall", envelope, currentUserUUID(r))
			return catalogMutationResult[sqlc.InstalledChart]{row: installation, op: op}, mutationErr
		},
		func(m catalogMutationResult[sqlc.InstalledChart]) clusterAuditEvent {
			return clusterAuditEvent{action: "catalog.installation.delete", resourceType: "installed_chart", resourceID: m.row.ID.String(), resourceName: m.row.ReleaseName, status: http.StatusAccepted, detail: map[string]any{
				"cluster_id": m.row.ClusterID.String(), "namespace": m.row.Namespace, "operation_id": m.op.ID.String(),
			}}
		})
	if err != nil {
		respondCatalogMutationError(w, r, err, apierror.EnqueueError, "Failed to stage and enqueue uninstall")
		return
	}
	op := result.op
	if h.runTx != nil {
		h.TriggerReconcile()
	}
	h.publishCatalogReleaseChanged(installation.ClusterID.String(), installation.ID.String())
	RespondAcceptedOperation(w, "/api/v1/catalog/operations/"+op.ID.String()+"/", catalogOperationResponse(op))
}

// ListInstalledCharts handles GET /api/v1/catalog/installed/.
func (h *CatalogHandler) ListInstalledCharts(w http.ResponseWriter, r *http.Request) {
	clusterIDStr := r.URL.Query().Get("cluster_id")
	if clusterIDStr != "" {
		ctx := chi.NewRouteContext()
		ctx.URLParams.Add("cluster_id", clusterIDStr)
		h.ListInstallations(w, r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx)))
		return
	}

	// Resolve the authorized cluster set before the database page boundary.
	// The list projection never exposes values_override; that secret-bearing
	// field remains available only through the separately gated detail route.
	all, clusterIDs, _, err := h.authz.authorizedScopeIDs(r.Context(), rbac.ResourceCatalog, rbac.VerbRead, rbac.NarrowedClustersWiden)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return
	}
	limit := queryLimit(r, 20)
	offset := queryInt(r, "offset", 0)
	var rows []sqlc.InstalledChart
	var total int64
	if all {
		rows, err = h.queries.ListInstalledCharts(r.Context(), sqlc.ListInstalledChartsParams{Limit: int32(limit), Offset: int32(offset)})
		if err == nil {
			total, err = h.queries.CountInstalledCharts(r.Context())
		}
	} else {
		pager, ok := h.queries.(installedChartScopedPager)
		if !ok {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Scoped installed-chart pagination is unavailable")
			return
		}
		rows, err = pager.ListInstalledChartsForScopes(r.Context(), sqlc.ListInstalledChartsForScopesParams{
			ClusterIds: clusterIDs, QueryLimit: int32(limit), QueryOffset: int32(offset),
		})
		if err == nil {
			total, err = pager.CountInstalledChartsForScopes(r.Context(), clusterIDs)
		}
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list installed charts")
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, ic := range rows {
		items = append(items, installedChartListItem(ic))
	}
	RespondList(w, items, NewPagination(int(total), limit, offset, len(rows)))
}

// installedChartListItem projects an installed_charts row for the fleet list.
// It deliberately omits values_override (secrets) — that field is only
// returned by the cluster-gated GetInstalledChartValues endpoint.
func installedChartListItem(ic sqlc.InstalledChart) map[string]any {
	return map[string]any{
		"id":               ic.ID.String(),
		"cluster_id":       ic.ClusterID.String(),
		"chart_version_id": ic.ChartVersionID,
		"release_name":     ic.ReleaseName,
		"namespace":        ic.Namespace,
		"status":           ic.Status,
		"revision":         ic.Revision,
		"notes":            ic.Notes,
		"tool_slug":        ic.ToolSlug,
		"preset_used":      ic.PresetUsed,
		"drift_detected":   ic.DriftDetected,
		"created_at":       ic.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":       ic.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

// CreateInstalledChart handles POST /api/v1/catalog/installed/.
func (h *CatalogHandler) CreateInstalledChart(w http.ResponseWriter, r *http.Request) {
	// openapi:request-operation postCatalogInstalled
	var req struct {
		ClusterID string `json:"cluster_id"`
		CreateInstallationRequest
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid cluster ID")
		return
	}
	// Gate before adapting to the legacy path-based handler. The adapter body
	// intentionally omits cluster_id, which would make a deferred replay of the
	// public /catalog/installed/ endpoint incomplete.
	fullBody, err := json.Marshal(req)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(fullBody))
	if h.checkCatalogMaintenanceWindow(w, r, clusterID, maintenance.OpHelmInstall) {
		return
	}
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("cluster_id", req.ClusterID)
	body, _ := json.Marshal(req.CreateInstallationRequest)
	r.Body = io.NopCloser(bytes.NewReader(body))
	h.CreateInstallation(w, r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx)))
}

// UpgradeInstalledChart handles PUT /api/v1/catalog/installed/{id}/upgrade/.
func (h *CatalogHandler) UpgradeInstalledChart(w http.ResponseWriter, r *http.Request) {
	// Defensive no-op for an unwired store: production always injects queries,
	// but the route-security tests reach here with a nil querier once a caller
	// clears the scope/RBAC gate. Answer 500 rather than dereferencing nil.
	if h == nil || h.queries == nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.InternalError, "Catalog store not configured")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid installed chart ID")
		return
	}
	// openapi:request-operation putCatalogInstalledByIdUpgrade
	var req struct {
		ChartVersionID string  `json:"chart_version_id"`
		ValuesOverride *string `json:"values_override"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	installed, err := h.queries.GetInstalledChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Installed chart not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, installed.ClusterID, rbac.ResourceCatalog, rbac.VerbUpdate) {
		return
	}
	version, chart, repo, err := h.resolveInstalledChartRelease(r.Context(), installed)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve installed chart release")
		return
	}
	targetVersionID := installed.ChartVersionID
	valuesOverride := installed.ValuesOverride
	if req.ValuesOverride != nil {
		valuesOverride = *req.ValuesOverride
	}
	if req.ChartVersionID != "" {
		requestedID, parseErr := uuid.Parse(req.ChartVersionID)
		if parseErr != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "chart_version_id must be a UUID")
			return
		}
		requestedVersion, lookupErr := h.queries.GetHelmChartVersionByID(r.Context(), requestedID)
		if lookupErr != nil {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart version not found")
			return
		}
		if requestedVersion.ChartID != chart.ID {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "chart_version_id must belong to the installed chart")
			return
		}
		version = requestedVersion
		targetVersionID = pgtype.UUID{Bytes: requestedID, Valid: true}
	}
	updateParams := sqlc.UpdateInstalledChartValuesParams{
		ID:             id,
		ChartVersionID: targetVersionID,
		ValuesOverride: valuesOverride,
		Status:         "pending_upgrade",
		Revision:       installed.Revision,
	}
	// The values blob keeps its ${vault://...} markers here and in the
	// persisted installed_charts row; the reconciler (sendHelm) resolves
	// them in-memory at execution time. Without that, the upgrade path
	// previously shipped the literal placeholder straight to Helm.
	envelope := catalogOperationEnvelope{
		InstalledChartID: installed.ID.String(),
		ClusterID:        installed.ClusterID.String(),
		ReleaseName:      installed.ReleaseName,
		Namespace:        installed.Namespace,
		ChartVersionID:   uuidFromPg(targetVersionID),
		ChartName:        chart.Name,
		RepoURL:          repo.Url,
		Version:          version.Version,
		ValuesOverride:   valuesOverride,
		Notes:            installed.Notes,
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	opCtx := withOperationIdempotency(r, "catalog")
	result, err := executeCatalogMutation(r, h,
		func(q CatalogMutationTx) (catalogMutationResult[sqlc.InstalledChart], error) {
			updated, mutationErr := q.UpdateInstalledChartValues(r.Context(), updateParams)
			if mutationErr != nil {
				return catalogMutationResult[sqlc.InstalledChart]{}, mutationErr
			}
			op, mutationErr := createCatalogOperation(opCtx, q, "installed_chart", installed.ID.String(), "upgrade", envelope, currentUserUUID(r))
			return catalogMutationResult[sqlc.InstalledChart]{row: updated, op: op}, mutationErr
		},
		func() (catalogMutationResult[sqlc.InstalledChart], error) {
			updated, mutationErr := h.queries.UpdateInstalledChartValues(r.Context(), updateParams)
			if mutationErr != nil {
				return catalogMutationResult[sqlc.InstalledChart]{}, mutationErr
			}
			op, mutationErr := h.enqueueOperation(opCtx, "installed_chart", installed.ID.String(), "upgrade", envelope, currentUserUUID(r))
			return catalogMutationResult[sqlc.InstalledChart]{row: updated, op: op}, mutationErr
		},
		func(m catalogMutationResult[sqlc.InstalledChart]) clusterAuditEvent {
			return clusterAuditEvent{action: "catalog.installation.upgrade", resourceType: "installed_chart", resourceID: installed.ID.String(), resourceName: installed.ReleaseName, status: http.StatusAccepted, detail: map[string]any{
				"cluster_id": installed.ClusterID.String(), "namespace": installed.Namespace, "chart_name": chart.Name,
				"repository_id": repo.ID.String(), "version": version.Version, "operation_id": m.op.ID.String(),
			}}
		})
	if err != nil {
		respondCatalogMutationError(w, r, err, apierror.EnqueueError, "Failed to stage and enqueue installed chart upgrade")
		return
	}
	updated, op := result.row, result.op
	if h.runTx != nil {
		h.TriggerReconcile()
	}
	h.publishCatalogReleaseChanged(installed.ClusterID.String(), installed.ID.String())
	RespondAcceptedOperation(w, "/api/v1/catalog/operations/"+op.ID.String()+"/", map[string]any{
		"installation": updated,
		"operation":    catalogOperationResponse(op),
	})
}

// RollbackInstalledChart handles POST /api/v1/catalog/installed/{id}/rollback/.
func (h *CatalogHandler) RollbackInstalledChart(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid installed chart ID")
		return
	}
	current, err := h.queries.GetInstalledChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Installed chart not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, current.ClusterID, rbac.ResourceCatalog, rbac.VerbUpdate) {
		return
	}
	// Optional body: an explicit target revision lets operators roll back to
	// ANY prior revision (parity with `helm rollback <name> <revision>`), not
	// just the immediately preceding one. An empty body — or a non-positive
	// revision — falls back to the previous revision.
	// openapi:request-operation postCatalogInstalledByIdRollback
	var req struct {
		Revision int `json:"revision,omitempty"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	targetRevision := int(max(current.Revision-1, 1))
	if req.Revision > 0 {
		targetRevision = req.Revision
	}
	if targetRevision >= int(current.Revision) {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "revision must be lower than the current revision")
		return
	}
	statusParams := sqlc.UpdateInstalledChartStatusParams{
		ID:       id,
		Status:   "pending_rollback",
		Revision: current.Revision,
	}
	envelope := catalogOperationEnvelope{
		InstalledChartID: current.ID.String(),
		ClusterID:        current.ClusterID.String(),
		ReleaseName:      current.ReleaseName,
		Namespace:        current.Namespace,
		RollbackRevision: targetRevision,
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	opCtx := withOperationIdempotency(r, "catalog")
	result, err := executeCatalogMutation(r, h,
		func(q CatalogMutationTx) (catalogMutationResult[sqlc.InstalledChart], error) {
			if mutationErr := q.UpdateInstalledChartStatus(r.Context(), statusParams); mutationErr != nil {
				return catalogMutationResult[sqlc.InstalledChart]{}, mutationErr
			}
			op, mutationErr := createCatalogOperation(opCtx, q, "installed_chart", current.ID.String(), "rollback", envelope, currentUserUUID(r))
			return catalogMutationResult[sqlc.InstalledChart]{row: current, op: op}, mutationErr
		},
		func() (catalogMutationResult[sqlc.InstalledChart], error) {
			if mutationErr := h.queries.UpdateInstalledChartStatus(r.Context(), statusParams); mutationErr != nil {
				return catalogMutationResult[sqlc.InstalledChart]{}, mutationErr
			}
			op, mutationErr := h.enqueueOperation(opCtx, "installed_chart", current.ID.String(), "rollback", envelope, currentUserUUID(r))
			return catalogMutationResult[sqlc.InstalledChart]{row: current, op: op}, mutationErr
		},
		func(m catalogMutationResult[sqlc.InstalledChart]) clusterAuditEvent {
			return clusterAuditEvent{action: "catalog.installation.rollback", resourceType: "installed_chart", resourceID: current.ID.String(), resourceName: current.ReleaseName, status: http.StatusAccepted, detail: map[string]any{
				"cluster_id": current.ClusterID.String(), "namespace": current.Namespace,
				"rollback_revision": targetRevision, "operation_id": m.op.ID.String(),
			}}
		})
	if err != nil {
		respondCatalogMutationError(w, r, err, apierror.EnqueueError, "Failed to stage and enqueue rollback")
		return
	}
	op := result.op
	if h.runTx != nil {
		h.TriggerReconcile()
	}
	h.publishCatalogReleaseChanged(current.ClusterID.String(), current.ID.String())
	RespondAcceptedOperation(w, "/api/v1/catalog/operations/"+op.ID.String()+"/", catalogOperationResponse(op))
}

// DeleteInstalledChart is a compatibility alias for DeleteInstallation.
func (h *CatalogHandler) DeleteInstalledChart(w http.ResponseWriter, r *http.Request) {
	h.DeleteInstallation(w, r)
}

// TestRepoConnection handles POST /api/v1/catalog/repositories/{id}/test-connection/.
// Probes the repository's index.yaml endpoint to verify reachability.
func (h *CatalogHandler) respondRepoConnectionResult(w http.ResponseWriter, r *http.Request, repo sqlc.HelmRepository, status int, success bool, message string, upstreamStatus int) {
	detail := map[string]any{"success": success, "repo_type": repo.RepoType}
	if upstreamStatus > 0 {
		detail["upstream_status"] = upstreamStatus
	}
	if h.runTx != nil {
		if err := recordMandatoryAudit(r, h.queries, "catalog.repo.test_connection", "helm_repository", repo.ID.String(), repo.Name, detail); err != nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.AuditUnavailable,
				"Mandatory audit storage is unavailable; the connection result was not returned")
			return
		}
	} else {
		recordAudit(r, h.queries, "catalog.repo.test_connection", "helm_repository", repo.ID.String(), repo.Name, detail)
	}
	RespondJSON(w, status, map[string]any{"success": success, "message": message})
}

func (h *CatalogHandler) TestRepoConnection(w http.ResponseWriter, r *http.Request) {
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
	if isOCIRepoSpec(repo) {
		// For OCI we just hit the /v2/ ping endpoint, which all
		// distribution-spec registries implement and all return 200/401
		// (401 here still proves the host is a registry).
		host, _, err := splitOCIURL(repo.Url)
		if err != nil {
			h.respondRepoConnectionResult(w, r, repo, http.StatusBadGateway, false, "Stored OCI repository URL is invalid.", 0)
			return
		}
		pingURL := "https://" + host + "/v2/"
		// SSRF backstop: this handler fetches an operator-supplied URL and
		// echoes the upstream status/error back, so reject probes aimed at
		// loopback / RFC-1918 / link-local (incl. the 169.254.169.254 metadata
		// endpoint) before any request leaves the process.
		if err := httpclient.GuardPublicHost(pingURL); err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidURL, "repository host is not permitted")
			return
		}
		client := httpclient.SafeClient(10 * time.Second)
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, pingURL, nil)
		if err != nil {
			RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidURL, err.Error())
			return
		}
		// Test-connection is the one place an operator is explicitly asking
		// "does this credential work", so an unreadable credential is reported
		// as such instead of being downgraded to an anonymous probe that
		// answers "reachable" and teaches them nothing.
		cfg, err := h.resolveOCIAuthConfig(repo)
		if err != nil {
			h.log.Error("test connection: chart repository credential could not be decrypted",
				"repository", repo.Name, "error", err)
			h.respondRepoConnectionResult(w, r, repo, http.StatusOK, false, "Stored credentials could not be decrypted; check the platform encryption key.", 0)
			return
		}
		if cfg.Username != "" || cfg.Password != "" {
			req.SetBasicAuth(cfg.Username, cfg.Password)
		}
		resp, err := client.Do(req)
		if err != nil {
			h.respondRepoConnectionResult(w, r, repo, http.StatusBadGateway, false, "Repository connection failed.", 0)
			return
		}
		defer func() {
			_ = resp.Body.Close()
		}()
		if resp.StatusCode >= 200 && resp.StatusCode < 500 {
			h.respondRepoConnectionResult(w, r, repo, http.StatusOK, true, fmt.Sprintf("OCI registry reachable (status %d).", resp.StatusCode), resp.StatusCode)
			return
		}
		h.respondRepoConnectionResult(w, r, repo, http.StatusBadGateway, false, fmt.Sprintf("Registry returned status %d.", resp.StatusCode), resp.StatusCode)
		return
	}
	url := strings.TrimRight(repo.Url, "/") + "/index.yaml"
	// SSRF backstop (see the OCI branch above): block probes at non-public
	// hosts before dialing.
	if err := httpclient.GuardPublicHost(url); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidURL, "repository host is not permitted")
		return
	}
	client := httpclient.SafeClient(10 * time.Second)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidURL, err.Error())
		return
	}
	// Same rule as the OCI branch above: test-connection is the one endpoint
	// where the operator is explicitly asking "does this credential work", so
	// resolve it here rather than through applyRepoIndexAuth, which logs a
	// decrypt failure and continues unauthenticated. Doing that here would
	// report the upstream's 401 as the answer, and the operator would conclude
	// the password is wrong when the real fault is the platform encryption key
	// — verbatim the misdiagnosis catalog.ErrAuthConfigUnavailable exists to
	// prevent.
	authCfg, err := catalog.ResolveIndexAuthConfig(repo, h.decryptor())
	if err != nil {
		h.log.Error("test connection: chart repository credential could not be decrypted",
			"repository", repo.Name, "error", err)
		h.respondRepoConnectionResult(w, r, repo, http.StatusOK, false, "Stored credentials could not be decrypted; check the platform encryption key.", 0)
		return
	}
	catalog.SetIndexAuthHeader(req, repo.AuthType, authCfg)
	resp, err := client.Do(req)
	if err != nil {
		h.respondRepoConnectionResult(w, r, repo, http.StatusBadGateway, false, "Repository connection failed.", 0)
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= http.StatusBadRequest {
		h.respondRepoConnectionResult(w, r, repo, http.StatusBadGateway, false, fmt.Sprintf("Repository returned status %d.", resp.StatusCode), resp.StatusCode)
		return
	}
	h.respondRepoConnectionResult(w, r, repo, http.StatusOK, true, "Connection successful.", resp.StatusCode)
}

// redactHelmRepository strips secret fields from auth_config for API responses
// (SEC-01). Mirrors webhook SecretSentinel: clients that echo the sentinel on
// PUT leave the stored secret unchanged.
//
// GET/POST/PUT /api/v1/catalog/repositories/ serialise sqlc.HelmRepository
// wholesale, so this function is the ONLY thing standing between the stored
// credential and the wire. Since migration 145 that means two jobs:
//
//   - auth_config_encrypted is blanked unconditionally. It is ciphertext, it
//     is of no use to any client, and shipping it hands every catalog reader
//     an offline target for whoever later obtains the Fernet key.
//   - the sentinel is reconstructed from the DECRYPTED document, so the
//     response shape is unchanged from before 145: a client can still tell
//     that a password is configured, and can still echo the sentinel back on
//     PUT to leave it alone. When the credential cannot be decrypted the key
//     is simply absent — fail closed, never emit ciphertext as if it were the
//     secret.
func (h *CatalogHandler) redactHelmRepository(repo sqlc.HelmRepository) sqlc.HelmRepository {
	out := repo
	out.AuthConfigEncrypted = ""
	resolved, err := catalog.ResolveAuthConfig(repo, h.decryptor())
	if err != nil {
		h.log.Error("chart repository credential could not be decrypted for redaction",
			"repository", repo.Name, "error", err)
		out.AuthConfig = redactAuthConfigJSON(catalog.StripAuthConfigSecrets(repo.AuthConfig))
		return out
	}
	out.AuthConfig = redactAuthConfigJSON(resolved)
	return out
}

func (h *CatalogHandler) redactHelmRepositories(repos []sqlc.HelmRepository) []sqlc.HelmRepository {
	if len(repos) == 0 {
		return repos
	}
	out := make([]sqlc.HelmRepository, len(repos))
	for i := range repos {
		out[i] = h.redactHelmRepository(repos[i])
	}
	return out
}

func redactAuthConfigJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return json.RawMessage(`{}`)
	}
	// The one list, not a copy of it. redactHelmRepository now feeds the
	// DECRYPTED document in here, so a key that catalog.SealAuthConfig knows
	// is secret but this function did not would be stripped into the envelope
	// and then emitted in the clear in every list/get response.
	for _, k := range catalog.AuthConfigSecretKeys {
		if v, ok := m[k]; ok {
			if s, isStr := v.(string); isStr && s != "" {
				m[k] = SecretSentinel
			}
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// mergeAuthConfigPreservingSentinel keeps existing secret values when the
// request carries SecretSentinel (UI "leave unchanged" pattern).
func mergeAuthConfigPreservingSentinel(existing, incoming json.RawMessage) json.RawMessage {
	if len(incoming) == 0 {
		return existing
	}
	var in map[string]any
	if err := json.Unmarshal(incoming, &in); err != nil || in == nil {
		return existing
	}
	var ex map[string]any
	_ = json.Unmarshal(existing, &ex)
	if ex == nil {
		ex = map[string]any{}
	}
	// Same list the sealing and redaction paths use; see
	// catalog.AuthConfigSecretKeys.
	for _, k := range catalog.AuthConfigSecretKeys {
		v, ok := in[k]
		if !ok {
			continue
		}
		s, isStr := v.(string)
		if !isStr {
			continue
		}
		if s == SecretSentinel || s == "" {
			if old, ok := ex[k]; ok {
				in[k] = old
			} else {
				delete(in, k)
			}
		}
	}
	b, err := json.Marshal(in)
	if err != nil {
		return existing
	}
	return b
}

// GetChartReadme handles GET /api/v1/catalog/charts/{id}/readme/.
// Returns the README from the latest (or ?version=) chart version.
func (h *CatalogHandler) GetChartReadme(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid chart ID")
		return
	}
	chart, err := h.queries.GetHelmChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart not found")
		return
	}
	if !h.authorizeChartRead(w, r, chart) {
		return
	}
	version, err := h.resolveChartVersion(r, chart)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "No versions found for this chart.")
		return
	}
	// Lazy hydrate on cache miss. hydrateChartVersion self-guards on
	// content_hydrated_at, so this is cheap once hydrated.
	if hydrated, hErr := h.hydrateChartVersion(r.Context(), version); hErr == nil {
		version = hydrated
	} else if h.log != nil {
		h.log.Warn("chart readme hydration failed",
			"chart_id", chart.ID, "version_id", version.ID, "error", hErr)
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"chart":   chart.Name,
		"version": version.Version,
		"readme":  version.Readme,
	})
}

// GetChartValues handles GET /api/v1/catalog/charts/{id}/values/.
// Returns the default values + values_schema from the latest (or ?version=) chart version.
func (h *CatalogHandler) GetChartValues(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid chart ID")
		return
	}
	chart, err := h.queries.GetHelmChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Chart not found")
		return
	}
	if !h.authorizeChartRead(w, r, chart) {
		return
	}
	version, err := h.resolveChartVersion(r, chart)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "No versions found for this chart.")
		return
	}
	// Lazy hydrate default_values + README + values_schema on cache miss so the
	// install modal gets real defaults + form. hydrateChartVersion self-guards
	// on content_hydrated_at, so calling it unconditionally is cheap once a row
	// is hydrated and also backfills schema for rows hydrated before that column
	// existed (they have values but no schema).
	if hydrated, hErr := h.hydrateChartVersion(r.Context(), version); hErr == nil {
		version = hydrated
	} else if h.log != nil {
		h.log.Warn("chart values hydration failed",
			"chart_id", chart.ID, "version_id", version.ID, "error", hErr)
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"chart":          chart.Name,
		"version":        version.Version,
		"default_values": version.DefaultValues,
		"values_schema":  version.ValuesSchema,
	})
}

// ListInstalledChartRevisions handles GET /api/v1/catalog/installed/{id}/revisions/.
// DIR-12: returns helm release history via the agent tunnel.
func (h *CatalogHandler) ListInstalledChartRevisions(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid release ID")
		return
	}
	installed, err := h.queries.GetInstalledChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Installed chart not found")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, installed.ClusterID, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}
	if h.helm == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.NotConfigured, "Helm requester not configured")
		return
	}
	result, err := h.helm.History(r.Context(), installed.ClusterID.String(), installed.ReleaseName, installed.Namespace)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadGateway, apierror.ProxyError, err.Error())
		return
	}
	revs := result.Revisions
	if revs == nil {
		revs = []protocol.HelmRevision{}
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"release_name": installed.ReleaseName,
		"namespace":    installed.Namespace,
		"revisions":    revs,
	})
}

// GetInstalledChartValues handles GET /api/v1/catalog/installed/{id}/values/.
// Returns the values_override stored on the release.
func (h *CatalogHandler) GetInstalledChartValues(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid release ID")
		return
	}
	installed, err := h.queries.GetInstalledChartByID(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Installed chart not found")
		return
	}
	// values_override routinely carries secrets (DB passwords, API keys).
	// Gate on the release's own cluster like the sibling upgrade/delete
	// handlers so a caller without catalog:read on that cluster gets a 403
	// instead of a fleet-wide values leak.
	if !h.authz.authorizeClusterAction(w, r, installed.ClusterID, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}
	RespondJSON(w, http.StatusOK, map[string]any{
		"release_name":    installed.ReleaseName,
		"namespace":       installed.Namespace,
		"values_override": installed.ValuesOverride,
	})
}

// resolveChartVersion picks a specific chart version (by ?version= query param) or the latest.
func (h *CatalogHandler) resolveChartVersion(r *http.Request, chart sqlc.HelmChart) (sqlc.HelmChartVersion, error) {
	if v := strings.TrimSpace(r.URL.Query().Get("version")); v != "" {
		versions, err := h.queries.ListChartVersions(r.Context(), sqlc.ListChartVersionsParams{
			ChartID: chart.ID,
			Limit:   200,
			Offset:  0,
		})
		if err != nil {
			return sqlc.HelmChartVersion{}, err
		}
		for _, ver := range versions {
			if ver.Version == v {
				return ver, nil
			}
		}
		return sqlc.HelmChartVersion{}, fmt.Errorf("version %q not found", v)
	}
	return h.queries.GetLatestChartVersion(r.Context(), chart.ID)
}

func (h *CatalogHandler) ListOperations(w http.ResponseWriter, r *http.Request) {
	limit := queryLimit(r, 50)
	offset := queryInt(r, "offset", 0)
	arg := sqlc.ListCatalogOperationsParams{Limit: int32(limit), Offset: int32(offset)}
	if v := strings.TrimSpace(r.URL.Query().Get("targetType")); v != "" {
		arg.TargetType = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("targetKey")); v != "" {
		arg.TargetKey = pgtype.Text{String: v, Valid: true}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("status")); v != "" {
		arg.Status = pgtype.Text{String: v, Valid: true}
	}
	all, clusterIDs, _, err := h.authz.authorizedScopeIDs(r.Context(), rbac.ResourceCatalog, rbac.VerbRead, rbac.NarrowedClustersWiden)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.Forbidden, "Failed to retrieve user permissions")
		return
	}
	var ops []sqlc.CatalogOperation
	var total int64
	pager, hasPager := h.queries.(catalogOperationPager)
	if all {
		ops, err = h.queries.ListCatalogOperations(r.Context(), arg)
		if err == nil && hasPager {
			total, err = pager.CountCatalogOperations(r.Context(), sqlc.CountCatalogOperationsParams{
				TargetType: arg.TargetType, TargetKey: arg.TargetKey, Status: arg.Status,
			})
		}
	} else {
		if !hasPager {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.Unavailable, "Scoped catalog-operation pagination is unavailable")
			return
		}
		ops, err = pager.ListCatalogOperationsForScopes(r.Context(), sqlc.ListCatalogOperationsForScopesParams{
			TargetType: arg.TargetType, TargetKey: arg.TargetKey, Status: arg.Status,
			ClusterIds: clusterIDs, QueryLimit: int32(limit), QueryOffset: int32(offset),
		})
		if err == nil {
			total, err = pager.CountCatalogOperationsForScopes(r.Context(), sqlc.CountCatalogOperationsForScopesParams{
				TargetType: arg.TargetType, TargetKey: arg.TargetKey, Status: arg.Status, ClusterIds: clusterIDs,
			})
		}
	}
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list catalog operations")
		return
	}
	items := make([]map[string]any, 0, len(ops))
	for _, op := range ops {
		items = append(items, catalogOperationResponse(op))
	}
	if !hasPager {
		RespondList(w, items, NewPaginationFromPage(limit, offset, len(ops)))
		return
	}
	RespondList(w, items, NewPagination(int(total), limit, offset, len(ops)))
}

func (h *CatalogHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetCatalogOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Catalog operation not found")
		return
	}
	clusterID, err := catalogOperationClusterID(op)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve catalog operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbRead) {
		return
	}
	resp := catalogOperationResponse(op)
	if events, err := h.queries.ListCatalogOperationEvents(r.Context(), op.ID); err == nil {
		resp["events"] = catalogOperationEventsResponse(events)
	}
	RespondJSON(w, http.StatusOK, resp)
}

func (h *CatalogHandler) RetryOperation(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid operation ID")
		return
	}
	op, err := h.queries.GetCatalogOperation(r.Context(), id)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Catalog operation not found")
		return
	}
	if !requireRetryableOperation(w, r, op.Status) {
		return
	}
	clusterID, err := catalogOperationClusterID(op)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ResolveError, "Failed to resolve catalog operation target")
		return
	}
	if !h.authz.authorizeClusterAction(w, r, clusterID, rbac.ResourceCatalog, rbac.VerbUpdate) {
		return
	}
	if !RequireOperationIdempotencyKey(w, r) {
		return
	}
	requeued, err := executeCatalogMutation(r, h,
		func(q CatalogMutationTx) (sqlc.CatalogOperation, error) {
			return q.RequeueCatalogOperation(r.Context(), id)
		},
		func() (sqlc.CatalogOperation, error) { return h.queries.RequeueCatalogOperation(r.Context(), id) },
		func(row sqlc.CatalogOperation) clusterAuditEvent {
			return clusterAuditEvent{action: "catalog.operation.retry", resourceType: "catalog_operation", resourceID: id.String(), resourceName: op.TargetKey, status: http.StatusAccepted, detail: map[string]any{
				"target_type": op.TargetType, "previous_status": op.Status,
			}}
		})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.RetryError, "Failed to retry catalog operation")
		return
	}
	h.TriggerReconcile()
	RespondAcceptedOperation(w, "/api/v1/catalog/operations/"+requeued.ID.String()+"/", catalogOperationResponse(requeued))
}

func catalogOperationClusterID(op sqlc.CatalogOperation) (uuid.UUID, error) {
	var env catalogOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return uuid.UUID{}, err
	}
	return uuid.Parse(env.ClusterID)
}

func (h *CatalogHandler) ControllerStatus(w http.ResponseWriter, r *http.Request) {
	summary, err := h.controllerSummary(r.Context())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.StatusError, "Failed to load catalog operations")
		return
	}
	RespondJSON(w, http.StatusOK, summary)
}

func (h *CatalogHandler) controllerSummary(ctx context.Context) (map[string]any, error) {
	ops, err := h.queries.ListCatalogOperations(ctx, sqlc.ListCatalogOperationsParams{Limit: 1000, Offset: 0})
	if err != nil {
		return nil, err
	}
	bindings, restricted, err := h.authz.bindingsForContext(ctx)
	if err != nil {
		return nil, err
	}
	opSummary := summarizeOperations(ctx, ops, operationStatusSummaryConfig[sqlc.CatalogOperation]{
		Status:    func(op sqlc.CatalogOperation) string { return op.Status },
		CreatedAt: func(op sqlc.CatalogOperation) time.Time { return op.CreatedAt },
		IsStaleRunning: func(op sqlc.CatalogOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) > time.Minute
		},
		Include: func(_ context.Context, op sqlc.CatalogOperation) bool {
			if !restricted {
				return true
			}
			clusterID, err := catalogOperationClusterID(op)
			return err == nil && h.authz.allowsCluster(bindings, clusterID, rbac.ResourceCatalog, rbac.VerbRead)
		},
		Preview:               func(ctx context.Context, op sqlc.CatalogOperation) map[string]any { return h.operationPreview(ctx, op) },
		StaleThresholdSeconds: 60,
	})
	charts, _ := h.queries.CountHelmCharts(ctx)
	installed, _ := h.queries.CountInstalledCharts(ctx)
	return map[string]any{
		"reconciler": opSummary.reconcilerMap(),
		"catalog": map[string]any{
			"chartCount": charts,
			"installedCount": func() any {
				if restricted {
					return nil
				}
				return installed
			}(),
		},
		"operations":         opSummary.Counts,
		"recentFailureCount": opSummary.RecentFailures,
		"recentOperations":   opSummary.Recent,
		"latestFailure":      opSummary.LatestFailure,
	}, nil
}

type catalogOperationCreator interface {
	CreateCatalogOperation(context.Context, sqlc.CreateCatalogOperationParams) (sqlc.CatalogOperation, error)
}

type idempotentCatalogOperationCreator interface {
	CreateCatalogOperationIdempotent(context.Context, sqlc.CreateCatalogOperationIdempotentParams) (sqlc.CatalogOperation, error)
}

type dispositionCatalogOperationCreator interface {
	CreateCatalogOperationIdempotentWithDisposition(context.Context, sqlc.CreateCatalogOperationIdempotentWithDispositionParams) (sqlc.CreateCatalogOperationIdempotentWithDispositionRow, error)
}

var errCatalogOperationIdempotencyConflict = errors.New("catalog operation idempotency key already identifies a committed operation")

func respondCatalogMutationError(w http.ResponseWriter, r *http.Request, err error, fallbackCode, fallbackMessage string) {
	if errors.Is(err, errCatalogOperationIdempotencyConflict) {
		RespondRequestError(w, r, http.StatusConflict, apierror.Conflict,
			"Idempotency-Key already identifies a catalog operation; retrieve the existing operation instead of restaging it")
		return
	}
	respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, fallbackCode, fallbackMessage)
}

func createCatalogOperation(ctx context.Context, q catalogOperationCreator, targetType, targetKey, operationType string, env catalogOperationEnvelope, userID pgtype.UUID) (sqlc.CatalogOperation, error) {
	payload, err := json.Marshal(env)
	if err != nil {
		return sqlc.CatalogOperation{}, err
	}
	params := sqlc.CreateCatalogOperationParams{
		TargetType:    targetType,
		TargetKey:     targetKey,
		OperationType: operationType,
		Payload:       payload,
		Status:        OpStatusPending,
		CreatedByID:   userID,
	}
	var op sqlc.CatalogOperation
	if idem, ok := operationIdempotencyFromContext(ctx); ok {
		if creator, ok := q.(dispositionCatalogOperationCreator); ok {
			result, createErr := creator.CreateCatalogOperationIdempotentWithDisposition(ctx, sqlc.CreateCatalogOperationIdempotentWithDispositionParams{
				Scope: idem.scope, IdempotencyKey: idem.key, TargetType: params.TargetType, TargetKey: params.TargetKey,
				OperationType: params.OperationType, Payload: params.Payload, Status: params.Status, CreatedByID: params.CreatedByID,
			})
			if createErr != nil {
				return sqlc.CatalogOperation{}, createErr
			}
			if !result.Inserted {
				return sqlc.CatalogOperation{}, errCatalogOperationIdempotencyConflict
			}
			op = result.CatalogOperation
		} else if creator, ok := q.(idempotentCatalogOperationCreator); ok {
			op, err = creator.CreateCatalogOperationIdempotent(ctx, sqlc.CreateCatalogOperationIdempotentParams{
				Scope:          idem.scope,
				IdempotencyKey: idem.key,
				TargetType:     params.TargetType,
				TargetKey:      params.TargetKey,
				OperationType:  params.OperationType,
				Payload:        params.Payload,
				Status:         params.Status,
				CreatedByID:    params.CreatedByID,
			})
			if err == nil && op.ID != uuid.Nil && (op.TargetType != params.TargetType || op.TargetKey != params.TargetKey || op.OperationType != params.OperationType || !bytes.Equal(op.Payload, params.Payload)) {
				return sqlc.CatalogOperation{}, errCatalogOperationIdempotencyConflict
			}
		}
	}
	if op.ID == uuid.Nil && err == nil {
		op, err = q.CreateCatalogOperation(ctx, params)
	}
	return op, err
}

func (h *CatalogHandler) enqueueOperation(ctx context.Context, targetType, targetKey, operationType string, env catalogOperationEnvelope, userID pgtype.UUID) (sqlc.CatalogOperation, error) {
	op, err := createCatalogOperation(ctx, h.queries, targetType, targetKey, operationType, env, userID)
	if err == nil {
		h.TriggerReconcile()
	}
	return op, err
}

func catalogOperationResponse(op sqlc.CatalogOperation) map[string]any {
	return map[string]any{
		"id":            op.ID.String(),
		"targetType":    op.TargetType,
		"targetKey":     op.TargetKey,
		"operationType": op.OperationType,
		"status":        op.Status,
		"attemptCount":  op.AttemptCount,
		"startedAt":     nullablePgTime(op.StartedAt),
		"completedAt":   nullablePgTime(op.CompletedAt),
		"errorMessage":  op.ErrorMessage,
		"createdAt":     op.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":     op.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func catalogOperationEventsResponse(events []sqlc.CatalogOperationEvent) []map[string]any {
	out := make([]map[string]any, 0, len(events))
	for _, event := range events {
		out = append(out, map[string]any{
			"id":        event.ID.String(),
			"level":     event.Level,
			"stage":     event.Stage,
			"message":   event.Message,
			"detail":    decodeJSONMap(event.Detail),
			"createdAt": event.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out
}

func (h *CatalogHandler) operationPreview(ctx context.Context, op sqlc.CatalogOperation) map[string]any {
	resp := catalogOperationResponse(op)
	if events, err := h.queries.ListCatalogOperationEvents(ctx, op.ID); err == nil && len(events) > 0 {
		resp["eventsPreview"] = catalogOperationEventsResponse(lastCatalogEvents(events, 3))
	}
	return resp
}

func lastCatalogEvents(events []sqlc.CatalogOperationEvent, n int) []sqlc.CatalogOperationEvent {
	if len(events) <= n {
		return events
	}
	return events[len(events)-n:]
}

func (h *CatalogHandler) processPendingOperations(ctx context.Context) {
	// Claim under the lock, then release before
	// the (potentially 10-minute) helm dispatch so other clusters'
	// operations are not stalled behind one stuck install.
	dispatchClaimed(ctx, h.helmConcurrency, h.claimPendingCatalogOperations(ctx))
}

// claimPendingCatalogOperations holds h.mu just long enough to mark
// supersession + claim the batch ("running" state). Returns the rows
// it owns wrapped as claimedOps; dispatchClaimed runs them outside the
// lock via per-row Run/OnComplete/OnFailure closures.
func (h *CatalogHandler) claimPendingCatalogOperations(ctx context.Context) []claimedOp {
	h.mu.Lock()
	defer h.mu.Unlock()
	ops, err := h.queries.ListPendingCatalogOperations(ctx, 20)
	if err != nil {
		return nil
	}
	return claimLatestOperations(ctx, ops, operationRunnerConfig[sqlc.CatalogOperation]{
		ID:        func(op sqlc.CatalogOperation) uuid.UUID { return op.ID },
		TargetKey: func(op sqlc.CatalogOperation) string { return op.TargetType + ":" + op.TargetKey },
		Status:    func(op sqlc.CatalogOperation) string { return op.Status },
		IsFreshRunning: func(op sqlc.CatalogOperation, now time.Time) bool {
			return op.StartedAt.Valid && now.Sub(op.StartedAt.Time) < time.Minute
		},
		Supersede: func(ctx context.Context, op sqlc.CatalogOperation) {
			h.recordCatalogOperationEvent(ctx, op.ID, "info", "queue", "operation superseded by newer desired state", map[string]any{
				"targetType": op.TargetType,
				"targetKey":  op.TargetKey,
			})
			_, _ = h.queries.MarkCatalogOperationSuperseded(ctx, sqlc.MarkCatalogOperationSupersededParams{
				ID:           op.ID,
				ErrorMessage: operationSupersededMessage,
			})
		},
		MarkRunning: func(ctx context.Context, op sqlc.CatalogOperation) (sqlc.CatalogOperation, error) {
			running, err := h.queries.MarkCatalogOperationRunning(ctx, op.ID)
			if err != nil {
				return sqlc.CatalogOperation{}, err
			}
			h.recordCatalogOperationEvent(ctx, running.ID, "info", "queue", "operation execution started", map[string]any{
				"operationType": running.OperationType,
				"targetType":    running.TargetType,
				"targetKey":     running.TargetKey,
				"attemptCount":  running.AttemptCount,
			})
			return running, nil
		},
		Claimed: func(running sqlc.CatalogOperation) claimedOp {
			return claimedOp{
				ID: running.ID,
				Run: func(ctx context.Context) error {
					return h.executeOperation(ctx, running)
				},
				OnComplete: func(ctx context.Context) {
					h.recordCatalogOperationEvent(ctx, running.ID, "info", "complete", "operation completed", map[string]any{})
					_, _ = h.queries.MarkCatalogOperationCompleted(ctx, running.ID)
				},
				OnFailure: func(ctx context.Context, err error) {
					h.recordCatalogOperationEvent(ctx, running.ID, "error", "complete", "operation failed", map[string]any{"error": err.Error()})
					_, _ = h.queries.MarkCatalogOperationFailed(ctx, sqlc.MarkCatalogOperationFailedParams{
						ID:           running.ID,
						ErrorMessage: err.Error(),
					})
					if h.log != nil {
						h.log.Warn("catalog operation failed", "id", running.ID.String(), "error", err)
					}
				},
			}
		},
	})
}

func (h *CatalogHandler) executeOperation(ctx context.Context, op sqlc.CatalogOperation) error {
	if h.helm == nil {
		return errors.New("helm requester not configured")
	}
	var env catalogOperationEnvelope
	if err := json.Unmarshal(op.Payload, &env); err != nil {
		return err
	}
	installationID, err := uuid.Parse(env.InstalledChartID)
	if err != nil {
		return err
	}
	installation, err := h.queries.GetInstalledChartByID(ctx, installationID)
	if err != nil {
		return err
	}
	clusterID := installation.ClusterID.String()
	// Every path below writes a terminal installed-chart status (success or
	// failed_*), so one deferred publish covers them all (P4.9).
	defer h.publishCatalogReleaseChanged(clusterID, installation.ID.String())
	switch op.OperationType {
	case "install":
		h.recordCatalogOperationEvent(ctx, op.ID, "info", "install", "installing catalog release", map[string]any{
			"clusterId":   clusterID,
			"releaseName": installation.ReleaseName,
			"namespace":   installation.Namespace,
		})
		result, err := h.sendHelm(ctx, clusterID, protocol.MsgHelmInstall, env)
		if err != nil {
			_ = h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{ID: installation.ID, Status: "failed_install", Revision: installation.Revision})
			return err
		}
		return h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{
			ID:       installation.ID,
			Status:   normalizeToolStatus(result.Status),
			Revision: int32(result.Revision),
		})
	case "upgrade":
		h.recordCatalogOperationEvent(ctx, op.ID, "info", "upgrade", "upgrading catalog release", map[string]any{
			"clusterId":   clusterID,
			"releaseName": installation.ReleaseName,
			"namespace":   installation.Namespace,
		})
		result, err := h.sendHelm(ctx, clusterID, protocol.MsgHelmUpgrade, env)
		if err != nil {
			_ = h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{ID: installation.ID, Status: "failed_upgrade", Revision: installation.Revision})
			return err
		}
		_, err = h.queries.UpdateInstalledChartValues(ctx, sqlc.UpdateInstalledChartValuesParams{
			ID:             installation.ID,
			ValuesOverride: env.ValuesOverride,
			Status:         normalizeToolStatus(result.Status),
			Revision:       int32(result.Revision),
		})
		return err
	case "rollback":
		h.recordCatalogOperationEvent(ctx, op.ID, "info", "rollback", "rolling back catalog release", map[string]any{
			"clusterId":        clusterID,
			"releaseName":      installation.ReleaseName,
			"namespace":        installation.Namespace,
			"rollbackRevision": env.RollbackRevision,
		})
		result, err := h.helm.Do(ctx, clusterID, protocol.MsgHelmRollback, protocol.HelmRequestPayload{
			ReleaseName: installation.ReleaseName,
			Namespace:   installation.Namespace,
			Revision:    env.RollbackRevision,
		})
		if err != nil {
			_ = h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{ID: installation.ID, Status: "failed_rollback", Revision: installation.Revision})
			return err
		}
		return h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{
			ID:       installation.ID,
			Status:   normalizeToolStatus(result.Status),
			Revision: int32(result.Revision),
		})
	case "uninstall":
		h.recordCatalogOperationEvent(ctx, op.ID, "info", "uninstall", "uninstalling catalog release", map[string]any{
			"clusterId":   clusterID,
			"releaseName": installation.ReleaseName,
			"namespace":   installation.Namespace,
		})
		_, err := h.helm.Do(ctx, clusterID, protocol.MsgHelmUninstall, protocol.HelmRequestPayload{
			ReleaseName: installation.ReleaseName,
			Namespace:   installation.Namespace,
		})
		if err != nil {
			_ = h.queries.UpdateInstalledChartStatus(ctx, sqlc.UpdateInstalledChartStatusParams{ID: installation.ID, Status: "failed_uninstall", Revision: installation.Revision})
			return err
		}
		return h.queries.DeleteInstalledChart(ctx, installation.ID)
	default:
		return fmt.Errorf("unsupported catalog operation type: %s", op.OperationType)
	}
}

// checkCatalogMaintenanceWindow consults the migration-057 gate and
// writes the 409/202 response when the operation is blocked. Returns
// true when the caller should stop. Best-effort cluster lookup —
// failing to resolve labels leaves the selector check on an empty
// label set rather than erroring the user out at gate time.
func (h *CatalogHandler) checkCatalogMaintenanceWindow(w http.ResponseWriter, r *http.Request, clusterID uuid.UUID, opType string) bool {
	if h == nil || h.maintenanceGate == nil {
		return false
	}
	labels := map[string]string{}
	if cluster, err := h.queries.GetClusterByID(r.Context(), clusterID); err == nil {
		labels = MaintenanceGateClusterLabels(cluster)
	}
	return EnforceMaintenanceWindow(w, r, h.maintenanceGate, opType, labels,
		pgtype.UUID{Bytes: clusterID, Valid: true}, pgtype.UUID{})
}

func (h *CatalogHandler) sendHelm(ctx context.Context, clusterID string, msgType protocol.MessageType, env catalogOperationEnvelope) (*protocol.HelmResultPayload, error) {
	// Resolve ${vault://...} markers at execution time. The operation
	// payload (and the installed_charts row) persist the ORIGINAL
	// marker-bearing blob, so no cleartext secret ever lands in
	// catalog_operations.payload; substitution happens here, in-memory,
	// right before the values are shipped to the cluster. This is also
	// the path that resolves markers on the UPGRADE flow, which
	// previously shipped the literal placeholder through to Helm.
	blob, err := vaultResolveBlob(ctx, h.vaultResolver, uuid.Nil, env.ValuesOverride)
	if err != nil {
		return nil, err
	}
	var values map[string]any
	if blob != "" {
		if err := yaml.Unmarshal([]byte(blob), &values); err != nil {
			return nil, err
		}
	}
	return h.helm.Do(ctx, clusterID, msgType, protocol.HelmRequestPayload{
		ReleaseName: env.ReleaseName,
		Namespace:   env.Namespace,
		ChartName:   env.ChartName,
		RepoURL:     env.RepoURL,
		Version:     env.Version,
		Values:      values,
	})
}

func (h *CatalogHandler) resolveInstalledChartRelease(ctx context.Context, installed sqlc.InstalledChart) (sqlc.HelmChartVersion, sqlc.HelmChart, sqlc.HelmRepository, error) {
	if !installed.ChartVersionID.Valid {
		return sqlc.HelmChartVersion{}, sqlc.HelmChart{}, sqlc.HelmRepository{}, errors.New("installed chart has no chart version")
	}
	versionID := uuid.UUID(installed.ChartVersionID.Bytes)
	version, err := h.queries.GetHelmChartVersionByID(ctx, versionID)
	if err != nil {
		return sqlc.HelmChartVersion{}, sqlc.HelmChart{}, sqlc.HelmRepository{}, err
	}
	chart, err := h.queries.GetHelmChartByID(ctx, version.ChartID)
	if err != nil {
		return sqlc.HelmChartVersion{}, sqlc.HelmChart{}, sqlc.HelmRepository{}, err
	}
	repo, err := h.queries.GetHelmRepositoryByID(ctx, chart.RepositoryID)
	if err != nil {
		return sqlc.HelmChartVersion{}, sqlc.HelmChart{}, sqlc.HelmRepository{}, err
	}
	return version, chart, repo, nil
}

func uuidFromPg(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

func (h *CatalogHandler) recordCatalogOperationEvent(ctx context.Context, operationID uuid.UUID, level, stage, message string, detail map[string]any) {
	if h == nil || h.queries == nil {
		return
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = json.RawMessage(`{}`)
	}
	_, _ = h.queries.CreateCatalogOperationEvent(ctx, sqlc.CreateCatalogOperationEventParams{
		OperationID: operationID,
		Level:       level,
		Stage:       stage,
		Message:     message,
		Detail:      raw,
	})
}

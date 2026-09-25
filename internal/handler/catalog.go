package handler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/catalogapp"
	"github.com/alphabravocompany/astronomer-go/internal/events"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	avault "github.com/alphabravocompany/astronomer-go/internal/vault"
	"github.com/alphabravocompany/astronomer-go/internal/worker/tasks"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/sync/singleflight"
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
	ListFilteredHelmCharts(context.Context, sqlc.ListFilteredHelmChartsParams) ([]sqlc.HelmChart, error)
	CountFilteredHelmCharts(context.Context, sqlc.CountFilteredHelmChartsParams) (int64, error)
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
	DeleteFailedInstallationsByCluster(context.Context, uuid.UUID) (int64, error)
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

// CatalogApplicationDelivery is the Flux-native application lifecycle seam.
type CatalogApplicationDelivery interface {
	Install(context.Context, catalogapp.InstallRequest) (catalogapp.InstallResult, error)
	Upgrade(context.Context, catalogapp.InstallRequest) (catalogapp.InstallResult, error)
	Uninstall(context.Context, uuid.UUID, pgtype.UUID) error
	Rollback(context.Context, uuid.UUID, pgtype.UUID, string) (catalogapp.InstallResult, error)
	Status(context.Context, uuid.UUID) (catalogapp.Status, error)
}

// CatalogHandler handles catalog endpoints (helm repositories, charts, installations).
type CatalogHandler struct {
	queries  CatalogQuerier
	helm     HelmRequester
	delivery CatalogApplicationDelivery
	log      *slog.Logger
	authz    authorizationSupport
	mu       sync.Mutex
	trigger  chan struct{}
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
	// chartHydration collapses concurrent README/values cache misses for the
	// same immutable chart version. The durable row remains the cache; this
	// group only prevents an in-flight download stampede.
	chartHydration        singleflight.Group
	chartHydrationTimeout time.Duration
}

func (h *CatalogHandler) SetApplicationDelivery(delivery CatalogApplicationDelivery) {
	if h != nil {
		h.delivery = delivery
	}
}

// SetRunTx wires the production transaction boundary used for catalog state,
// operation intent, and mandatory audit intent.
func (h *CatalogHandler) SetRunTx(runTx catalogRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *CatalogHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

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
		queries:               queries,
		log:                   slog.Default(),
		trigger:               make(chan struct{}, 1),
		chartHydrationTimeout: defaultChartHydrationTimeout,
	}
}

func NewCatalogHandlerWithHelm(queries CatalogQuerier, helm HelmRequester) *CatalogHandler {
	return &CatalogHandler{
		queries:               queries,
		helm:                  helm,
		log:                   slog.Default(),
		trigger:               make(chan struct{}, 1),
		chartHydrationTimeout: defaultChartHydrationTimeout,
	}
}

func (h *CatalogHandler) SetLogger(log *slog.Logger) {
	if log != nil {
		h.log = log
	}
}

func (h *CatalogHandler) SetAuthorization(engine *rbac.Engine, querier rbac.BindingQuerier) {
	h.authz.SetAuthorization(engine, querier)
}

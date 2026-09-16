// Per-project ("BYO") Helm catalogs handler — sprint 061.
//
// Routes:
//   GET    /api/v1/projects/{project_id}/catalogs/                       List visible (own + subscribed + globals)
//   POST   /api/v1/projects/{project_id}/catalogs/                       Create project-owned catalog (auto-subscribes)
//   POST   /api/v1/projects/{project_id}/catalogs/{catalog_id}/subscribe/  Subscribe to an existing public (or another-project) catalog
//   DELETE /api/v1/projects/{project_id}/catalogs/{catalog_id}/          Unsubscribe; deletes catalog when project-owned
//   GET    /api/v1/projects/{project_id}/catalogs/{catalog_id}/charts/   List charts in catalog (refuses without subscription)
//
// Access model:
//   - "Globals" = helm_repositories.owner_project_id IS NULL. Always
//     visible to every project. Subscribing makes the relationship
//     explicit (used by ListProjectSubscriptions) but doesn't grant
//     additional access — globals are universally browseable.
//   - "Own" = owner_project_id == caller's project_id. Auto-subscribed
//     at create time so the subscription row drives the UI's "active"
//     state without special-casing.
//   - "Foreign-private" = owner_project_id IS NOT NULL AND != caller's
//     project_id. Subscription is REJECTED for non-superusers. Superuser
//     bypass exists for shared-curation projects.
//
// Unsubscribe semantics (the load-bearing nuance):
//   - DELETE on a SUBSCRIBED public/foreign catalog removes only the
//     subscription row. The catalog persists.
//   - DELETE on an OWN catalog removes the helm_repositories row entirely
//     (the project is its sole owner; the CASCADE on owner_project_id
//     and on the subscriptions FK cleans up everything else).
//   The audit emits distinct keys so the security trail can tell which
//   semantics fired.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	paging "github.com/alphabravocompany/astronomer-go/internal/pagination"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ProjectCatalogQuerier is the database surface the handler needs.
// The production *sqlc.Queries satisfies it; tests stand up a narrow fake.
type ProjectCatalogQuerier interface {
	// Project existence (FK validation).
	GetProjectByID(ctx context.Context, id uuid.UUID) (sqlc.Project, error)
	// User identity for the superuser bypass.
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlc.User, error)
	// Catalog reads (migration 061).
	ListCatalogsForProject(ctx context.Context, projectID uuid.UUID) ([]sqlc.HelmRepositoryWithOwner, error)
	ListProjectOwnedCatalogs(ctx context.Context, projectID uuid.UUID) ([]sqlc.HelmRepositoryWithOwner, error)
	ListProjectSubscriptions(ctx context.Context, projectID uuid.UUID) ([]sqlc.ProjectCatalogSubscription, error)
	GetHelmRepositoryWithOwner(ctx context.Context, id uuid.UUID) (sqlc.HelmRepositoryWithOwner, error)
	GetProjectCatalogSubscription(ctx context.Context, arg sqlc.GetProjectCatalogSubscriptionParams) (sqlc.ProjectCatalogSubscription, error)
	GetCatalogVisibilityForProject(ctx context.Context, projectID, catalogID uuid.UUID) (sqlc.CatalogVisibility, error)
	// Chart browse path.
	ListChartsByRepository(ctx context.Context, arg sqlc.ListChartsByRepositoryParams) ([]sqlc.HelmChart, error)
}

// ProjectCatalogMutationTx is the transaction-bound write surface for
// project-owned repositories and subscriptions. It prevents the historical
// half-created state where the repository committed but auto-subscribe or its
// audit evidence failed.
type ProjectCatalogMutationTx interface {
	audit.OutboxQuerier
	CreateProjectOwnedCatalog(context.Context, sqlc.CreateProjectOwnedCatalogParams) (sqlc.HelmRepository, error)
	CreateProjectCatalogSubscription(context.Context, sqlc.CreateProjectCatalogSubscriptionParams) (sqlc.ProjectCatalogSubscription, error)
	DeleteProjectCatalogSubscription(context.Context, sqlc.DeleteProjectCatalogSubscriptionParams) error
	DeleteHelmRepository(context.Context, uuid.UUID) error
}

type projectCatalogRunTxFunc func(context.Context, func(ProjectCatalogMutationTx) error) error

// ProjectCatalogHandler owns /api/v1/projects/{project_id}/catalogs/*.
type ProjectCatalogHandler struct {
	queries ProjectCatalogQuerier
	// encryptor seals helm_repositories.auth_config (migration 145). This
	// handler writes the same table as CatalogHandler, so it has to seal the
	// same way or a project-owned private catalog would be the one row shape
	// still storing its password in the clear.
	encryptor *auth.Encryptor
	runTx     projectCatalogRunTxFunc
}

// NewProjectCatalogHandler constructs the handler.
func NewProjectCatalogHandler(q ProjectCatalogQuerier) *ProjectCatalogHandler {
	return &ProjectCatalogHandler{queries: q}
}

func (h *ProjectCatalogHandler) SetRunTx(runTx projectCatalogRunTxFunc) {
	if h != nil {
		h.runTx = runTx
	}
}

func (h *ProjectCatalogHandler) TransactionalAuditWired() bool { return h != nil && h.runTx != nil }

// SetEncryptor wires the Fernet encryptor used for catalog credentials at rest.
func (h *ProjectCatalogHandler) SetEncryptor(encryptor *auth.Encryptor) {
	if h == nil {
		return
	}
	h.encryptor = encryptor
}

// sealer returns h.encryptor as a catalog.Encryptor, or a genuinely nil
// interface when none is wired (see CatalogHandler.decryptor for why the
// explicit nil matters).
func (h *ProjectCatalogHandler) sealer() catalog.Encryptor {
	if h == nil || h.encryptor == nil {
		return nil
	}
	return h.encryptor
}

// --- Wire shapes -----------------------------------------------------------

// CatalogResponse is the per-row shape returned by List and the write echoes.
// The visibility field is computed against the URL's project_id so the UI
// can render "Private", "Subscribed", or "Global" badges without a second
// round-trip.
type CatalogResponse struct {
	ID             uuid.UUID `json:"id"`
	Name           string    `json:"name"`
	URL            string    `json:"url"`
	RepoType       string    `json:"repo_type"`
	Description    string    `json:"description"`
	AuthType       string    `json:"auth_type"`
	Enabled        bool      `json:"enabled"`
	OwnerProjectID *string   `json:"owner_project_id"`
	Visibility     string    `json:"visibility"`
	CreatedAt      string    `json:"created_at"`
	UpdatedAt      string    `json:"updated_at"`
	LastSyncedAt   string    `json:"last_synced_at,omitempty"`
}

// CreateProjectCatalogRequest is the POST body for the create-private path.
// Mirrors the admin CreateRepoRequest minus is_default (which is a
// global-only concept).
// openapi:request CreateProjectCatalogRequest
// openapi:request-allow username  decoded only so rejectMisplacedCredentials can answer 400; never accepted input, so not documented
// openapi:request-allow password  decoded only so rejectMisplacedCredentials can answer 400; never accepted input, so not documented
// openapi:request-allow token  decoded only so rejectMisplacedCredentials can answer 400; never accepted input, so not documented
type CreateProjectCatalogRequest struct {
	Name        string          `json:"name"`
	URL         string          `json:"url"`
	RepoType    string          `json:"repo_type"`
	Description string          `json:"description"`
	AuthType    string          `json:"auth_type"`
	AuthConfig  json.RawMessage `json:"auth_config"`
	Enabled     *bool           `json:"enabled,omitempty"`

	// Rejected, not accepted — see misplacedCredentialFields. This endpoint
	// writes the same helm_repositories row as CatalogHandler.CreateRepo, so
	// it owes callers the same 400 instead of quietly dropping a credential
	// posted at the top level.
	misplacedCredentialFields
}

// --- Helpers ---------------------------------------------------------------

func toCatalogResponse(c sqlc.HelmRepositoryWithOwner, callerProjectID uuid.UUID, subscribed bool) CatalogResponse {
	var owner *string
	visibility := "public"
	if c.OwnerProjectID.Valid {
		s := uuid.UUID(c.OwnerProjectID.Bytes).String()
		owner = &s
		if uuid.UUID(c.OwnerProjectID.Bytes) == callerProjectID {
			visibility = "own"
		} else {
			visibility = "foreign_private"
		}
	}
	if subscribed && visibility == "public" {
		visibility = "subscribed_public"
	}
	resp := CatalogResponse{
		ID:             c.ID,
		Name:           c.Name,
		URL:            c.Url,
		RepoType:       c.RepoType,
		Description:    c.Description,
		AuthType:       c.AuthType,
		Enabled:        c.Enabled,
		OwnerProjectID: owner,
		Visibility:     visibility,
		CreatedAt:      c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:      c.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if c.LastSyncedAt.Valid {
		resp.LastSyncedAt = c.LastSyncedAt.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	return resp
}

// callerIsSuperuser checks the caller's superuser bit. Returns false when
// the caller is unauthenticated or the DB lookup fails — we'd rather
// reject than accidentally promote.
func (h *ProjectCatalogHandler) callerIsSuperuser(r *http.Request) bool {
	user, ok := reqctx.AuthenticatedUser(r.Context())
	if !ok || user == nil {
		return false
	}
	id, err := uuid.Parse(user.ID)
	if err != nil {
		return false
	}
	row, err := h.queries.GetUserByID(r.Context(), id)
	if err != nil {
		return false
	}
	return row.IsSuperuser
}

// --- Handlers --------------------------------------------------------------

// List handles GET /api/v1/projects/{project_id}/catalogs/.
//
// Returns globals (always) + the project's own (private) catalogs + any
// catalogs the project has explicitly subscribed to. The Visibility
// field discriminates the three buckets.
func (h *ProjectCatalogHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseProjectID(w, r)
	if !ok {
		return
	}
	if _, err := h.queries.GetProjectByID(r.Context(), projectID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
		return
	}
	rows, err := h.queries.ListCatalogsForProject(r.Context(), projectID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list catalogs")
		return
	}
	// Build a subscription-set so we can flip visibility = "subscribed_public"
	// for public catalogs the project has explicitly opted into.
	subs, err := h.queries.ListProjectSubscriptions(r.Context(), projectID)
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list subscriptions")
		return
	}
	subSet := map[uuid.UUID]struct{}{}
	for _, s := range subs {
		subSet[s.CatalogID] = struct{}{}
	}
	out := make([]CatalogResponse, 0, len(rows))
	for _, row := range rows {
		_, subbed := subSet[row.ID]
		out = append(out, toCatalogResponse(row, projectID, subbed))
	}
	// ListCatalogsForProject returns the full visible set in one query (no
	// SQL limit/offset), so the page is the whole authorized result.
	// add a counted, paged query if a project's visible catalog count ever
	// grows unbounded.
	paging.Write(w, out, paging.Exact(len(out), len(out), 0, len(out)))
}

// Create handles POST /api/v1/projects/{project_id}/catalogs/.
//
// Creates a project-owned (private) catalog row and auto-subscribes the
// project so the catalog shows up in subsequent List responses with
// Visibility="own".
func (h *ProjectCatalogHandler) Create(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseProjectID(w, r)
	if !ok {
		return
	}
	if _, err := h.queries.GetProjectByID(r.Context(), projectID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
		return
	}
	var req CreateProjectCatalogRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidBody, "Invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Catalog name is required")
		return
	}
	if strings.TrimSpace(req.URL) == "" {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, "Catalog URL is required")
		return
	}
	cleanURL, urlErr := catalog.ValidateRepositoryURL(req.URL)
	if urlErr != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.ValidationError, urlErr.Error())
		return
	}
	req.URL = cleanURL
	if !rejectMisplacedCredentials(w, r, req.misplacedCredentialFields) {
		return
	}
	if req.AuthConfig == nil {
		req.AuthConfig = json.RawMessage(`{}`)
	}
	if req.RepoType == "" && IsOCIRepo(req.URL) {
		req.RepoType = "oci"
	}
	// Both halves of the credential, same as CatalogHandler.CreateRepo: an
	// auth_config carrying a username/password with no auth_type would be
	// stored and encrypted and then never sent, because ApplyIndexAuth
	// short-circuits on an empty auth_type. A stated auth_type always wins.
	if req.AuthType == "" {
		req.AuthType = catalog.InferAuthType(req.AuthConfig)
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if h.sealer() == nil && catalog.HasAuthConfigSecret(req.AuthConfig) {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.CryptoError, "Catalog credential encryption is unavailable")
		return
	}
	sealed, publicCfg, err := catalog.SealAuthConfig(req.AuthConfig, h.sealer())
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to secure catalog credentials")
		return
	}
	params := sqlc.CreateProjectOwnedCatalogParams{
		Name:                req.Name,
		Url:                 req.URL,
		RepoType:            req.RepoType,
		Description:         req.Description,
		IsDefault:           false,
		AuthType:            req.AuthType,
		AuthConfig:          publicCfg,
		AuthConfigEncrypted: sealed,
		Enabled:             enabled,
		CreatedByID:         currentUserUUID(r),
		OwnerProjectID: pgtype.UUID{
			Bytes: projectID,
			Valid: true,
		},
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "project catalog transaction runner is not configured")
		return
	}
	var cat sqlc.HelmRepository
	err = h.runTx(r.Context(), func(q ProjectCatalogMutationTx) error {
		var mutationErr error
		cat, mutationErr = q.CreateProjectOwnedCatalog(r.Context(), params)
		if mutationErr != nil {
			return mutationErr
		}
		if _, mutationErr = q.CreateProjectCatalogSubscription(r.Context(), sqlc.CreateProjectCatalogSubscriptionParams{
			ProjectID: projectID, CatalogID: cat.ID, CreatedBy: currentUserUUID(r),
		}); mutationErr != nil {
			return mutationErr
		}
		return recordAuditOutbox(r, q, "project.catalog.owned_created", "helm_repository", cat.ID.String(), cat.Name, http.StatusCreated, map[string]any{
			"project_id": projectID.String(), "repo_type": cat.RepoType,
		})
	})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.CreateError, "Failed to create and subscribe project catalog")
		return
	}
	w.Header().Set("Location", "/api/v1/projects/"+projectID.String()+"/catalogs/"+cat.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, toCatalogResponse(cat, projectID, true))
}

// Subscribe handles POST /api/v1/projects/{project_id}/catalogs/{catalog_id}/subscribe/.
//
// Allows a project admin to subscribe to a PUBLIC catalog (or another
// project's private catalog only if the caller is a superuser). Idempotent:
// re-subscribing returns the existing row with 200.
func (h *ProjectCatalogHandler) Subscribe(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseProjectID(w, r)
	if !ok {
		return
	}
	catalogID, err := uuid.Parse(chi.URLParam(r, "catalog_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid catalog ID")
		return
	}
	if _, err := h.queries.GetProjectByID(r.Context(), projectID); err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Project not found")
		return
	}
	cat, err := h.queries.GetHelmRepositoryWithOwner(r.Context(), catalogID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Catalog not found")
		return
	}
	// Foreign-private gate: a non-superuser can't subscribe to another
	// project's private catalog. Note: subscribing to your OWN catalog
	// is also a no-op (it was auto-subscribed at create time) — we let
	// the UNIQUE constraint catch that case via the idempotent path.
	if cat.OwnerProjectID.Valid && uuid.UUID(cat.OwnerProjectID.Bytes) != projectID {
		if !h.callerIsSuperuser(r) {
			RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Cannot subscribe to another project's private catalog")
			return
		}
	}
	auditKey := "project.catalog.subscribed_public"
	if cat.OwnerProjectID.Valid && uuid.UUID(cat.OwnerProjectID.Bytes) != projectID {
		auditKey = "project.catalog.subscribed_foreign"
	}
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "project catalog transaction runner is not configured")
		return
	}
	var row sqlc.ProjectCatalogSubscription
	err = h.runTx(r.Context(), func(q ProjectCatalogMutationTx) error {
		var mutationErr error
		row, mutationErr = q.CreateProjectCatalogSubscription(r.Context(), sqlc.CreateProjectCatalogSubscriptionParams{
			ProjectID: projectID, CatalogID: catalogID, CreatedBy: currentUserUUID(r),
		})
		if mutationErr != nil {
			return mutationErr
		}
		return recordAuditOutbox(r, q, auditKey, "helm_repository", cat.ID.String(), cat.Name, http.StatusCreated, map[string]any{
			"project_id": projectID.String(),
		})
	})
	if err != nil {
		// A duplicate subscription is a genuine no-op and therefore needs no
		// second audit row. The failed transaction is discarded before this
		// read, so PostgreSQL's aborted-transaction state cannot leak here.
		existing, lookupErr := h.queries.GetProjectCatalogSubscription(r.Context(), sqlc.GetProjectCatalogSubscriptionParams{
			ProjectID: projectID, CatalogID: catalogID,
		})
		if lookupErr == nil {
			RespondJSON(w, http.StatusOK, existing)
			return
		}
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.SubscribeError, "Failed to subscribe to catalog")
		return
	}
	w.Header().Set("Location", "/api/v1/projects/"+projectID.String()+"/catalogs/"+catalogID.String()+"/")
	RespondJSON(w, http.StatusCreated, row)
}

// Delete handles DELETE /api/v1/projects/{project_id}/catalogs/{catalog_id}/.
//
// Bifurcated semantics:
//   - When the catalog is project-owned by the caller, DELETE removes
//     the catalog ROW (the project is its sole owner). Cascade cleans
//     up subscriptions, charts, and chart_versions.
//   - When the catalog is global or foreign, DELETE removes only the
//     project's subscription row.
//
// The audit emits two distinct keys so the security feed can tell the
// "I removed a catalog" case apart from "I unsubscribed from one".
func (h *ProjectCatalogHandler) Delete(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseProjectID(w, r)
	if !ok {
		return
	}
	catalogID, err := uuid.Parse(chi.URLParam(r, "catalog_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid catalog ID")
		return
	}
	cat, err := h.queries.GetHelmRepositoryWithOwner(r.Context(), catalogID)
	if err != nil {
		RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Catalog not found")
		return
	}
	if cat.OwnerProjectID.Valid && uuid.UUID(cat.OwnerProjectID.Bytes) == projectID {
		// Owned by caller → drop the row entirely.
		if h.runTx == nil {
			RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "project catalog transaction runner is not configured")
			return
		}
		err := h.runTx(r.Context(), func(q ProjectCatalogMutationTx) error {
			if mutationErr := q.DeleteHelmRepository(r.Context(), catalogID); mutationErr != nil {
				return mutationErr
			}
			return recordAuditOutbox(r, q, "project.catalog.unsubscribed_owned_deleted", "helm_repository", cat.ID.String(), cat.Name, http.StatusNoContent, map[string]any{
				"project_id": projectID.String(),
			})
		})
		if err != nil {
			respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete catalog")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Else: unsubscribe only. Foreign-private catalogs without a
	// subscription have nothing to unsubscribe from; we return 204
	// regardless so the UI can rely on idempotency.
	if h.runTx == nil {
		RespondRequestError(w, r, http.StatusServiceUnavailable, apierror.RunnerUnwired, "project catalog transaction runner is not configured")
		return
	}
	err = h.runTx(r.Context(), func(q ProjectCatalogMutationTx) error {
		if mutationErr := q.DeleteProjectCatalogSubscription(r.Context(), sqlc.DeleteProjectCatalogSubscriptionParams{
			ProjectID: projectID, CatalogID: catalogID,
		}); mutationErr != nil {
			return mutationErr
		}
		return recordAuditOutbox(r, q, "project.catalog.unsubscribed_subscription", "helm_repository", cat.ID.String(), cat.Name, http.StatusNoContent, map[string]any{
			"project_id": projectID.String(),
		})
	})
	if err != nil {
		respondTransactionalMutationError(w, r, err, http.StatusInternalServerError, apierror.DeleteError, "Failed to unsubscribe from catalog")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListCharts handles GET /api/v1/projects/{project_id}/catalogs/{catalog_id}/charts/.
//
// Requires the project to have visibility on the catalog (own, subscribed,
// or globally public). Foreign-private catalogs return 403.
func (h *ProjectCatalogHandler) ListCharts(w http.ResponseWriter, r *http.Request) {
	projectID, ok := parseProjectID(w, r)
	if !ok {
		return
	}
	catalogID, err := uuid.Parse(chi.URLParam(r, "catalog_id"))
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid catalog ID")
		return
	}
	vis, err := h.queries.GetCatalogVisibilityForProject(r.Context(), projectID, catalogID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			RespondRequestError(w, r, http.StatusNotFound, apierror.NotFound, "Catalog not found")
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.LookupError, "Failed to resolve catalog visibility")
		return
	}
	if vis == sqlc.CatalogVisibilityForeignPrivate && !h.callerIsSuperuser(r) {
		RespondRequestError(w, r, http.StatusForbidden, apierror.Forbidden, "Catalog not accessible to this project")
		return
	}
	charts, err := h.queries.ListChartsByRepository(r.Context(), sqlc.ListChartsByRepositoryParams{
		RepositoryID: catalogID,
		Limit:        int32(queryLimit(r, 100)),
		Offset:       int32(queryOffset(r)),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list charts")
		return
	}
	// ListChartsByRepository is limit/offset paged but no COUNT query is
	// exposed for it, so has_more is inferred from a full page.
	// An exact total is intentionally omitted until a matching count is available.
	limit := queryLimit(r, 100)
	offset := queryOffset(r)
	paging.Write(w, charts, paging.FromPage(limit, offset, len(charts)))
}

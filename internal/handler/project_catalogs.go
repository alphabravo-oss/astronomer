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

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/handler/apierror"
	"github.com/alphabravocompany/astronomer-go/internal/server/middleware"
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
	// Catalog writes.
	CreateProjectOwnedCatalog(ctx context.Context, arg sqlc.CreateProjectOwnedCatalogParams) (sqlc.HelmRepositoryWithOwner, error)
	CreateProjectCatalogSubscription(ctx context.Context, arg sqlc.CreateProjectCatalogSubscriptionParams) (sqlc.ProjectCatalogSubscription, error)
	DeleteProjectCatalogSubscription(ctx context.Context, arg sqlc.DeleteProjectCatalogSubscriptionParams) error
	DeleteHelmRepository(ctx context.Context, id uuid.UUID) error
	// Chart browse path.
	ListChartsByRepository(ctx context.Context, arg sqlc.ListChartsByRepositoryParams) ([]sqlc.HelmChart, error)
}

// ProjectCatalogHandler owns /api/v1/projects/{project_id}/catalogs/*.
type ProjectCatalogHandler struct {
	queries ProjectCatalogQuerier
	auditor any // recordAudit type-asserts to auditWriterV1 internally
}

// NewProjectCatalogHandler constructs the handler.
func NewProjectCatalogHandler(q ProjectCatalogQuerier) *ProjectCatalogHandler {
	return &ProjectCatalogHandler{queries: q}
}

// SetAuditor wires the audit writer. Best-effort; nil-safe.
func (h *ProjectCatalogHandler) SetAuditor(a any) {
	if h == nil {
		return
	}
	h.auditor = a
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
type CreateProjectCatalogRequest struct {
	Name        string          `json:"name"`
	URL         string          `json:"url"`
	RepoType    string          `json:"repo_type"`
	Description string          `json:"description"`
	AuthType    string          `json:"auth_type"`
	AuthConfig  json.RawMessage `json:"auth_config"`
	Enabled     *bool           `json:"enabled,omitempty"`
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
	user, ok := middleware.GetAuthenticatedUser(r.Context())
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

func parseProjectID(r *http.Request) (uuid.UUID, error) {
	// Routes mount under /projects/{project_id}/... so chi exposes the
	// URL parameter under that key. The cloud_credentials handler uses
	// the same convention.
	raw := chi.URLParam(r, "project_id")
	if raw == "" {
		raw = chi.URLParam(r, "id")
	}
	return uuid.Parse(raw)
}

// --- Handlers --------------------------------------------------------------

// List handles GET /api/v1/projects/{project_id}/catalogs/.
//
// Returns globals (always) + the project's own (private) catalogs + any
// catalogs the project has explicitly subscribed to. The Visibility
// field discriminates the three buckets.
func (h *ProjectCatalogHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
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
	// SQL limit/offset), so the page is the whole result. // TODO(total):
	// add a counted, paged query if a project's visible catalog count ever
	// grows unbounded.
	RespondList(w, out, NewPagination(len(out), len(out), 0, len(out)))
}

// Create handles POST /api/v1/projects/{project_id}/catalogs/.
//
// Creates a project-owned (private) catalog row and auto-subscribes the
// project so the catalog shows up in subsequent List responses with
// Visibility="own".
func (h *ProjectCatalogHandler) Create(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
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
	if req.AuthConfig == nil {
		req.AuthConfig = json.RawMessage(`{}`)
	}
	if req.RepoType == "" && IsOCIRepo(req.URL) {
		req.RepoType = "oci"
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	cat, err := h.queries.CreateProjectOwnedCatalog(r.Context(), sqlc.CreateProjectOwnedCatalogParams{
		Name:        req.Name,
		Url:         req.URL,
		RepoType:    req.RepoType,
		Description: req.Description,
		IsDefault:   false,
		AuthType:    req.AuthType,
		AuthConfig:  req.AuthConfig,
		Enabled:     enabled,
		CreatedByID: currentUserUUID(r),
		OwnerProjectID: pgtype.UUID{
			Bytes: projectID,
			Valid: true,
		},
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.CreateError, "Failed to create catalog")
		return
	}
	// Auto-subscribe so the project sees it in browse + List immediately.
	// The subscription is what powers the "this catalog is mine" flag in
	// the audit trail of subsequent installs.
	if _, err := h.queries.CreateProjectCatalogSubscription(r.Context(), sqlc.CreateProjectCatalogSubscriptionParams{
		ProjectID: projectID,
		CatalogID: cat.ID,
		CreatedBy: currentUserUUID(r),
	}); err != nil {
		// Don't unwind the catalog — the failure mode is the next
		// List skipping it from the subscribed view, which is
		// recoverable from the UI. Log via audit detail.
		recordAudit(r, h.auditor, "project.catalog.owned_created", "helm_repository", cat.ID.String(), cat.Name, map[string]any{
			"project_id":            projectID.String(),
			"auto_subscribe_failed": err.Error(),
		})
		w.Header().Set("Location", "/api/v1/projects/"+projectID.String()+"/catalogs/"+cat.ID.String()+"/")
		RespondJSON(w, http.StatusCreated, toCatalogResponse(cat, projectID, false))
		return
	}
	recordAudit(r, h.auditor, "project.catalog.owned_created", "helm_repository", cat.ID.String(), cat.Name, map[string]any{
		"project_id": projectID.String(),
		"url":        cat.Url,
		"repo_type":  cat.RepoType,
	})
	w.Header().Set("Location", "/api/v1/projects/"+projectID.String()+"/catalogs/"+cat.ID.String()+"/")
	RespondJSON(w, http.StatusCreated, toCatalogResponse(cat, projectID, true))
}

// Subscribe handles POST /api/v1/projects/{project_id}/catalogs/{catalog_id}/subscribe/.
//
// Allows a project admin to subscribe to a PUBLIC catalog (or another
// project's private catalog only if the caller is a superuser). Idempotent:
// re-subscribing returns the existing row with 200.
func (h *ProjectCatalogHandler) Subscribe(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
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
	row, err := h.queries.CreateProjectCatalogSubscription(r.Context(), sqlc.CreateProjectCatalogSubscriptionParams{
		ProjectID: projectID,
		CatalogID: catalogID,
		CreatedBy: currentUserUUID(r),
	})
	if err != nil {
		// Idempotent fallback: if the UNIQUE constraint fired we already
		// have the row; look it up and return 200.
		existing, lookupErr := h.queries.GetProjectCatalogSubscription(r.Context(), sqlc.GetProjectCatalogSubscriptionParams{
			ProjectID: projectID,
			CatalogID: catalogID,
		})
		if lookupErr == nil {
			RespondJSON(w, http.StatusOK, existing)
			return
		}
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.SubscribeError, "Failed to subscribe to catalog")
		return
	}
	recordAudit(r, h.auditor, auditKey, "helm_repository", cat.ID.String(), cat.Name, map[string]any{
		"project_id": projectID.String(),
	})
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
	projectID, err := parseProjectID(r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
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
		if err := h.queries.DeleteHelmRepository(r.Context(), catalogID); err != nil {
			RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to delete catalog")
			return
		}
		recordAudit(r, h.auditor, "project.catalog.unsubscribed_owned_deleted", "helm_repository", cat.ID.String(), cat.Name, map[string]any{
			"project_id": projectID.String(),
		})
		w.WriteHeader(http.StatusNoContent)
		return
	}
	// Else: unsubscribe only. Foreign-private catalogs without a
	// subscription have nothing to unsubscribe from; we return 204
	// regardless so the UI can rely on idempotency.
	if err := h.queries.DeleteProjectCatalogSubscription(r.Context(), sqlc.DeleteProjectCatalogSubscriptionParams{
		ProjectID: projectID,
		CatalogID: catalogID,
	}); err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.DeleteError, "Failed to unsubscribe from catalog")
		return
	}
	recordAudit(r, h.auditor, "project.catalog.unsubscribed_subscription", "helm_repository", cat.ID.String(), cat.Name, map[string]any{
		"project_id": projectID.String(),
	})
	w.WriteHeader(http.StatusNoContent)
}

// ListCharts handles GET /api/v1/projects/{project_id}/catalogs/{catalog_id}/charts/.
//
// Requires the project to have visibility on the catalog (own, subscribed,
// or globally public). Foreign-private catalogs return 403.
func (h *ProjectCatalogHandler) ListCharts(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		RespondRequestError(w, r, http.StatusBadRequest, apierror.InvalidID, "Invalid project ID")
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
		Limit:        int32(queryInt(r, "limit", 100)),
		Offset:       int32(queryInt(r, "offset", 0)),
	})
	if err != nil {
		RespondRequestError(w, r, http.StatusInternalServerError, apierror.ListError, "Failed to list charts")
		return
	}
	// ListChartsByRepository is limit/offset paged but no COUNT query is
	// exposed for it, so has_more is inferred from a full page.
	// // TODO(total): add a CountChartsByRepository query.
	limit := queryInt(r, "limit", 100)
	offset := queryInt(r, "offset", 0)
	RespondList(w, charts, NewPaginationFromPage(limit, offset, len(charts)))
}

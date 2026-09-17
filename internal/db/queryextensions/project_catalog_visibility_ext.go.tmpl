package sqlc

import (
	"context"

	"github.com/google/uuid"
)

// HelmRepositoryWithOwner is kept as a compatibility alias for handlers that
// were written before owner_project_id was added to the generated
// HelmRepository model.
type HelmRepositoryWithOwner = HelmRepository

// CatalogVisibility classifies a (project, catalog) pair for the access check.
// The handler decides 200 vs 403 vs 404 based on this.
type CatalogVisibility string

const (
	CatalogVisibilityOwn              CatalogVisibility = "own"
	CatalogVisibilitySubscribedPublic CatalogVisibility = "subscribed_public"
	CatalogVisibilityPublic           CatalogVisibility = "public"
	CatalogVisibilityForeignPrivate   CatalogVisibility = "foreign_private"
	CatalogVisibilityUnauthorized     CatalogVisibility = "unauthorized"
)

// GetCatalogVisibilityForProject returns the relationship between a project and
// a catalog. Pure-DB; the handler layer mixes in superuser bypass logic.
func (q *Queries) GetCatalogVisibilityForProject(ctx context.Context, projectID, catalogID uuid.UUID) (CatalogVisibility, error) {
	cat, err := q.GetHelmRepositoryWithOwner(ctx, catalogID)
	if err != nil {
		return CatalogVisibilityUnauthorized, err
	}
	if cat.OwnerProjectID.Valid && cat.OwnerProjectID.Bytes == projectID {
		return CatalogVisibilityOwn, nil
	}
	if _, subErr := q.GetProjectCatalogSubscription(ctx, GetProjectCatalogSubscriptionParams{
		ProjectID: projectID,
		CatalogID: catalogID,
	}); subErr == nil {
		return CatalogVisibilitySubscribedPublic, nil
	}
	if !cat.OwnerProjectID.Valid {
		return CatalogVisibilityPublic, nil
	}
	return CatalogVisibilityForeignPrivate, nil
}

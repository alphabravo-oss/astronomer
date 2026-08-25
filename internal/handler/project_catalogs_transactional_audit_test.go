package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type projectCatalogPreflightFake struct {
	ProjectCatalogQuerier
	project sqlc.Project
}

func (q *projectCatalogPreflightFake) GetProjectByID(context.Context, uuid.UUID) (sqlc.Project, error) {
	return q.project, nil
}

type stagedProjectCatalogMutationTx struct {
	ProjectCatalogMutationTx
	catalog      sqlc.HelmRepository
	subscription sqlc.ProjectCatalogSubscription
	audits       []sqlc.UpsertAuditOutboxParams
	auditErr     error
}

func (tx *stagedProjectCatalogMutationTx) CreateProjectOwnedCatalog(_ context.Context, arg sqlc.CreateProjectOwnedCatalogParams) (sqlc.HelmRepository, error) {
	tx.catalog = sqlc.HelmRepository{
		ID: uuid.New(), Name: arg.Name, Url: arg.Url, RepoType: arg.RepoType, Description: arg.Description,
		AuthType: arg.AuthType, AuthConfig: arg.AuthConfig, Enabled: arg.Enabled, OwnerProjectID: arg.OwnerProjectID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	return tx.catalog, nil
}

func (tx *stagedProjectCatalogMutationTx) CreateProjectCatalogSubscription(_ context.Context, arg sqlc.CreateProjectCatalogSubscriptionParams) (sqlc.ProjectCatalogSubscription, error) {
	tx.subscription = sqlc.ProjectCatalogSubscription{ID: uuid.New(), ProjectID: arg.ProjectID, CatalogID: arg.CatalogID, CreatedBy: arg.CreatedBy, CreatedAt: time.Now().UTC()}
	return tx.subscription, nil
}

func (tx *stagedProjectCatalogMutationTx) UpsertAuditOutbox(_ context.Context, arg sqlc.UpsertAuditOutboxParams) (sqlc.AuditOutbox, error) {
	if tx.auditErr != nil {
		return sqlc.AuditOutbox{}, tx.auditErr
	}
	tx.audits = append(tx.audits, arg)
	return sqlc.AuditOutbox{ID: arg.ID, Action: arg.Action}, nil
}

func TestProjectCatalogCreateCommitsCatalogSubscriptionAndAuditTogether(t *testing.T) {
	for _, tc := range []struct {
		name       string
		auditErr   error
		wantStatus int
		wantCommit int
	}{
		{name: "commit all", wantStatus: http.StatusCreated, wantCommit: 1},
		{name: "audit failure rolls back catalog and subscription", auditErr: errors.New("audit unavailable"), wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projectID := uuid.New()
			preflight := &projectCatalogPreflightFake{project: sqlc.Project{ID: projectID}}
			h := NewProjectCatalogHandler(preflight)
			committedCatalogs, committedSubscriptions, committedAudits := 0, 0, 0
			h.SetRunTx(func(_ context.Context, fn func(ProjectCatalogMutationTx) error) error {
				tx := &stagedProjectCatalogMutationTx{auditErr: tc.auditErr}
				if err := fn(tx); err != nil {
					return err
				}
				if tx.catalog.ID != uuid.Nil {
					committedCatalogs++
				}
				if tx.subscription.ID != uuid.Nil {
					committedSubscriptions++
				}
				committedAudits += len(tx.audits)
				return nil
			})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/"+projectID.String()+"/catalogs/", strings.NewReader(`{"name":"private","url":"https://charts.example.com","repo_type":"helm"}`))
			routeContext := chi.NewRouteContext()
			routeContext.URLParams.Add("project_id", projectID.String())
			request = request.WithContext(context.WithValue(request.Context(), chi.RouteCtxKey, routeContext))
			response := httptest.NewRecorder()

			h.Create(response, request)

			if response.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tc.wantStatus, response.Body.String())
			}
			if committedCatalogs != tc.wantCommit || committedSubscriptions != tc.wantCommit || committedAudits != tc.wantCommit {
				t.Fatalf("committed catalog/subscription/audit = %d/%d/%d, want %d each", committedCatalogs, committedSubscriptions, committedAudits, tc.wantCommit)
			}
		})
	}
}

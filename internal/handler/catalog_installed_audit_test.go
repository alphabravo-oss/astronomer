package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

type installedCatalogAuditQuerier struct {
	*minimalCatalogQuerier

	clusters      map[uuid.UUID]sqlc.Cluster
	repos         map[uuid.UUID]sqlc.HelmRepository
	charts        map[uuid.UUID]sqlc.HelmChart
	versions      map[uuid.UUID]sqlc.HelmChartVersion
	installations map[uuid.UUID]sqlc.InstalledChart
	operations    []sqlc.CatalogOperation
	audits        []sqlc.CreateAuditLogV1Params
}

func newInstalledCatalogAuditQuerier() (*installedCatalogAuditQuerier, uuid.UUID, uuid.UUID) {
	clusterID := uuid.New()
	repoID := uuid.New()
	chartID := uuid.New()
	versionID := uuid.New()
	q := &installedCatalogAuditQuerier{
		minimalCatalogQuerier: &minimalCatalogQuerier{fakeProjectCatalogQuerier: newFakeProjectCatalogQuerier()},
		clusters:              map[uuid.UUID]sqlc.Cluster{},
		repos:                 map[uuid.UUID]sqlc.HelmRepository{},
		charts:                map[uuid.UUID]sqlc.HelmChart{},
		versions:              map[uuid.UUID]sqlc.HelmChartVersion{},
		installations:         map[uuid.UUID]sqlc.InstalledChart{},
	}
	q.clusters[clusterID] = sqlc.Cluster{ID: clusterID, Name: "alpha", Labels: json.RawMessage(`{}`)}
	q.repos[repoID] = sqlc.HelmRepository{ID: repoID, Name: "stable", Url: "https://charts.example.com", Enabled: true}
	q.charts[chartID] = sqlc.HelmChart{ID: chartID, RepositoryID: repoID, Name: "nginx", DisplayName: "NGINX"}
	q.versions[versionID] = sqlc.HelmChartVersion{ID: versionID, ChartID: chartID, Version: "1.2.3"}
	return q, clusterID, versionID
}

func (q *installedCatalogAuditQuerier) GetClusterByID(_ context.Context, id uuid.UUID) (sqlc.Cluster, error) {
	row, ok := q.clusters[id]
	if !ok {
		return sqlc.Cluster{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *installedCatalogAuditQuerier) GetHelmRepositoryByID(_ context.Context, id uuid.UUID) (sqlc.HelmRepository, error) {
	row, ok := q.repos[id]
	if !ok {
		return sqlc.HelmRepository{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *installedCatalogAuditQuerier) GetHelmChartByID(_ context.Context, id uuid.UUID) (sqlc.HelmChart, error) {
	row, ok := q.charts[id]
	if !ok {
		return sqlc.HelmChart{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *installedCatalogAuditQuerier) GetHelmChartVersionByID(_ context.Context, id uuid.UUID) (sqlc.HelmChartVersion, error) {
	row, ok := q.versions[id]
	if !ok {
		return sqlc.HelmChartVersion{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *installedCatalogAuditQuerier) CreateInstalledChart(_ context.Context, arg sqlc.CreateInstalledChartParams) (sqlc.InstalledChart, error) {
	now := time.Now()
	row := sqlc.InstalledChart{
		ID:             uuid.New(),
		ClusterID:      arg.ClusterID,
		ChartVersionID: arg.ChartVersionID,
		ReleaseName:    arg.ReleaseName,
		Namespace:      arg.Namespace,
		ValuesOverride: arg.ValuesOverride,
		Status:         arg.Status,
		Revision:       arg.Revision,
		Notes:          arg.Notes,
		InstalledByID:  arg.InstalledByID,
		RequestID:      arg.RequestID,
		ToolSlug:       arg.ToolSlug,
		PresetUsed:     arg.PresetUsed,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	q.installations[row.ID] = row
	return row, nil
}

func (q *installedCatalogAuditQuerier) GetInstalledChartByID(_ context.Context, id uuid.UUID) (sqlc.InstalledChart, error) {
	row, ok := q.installations[id]
	if !ok {
		return sqlc.InstalledChart{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *installedCatalogAuditQuerier) UpdateInstalledChartStatus(_ context.Context, arg sqlc.UpdateInstalledChartStatusParams) error {
	row, ok := q.installations[arg.ID]
	if !ok {
		return pgx.ErrNoRows
	}
	row.Status = arg.Status
	row.Revision = arg.Revision
	row.UpdatedAt = time.Now()
	q.installations[arg.ID] = row
	return nil
}

func (q *installedCatalogAuditQuerier) UpdateInstalledChartValues(_ context.Context, arg sqlc.UpdateInstalledChartValuesParams) (sqlc.InstalledChart, error) {
	row, ok := q.installations[arg.ID]
	if !ok {
		return sqlc.InstalledChart{}, pgx.ErrNoRows
	}
	row.ValuesOverride = arg.ValuesOverride
	row.Status = arg.Status
	row.UpdatedAt = time.Now()
	q.installations[arg.ID] = row
	return row, nil
}

func (q *installedCatalogAuditQuerier) CreateCatalogOperation(_ context.Context, arg sqlc.CreateCatalogOperationParams) (sqlc.CatalogOperation, error) {
	now := time.Now()
	row := sqlc.CatalogOperation{
		ID:            uuid.New(),
		TargetType:    arg.TargetType,
		TargetKey:     arg.TargetKey,
		OperationType: arg.OperationType,
		Payload:       arg.Payload,
		Status:        arg.Status,
		CreatedByID:   arg.CreatedByID,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	q.operations = append(q.operations, row)
	return row, nil
}

func (q *installedCatalogAuditQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	q.audits = append(q.audits, arg)
	return nil
}

func TestCatalogInstalledMutationsAreAudited(t *testing.T) {
	q, clusterID, versionID := newInstalledCatalogAuditQuerier()
	h := NewCatalogHandler(q)

	createBody, _ := json.Marshal(map[string]any{
		"cluster_id":       clusterID.String(),
		"chart_version_id": versionID.String(),
		"release_name":     "nginx",
		"namespace":        "apps",
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/catalog/installed/", bytes.NewReader(createBody))
	createRec := httptest.NewRecorder()
	h.CreateInstalledChart(createRec, createReq)
	if createRec.Code != http.StatusAccepted {
		t.Fatalf("create installed chart status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	if len(q.installations) != 1 {
		t.Fatalf("installations=%d, want 1", len(q.installations))
	}
	var installation sqlc.InstalledChart
	for _, row := range q.installations {
		installation = row
	}
	assertInstalledCatalogAudit(t, q.audits[0], "catalog.installation.create", installation.ID.String(), "nginx")
	assertAuditDetail(t, q.audits[0].Detail, "cluster_id", clusterID.String())

	upgradeBody, _ := json.Marshal(map[string]any{"values_override": "replicaCount: 3\n"})
	upgradeReq := httptest.NewRequest(http.MethodPut, "/api/v1/catalog/installed/"+installation.ID.String()+"/upgrade/", bytes.NewReader(upgradeBody))
	upgradeReq = withChiParams(upgradeReq, map[string]string{"id": installation.ID.String()})
	upgradeRec := httptest.NewRecorder()
	h.UpgradeInstalledChart(upgradeRec, upgradeReq)
	if upgradeRec.Code != http.StatusAccepted {
		t.Fatalf("upgrade installed chart status=%d body=%s", upgradeRec.Code, upgradeRec.Body.String())
	}
	assertInstalledCatalogAudit(t, q.audits[1], "catalog.installation.upgrade", installation.ID.String(), "nginx")
	assertAuditDetail(t, q.audits[1].Detail, "cluster_id", clusterID.String())

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/catalog/installed/"+installation.ID.String()+"/", nil)
	deleteReq = withChiParams(deleteReq, map[string]string{"id": installation.ID.String()})
	deleteRec := httptest.NewRecorder()
	h.DeleteInstalledChart(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusAccepted {
		t.Fatalf("delete installed chart status=%d body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	assertInstalledCatalogAudit(t, q.audits[2], "catalog.installation.delete", installation.ID.String(), "nginx")
	assertAuditDetail(t, q.audits[2].Detail, "cluster_id", clusterID.String())
}

func assertInstalledCatalogAudit(t *testing.T, row sqlc.CreateAuditLogV1Params, action, resourceID, resourceName string) {
	t.Helper()
	if row.Action != action {
		t.Fatalf("audit action=%q want %q; row=%+v", row.Action, action, row)
	}
	if row.ResourceType != "installed_chart" {
		t.Fatalf("audit resource_type=%q want installed_chart; row=%+v", row.ResourceType, row)
	}
	if row.ResourceID != resourceID || row.ResourceName != resourceName {
		t.Fatalf("audit target=(%q,%q), want (%q,%q)", row.ResourceID, row.ResourceName, resourceID, resourceName)
	}
}

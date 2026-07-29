package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// contractRepoQuerier serves one fixed page of repositories with a known
// chart distribution, so the assertions below are about the RESPONSE SHAPE
// rather than about database behaviour.
type contractRepoQuerier struct {
	CatalogQuerier
	repos  []sqlc.HelmRepository
	counts map[uuid.UUID]int64
}

func (q *contractRepoQuerier) ListGlobalHelmRepositories(context.Context, sqlc.ListGlobalHelmRepositoriesParams) ([]sqlc.HelmRepository, error) {
	return q.repos, nil
}

func (q *contractRepoQuerier) CountGlobalHelmRepositories(context.Context) (int64, error) {
	return int64(len(q.repos)), nil
}

func (q *contractRepoQuerier) CountChartsPerRepository(_ context.Context, ids []uuid.UUID) ([]sqlc.CountChartsPerRepositoryRow, error) {
	out := []sqlc.CountChartsPerRepositoryRow{}
	for _, id := range ids {
		// Mirror the real GROUP BY: no row at all for an empty repository.
		if n, ok := q.counts[id]; ok && n > 0 {
			out = append(out, sqlc.CountChartsPerRepositoryRow{RepositoryID: id, ChartCount: n})
		}
	}
	return out, nil
}

func (q *contractRepoQuerier) GetHelmRepositoryByID(_ context.Context, id uuid.UUID) (sqlc.HelmRepository, error) {
	for _, r := range q.repos {
		if r.ID == id {
			return r, nil
		}
	}
	return sqlc.HelmRepository{}, nil
}

// TestListReposResponseFieldNames pins the wire contract for the catalog
// Repositories table.
//
// Pre-fix, GET /api/v1/catalog/repositories/ marshalled sqlc.HelmRepository
// directly. The row carried no chart count of any kind, so the Charts column
// had nothing to render, and the response shape was whatever the schema
// happened to be that week — including, after migration 145, the Fernet
// ciphertext in auth_config_encrypted.
func TestListReposResponseFieldNames(t *testing.T) {
	synced := time.Date(2026, 7, 28, 10, 30, 0, 0, time.UTC)
	full := sqlc.HelmRepository{
		ID: uuid.New(), Name: "bitnami", Url: "https://charts.bitnami.com/bitnami",
		RepoType: "helm", AuthType: "none", Enabled: true, IsDefault: true,
		AuthConfig:          json.RawMessage(`{}`),
		AuthConfigEncrypted: "gAAAAA-pretend-ciphertext",
		LastSyncedAt:        pgtype.Timestamptz{Time: synced, Valid: true},
		LastSyncAttemptedAt: pgtype.Timestamptz{Time: synced, Valid: true},
		CreatedAt:           synced, UpdatedAt: synced,
	}
	empty := sqlc.HelmRepository{
		ID: uuid.New(), Name: "never-synced", Url: "https://example.invalid",
		RepoType: "helm", AuthType: "none", Enabled: true,
		AuthConfig: json.RawMessage(`{}`),
		CreatedAt:  synced, UpdatedAt: synced,
	}

	q := &contractRepoQuerier{
		repos:  []sqlc.HelmRepository{full, empty},
		counts: map[uuid.UUID]int64{full.ID: 42},
	}
	h := &CatalogHandler{queries: q, log: slog.Default()}

	rec := httptest.NewRecorder()
	h.ListRepos(rec, httptest.NewRequest(http.MethodGet, "/api/v1/catalog/repositories/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d body %s", rec.Code, rec.Body.String())
	}

	var env struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (body %s)", err, rec.Body.String())
	}
	if len(env.Data) != 2 {
		t.Fatalf("expected 2 repositories, got %d", len(env.Data))
	}

	// Every documented key must be present, spelled exactly as the OpenAPI
	// schema spells it. snake_case is this API's convention; the frontend's
	// camelCase types are produced by the axios camelize interceptor.
	for _, key := range []string{
		"id", "name", "url", "repo_type", "description", "is_default",
		"auth_type", "auth_config", "enabled", "last_synced_at",
		"last_sync_attempted_at", "last_sync_error", "created_by_id",
		"created_at", "updated_at", "owner_project_id", "chart_count",
	} {
		if _, ok := env.Data[0][key]; !ok {
			t.Errorf("response is missing documented field %q", key)
		}
	}

	// The DB row shape must not leak. auth_config_encrypted is the
	// migration-145 Fernet envelope and has no business on the wire.
	for _, key := range []string{"auth_config_encrypted"} {
		if _, ok := env.Data[0][key]; ok {
			t.Errorf("response leaks internal column %q", key)
		}
	}
	if body := rec.Body.String(); strings.Contains(body, "pretend-ciphertext") {
		t.Errorf("response body carries the auth_config ciphertext: %s", body)
	}

	// chart_count carries the real total, and a repository that has never
	// ingested anything reports 0 rather than being absent.
	if got := string(env.Data[0]["chart_count"]); got != "42" {
		t.Errorf("chart_count = %s, want 42", got)
	}
	if got := string(env.Data[1]["chart_count"]); got != "0" {
		t.Errorf("chart_count for an unsynced repository = %s, want 0", got)
	}

	// last_synced_at is a real timestamp for a synced repo and null (not the
	// zero time, which the UI would render as a date in year 1) when unset.
	var syncedAt *string
	if err := json.Unmarshal(env.Data[0]["last_synced_at"], &syncedAt); err != nil {
		t.Fatalf("last_synced_at: %v", err)
	}
	if syncedAt == nil || *syncedAt != synced.Format(time.RFC3339Nano) {
		t.Errorf("last_synced_at = %v, want %s", syncedAt, synced.Format(time.RFC3339Nano))
	}
	var neverSynced *string
	if err := json.Unmarshal(env.Data[1]["last_synced_at"], &neverSynced); err != nil {
		t.Fatalf("last_synced_at (unsynced): %v", err)
	}
	if neverSynced != nil {
		t.Errorf("last_synced_at for an unsynced repository = %q, want null", *neverSynced)
	}
}

// TestGetRepoResponseCarriesChartCount covers the single-repository read,
// which uses the same DTO but resolves its count for one id.
func TestGetRepoResponseCarriesChartCount(t *testing.T) {
	repo := sqlc.HelmRepository{
		ID: uuid.New(), Name: "bitnami", Url: "https://charts.bitnami.com/bitnami",
		RepoType: "helm", AuthType: "none", Enabled: true,
		AuthConfig: json.RawMessage(`{}`),
		CreatedAt:  time.Now(), UpdatedAt: time.Now(),
	}
	q := &contractRepoQuerier{
		repos:  []sqlc.HelmRepository{repo},
		counts: map[uuid.UUID]int64{repo.ID: 7},
	}
	h := &CatalogHandler{queries: q, log: slog.Default()}

	req := withURLParam(
		httptest.NewRequest(http.MethodGet, "/api/v1/catalog/repositories/"+repo.ID.String()+"/", nil),
		"id", repo.ID.String())
	rec := httptest.NewRecorder()
	h.GetRepo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("get: status %d body %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := string(env.Data["chart_count"]); got != "7" {
		t.Errorf("chart_count = %s, want 7", got)
	}
}

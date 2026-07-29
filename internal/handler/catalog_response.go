package handler

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// HelmRepositoryResponse is the explicit wire shape for the
// /api/v1/catalog/repositories/ family (list, get, create, update).
//
// Before this DTO the handlers marshalled sqlc.HelmRepository — the raw
// database row — straight to the client. That coupled the public API to the
// schema in both directions and cost us twice:
//
//   - Every column added to helm_repositories became a public API field with
//     no review. Migration 145 added auth_config_encrypted, and the Fernet
//     ciphertext started shipping to every catalog reader; redactHelmRepository
//     has to blank it explicitly, and the key is still on the wire. Here the
//     field simply does not exist, so it cannot leak by default.
//   - Nothing pinned the field NAMES, so docs/openapi.yaml and the row drifted
//     apart silently. chart_count is the reverse case: the frontend had
//     believed in the field for so long that its hand-written type declared it
//     required, but no column, query, or spec entry ever produced it.
//
// JSON keys are snake_case, matching ClusterResponse/BackupResponse and the
// rest of this API. The frontend's camelCase types are produced by the axios
// response interceptor in frontend/src/lib/camelize.ts — the wire itself is
// snake_case everywhere.
//
// pgtype unpack rules (matching pgx v5 native MarshalJSON, so the wire is
// unchanged from the pre-DTO embed shape):
//   - pgtype.Timestamptz → *string (RFC3339Nano when valid, nil when invalid)
//   - pgtype.UUID        → *string (uuid.String when valid, nil when invalid)
//
// See TestHelmRepositoryResponse_WireCompat for the byte-for-byte assertion.
type HelmRepositoryResponse struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Url         string          `json:"url"`
	RepoType    string          `json:"repo_type"`
	Description string          `json:"description"`
	IsDefault   bool            `json:"is_default"`
	AuthType    string          `json:"auth_type"`
	AuthConfig  json.RawMessage `json:"auth_config"`
	Enabled     bool            `json:"enabled"`
	// LastSyncedAt is the last SUCCESSFUL ingest. A failed attempt
	// deliberately does not advance it (29f8c94), so a repository that has
	// been failing keeps reporting its real staleness.
	LastSyncedAt *string `json:"last_synced_at"`
	// LastSyncAttemptedAt is the last attempt, successful or not. Compare
	// with LastSyncedAt to tell "failing" from "no longer being visited".
	LastSyncAttemptedAt *string `json:"last_sync_attempted_at"`
	LastSyncError       string  `json:"last_sync_error"`
	CreatedByID         *string `json:"created_by_id"`
	CreatedAt           string  `json:"created_at"`
	UpdatedAt           string  `json:"updated_at"`
	OwnerProjectID      *string `json:"owner_project_id"`
	// ChartCount is the number of helm_charts rows ingested from this
	// repository. It is enrichment, not a column: see chartCountsFor.
	ChartCount int64 `json:"chart_count"`

	// NOTE: auth_config_encrypted is intentionally absent. It is the
	// migration-145 Fernet envelope; it is of no use to any client and
	// shipping it would hand every catalog reader an offline target.
}

// helmRepositoryToResponse converts one row. The caller is responsible for
// having passed the row through redactHelmRepository first — this function
// does no redaction of its own, so that the credential-handling rules stay in
// exactly one place.
func helmRepositoryToResponse(repo sqlc.HelmRepository, chartCount int64) HelmRepositoryResponse {
	resp := HelmRepositoryResponse{
		ID:                  repo.ID.String(),
		Name:                repo.Name,
		Url:                 repo.Url,
		RepoType:            repo.RepoType,
		Description:         repo.Description,
		IsDefault:           repo.IsDefault,
		AuthType:            repo.AuthType,
		AuthConfig:          repo.AuthConfig,
		Enabled:             repo.Enabled,
		LastSyncedAt:        timestamptzNano(repo.LastSyncedAt),
		LastSyncAttemptedAt: timestamptzNano(repo.LastSyncAttemptedAt),
		LastSyncError:       repo.LastSyncError,
		CreatedAt:           repo.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:           repo.UpdatedAt.Format(time.RFC3339Nano),
		ChartCount:          chartCount,
	}
	if repo.AuthConfig == nil {
		resp.AuthConfig = json.RawMessage(`{}`)
	}
	if repo.CreatedByID.Valid {
		s := uuid.UUID(repo.CreatedByID.Bytes).String()
		resp.CreatedByID = &s
	}
	if repo.OwnerProjectID.Valid {
		s := uuid.UUID(repo.OwnerProjectID.Bytes).String()
		resp.OwnerProjectID = &s
	}
	return resp
}

// helmRepositoriesToResponse converts a page of rows, attaching the chart
// count each one earned. Repositories absent from counts have ingested no
// charts and correctly report 0.
func helmRepositoriesToResponse(repos []sqlc.HelmRepository, counts map[uuid.UUID]int64) []HelmRepositoryResponse {
	out := make([]HelmRepositoryResponse, len(repos))
	for i, repo := range repos {
		out[i] = helmRepositoryToResponse(repo, counts[repo.ID])
	}
	return out
}

// chartCountsFor resolves the chart total for a page of repositories in a
// single grouped aggregate, not a count per row.
//
// Failure is deliberately non-fatal. The chart total is enrichment on top of
// the repositories themselves — the same call the cluster list makes for its
// CPU/memory scalars — and answering the Repositories screen with names, URLs,
// sync state and a zero count beats a 500 that renders nothing. The important
// difference from the bug this replaces is that a zero now has to survive a
// logged error, instead of being the structurally guaranteed outcome of a
// field the API never sent at all.
func (h *CatalogHandler) chartCountsFor(ctx context.Context, repos []sqlc.HelmRepository) map[uuid.UUID]int64 {
	counts := make(map[uuid.UUID]int64, len(repos))
	if len(repos) == 0 {
		return counts
	}
	ids := make([]uuid.UUID, 0, len(repos))
	for _, repo := range repos {
		ids = append(ids, repo.ID)
	}
	rows, err := h.queries.CountChartsPerRepository(ctx, ids)
	if err != nil {
		h.log.Error("count charts per repository", "repositories", len(ids), "error", err)
		return counts
	}
	for _, row := range rows {
		counts[row.RepositoryID] = row.ChartCount
	}
	return counts
}

// timestamptzNano mirrors pgx v5's native pgtype.Timestamptz MarshalJSON so
// the DTO emits the same bytes the embedded sqlc row used to.
//
// Distinct from timestampPtr in agent_fleet.go, which normalises to UTC and
// truncates to whole seconds — applying that here would silently change the
// timestamps this endpoint has always returned.
func timestamptzNano(ts pgtype.Timestamptz) *string {
	if !ts.Valid {
		return nil
	}
	s := ts.Time.Format(time.RFC3339Nano)
	return &s
}

package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/repo"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// catalogMaxVersionsPerChart caps how many recent versions we ingest per chart.
// ponytail: last-N only; the form/install UI never needs ancient releases.
const catalogMaxVersionsPerChart = 3

// CatalogSyncPayload contains parameters for catalog sync.
type CatalogSyncPayload struct {
	RepositoryURL string `json:"repository_url,omitempty"` // empty = sync all repos
}

// NewCatalogSyncTask creates a new catalog sync task.
func NewCatalogSyncTask(payload CatalogSyncPayload) (*asynq.Task, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal catalog sync payload: %w", err)
	}
	return asynq.NewTask("catalog:sync", data), nil
}

// HandleCatalogSync syncs Helm repositories and updates chart listings.
func HandleCatalogSync(ctx context.Context, t *asynq.Task) error {
	return runPeriodicTaskWithLeader(ctx, "catalog:sync", func() error {
		var p CatalogSyncPayload
		if len(t.Payload()) > 0 {
			if err := json.Unmarshal(t.Payload(), &p); err != nil {
				return fmt.Errorf("unmarshal catalog sync payload: %w", err)
			}
		}

		if p.RepositoryURL != "" {
			slog.InfoContext(ctx, "syncing catalog repository", "url", p.RepositoryURL)
		} else {
			slog.InfoContext(ctx, "syncing all catalog repositories")
		}

		if runtimeDeps.Queries == nil {
			slog.InfoContext(ctx, "catalog sync runtime not configured, skipping repository sync")
			return nil
		}

		repos, err := runtimeDeps.Queries.ListEnabledHelmRepositories(ctx)
		if err != nil {
			return err
		}
		for _, repoRecord := range repos {
			if p.RepositoryURL != "" && repoRecord.Url != p.RepositoryURL {
				continue
			}
			indexURL, err := repositoryIndexURL(repoRecord.Url)
			if err != nil {
				return err
			}
			indexFile, err := fetchRepositoryIndex(ctx, httpclient.SafeClient(catalogFetchTimeout), indexURL, repoRecord.Url)
			if err != nil {
				return err
			}
			if err := syncRepositoryIndex(ctx, repoRecord.ID, repoRecord.Url, indexFile); err != nil {
				return err
			}
			if err := runtimeDeps.Queries.UpdateHelmRepositoryLastSynced(ctx, repoRecord.ID); err != nil {
				return err
			}
		}

		slog.InfoContext(ctx, "catalog sync complete")
		return nil
	})
}

const catalogFetchTimeout = 30 * time.Second

func fetchRepositoryIndex(ctx context.Context, client *http.Client, indexURL, repositoryURL string) (*repo.IndexFile, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, catalogFetchTimeout)
	defer cancel()
	// SSRF guard: the repository URL is operator/DB-supplied and fetched
	// server-side, so refuse loopback/internal/metadata targets. GuardPublicHost
	// is the cheap pre-check; the caller passes a SafeClient whose dialer
	// re-validates the connected IP to close the DNS-rebinding window.
	if err := httpclient.GuardPublicHost(indexURL); err != nil {
		return nil, fmt.Errorf("catalog repository host is not a permitted public address")
	}
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, indexURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("catalog repository %s returned status %d", repositoryURL, resp.StatusCode)
	}
	return decodeIndex(resp)
}

func repositoryIndexURL(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(u.Path, "/index.yaml") {
		u.Path = path.Join(u.Path, "index.yaml")
	}
	return u.String(), nil
}

func decodeIndex(resp *http.Response) (*repo.IndexFile, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	index := repo.NewIndexFile()
	if err := yaml.Unmarshal(body, index); err != nil {
		return nil, err
	}
	return index, nil
}

func syncRepositoryIndex(ctx context.Context, repositoryID uuid.UUID, repositoryURL string, indexFile *repo.IndexFile) error {
	if indexFile == nil {
		return nil
	}
	// Sort each chart's versions newest-first so the last-N cap keeps recent releases.
	indexFile.SortEntries()
	seenCharts := map[string]struct{}{}
	for chartName, versions := range indexFile.Entries {
		if len(versions) > catalogMaxVersionsPerChart {
			versions = versions[:catalogMaxVersionsPerChart]
		}
		seenCharts[chartName] = struct{}{}
		chart, err := runtimeDeps.Queries.GetHelmChartByRepoAndName(ctx, sqlc.GetHelmChartByRepoAndNameParams{
			RepositoryID: repositoryID,
			Name:         chartName,
		})
		if err != nil {
			if err != pgx.ErrNoRows {
				return err
			}
			chart, err = runtimeDeps.Queries.CreateHelmChart(ctx, sqlc.CreateHelmChartParams{
				RepositoryID: repositoryID,
				Name:         chartName,
				DisplayName:  chartName,
				Description:  firstNonEmptyEntryField(versions, func(v *repo.ChartVersion) string { return v.Description }),
				IconUrl:      firstNonEmptyEntryField(versions, func(v *repo.ChartVersion) string { return v.Icon }),
				HomeUrl:      firstNonEmptyEntryField(versions, func(v *repo.ChartVersion) string { return v.Home }),
				Category:     "",
				Keywords:     mustJSON(firstSliceEntryField(versions, func(v *repo.ChartVersion) []string { return v.Keywords })),
				Maintainers:  mustJSON(firstMaintainers(versions)),
				Deprecated:   false,
			})
			if err != nil {
				return err
			}
		}
		seenVersions := map[string]struct{}{}
		for _, version := range versions {
			if version == nil || version.Version == "" {
				continue
			}
			seenVersions[version.Version] = struct{}{}
			if _, err := runtimeDeps.Queries.GetHelmChartVersion(ctx, sqlc.GetHelmChartVersionParams{
				ChartID: chart.ID,
				Version: version.Version,
			}); err == nil {
				continue
			} else if err != pgx.ErrNoRows {
				return err
			}
			// Pull the chart archive once to populate the values form (schema),
			// the YAML editor (default values) and the README. Best-effort:
			// a chart that won't fetch still lands as a card + installable version.
			defaultValues, valuesSchema, readme := fetchChartAssets(ctx, repositoryURL, version.URLs)
			if _, err := runtimeDeps.Queries.CreateHelmChartVersion(ctx, sqlc.CreateHelmChartVersionParams{
				ChartID:       chart.ID,
				Version:       version.Version,
				AppVersion:    version.AppVersion,
				Digest:        version.Digest,
				Urls:          mustJSON(version.URLs),
				ValuesSchema:  valuesSchema,
				DefaultValues: defaultValues,
				Readme:        readme,
				CreatedAtUpstream: pgtype.Timestamptz{
					Time:  version.Created,
					Valid: !version.Created.IsZero(),
				},
			}); err != nil {
				return err
			}
		}
		// CORR-R04: GC every version not in the current index. Must not
		// advance OFFSET while deleting — deletes compact the list and a
		// naive offset+=pageSize leaves orphans past the first page.
		if err := gcPagedOrphans(catalogGCPageSize, func(limit, offset int32) (int, int, error) {
			existingVersions, err := runtimeDeps.Queries.ListChartVersions(ctx, sqlc.ListChartVersionsParams{
				ChartID: chart.ID,
				Limit:   limit,
				Offset:  offset,
			})
			if err != nil {
				return 0, 0, err
			}
			deleted := 0
			for _, existing := range existingVersions {
				if _, ok := seenVersions[existing.Version]; ok {
					continue
				}
				if err := runtimeDeps.Queries.DeleteHelmChartVersion(ctx, existing.ID); err != nil {
					return 0, 0, err
				}
				deleted++
			}
			return len(existingVersions), deleted, nil
		}); err != nil {
			return err
		}
	}
	// CORR-R04: GC charts removed from the index (same offset-stable algorithm).
	if err := gcPagedOrphans(catalogGCPageSize, func(limit, offset int32) (int, int, error) {
		existingCharts, err := runtimeDeps.Queries.ListChartsByRepository(ctx, sqlc.ListChartsByRepositoryParams{
			RepositoryID: repositoryID,
			Limit:        limit,
			Offset:       offset,
		})
		if err != nil {
			return 0, 0, err
		}
		deleted := 0
		for _, existing := range existingCharts {
			if _, ok := seenCharts[existing.Name]; ok {
				continue
			}
			if err := runtimeDeps.Queries.DeleteHelmChart(ctx, existing.ID); err != nil {
				return 0, 0, err
			}
			deleted++
		}
		return len(existingCharts), deleted, nil
	}); err != nil {
		return err
	}
	return nil
}

// catalogGCPageSize is the page size for CORR-R04 catalog GC list queries.
const catalogGCPageSize int32 = 1000

// gcPagedOrphans walks a paginated list and deletes orphans. page(limit, offset)
// returns (pageLen, deletedCount, err).
//
// Algorithm (delete-stable OFFSET):
//   - If the page deleted any rows, re-query the same offset (rows above
//     compact into this window).
//   - If the page deleted nothing and is a full page of keepers, advance offset.
//   - If the page deleted nothing and is short, we are past the last orphan.
//
// A naive "for offset += pageSize" loop after deletes silently orphans every
// row past the first page when most/all rows need GC (N>pageSize).
func gcPagedOrphans(pageSize int32, page func(limit, offset int32) (pageLen int, deleted int, err error)) error {
	if pageSize < 1 {
		pageSize = 1000
	}
	var offset int32
	for {
		pageLen, deleted, err := page(pageSize, offset)
		if err != nil {
			return err
		}
		if pageLen == 0 {
			return nil
		}
		if deleted > 0 {
			// Same offset again — deleted rows left a hole that later orphans
			// will fill into this page on the next query.
			continue
		}
		if int32(pageLen) < pageSize {
			return nil
		}
		// Full page of keepers only — skip past them.
		offset += pageSize
	}
}

// fetchChartAssets pulls the chart .tgz and extracts the three things the UI
// needs: the raw values.yaml (YAML editor), values.schema.json (the form), and
// README.md. Pull-read-discard — nothing is stored on disk, no mirror. Returns
// safe defaults ("" / "{}") on any failure so sync never fails over one chart.
func fetchChartAssets(ctx context.Context, repositoryURL string, urls []string) (string, json.RawMessage, string) {
	emptySchema := json.RawMessage(`{}`)
	if len(urls) == 0 {
		return "", emptySchema, ""
	}
	chartURL, err := resolveChartURL(repositoryURL, urls[0])
	if err != nil {
		return "", emptySchema, ""
	}
	fetchCtx, cancel := context.WithTimeout(ctx, catalogFetchTimeout)
	defer cancel()
	// SSRF guard on the operator/DB-supplied chart URL (same rationale as the
	// index fetch): pre-check the host, dial through the rebind-safe client.
	if err := httpclient.GuardPublicHost(chartURL); err != nil {
		return "", emptySchema, ""
	}
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, chartURL, nil)
	if err != nil {
		return "", emptySchema, ""
	}
	resp, err := httpclient.SafeClient(catalogFetchTimeout).Do(req)
	if err != nil {
		return "", emptySchema, ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		slog.WarnContext(ctx, "catalog chart fetch failed", "url", chartURL, "status", resp.StatusCode)
		return "", emptySchema, ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20)) // 64MiB ceiling
	if err != nil {
		return "", emptySchema, ""
	}
	loaded, err := loader.LoadArchive(bytes.NewReader(body))
	if err != nil {
		slog.WarnContext(ctx, "catalog chart parse failed", "url", chartURL, "error", err)
		return "", emptySchema, ""
	}
	schema := emptySchema
	if len(loaded.Schema) > 0 && json.Valid(loaded.Schema) {
		schema = json.RawMessage(loaded.Schema)
	}
	var defaultValues, readme string
	for _, f := range loaded.Raw {
		switch path.Base(f.Name) {
		case "values.yaml":
			defaultValues = string(f.Data)
		case "README.md":
			readme = string(f.Data)
		}
	}
	return defaultValues, schema, readme
}

// resolveChartURL handles index entries whose URLs are relative to the repo.
func resolveChartURL(repositoryURL, chartURL string) (string, error) {
	u, err := url.Parse(chartURL)
	if err != nil {
		return "", err
	}
	if u.IsAbs() {
		return u.String(), nil
	}
	base, err := url.Parse(repositoryURL)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(u).String(), nil
}

func firstNonEmptyEntryField(versions repo.ChartVersions, field func(*repo.ChartVersion) string) string {
	for _, version := range versions {
		if version == nil {
			continue
		}
		if value := field(version); value != "" {
			return value
		}
	}
	return ""
}

func firstSliceEntryField(versions repo.ChartVersions, field func(*repo.ChartVersion) []string) []string {
	for _, version := range versions {
		if version == nil {
			continue
		}
		if values := field(version); len(values) > 0 {
			return values
		}
	}
	return []string{}
}

func firstMaintainers(versions repo.ChartVersions) []map[string]string {
	for _, version := range versions {
		if version == nil || len(version.Maintainers) == 0 {
			continue
		}
		items := make([]map[string]string, 0, len(version.Maintainers))
		for _, maintainer := range version.Maintainers {
			items = append(items, map[string]string{
				"name":  maintainer.Name,
				"email": maintainer.Email,
				"url":   maintainer.URL,
			})
		}
		return items
	}
	return []map[string]string{}
}

func mustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

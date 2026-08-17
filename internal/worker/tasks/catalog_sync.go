package tasks

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
	"path"
	"strings"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/repo"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// catalogMaxVersionsPerChart caps how many recent versions we ingest per chart.
// ponytail: last-N only; the form/install UI never needs ancient releases.
//
// Shared with the interactive handler path, which applies the same cap — the
// sweep's GC below deletes anything outside it, so a handler that ingested
// more would just be undone six hours later.
const catalogMaxVersionsPerChart = catalog.MaxIndexVersionsPerChart

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
		var (
			failures    []error
			synced      int
			failed      int
			unsupported int
			aborted     bool
		)
		for _, repoRecord := range repos {
			// Worker shutdown / task deadline. Stop instead of walking the
			// rest of the list fast-failing every remaining repository with
			// `context canceled` — that reports healthy repos as broken and
			// fires two doomed DB writes apiece. Same guard as
			// agent_token_rotate.go and control_plane_snapshot.go.
			if ctx.Err() != nil {
				aborted = true
				break
			}
			if p.RepositoryURL != "" && repoRecord.Url != p.RepositoryURL {
				continue
			}
			// Per-repo isolation: one repository's failure is recorded
			// against THAT repository and the sweep carries on. Before this,
			// every failure path here was `return err`, so a single 401,
			// DNS blip, or oci:// URL froze the catalog for every repo after
			// it in the (name-ordered) list, fleet-wide, with no UI signal.
			if err := syncOneRepository(ctx, repoRecord); err != nil {
				// An unimplemented repo_type must not hold the task red. It
				// is recorded against the row (visible, actionable) but kept
				// out of the task's error, because CreateRepo accepts
				// repo_type='git' and nothing anywhere syncs one: counting it
				// as a failure marks the catalog:sync reconciler `failed` on
				// every 6h tick forever and burns asynq's 25 retries, with no
				// operator action short of deleting the repository that ever
				// clears it. Durable fix is the P3 item
				// `git-backed-chart-repos-accepted-never-synced` — reject at
				// create.
				if errors.Is(err, errRepoTypeUnsupported) {
					unsupported++
					recordRepositorySyncFailure(ctx, repoRecord, err)
					continue
				}
				failed++
				failures = append(failures, fmt.Errorf("%s (%s): %w", repoRecord.Name, repoRecord.Url, err))
				recordRepositorySyncFailure(ctx, repoRecord, err)
				continue
			}
			synced++
		}

		if len(failures) > 0 {
			// A partial sweep must not look like a clean one: returning the
			// joined error marks the catalog:sync reconciler run `failed`
			// (runPeriodicTaskWithLeader → observability.RecordReconcilerRun),
			// which is what the stalled/failing-reconciler alerting watches.
			slog.WarnContext(ctx, "catalog sync completed with failures",
				"synced", synced, "failed", failed, "unsupported", unsupported, "aborted", aborted)
			attempted := synced + failed + unsupported
			if aborted {
				// Say "aborted", or "3 of 5 failed" reads as a fleet of broken
				// repositories when the sweep simply never reached the rest.
				return fmt.Errorf("catalog sync aborted after %d repositories (%d failed): %w",
					attempted, failed, errors.Join(append(failures, ctx.Err())...))
			}
			return fmt.Errorf("catalog sync: %d of %d repositories failed: %w", failed, attempted, errors.Join(failures...))
		}
		if aborted {
			return fmt.Errorf("catalog sync aborted after %d repositories: %w", synced+unsupported, ctx.Err())
		}
		slog.InfoContext(ctx, "catalog sync complete", "synced", synced, "unsupported", unsupported)
		return nil
	})
}

// ociIngest is the OCI ingest entry point, indirected so tests can substitute
// a recorder instead of standing up an OCI registry.
var ociIngest = catalog.IngestOCIRepo

// errRepoTypeUnsupported marks a repository whose repo_type no ingest path
// implements. It is reported against the row but not counted as a sweep
// failure — see the call site in HandleCatalogSync for why.
var errRepoTypeUnsupported = errors.New("repository type has no sync implementation")

// syncOneRepository ingests a single repository, dispatching on its kind.
//
// The scheduled sweep used to have no branch at all: it appended /index.yaml
// to whatever was stored and issued a plain GET, which cannot describe an
// oci:// registry (helm addresses those charts as individual artifacts) and is
// meaningless for a git clone URL. OCI ingest was implemented and reachable
// from the operator-triggered Sync button only, so an OCI catalog refreshed
// exactly as often as somebody clicked it.
func syncOneRepository(ctx context.Context, repoRecord sqlc.HelmRepository) error {
	switch {
	case catalog.IsOCIRepo(repoRecord):
		// KNOWN GAP — OCI is create-only: IngestOCIRepo contains no delete,
		// and this branch does not run syncRepositoryIndex, so the chart/
		// version GC below never executes for an OCI repository. A tag yanked
		// upstream stays in the catalog permanently, and now that the sweep
		// runs unattended every 6h the stored set only grows. The per-sweep
		// cap (catalog.MaxOCIChartVersionsPerChart) bounds the WORK each
		// sweep does, not the stored set — the two paths are NOT equivalent
		// on deletion, only on ingest.
		//
		// Not fixed here on purpose: OCI ingest `continue`s past a chart whose
		// rc.Tags call fails (registry hiccup, rate limit), so that chart is
		// absent from any seen-set this loop could build, and feeding that set
		// to gcPagedOrphans would delete the whole chart on a transient error.
		// A correct OCI GC needs IngestOCIRepo to distinguish "saw no tags"
		// from "could not ask", which is a change to that function's contract
		// and out of scope for this item.
		if _, _, err := ociIngest(ctx, runtimeDeps.Queries, repoRecord, runtimeDeps.CatalogDecryptor, runtimeLogger()); err != nil {
			return err
		}
	case catalog.IsGitRepo(repoRecord):
		// CreateRepo accepts repo_type='git' but nothing — here or on the
		// handler path — clones or indexes one. Say so against the row
		// instead of fetching `<git url>/index.yaml` and reporting a 404
		// the operator cannot act on. Wrapped in errRepoTypeUnsupported so
		// the caller records it without failing the task; see the comment at
		// the call site.
		return fmt.Errorf("git-backed chart repositories are not synced (%w): repo_type='git' has no clone/index implementation; use a Helm HTTP repository or an oci:// registry", errRepoTypeUnsupported)
	default:
		indexURL, err := repositoryIndexURL(repoRecord.Url)
		if err != nil {
			return err
		}
		indexFile, err := fetchRepositoryIndex(ctx, httpclient.SafeClientWithLimit(catalogFetchTimeout, catalog.MaxIndexBytes), indexURL, repoRecord)
		if err != nil {
			return err
		}
		if err := syncRepositoryIndex(ctx, repoRecord, indexFile); err != nil {
			return err
		}
	}
	return runtimeDeps.Queries.UpdateHelmRepositoryLastSynced(ctx, repoRecord.ID)
}

// repositorySyncFailureRecorder is the optional sub-interface satisfied by the
// production *sqlc.Queries; declared locally so the RuntimeQuerier surface
// need not grow for an error-reporting-only write.
type repositorySyncFailureRecorder interface {
	UpdateHelmRepositorySyncFailure(ctx context.Context, arg sqlc.UpdateHelmRepositorySyncFailureParams) error
}

// maxRepositorySyncErrorLen bounds what we persist. Upstream errors can carry
// a whole HTML error page; the column is TEXT but the UI renders it inline.
const maxRepositorySyncErrorLen = 1000

// recordRepositorySyncFailure surfaces a per-repository failure where an
// operator will actually meet it:
//
//   - helm_repositories.last_sync_error / last_sync_attempted_at (migration
//     144), returned by GET /api/v1/catalog/repositories/ — the same
//     `last_error` shape the rest of this schema uses for per-resource sync
//     state. last_synced_at is left untouched so a broken repo keeps showing
//     its real (stale) freshness.
//   - an audit row, so the failure appears in the audit log alongside the
//     `catalog.repo.sync` rows the manual Sync button writes.
//   - a WARN log line with the repository name and URL.
//
// All three are best-effort: failing to report a failure must never abort the
// remaining repositories, which is the whole point of the change.
func recordRepositorySyncFailure(ctx context.Context, repoRecord sqlc.HelmRepository, cause error) {
	msg := cause.Error()
	if len(msg) > maxRepositorySyncErrorLen {
		// Cut on a byte boundary, then drop the partial trailing rune —
		// Postgres rejects invalid UTF-8 in a TEXT column, and refusing to
		// store the reason because we sliced a multi-byte character in half
		// would put us back where we started.
		msg = strings.ToValidUTF8(msg[:maxRepositorySyncErrorLen], "")
	}
	slog.WarnContext(ctx, "catalog repository sync failed",
		"repository", repoRecord.Name,
		"url", repoRecord.Url,
		"repo_type", repoRecord.RepoType,
		"error", msg,
	)
	if recorder, ok := runtimeDeps.Queries.(repositorySyncFailureRecorder); ok {
		if err := recorder.UpdateHelmRepositorySyncFailure(ctx, sqlc.UpdateHelmRepositorySyncFailureParams{
			ID:            repoRecord.ID,
			LastSyncError: msg,
		}); err != nil {
			slog.WarnContext(ctx, "recording catalog repository sync failure failed",
				"repository", repoRecord.Name, "error", err)
		}
	}
	detail, err := json.Marshal(map[string]any{
		"url":       repoRecord.Url,
		"repo_type": repoRecord.RepoType,
		"error":     msg,
	})
	if err != nil {
		return
	}
	_ = runtimeDeps.Queries.CreateAuditLogV1(ctx, sqlc.CreateAuditLogV1Params{
		Source:       "worker",
		Action:       "catalog.repo.sync_failed",
		ResourceType: "helm_repository",
		ResourceID:   repoRecord.ID.String(),
		ResourceName: repoRecord.Name,
		Detail:       detail,
	})
}

const catalogFetchTimeout = 30 * time.Second

func fetchRepositoryIndex(ctx context.Context, client *http.Client, indexURL string, repoRecord sqlc.HelmRepository) (*repo.IndexFile, error) {
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
	// Same credentials the operator-triggered sync applies. Without this a
	// private ChartMuseum/Artifactory/Nexus repo answers the unattended sweep
	// with a 401 forever while its Sync button works, which reads as "the
	// scheduler is broken" rather than "the scheduler never authenticated".
	catalog.ApplyIndexAuth(req, repoRecord, runtimeDeps.CatalogDecryptor, runtimeLogger())
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("catalog repository %s returned status %d", repoRecord.Url, resp.StatusCode)
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, catalog.MaxIndexBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > catalog.MaxIndexBytes {
		return nil, fmt.Errorf("repository index exceeds %d bytes", catalog.MaxIndexBytes)
	}
	index := repo.NewIndexFile()
	if err := yaml.Unmarshal(body, index); err != nil {
		return nil, err
	}
	return index, nil
}

// syncRepositoryIndex takes the whole repository record, not just its id and
// URL: the per-version chart-asset fetch below needs the same credentials the
// index fetch used. Passing only (id, url) is what let a private repo
// authenticate its index, create every chart/version row, then 401 on every
// .tgz — and because the rows exist, the next sweep short-circuits on
// GetHelmChartVersion and never retries, leaving the install form and YAML
// editor permanently empty.
func syncRepositoryIndex(ctx context.Context, repoRecord sqlc.HelmRepository, indexFile *repo.IndexFile) error {
	if indexFile == nil {
		return nil
	}
	repositoryID := repoRecord.ID
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
			defaultValues, valuesSchema, readme := fetchChartAssets(ctx, repoRecord, version.URLs)
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
func fetchChartAssets(ctx context.Context, repoRecord sqlc.HelmRepository, urls []string) (string, json.RawMessage, string) {
	emptySchema := json.RawMessage(`{}`)
	if len(urls) == 0 {
		return "", emptySchema, ""
	}
	chartURL, err := resolveChartURL(repoRecord.Url, urls[0])
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
	// A private ChartMuseum/Artifactory/Nexus serves its .tgz from behind the
	// same auth as its index.yaml. Without this the sweep authenticates the
	// index, writes every chart/version row, 401s on every archive, and the
	// rows stick with an empty schema/values/README forever (the next sweep
	// finds the row and skips it).
	//
	// Same-host only — see catalog.SameHost for why, and note that the
	// handler's lazy-hydrate path guards on the same shared helper so the two
	// chart-asset fetches cannot drift apart on this rule again.
	if catalog.SameHost(repoRecord.Url, chartURL) {
		catalog.ApplyIndexAuth(req, repoRecord, runtimeDeps.CatalogDecryptor, runtimeLogger())
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

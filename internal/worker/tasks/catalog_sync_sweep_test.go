package tasks

// Regressions for the scheduled catalog sweep (catalog:sync, @every 6h).
//
// Before this file the sweep did three things wrong and all three were
// invisible from the UI:
//   - it `return err`'d on the first repository that failed, so one bad repo
//     froze every repo after it in the name-ordered list;
//   - it had no repo_type branch at all, so an oci:// repository was fed to
//     `<url>/index.yaml` + http.Get and could never refresh unattended;
//   - it never applied the repository's stored credentials, so a private
//     repo 401'd on the sweep while its Sync button worked.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// catalogSweepQuerier embeds RuntimeQuerier so only the sweep's methods need
// implementations; any other call nil-derefs, flagging an unexpected path.
type catalogSweepQuerier struct {
	RuntimeQuerier
	repos []sqlc.HelmRepository

	lastSynced []uuid.UUID
	failures   []sqlc.UpdateHelmRepositorySyncFailureParams
	audits     []sqlc.CreateAuditLogV1Params
	charts     []sqlc.CreateHelmChartParams
}

func (q *catalogSweepQuerier) ListEnabledHelmRepositories(context.Context) ([]sqlc.HelmRepository, error) {
	return q.repos, nil
}

func (q *catalogSweepQuerier) UpdateHelmRepositoryLastSynced(_ context.Context, id uuid.UUID) error {
	q.lastSynced = append(q.lastSynced, id)
	return nil
}

func (q *catalogSweepQuerier) UpdateHelmRepositorySyncFailure(_ context.Context, arg sqlc.UpdateHelmRepositorySyncFailureParams) error {
	q.failures = append(q.failures, arg)
	return nil
}

func (q *catalogSweepQuerier) CreateAuditLogV1(_ context.Context, arg sqlc.CreateAuditLogV1Params) error {
	q.audits = append(q.audits, arg)
	return nil
}

// The index ingest path below only ever needs to create charts: every lookup
// misses (empty catalog) and the GC list queries return nothing.
func (q *catalogSweepQuerier) GetHelmChartByRepoAndName(context.Context, sqlc.GetHelmChartByRepoAndNameParams) (sqlc.HelmChart, error) {
	return sqlc.HelmChart{}, pgx.ErrNoRows
}

func (q *catalogSweepQuerier) CreateHelmChart(_ context.Context, arg sqlc.CreateHelmChartParams) (sqlc.HelmChart, error) {
	q.charts = append(q.charts, arg)
	return sqlc.HelmChart{ID: uuid.New(), RepositoryID: arg.RepositoryID, Name: arg.Name}, nil
}

func (q *catalogSweepQuerier) GetHelmChartVersion(context.Context, sqlc.GetHelmChartVersionParams) (sqlc.HelmChartVersion, error) {
	return sqlc.HelmChartVersion{}, pgx.ErrNoRows
}

func (q *catalogSweepQuerier) CreateHelmChartVersion(_ context.Context, arg sqlc.CreateHelmChartVersionParams) (sqlc.HelmChartVersion, error) {
	return sqlc.HelmChartVersion{ID: uuid.New(), ChartID: arg.ChartID, Version: arg.Version}, nil
}

func (q *catalogSweepQuerier) ListChartVersions(context.Context, sqlc.ListChartVersionsParams) ([]sqlc.HelmChartVersion, error) {
	return nil, nil
}

func (q *catalogSweepQuerier) ListChartsByRepository(context.Context, sqlc.ListChartsByRepositoryParams) ([]sqlc.HelmChart, error) {
	return nil, nil
}

func (q *catalogSweepQuerier) syncedContains(id uuid.UUID) bool {
	for _, got := range q.lastSynced {
		if got == id {
			return true
		}
	}
	return false
}

func (q *catalogSweepQuerier) failureFor(id uuid.UUID) (sqlc.UpdateHelmRepositorySyncFailureParams, bool) {
	for _, got := range q.failures {
		if got.ID == id {
			return got, true
		}
	}
	return sqlc.UpdateHelmRepositorySyncFailureParams{}, false
}

// indexServer serves a one-chart index.yaml and records the requests it saw.
//
// The recorder is mutex-guarded: the handler runs on the server's goroutine
// while the test body reads the slice, and net/http gives the race detector no
// happens-before edge between the two. Unsynchronised, that is a real (if
// rarely-caught) data race in the test itself, and a race report lands on
// whichever test happens to be running.
func indexServer(t *testing.T, status int) (*httptest.Server, func() []*http.Request) {
	t.Helper()
	var (
		mu   sync.Mutex
		seen []*http.Request
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Clone(context.Background()))
		mu.Unlock()
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte("apiVersion: v1\nentries:\n  app:\n  - name: app\n    version: 1.0.0\n"))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []*http.Request {
		mu.Lock()
		defer mu.Unlock()
		return append([]*http.Request(nil), seen...)
	}
}

// chartAssetServer serves an index whose one entry carries a RELATIVE urls:
// entry, so the chart-asset fetch resolves back to this same server and its
// request can be inspected. Requests are recorded per path.
//
// The .tgz body is deliberately not a real archive: this exercises what the
// asset fetch SENDS, and loader.LoadArchive failing afterwards is the
// documented best-effort path.
func chartAssetServer(t *testing.T) (*httptest.Server, func(path string) []*http.Request) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string][]*http.Request{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = append(seen[r.URL.Path], r.Clone(context.Background()))
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, ".tgz") {
			_, _ = w.Write([]byte("not-a-real-archive"))
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte("apiVersion: v1\nentries:\n  app:\n  - name: app\n    version: 1.0.0\n    urls:\n    - charts/app-1.0.0.tgz\n"))
	}))
	t.Cleanup(srv.Close)
	return srv, func(path string) []*http.Request {
		mu.Lock()
		defer mu.Unlock()
		return seen[path]
	}
}

// TestHandleCatalogSyncIsolatesPerRepoFailure is the core regression: repo 2
// of 4 fails and the sweep must still sync 1, 3 and 4, record the failure
// against repo 2 only, and report the partial outcome in its own return value.
//
// Pre-fix this failed at the first assertion — the sweep returned on repo 2
// and repos 3/4 were never fetched.
func TestHandleCatalogSyncIsolatesPerRepoFailure(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })
	defer httpclient.DisableGuardForTest()()

	good1, _ := indexServer(t, http.StatusOK)
	broken, _ := indexServer(t, http.StatusInternalServerError)
	good2, _ := indexServer(t, http.StatusOK)
	good3, _ := indexServer(t, http.StatusOK)

	repos := []sqlc.HelmRepository{
		{ID: uuid.New(), Name: "one", Url: good1.URL, RepoType: "helm"},
		{ID: uuid.New(), Name: "two-broken", Url: broken.URL, RepoType: "helm"},
		{ID: uuid.New(), Name: "three", Url: good2.URL, RepoType: "helm"},
		{ID: uuid.New(), Name: "four", Url: good3.URL, RepoType: "helm"},
	}
	q := &catalogSweepQuerier{repos: repos}
	runtimeDeps = RuntimeDependencies{Queries: q, Log: slog.Default()}

	err := HandleCatalogSync(context.Background(), &asynq.Task{})

	for _, idx := range []int{0, 2, 3} {
		if !q.syncedContains(repos[idx].ID) {
			t.Fatalf("repo %q was starved by the failure on repo %q", repos[idx].Name, repos[1].Name)
		}
	}
	if q.syncedContains(repos[1].ID) {
		t.Fatalf("broken repo %q must not stamp last_synced_at", repos[1].Name)
	}
	if len(q.charts) != 3 {
		t.Fatalf("expected 3 repositories to ingest charts, got %d", len(q.charts))
	}

	// The failure is recorded against THAT repo, and only that repo.
	failure, ok := q.failureFor(repos[1].ID)
	if !ok {
		t.Fatalf("no last_sync_error recorded for %q; failures=%+v", repos[1].Name, q.failures)
	}
	if !strings.Contains(failure.LastSyncError, "500") {
		t.Fatalf("last_sync_error should name the upstream status, got %q", failure.LastSyncError)
	}
	if len(q.failures) != 1 {
		t.Fatalf("expected exactly 1 recorded failure, got %d: %+v", len(q.failures), q.failures)
	}
	if len(q.audits) != 1 || q.audits[0].Action != "catalog.repo.sync_failed" || q.audits[0].ResourceName != "two-broken" {
		t.Fatalf("expected one catalog.repo.sync_failed audit row for two-broken, got %+v", q.audits)
	}

	// A partial sweep must not look like a clean one.
	if err == nil {
		t.Fatalf("sweep reported success despite a failed repository")
	}
	if !strings.Contains(err.Error(), "two-broken") {
		t.Fatalf("aggregated error should name the failing repository, got: %v", err)
	}
	if !strings.Contains(err.Error(), "1 of 4") {
		t.Fatalf("aggregated error should report the partial count, got: %v", err)
	}
}

// TestHandleCatalogSyncIngestsOCIRepos asserts the SCHEDULED path routes an
// oci:// repository into the OCI ingest and stamps its freshness.
//
// Pre-fix this failed on the very first assertion: the sweep had no repo_type
// branch, so the OCI repo was sent to `oci://.../index.yaml`, GuardPublicHost
// rejected it, and the whole sweep aborted — the OCI ingest was never called
// even once, from the scheduler, ever.
func TestHandleCatalogSyncIngestsOCIRepos(t *testing.T) {
	saved := runtimeDeps
	savedIngest := ociIngest
	t.Cleanup(func() { runtimeDeps = saved; ociIngest = savedIngest })
	defer httpclient.DisableGuardForTest()()

	http1, _ := indexServer(t, http.StatusOK)
	ociRepo := sqlc.HelmRepository{
		ID:         uuid.New(),
		Name:       "ghcr",
		Url:        "oci://ghcr.io/argoproj/argo-helm",
		RepoType:   "oci",
		AuthConfig: json.RawMessage(`{"charts":["argo-cd"]}`),
	}
	helmRepo := sqlc.HelmRepository{ID: uuid.New(), Name: "plain", Url: http1.URL, RepoType: "helm"}

	q := &catalogSweepQuerier{repos: []sqlc.HelmRepository{ociRepo, helmRepo}}
	runtimeDeps = RuntimeDependencies{Queries: q, Log: slog.Default()}

	var ingested []string
	ociIngest = func(_ context.Context, _ catalog.OCIQuerier, repo sqlc.HelmRepository, _ catalog.Decryptor, _ *slog.Logger) (int, int, error) {
		ingested = append(ingested, repo.Url)
		return 1, 3, nil
	}

	if err := HandleCatalogSync(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(ingested) != 1 || ingested[0] != ociRepo.Url {
		t.Fatalf("OCI ingest was not driven by the scheduled sweep: %v", ingested)
	}
	if !q.syncedContains(ociRepo.ID) {
		t.Fatalf("OCI repo did not get its last_synced_at stamped")
	}
	if _, failed := q.failureFor(ociRepo.ID); failed {
		t.Fatalf("OCI repo recorded a failure: %+v", q.failures)
	}
	// The plain repo is unaffected and did not go through the OCI branch.
	if !q.syncedContains(helmRepo.ID) || len(q.charts) != 1 {
		t.Fatalf("HTTP repo was not ingested alongside the OCI one (charts=%d)", len(q.charts))
	}
}

// TestHandleCatalogSyncGitRepoIsReportedNotSilentlyStale covers the third
// repo_type CreateRepo accepts. Nothing clones or indexes a git repo on any
// path, so the sweep must say that against the row rather than fetching
// `<git url>/index.yaml` and reporting an unactionable 404 — and must not
// stamp last_synced_at, which would make the stale catalog look fresh.
//
// Pre-fix the git repo took the index path, produced a confusing error, and
// aborted the sweep for every repo after it.
//
// The task must NOT go red for it. CreateRepo accepts repo_type='git', so a
// supported API call can put the catalog:sync reconciler in `failed` on every
// 6h tick forever (and burn asynq's 25 retries) with no operator action short
// of deleting the row that clears it. The row-level report is the signal; the
// durable fix is rejecting git at create (P3
// `git-backed-chart-repos-accepted-never-synced`).
func TestHandleCatalogSyncGitRepoIsReportedNotSilentlyStale(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })
	defer httpclient.DisableGuardForTest()()

	good, _ := indexServer(t, http.StatusOK)
	gitRepo := sqlc.HelmRepository{ID: uuid.New(), Name: "charts-git", Url: "https://github.com/example/charts", RepoType: "git"}
	helmRepo := sqlc.HelmRepository{ID: uuid.New(), Name: "plain", Url: good.URL, RepoType: "helm"}

	q := &catalogSweepQuerier{repos: []sqlc.HelmRepository{gitRepo, helmRepo}}
	runtimeDeps = RuntimeDependencies{Queries: q, Log: slog.Default()}

	if err := HandleCatalogSync(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("an unimplemented repo_type must not fail the task (permanently red reconciler): %v", err)
	}
	failure, ok := q.failureFor(gitRepo.ID)
	if !ok {
		t.Fatalf("git repo recorded no last_sync_error: %+v", q.failures)
	}
	if !strings.Contains(failure.LastSyncError, "git-backed") {
		t.Fatalf("git failure should explain itself, got %q", failure.LastSyncError)
	}
	if q.syncedContains(gitRepo.ID) {
		t.Fatalf("git repo must not stamp last_synced_at")
	}
	if !q.syncedContains(helmRepo.ID) {
		t.Fatalf("the git repo starved the repository after it")
	}
}

// TestHandleCatalogSyncStopsOnCancelledContext — per-repo isolation must not
// turn a worker shutdown into a fleet-wide false alarm.
//
// Once every failure path `continue`s, a cancelled context walks the whole
// remaining list, fast-failing each repo with `context canceled`, appending a
// failure, and firing two doomed DB writes apiece — so a rolling restart
// reports "40 of 42 repositories failed" and blames 40 healthy repositories.
// The conventional `if ctx.Err() != nil { break }` guard (agent_token_rotate.go,
// control_plane_snapshot.go) stops the sweep instead, and the returned message
// says "aborted" so the count is not read as broken repos.
func TestHandleCatalogSyncStopsOnCancelledContext(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })
	defer httpclient.DisableGuardForTest()()

	srv, seen := indexServer(t, http.StatusOK)
	repos := make([]sqlc.HelmRepository, 0, 5)
	for i := range 5 {
		repos = append(repos, sqlc.HelmRepository{ID: uuid.New(), Name: string(rune('a' + i)), Url: srv.URL, RepoType: "helm"})
	}
	q := &catalogSweepQuerier{repos: repos}
	runtimeDeps = RuntimeDependencies{Queries: q, Log: slog.Default()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := HandleCatalogSync(ctx, &asynq.Task{})
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("a cancelled sweep must report itself as aborted, got: %v", err)
	}
	if got := seen(); len(got) != 0 {
		t.Fatalf("cancelled sweep still issued %d index fetches", len(got))
	}
	if len(q.failures) != 0 {
		t.Fatalf("shutdown must not record %d repositories as failing: %+v", len(q.failures), q.failures)
	}
	if len(q.lastSynced) != 0 {
		t.Fatalf("cancelled sweep stamped %d repositories as freshly synced", len(q.lastSynced))
	}
}

// TestHandleCatalogSyncAppliesRepoAuth — the scheduled index fetch must carry
// the repository's stored credentials, exactly as the operator-triggered sync
// already does via applyRepoIndexAuth.
//
// Pre-fix the Authorization header was absent: the worker built the request
// itself and never touched auth_config.
func TestHandleCatalogSyncAppliesRepoAuth(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })
	defer httpclient.DisableGuardForTest()()

	basicSrv, basicSeen := indexServer(t, http.StatusOK)
	bearerSrv, bearerSeen := indexServer(t, http.StatusOK)
	anonSrv, anonSeen := indexServer(t, http.StatusOK)

	repos := []sqlc.HelmRepository{
		{ID: uuid.New(), Name: "private-basic", Url: basicSrv.URL, RepoType: "helm",
			AuthType: "basic", AuthConfig: json.RawMessage(`{"username":"u","password":"p"}`)},
		{ID: uuid.New(), Name: "private-bearer", Url: bearerSrv.URL, RepoType: "helm",
			AuthType: "bearer", AuthConfig: json.RawMessage(`{"token":"tok"}`)},
		{ID: uuid.New(), Name: "public", Url: anonSrv.URL, RepoType: "helm"},
	}
	q := &catalogSweepQuerier{repos: repos}
	runtimeDeps = RuntimeDependencies{Queries: q, Log: slog.Default()}

	if err := HandleCatalogSync(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	basicReqs, bearerReqs, anonReqs := basicSeen(), bearerSeen(), anonSeen()
	if len(basicReqs) != 1 {
		t.Fatalf("basic-auth repo: expected 1 index request, got %d", len(basicReqs))
	}
	user, pass, ok := basicReqs[0].BasicAuth()
	if !ok || user != "u" || pass != "p" {
		t.Fatalf("basic credentials not sent on the scheduled index fetch (ok=%v user=%q)", ok, user)
	}
	if len(bearerReqs) != 1 || bearerReqs[0].Header.Get("Authorization") != "Bearer tok" {
		t.Fatalf("bearer token not sent on the scheduled index fetch: %q", bearerReqs[0].Header.Get("Authorization"))
	}
	if len(anonReqs) != 1 || anonReqs[0].Header.Get("Authorization") != "" {
		t.Fatalf("public repo must stay unauthenticated, got %q", anonReqs[0].Header.Get("Authorization"))
	}
}

// TestHandleCatalogSyncAppliesRepoAuthToChartAssets — the credentials must
// reach the chart ARCHIVE fetch too, not only index.yaml.
//
// Without it a private ChartMuseum/Artifactory/Nexus authenticates its index,
// the sweep writes every chart and version row, and then 401s on every .tgz —
// silently, since fetchChartAssets swallows the failure by design. The rows
// land with values_schema `{}` and empty values/README, so the install form
// and YAML editor are blank, and it is PERMANENT: the next sweep finds the
// version row via GetHelmChartVersion and skips it, so the assets are never
// re-fetched even once the credentials work.
//
// This gap only became reachable when the index fetch learned to authenticate;
// before that the 401 on index.yaml aborted the sweep before any row existed.
func TestHandleCatalogSyncAppliesRepoAuthToChartAssets(t *testing.T) {
	saved := runtimeDeps
	t.Cleanup(func() { runtimeDeps = saved })
	defer httpclient.DisableGuardForTest()()

	srv, requestsFor := chartAssetServer(t)
	repoRecord := sqlc.HelmRepository{
		ID: uuid.New(), Name: "private", Url: srv.URL, RepoType: "helm",
		AuthType: "basic", AuthConfig: json.RawMessage(`{"username":"u","password":"p"}`),
	}
	q := &catalogSweepQuerier{repos: []sqlc.HelmRepository{repoRecord}}
	runtimeDeps = RuntimeDependencies{Queries: q, Log: slog.Default()}

	if err := HandleCatalogSync(context.Background(), &asynq.Task{}); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	assetReqs := requestsFor("/charts/app-1.0.0.tgz")
	if len(assetReqs) != 1 {
		t.Fatalf("expected exactly 1 chart-archive fetch, got %d", len(assetReqs))
	}
	user, pass, ok := assetReqs[0].BasicAuth()
	if !ok || user != "u" || pass != "p" {
		t.Fatalf("chart archive fetched WITHOUT the repository credentials (ok=%v user=%q) — every version of this repo would land with an empty values schema, permanently", ok, user)
	}
}

// TestFetchChartAssetsDoesNotLeakCredentialsCrossHost — the repository's
// credentials follow the chart archive only while it stays on the repository's
// own host. index.yaml entries routinely carry absolute URLs to a CDN or a
// GitHub release; helm itself requires --pass-credentials to widen this, and
// an operator's Artifactory password has no business on a third-party host.
func TestFetchChartAssetsDoesNotLeakCredentialsCrossHost(t *testing.T) {
	defer httpclient.DisableGuardForTest()()

	elsewhere, seen := chartAssetServer(t)
	repoRecord := sqlc.HelmRepository{
		// A different host from `elsewhere`, which is where the URL points.
		Url: "http://127.0.0.1:1/charts", RepoType: "helm",
		AuthType: "bearer", AuthConfig: json.RawMessage(`{"token":"tok"}`),
	}

	fetchChartAssets(context.Background(), repoRecord, []string{elsewhere.URL + "/charts/app-1.0.0.tgz"})

	reqs := seen("/charts/app-1.0.0.tgz")
	if len(reqs) != 1 {
		t.Fatalf("expected the third-party archive to be fetched once, got %d", len(reqs))
	}
	if got := reqs[0].Header.Get("Authorization"); got != "" {
		t.Fatalf("repository credentials leaked to a third-party chart host: %q", got)
	}
}

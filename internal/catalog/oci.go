package catalog

// OCI chart-repository ingest.
//
// This was `(*handler.CatalogHandler).fetchAndIngestOCIRepo` and its helpers.
// It is moved here verbatim (bar the receiver → explicit Querier/logger) so
// the worker's catalog:sync sweep can reach the SAME implementation instead of
// falling back to the HTTP index path, which cannot describe an oci:// URL at
// all. Nothing about the ingest itself changed in the move.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"helm.sh/helm/v3/pkg/registry"
	"oras.land/oras-go/v2/registry/remote"
	remoteauth "oras.land/oras-go/v2/registry/remote/auth"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// MaxOCIChartVersionsPerChart caps how many tags per chart the OCI ingest
// pulls. Each tag costs a registry manifest pull, so this is a hard bound on
// the work one repository can create.
const MaxOCIChartVersionsPerChart = 10
const MaxOCIResponseBytes int64 = 64 << 20

const (
	ociChartMaxCount           = 100
	ociChartNameMaxBytes       = 255
	ociTagPageSize             = 100
	ociTagMaxPages             = 10
	ociTagMaxTotal             = ociTagPageSize * ociTagMaxPages
	ociMetadataMaxBytes  int64 = 4 << 20
)

var ociChartNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)

// OCIQuerier is the narrow DB surface the OCI ingest needs. Both
// handler.CatalogQuerier and worker tasks.RuntimeQuerier satisfy it.
type OCIQuerier interface {
	GetHelmChartByRepoAndName(ctx context.Context, arg sqlc.GetHelmChartByRepoAndNameParams) (sqlc.HelmChart, error)
	CreateHelmChart(ctx context.Context, arg sqlc.CreateHelmChartParams) (sqlc.HelmChart, error)
	GetHelmChartVersion(ctx context.Context, arg sqlc.GetHelmChartVersionParams) (sqlc.HelmChartVersion, error)
	CreateHelmChartVersion(ctx context.Context, arg sqlc.CreateHelmChartVersionParams) (sqlc.HelmChartVersion, error)
}

// OCIAuthConfig is the JSON shape we expect inside HelmRepository.auth_config
// for OCI repositories. All fields are optional; with no fields set the client
// will pull anonymously and require an explicit chart list (see selectOCICharts).
type OCIAuthConfig struct {
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	Charts   []string `json:"charts,omitempty"`
	// AllowCatalog opts in to using the OCI distribution-spec /v2/_catalog
	// endpoint when no explicit chart list is provided. Many registries
	// (Docker Hub, GHCR for unauthenticated users) do not implement it, so
	// we leave it off by default to fail loudly rather than silently ingest
	// nothing.
	AllowCatalog bool `json:"allow_catalog,omitempty"`
}

// ParseOCIAuthConfig is a permissive decoder — missing or invalid JSON falls
// back to an empty config rather than blocking the sync.
func ParseOCIAuthConfig(raw []byte) OCIAuthConfig {
	var cfg OCIAuthConfig
	if len(raw) == 0 {
		return cfg
	}
	_ = json.Unmarshal(raw, &cfg)
	return cfg
}

// IngestOCIRepo is the OCI counterpart to the index.yaml ingest.
//
// OCI registries do not expose a Helm-style index.yaml; charts are stored as
// individual artifacts addressed by `<registry>/<path>/<chart>:<tag>`. There
// is also no universally-supported way to list all charts in a namespace —
// the OCI distribution spec defines `/v2/_catalog`, but Docker Hub and most
// "anonymous" GHCR namespaces refuse it. We therefore prefer an explicit
// chart-name list (auth_config.charts) and only attempt /v2/_catalog when
// the operator opts in via auth_config.allow_catalog.
func IngestOCIRepo(ctx context.Context, q OCIQuerier, repo sqlc.HelmRepository, dec Decryptor, log *slog.Logger) (chartCount, versionCount int, err error) {
	if log == nil {
		log = slog.Default()
	}
	// Fail the ingest rather than silently falling back to an anonymous pull:
	// a private registry answers an anonymous pull with 401, which would be
	// recorded against the repository as an upstream authentication problem
	// and send the operator looking at registry ACLs instead of at the Fernet
	// key. See ErrAuthConfigUnavailable.
	cfg, err := ResolveOCIAuthConfig(repo, dec)
	if err != nil {
		return 0, 0, err
	}
	rc, err := NewOCIRegistryClient(ctx, repo.Url, cfg, MaxOCIResponseBytes)
	if err != nil {
		return 0, 0, fmt.Errorf("init OCI registry client: %w", err)
	}

	chartNames, err := selectOCICharts(ctx, repo, cfg)
	if err != nil {
		return 0, 0, err
	}
	if len(chartNames) == 0 {
		return 0, 0, errors.New("no charts to ingest: provide auth_config.charts or set auth_config.allow_catalog=true on a registry that supports /v2/_catalog")
	}

	base := strings.TrimPrefix(strings.TrimRight(repo.Url, "/"), OCIPrefix)
	for _, chartName := range chartNames {
		chartName = strings.Trim(chartName, "/ ")
		if chartName == "" {
			continue
		}
		chartRef := base + "/" + chartName
		tags, tagErr := ListOCIRegistryTags(ctx, chartRef, cfg)
		if tagErr != nil {
			log.Warn("OCI tags fetch failed", "chart", chartRef, "error", tagErr)
			continue
		}
		tags = SelectOCITags(tags, MaxOCIChartVersionsPerChart)
		if len(tags) == 0 {
			continue
		}

		// Pull the latest tag's manifest first to populate chart metadata
		// (description, icon, home, etc.) before we create the HelmChart row.
		latest, latestErr := rc.Pull(chartRef+":"+tags[0],
			registry.PullOptWithChart(true),
			registry.PullOptIgnoreMissingProv(true),
		)
		if latestErr != nil {
			log.Warn("OCI pull (latest) failed", "ref", chartRef+":"+tags[0], "error", latestErr)
			continue
		}
		meta := OCIMetadataFromPull(latest)

		dbChart, dbErr := q.GetHelmChartByRepoAndName(ctx, sqlc.GetHelmChartByRepoAndNameParams{
			RepositoryID: repo.ID,
			Name:         chartName,
		})
		if dbErr != nil {
			keywordsJSON, _ := json.Marshal(meta.Keywords)
			if len(keywordsJSON) == 0 {
				keywordsJSON = []byte(`[]`)
			}
			maintList := make([]map[string]string, 0, len(meta.Maintainers))
			for _, m := range meta.Maintainers {
				maintList = append(maintList, map[string]string{"name": m.Name, "email": m.Email, "url": m.URL})
			}
			maintJSON, _ := json.Marshal(maintList)
			if len(maintJSON) == 0 {
				maintJSON = []byte(`[]`)
			}
			dbChart, dbErr = q.CreateHelmChart(ctx, sqlc.CreateHelmChartParams{
				RepositoryID: repo.ID,
				Name:         chartName,
				DisplayName:  chartName,
				Description:  meta.Description,
				IconUrl:      meta.Icon,
				HomeUrl:      meta.Home,
				Category:     "",
				Keywords:     keywordsJSON,
				Maintainers:  maintJSON,
				Deprecated:   meta.Deprecated,
			})
			if dbErr != nil {
				return chartCount, versionCount, fmt.Errorf("create OCI chart %s: %w", chartName, dbErr)
			}
		}
		chartCount++

		for _, tag := range tags {
			if tag == "" {
				continue
			}
			if _, getErr := q.GetHelmChartVersion(ctx, sqlc.GetHelmChartVersionParams{
				ChartID: dbChart.ID,
				Version: tag,
			}); getErr == nil {
				continue
			}
			pulled := latest
			if tag != tags[0] {
				p, perr := rc.Pull(chartRef+":"+tag,
					registry.PullOptWithChart(true),
					registry.PullOptIgnoreMissingProv(true),
				)
				if perr != nil {
					log.Warn("OCI pull failed", "ref", chartRef+":"+tag, "error", perr)
					continue
				}
				pulled = p
			}

			urlsJSON, _ := json.Marshal([]string{chartRef + ":" + tag})
			appVersion := ""
			digest := ""
			if pulled != nil {
				if pulled.Chart != nil && pulled.Chart.Meta != nil {
					appVersion = pulled.Chart.Meta.AppVersion
				}
				if pulled.Manifest != nil {
					digest = pulled.Manifest.Digest
				}
			}

			if _, createErr := q.CreateHelmChartVersion(ctx, sqlc.CreateHelmChartVersionParams{
				ChartID:           dbChart.ID,
				Version:           tag,
				AppVersion:        appVersion,
				Digest:            digest,
				Urls:              urlsJSON,
				ValuesSchema:      json.RawMessage(`{}`),
				DefaultValues:     "",
				Readme:            "",
				CreatedAtUpstream: pgtype.Timestamptz{},
			}); createErr != nil {
				return chartCount, versionCount, fmt.Errorf("create OCI chart version %s/%s: %w", chartName, tag, createErr)
			}
			versionCount++
		}
	}

	return chartCount, versionCount, nil
}

// ListOCIRegistryTags bounds both response bytes and server-driven pagination.
// Helm's registry.Client.Tags accumulates an unlimited number of ORAS pages
// under context.Background, so catalog ingest must never use it directly.
func ListOCIRegistryTags(ctx context.Context, chartReference string, cfg OCIAuthConfig) ([]string, error) {
	host := strings.SplitN(chartReference, "/", 2)[0]
	if host == "" || !strings.Contains(chartReference, "/") {
		return nil, errors.New("OCI chart reference is invalid")
	}
	if err := httpclient.GuardPublicHost("https://" + host); err != nil {
		return nil, fmt.Errorf("OCI registry URL blocked: %w", err)
	}
	client := httpclient.SafeClientWithLimit(60*time.Second, ociMetadataMaxBytes)
	client.Transport = &catalogContextTransport{ctx: ctx, base: client.Transport}
	return listOCIRegistryTags(ctx, chartReference, cfg, client)
}

func listOCIRegistryTags(ctx context.Context, chartReference string, cfg OCIAuthConfig, client *http.Client) ([]string, error) {
	repository, err := remote.NewRepository(chartReference)
	if err != nil {
		return nil, errors.New("OCI chart reference is invalid")
	}
	credential := remoteauth.EmptyCredential
	if cfg.Username != "" || cfg.Password != "" {
		credential.Username = cfg.Username
		credential.Password = cfg.Password
	}
	repository.Client = &remoteauth.Client{
		Client:     client,
		Credential: remoteauth.StaticCredential(repository.Reference.Registry, credential),
	}
	repository.TagListPageSize = ociTagPageSize
	repository.TagListMaxPages = ociTagMaxPages
	repository.MaxMetadataBytes = ociMetadataMaxBytes
	tags := make([]string, 0, min(ociTagPageSize, ociTagMaxTotal))
	err = repository.Tags(ctx, "", func(page []string) error {
		if len(page) > ociTagMaxTotal-len(tags) {
			return errors.New("OCI tag listing exceeds the configured total limit")
		}
		tags = append(tags, page...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("bounded OCI tag listing failed: %w", err)
	}
	return tags, nil
}

// NewOCIRegistryClient is the single construction path for catalog OCI reads.
// It rejects private/metadata destinations at URL-validation and dial time,
// applies a wall-clock timeout, and bounds every registry/token/blob response.
func NewOCIRegistryClient(ctx context.Context, repoURL string, cfg OCIAuthConfig, maximum int64) (*registry.Client, error) {
	host, _, err := SplitOCIURL(repoURL)
	if err != nil {
		return nil, err
	}
	if err := httpclient.GuardPublicHost("https://" + host); err != nil {
		return nil, fmt.Errorf("OCI registry URL blocked: %w", err)
	}
	if maximum <= 0 || maximum > MaxOCIResponseBytes {
		maximum = MaxOCIResponseBytes
	}
	client := httpclient.SafeClientWithLimit(60*time.Second, maximum)
	client.Transport = &catalogContextTransport{ctx: ctx, base: client.Transport}
	clientOpts := []registry.ClientOption{
		registry.ClientOptWriter(io.Discard),
		registry.ClientOptHTTPClient(client),
	}
	if cfg.Username != "" || cfg.Password != "" {
		clientOpts = append(clientOpts, registry.ClientOptBasicAuth(cfg.Username, cfg.Password))
	}
	return registry.NewClient(clientOpts...)
}

type catalogContextTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t *catalogContextTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx := t.ctx
	if ctx == nil {
		ctx = request.Context()
	}
	return t.base.RoundTrip(request.Clone(ctx))
}

// SelectOCITags filters and orders registry tags newest-first, then caps.
func SelectOCITags(tags []string, limit int) []string {
	filtered := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || strings.HasSuffix(tag, "-metadata") {
			continue
		}
		filtered = append(filtered, tag)
	}
	slices.SortStableFunc(filtered, CompareVersionsDesc)
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered
}

// OCIChartMaintainer is a schema-friendly chart maintainer entry.
type OCIChartMaintainer struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	URL   string `json:"url"`
}

// OCIChartMeta is a flattened, schema-friendly view of the bits of
// chart.Metadata that we persist on HelmChart.
type OCIChartMeta struct {
	Description string
	Icon        string
	Home        string
	Deprecated  bool
	Keywords    []string
	Maintainers []OCIChartMaintainer
}

// OCIMetadataFromPull projects a registry.PullResult down to the fields we
// store on HelmChart. Returns zero-value fields when the manifest doesn't
// carry chart metadata (e.g. when WithChart is false, helm still returns
// Chart.Meta from the config blob).
func OCIMetadataFromPull(p *registry.PullResult) OCIChartMeta {
	if p == nil || p.Chart == nil || p.Chart.Meta == nil {
		return OCIChartMeta{}
	}
	m := p.Chart.Meta
	out := OCIChartMeta{
		Description: m.Description,
		Icon:        m.Icon,
		Home:        m.Home,
		Deprecated:  m.Deprecated,
		Keywords:    append([]string(nil), m.Keywords...),
	}
	for _, mm := range m.Maintainers {
		if mm == nil {
			continue
		}
		out.Maintainers = append(out.Maintainers, OCIChartMaintainer{
			Name:  mm.Name,
			Email: mm.Email,
			URL:   mm.URL,
		})
	}
	return out
}

// selectOCICharts decides which chart names the sync will target. Order:
//  1. auth_config.charts — explicit list (authoritative).
//  2. /v2/_catalog probe (only when AllowCatalog is true).
func selectOCICharts(ctx context.Context, repo sqlc.HelmRepository, cfg OCIAuthConfig) ([]string, error) {
	if len(cfg.Charts) > 0 {
		return normalizeOCIChartNames(cfg.Charts)
	}
	if !cfg.AllowCatalog {
		return nil, nil
	}
	charts, err := probeOCICatalog(ctx, repo.Url, cfg.Username, cfg.Password)
	if err != nil {
		return nil, err
	}
	return normalizeOCIChartNames(charts)
}

func normalizeOCIChartNames(charts []string) ([]string, error) {
	if len(charts) > ociChartMaxCount {
		return nil, fmt.Errorf("OCI chart list exceeds %d entries", ociChartMaxCount)
	}
	out := make([]string, 0, len(charts))
	seen := make(map[string]struct{}, len(charts))
	for _, raw := range charts {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if len(name) > ociChartNameMaxBytes || !ociChartNamePattern.MatchString(name) {
			return nil, fmt.Errorf("OCI chart name %q is invalid", name)
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

// probeOCICatalog calls /v2/_catalog on the OCI registry's host. Many
// registries (Docker Hub, anonymous GHCR namespaces, ECR public) refuse this
// endpoint with 401/404; we treat any non-200 as "no catalog support".
//
// The endpoint returns charts repository-wide, not scoped to the repo's path
// prefix; we filter by the path the repo URL points at.
func probeOCICatalog(ctx context.Context, repoURL, username, password string) ([]string, error) {
	host, pathPrefix, err := SplitOCIURL(repoURL)
	if err != nil {
		return nil, err
	}
	scheme := "https"
	catalogURL := scheme + "://" + host + "/v2/_catalog?n=1000"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build catalog request: %w", err)
	}
	if username != "" || password != "" {
		req.SetBasicAuth(username, password)
	}
	client := httpclient.SafeClientWithLimit(15*time.Second, 4<<20)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call /v2/_catalog: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry %s does not support /v2/_catalog (status %d): set auth_config.charts to enumerate manually", host, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read catalog response: %w", err)
	}
	var parsed struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse catalog response: %w", err)
	}
	out := make([]string, 0, len(parsed.Repositories))
	prefix := strings.TrimRight(pathPrefix, "/")
	for _, r := range parsed.Repositories {
		if prefix == "" {
			out = append(out, r)
			continue
		}
		if strings.HasPrefix(r, prefix+"/") {
			out = append(out, strings.TrimPrefix(r, prefix+"/"))
		} else if r == prefix {
			out = append(out, r)
		}
	}
	return out, nil
}

// SplitOCIURL parses an oci://host[:port]/path URL into (host, path).
func SplitOCIURL(repoURL string) (host, path string, err error) {
	if !IsOCIURL(repoURL) {
		return "", "", fmt.Errorf("not an OCI URL: %s", repoURL)
	}
	// url.Parse needs an http-like scheme to populate Host/Path correctly.
	swapped := "https://" + strings.TrimPrefix(strings.TrimSpace(repoURL), OCIPrefix)
	u, err := url.Parse(swapped)
	if err != nil {
		return "", "", fmt.Errorf("parse OCI URL: %w", err)
	}
	return u.Host, strings.TrimPrefix(u.Path, "/"), nil
}

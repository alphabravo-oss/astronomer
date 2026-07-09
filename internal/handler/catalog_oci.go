package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	semver "github.com/Masterminds/semver/v3"
	"github.com/jackc/pgx/v5/pgtype"
	"helm.sh/helm/v3/pkg/registry"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// OCIPrefix identifies an OCI Helm registry URL.
const OCIPrefix = "oci://"
const maxOCIChartVersionsPerChart = 10

// IsOCIRepo reports whether the given URL is an OCI Helm registry reference.
//
// Helm itself uses this same prefix check to dispatch between traditional
// HTTP-served chart repositories and OCI artifact registries.
func IsOCIRepo(repoURL string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(repoURL)), OCIPrefix)
}

// ociAuthConfig is the JSON shape we expect inside HelmRepository.auth_config
// for OCI repositories. All fields are optional; with no fields set the client
// will pull anonymously and require an explicit chart list (see selectCharts).
type ociAuthConfig struct {
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

// parseOCIAuthConfig is a permissive decoder — missing or invalid JSON falls
// back to an empty config rather than blocking the sync.
func parseOCIAuthConfig(raw []byte) ociAuthConfig {
	var cfg ociAuthConfig
	if len(raw) == 0 {
		return cfg
	}
	_ = json.Unmarshal(raw, &cfg)
	return cfg
}

// fetchAndIngestOCIRepo is the OCI counterpart to fetchAndIngestRepoIndex.
//
// OCI registries do not expose a Helm-style index.yaml; charts are stored as
// individual artifacts addressed by `<registry>/<path>/<chart>:<tag>`. There
// is also no universally-supported way to list all charts in a namespace —
// the OCI distribution spec defines `/v2/_catalog`, but Docker Hub and most
// "anonymous" GHCR namespaces refuse it. We therefore prefer an explicit
// chart-name list (auth_config.charts) and only attempt /v2/_catalog when
// the operator opts in via auth_config.allow_catalog.
func (h *CatalogHandler) fetchAndIngestOCIRepo(ctx context.Context, repo sqlc.HelmRepository) (chartCount, versionCount int, err error) {
	cfg := parseOCIAuthConfig(repo.AuthConfig)
	clientOpts := []registry.ClientOption{
		registry.ClientOptWriter(io.Discard),
	}
	if cfg.Username != "" || cfg.Password != "" {
		clientOpts = append(clientOpts, registry.ClientOptBasicAuth(cfg.Username, cfg.Password))
	}
	rc, err := registry.NewClient(clientOpts...)
	if err != nil {
		return 0, 0, fmt.Errorf("init OCI registry client: %w", err)
	}

	chartNames, err := h.selectOCICharts(ctx, repo, cfg)
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
		tags, tagErr := rc.Tags(chartRef)
		if tagErr != nil {
			h.log.Warn("OCI tags fetch failed", "chart", chartRef, "error", tagErr)
			continue
		}
		tags = selectOCITags(tags, maxOCIChartVersionsPerChart)
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
			h.log.Warn("OCI pull (latest) failed", "ref", chartRef+":"+tags[0], "error", latestErr)
			continue
		}
		meta := ociMetadataFromPull(latest)

		dbChart, dbErr := h.queries.GetHelmChartByRepoAndName(ctx, sqlc.GetHelmChartByRepoAndNameParams{
			RepositoryID: repo.ID,
			Name:         chartName,
		})
		if dbErr != nil {
			keywordsJSON, _ := json.Marshal(meta.keywords)
			if len(keywordsJSON) == 0 {
				keywordsJSON = []byte(`[]`)
			}
			maintList := make([]map[string]string, 0, len(meta.maintainers))
			for _, m := range meta.maintainers {
				maintList = append(maintList, map[string]string{"name": m.Name, "email": m.Email, "url": m.URL})
			}
			maintJSON, _ := json.Marshal(maintList)
			if len(maintJSON) == 0 {
				maintJSON = []byte(`[]`)
			}
			dbChart, dbErr = h.queries.CreateHelmChart(ctx, sqlc.CreateHelmChartParams{
				RepositoryID: repo.ID,
				Name:         chartName,
				DisplayName:  chartName,
				Description:  meta.description,
				IconUrl:      meta.icon,
				HomeUrl:      meta.home,
				Category:     "",
				Keywords:     keywordsJSON,
				Maintainers:  maintJSON,
				Deprecated:   meta.deprecated,
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
			if _, getErr := h.queries.GetHelmChartVersion(ctx, sqlc.GetHelmChartVersionParams{
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
					h.log.Warn("OCI pull failed", "ref", chartRef+":"+tag, "error", perr)
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

			if _, createErr := h.queries.CreateHelmChartVersion(ctx, sqlc.CreateHelmChartVersionParams{
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

func selectOCITags(tags []string, limit int) []string {
	filtered := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || strings.HasSuffix(tag, "-metadata") {
			continue
		}
		filtered = append(filtered, tag)
	}
	slices.SortStableFunc(filtered, func(a, b string) int {
		va, errA := semver.NewVersion(a)
		vb, errB := semver.NewVersion(b)
		switch {
		case errA == nil && errB == nil:
			return vb.Compare(va)
		case errA == nil:
			return -1
		case errB == nil:
			return 1
		default:
			return strings.Compare(b, a)
		}
	})
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
	}
	return filtered
}

// ociChartMeta is a flattened, schema-friendly view of the bits of
// chart.Metadata that we persist on HelmChart.
type ociChartMeta struct {
	description string
	icon        string
	home        string
	deprecated  bool
	keywords    []string
	maintainers []helmIndexChartMaint
}

// ociMetadataFromPull projects a registry.PullResult down to the fields we
// store on HelmChart. Returns zero-value fields when the manifest doesn't
// carry chart metadata (e.g. when WithChart is false, helm still returns
// Chart.Meta from the config blob).
func ociMetadataFromPull(p *registry.PullResult) ociChartMeta {
	if p == nil || p.Chart == nil || p.Chart.Meta == nil {
		return ociChartMeta{}
	}
	m := p.Chart.Meta
	out := ociChartMeta{
		description: m.Description,
		icon:        m.Icon,
		home:        m.Home,
		deprecated:  m.Deprecated,
		keywords:    append([]string(nil), m.Keywords...),
	}
	for _, mm := range m.Maintainers {
		if mm == nil {
			continue
		}
		out.maintainers = append(out.maintainers, helmIndexChartMaint{
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
func (h *CatalogHandler) selectOCICharts(ctx context.Context, repo sqlc.HelmRepository, cfg ociAuthConfig) ([]string, error) {
	if len(cfg.Charts) > 0 {
		out := make([]string, 0, len(cfg.Charts))
		for _, c := range cfg.Charts {
			if s := strings.TrimSpace(c); s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	}
	if !cfg.AllowCatalog {
		return nil, nil
	}
	return probeOCICatalog(ctx, repo.Url, cfg.Username, cfg.Password)
}

// probeOCICatalog calls /v2/_catalog on the OCI registry's host. Many
// registries (Docker Hub, anonymous GHCR namespaces, ECR public) refuse this
// endpoint with 401/404; we treat any non-200 as "no catalog support".
//
// The endpoint returns charts repository-wide, not scoped to the repo's path
// prefix; we filter by the path the repo URL points at.
func probeOCICatalog(ctx context.Context, repoURL, username, password string) ([]string, error) {
	host, pathPrefix, err := splitOCIURL(repoURL)
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
	client := httpclient.SafeClient(15 * time.Second)
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

// splitOCIURL parses an oci://host[:port]/path URL into (host, path).
func splitOCIURL(repoURL string) (host, path string, err error) {
	if !IsOCIRepo(repoURL) {
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

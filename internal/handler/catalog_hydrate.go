// Sprint 082 — chart-version content hydration (values.yaml + README.md).
//
// The catalog sync deliberately writes default_values="" and readme=""
// on every helm_chart_versions row to keep the sync fast (no per-chart
// tarball downloads). This module backfills those columns lazily on
// the first GetChartValues / GetChartReadme request:
//
//   1. Pull the chart archive (HTTP repo: download from urls[0];
//      OCI repo: registry.Client pull, reuses ingest path's pattern).
//   2. helm SDK loader.LoadArchive parses the .tgz into a *chart.Chart.
//   3. Extract values.yaml + README.md from chart.Raw.
//   4. Best-effort writeback via UpdateHelmChartVersionContent so
//      subsequent requests skip the download entirely.
//
// Errors during fetch/parse are returned to the caller as a 502-ish
// signal; persistence errors are logged and swallowed (the in-memory
// content still serves the current request, the row just stays empty
// for next time).

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/registry"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// chartArchiveMaxBytes caps the tarball size we accept. kube-prom-stack
// is ~1.5MB; this 50MB ceiling is well above any sane chart but
// prevents a malicious repo from streaming gigabytes at us.
const chartArchiveMaxBytes = 50 * 1024 * 1024

// hydrateChartVersion ensures the helm_chart_versions row has its
// default_values + readme populated, fetching + parsing the chart
// archive on cache miss. Returns the (possibly updated) version row
// so callers can pass the hydrated copy back in their response.
func (h *CatalogHandler) hydrateChartVersion(ctx context.Context, version sqlc.HelmChartVersion) (sqlc.HelmChartVersion, error) {
	// Hydrate-once by timestamp: once we've pulled the archive we never pull
	// again, even for charts that ship no values.schema.json / empty README.
	if version.ContentHydratedAt.Valid {
		return version, nil
	}

	chart, err := h.queries.GetHelmChartByID(ctx, version.ChartID)
	if err != nil {
		return version, fmt.Errorf("load chart: %w", err)
	}
	repo, err := h.queries.GetHelmRepositoryByID(ctx, chart.RepositoryID)
	if err != nil {
		return version, fmt.Errorf("load repo: %w", err)
	}

	archive, err := h.fetchChartArchive(ctx, repo, chart, version)
	if err != nil {
		return version, fmt.Errorf("fetch archive: %w", err)
	}

	c, err := loader.LoadArchive(bytes.NewReader(archive))
	if err != nil {
		return version, fmt.Errorf("parse chart archive: %w", err)
	}

	var valuesYAML, readme string
	for _, f := range c.Raw {
		switch strings.ToLower(f.Name) {
		case "values.yaml":
			valuesYAML = string(f.Data)
		case "readme.md":
			readme = string(f.Data)
		}
	}

	// values.schema.json drives the typed install form. Prefer the upstream
	// schema (helm parses it into chart.Schema); else scan Raw for older
	// layouts; else infer a types-only schema from the chart's default values
	// so every chart still gets a form (the UI keeps a YAML toggle for the
	// curated/large cases).
	schema := json.RawMessage(`{}`)
	switch {
	case len(c.Schema) > 0 && json.Valid(c.Schema):
		schema = json.RawMessage(c.Schema)
	default:
		found := false
		for _, f := range c.Raw {
			if strings.ToLower(f.Name) == "values.schema.json" && json.Valid(f.Data) {
				schema = json.RawMessage(f.Data)
				found = true
				break
			}
		}
		if !found && valuesYAML != "" {
			var vals map[string]interface{}
			if err := yaml.Unmarshal([]byte(valuesYAML), &vals); err != nil {
				if h.log != nil {
					h.log.Warn("infer schema: values.yaml parse failed",
						"chart", chart.Name, "version", version.Version, "bytes", len(valuesYAML), "error", err)
				}
			} else if inferred := inferSchema(vals); inferred != nil {
				schema = inferred
			} else if h.log != nil {
				h.log.Warn("infer schema: empty result after parse",
					"chart", chart.Name, "version", version.Version, "keys", len(vals))
			}
		}
	}

	if err := h.queries.UpdateHelmChartVersionContent(ctx, sqlc.UpdateHelmChartVersionContentParams{
		ID:            version.ID,
		DefaultValues: valuesYAML,
		Readme:        readme,
		ValuesSchema:  schema,
	}); err != nil && h.log != nil {
		// Best-effort cache write. Not fatal.
		h.log.Warn("persist hydrated chart content failed",
			"chart_version_id", version.ID, "chart", chart.Name, "version", version.Version, "error", err)
	}

	version.DefaultValues = valuesYAML
	version.Readme = readme
	version.ValuesSchema = schema
	version.ContentHydratedAt = pgtype.Timestamptz{Valid: true}
	return version, nil
}

// fetchChartArchive resolves the chart bytes for a version, routing
// between HTTP and OCI repos. Returns the gzipped tarball.
func (h *CatalogHandler) fetchChartArchive(ctx context.Context, repo sqlc.HelmRepository, chart sqlc.HelmChart, version sqlc.HelmChartVersion) ([]byte, error) {
	if IsOCIRepo(repo.Url) || strings.EqualFold(repo.RepoType, "oci") {
		return h.fetchOCIChartArchive(ctx, repo, chart, version)
	}
	return h.fetchHTTPChartArchive(ctx, repo, version)
}

func (h *CatalogHandler) fetchHTTPChartArchive(ctx context.Context, repo sqlc.HelmRepository, version sqlc.HelmChartVersion) ([]byte, error) {
	var urls []string
	_ = json.Unmarshal(version.Urls, &urls)
	if len(urls) == 0 {
		return nil, fmt.Errorf("no chart URLs recorded for version %s", version.ID)
	}
	target := urls[0]
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		base := strings.TrimRight(repo.Url, "/")
		target = base + "/" + strings.TrimLeft(target, "/")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	// Best-effort basic auth pass-through. The same auth_config format
	// the OCI path consumes works here: {"username": "...", "password": "..."}.
	if strings.EqualFold(repo.AuthType, "basic") && len(repo.AuthConfig) > 0 {
		var creds struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.Unmarshal(repo.AuthConfig, &creds); err == nil && creds.Username != "" {
			req.SetBasicAuth(creds.Username, creds.Password)
		}
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chart download %s returned HTTP %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, chartArchiveMaxBytes))
}

func (h *CatalogHandler) fetchOCIChartArchive(ctx context.Context, repo sqlc.HelmRepository, chart sqlc.HelmChart, version sqlc.HelmChartVersion) ([]byte, error) {
	_ = ctx // registry.Client doesn't take a context; we rely on its internal HTTP defaults

	cfg := parseOCIAuthConfig(repo.AuthConfig)
	clientOpts := []registry.ClientOption{
		registry.ClientOptWriter(io.Discard),
	}
	if cfg.Username != "" {
		clientOpts = append(clientOpts, registry.ClientOptBasicAuth(cfg.Username, cfg.Password))
	}
	rc, err := registry.NewClient(clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("oci client: %w", err)
	}

	base := strings.TrimSuffix(strings.TrimPrefix(repo.Url, "oci://"), "/")
	ref := base + "/" + chart.Name + ":" + version.Version
	pulled, err := rc.Pull(ref,
		registry.PullOptWithChart(true),
		registry.PullOptIgnoreMissingProv(true),
	)
	if err != nil {
		return nil, fmt.Errorf("oci pull %s: %w", ref, err)
	}
	if pulled == nil || pulled.Chart == nil || len(pulled.Chart.Data) == 0 {
		return nil, fmt.Errorf("oci pull returned empty chart for %s", ref)
	}
	return pulled.Chart.Data, nil
}

// inferSchema derives a types-only JSON Schema from a chart's coalesced default
// values, so charts without an upstream values.schema.json still render a form.
// Field keys become titles; types come from the default value. There are no
// descriptions/enums/validation — those live in values.yaml comments, which
// aren't recoverable on parse. Marked x-astronomer-inferred so the UI can flag
// the form as auto-generated and keep the YAML editor a click away.
func inferSchema(values map[string]interface{}) json.RawMessage {
	if len(values) == 0 {
		return nil
	}
	node, ok := inferNode(values, 0).(map[string]interface{})
	if !ok {
		return nil
	}
	node["x-astronomer-inferred"] = true
	data, err := json.Marshal(node)
	if err != nil {
		return nil
	}
	return data
}

const inferMaxDepth = 8

func inferNode(v interface{}, depth int) interface{} {
	if depth > inferMaxDepth {
		return map[string]interface{}{} // bail out: treat as opaque object
	}
	switch val := v.(type) {
	case map[string]interface{}:
		props := map[string]interface{}{}
		for k, child := range val {
			n := inferNode(child, depth+1)
			if cm, ok := n.(map[string]interface{}); ok {
				cm["title"] = k
			}
			props[k] = n
		}
		return map[string]interface{}{"type": "object", "properties": props}
	case []interface{}:
		items := map[string]interface{}{}
		if len(val) > 0 {
			if m, ok := inferNode(val[0], depth+1).(map[string]interface{}); ok {
				items = m
			}
		}
		return map[string]interface{}{"type": "array", "items": items}
	case string:
		return map[string]interface{}{"type": "string"}
	case bool:
		return map[string]interface{}{"type": "boolean"}
	case float64, int, int64:
		return map[string]interface{}{"type": "number"}
	default:
		return map[string]interface{}{} // nil / unknown: no type constraint
	}
}

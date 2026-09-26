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
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/registry"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// chartArchiveMaxBytes caps the tarball size we accept. kube-prom-stack
// is ~1.5MB; this 50MB ceiling is well above any sane chart but
// prevents a malicious repo from streaming gigabytes at us.
const chartArchiveMaxBytes = 50 * 1024 * 1024

// defaultChartHydrationTimeout is an end-to-end budget covering repository lookup,
// archive download, parsing, and cache persistence. A slow repository must not
// pin an API handler for the HTTP client's former 60-second timeout.
const defaultChartHydrationTimeout = 10 * time.Second

const chartSchemaHydrationVersion = 2

func (h *CatalogHandler) effectiveChartHydrationTimeout() time.Duration {
	if h != nil && h.chartHydrationTimeout > 0 {
		return h.chartHydrationTimeout
	}
	return defaultChartHydrationTimeout
}

// hydrateChartVersion ensures the helm_chart_versions row has its
// default_values + readme populated, fetching + parsing the chart
// archive on cache miss. Returns the (possibly updated) version row
// so callers can pass the hydrated copy back in their response.
func (h *CatalogHandler) hydrateChartVersion(ctx context.Context, version sqlc.HelmChartVersion) (sqlc.HelmChartVersion, error) {
	// Hydrate-once by timestamp: once we've pulled the archive we never pull
	// again, even for charts that ship no values.schema.json / empty README.
	if version.ContentHydratedAt.Valid && hydratedChartSchemaIsCurrent(version.ValuesSchema) {
		return version, nil
	}
	if h == nil {
		return version, errors.New("catalog handler is not configured")
	}

	// The first request owns a detached, strictly bounded hydration. Detaching
	// from caller cancellation lets a second waiter reuse useful work if the
	// first client disconnects, while the 10-second timeout prevents leaks.
	result := h.chartHydration.DoChan(version.ID.String(), func() (any, error) {
		hydrationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), h.effectiveChartHydrationTimeout())
		defer cancel()
		return h.hydrateChartVersionOnce(hydrationCtx, version)
	})
	select {
	case <-ctx.Done():
		return version, ctx.Err()
	case completed := <-result:
		if completed.Err != nil {
			return version, completed.Err
		}
		hydrated, ok := completed.Val.(sqlc.HelmChartVersion)
		if !ok {
			return version, errors.New("chart hydration returned an invalid result")
		}
		return hydrated, nil
	}
}

func (h *CatalogHandler) hydrateChartVersionOnce(ctx context.Context, version sqlc.HelmChartVersion) (sqlc.HelmChartVersion, error) {

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
	var questionsYAML []byte
	for _, f := range c.Raw {
		switch strings.ToLower(f.Name) {
		case "values.yaml":
			valuesYAML = string(f.Data)
		case "readme.md":
			readme = string(f.Data)
		case "questions.yaml", "questions.yml":
			questionsYAML = f.Data
		}
	}
	// Start with a complete type-only schema inferred from values.yaml, then
	// overlay the upstream schema's stronger descriptions, enums and bounds.
	// Some large charts intentionally ship a partial values.schema.json; using
	// it alone hides nearly every configurable value.
	var inferred json.RawMessage
	if valuesYAML != "" {
		var vals map[string]interface{}
		if err := yaml.Unmarshal([]byte(valuesYAML), &vals); err != nil {
			if h.log != nil {
				h.log.Warn("infer schema: values.yaml parse failed",
					"chart", chart.Name, "version", version.Version, "bytes", len(valuesYAML), "error", err)
			}
		} else {
			inferred = inferSchema(vals)
		}
	}
	var upstream json.RawMessage
	if len(c.Schema) > 0 && json.Valid(c.Schema) {
		upstream = json.RawMessage(c.Schema)
	} else {
		for _, f := range c.Raw {
			if strings.ToLower(f.Name) == "values.schema.json" && json.Valid(f.Data) {
				upstream = json.RawMessage(f.Data)
				break
			}
		}
	}
	schema := mergeChartSchemas(inferred, upstream)
	if len(schema) == 0 {
		schema = json.RawMessage(`{}`)
	}
	if len(questionsYAML) > 0 {
		if enriched := enrichSchemaWithRancherQuestions(schema, questionsYAML); enriched != nil {
			schema = enriched
		} else if h.log != nil {
			h.log.Warn("chart questions metadata could not be normalized", "chart", chart.Name, "version", version.Version)
		}
	}
	schema = markHydratedChartSchema(schema)

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

func hydratedChartSchemaIsCurrent(raw json.RawMessage) bool {
	var schema map[string]any
	if json.Unmarshal(raw, &schema) != nil {
		return false
	}
	version, ok := schema["x-astronomer-hydration-version"].(float64)
	return ok && int(version) == chartSchemaHydrationVersion
}

func markHydratedChartSchema(raw json.RawMessage) json.RawMessage {
	var schema map[string]any
	if json.Unmarshal(raw, &schema) != nil {
		return raw
	}
	schema["x-astronomer-hydration-version"] = chartSchemaHydrationVersion
	marked, err := json.Marshal(schema)
	if err != nil {
		return raw
	}
	return marked
}

func mergeChartSchemas(inferred, upstream json.RawMessage) json.RawMessage {
	if len(inferred) == 0 {
		return upstream
	}
	if len(upstream) == 0 {
		return inferred
	}
	var base, overlay map[string]any
	if json.Unmarshal(inferred, &base) != nil || json.Unmarshal(upstream, &overlay) != nil {
		return upstream
	}
	merged := mergeSchemaObjects(base, overlay)
	raw, err := json.Marshal(merged)
	if err != nil {
		return upstream
	}
	return raw
}

func mergeSchemaObjects(base, overlay map[string]any) map[string]any {
	result := make(map[string]any, len(base)+len(overlay))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range overlay {
		baseObject, baseOK := result[key].(map[string]any)
		overlayObject, overlayOK := value.(map[string]any)
		if baseOK && overlayOK {
			result[key] = mergeSchemaObjects(baseObject, overlayObject)
			continue
		}
		result[key] = value
	}
	return result
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

	// SEC-R03: pre-filter + dial-time guard so malicious chart URLs in a
	// repo index cannot SSRF loopback/private/metadata.
	if err := httpclient.GuardPublicHost(target); err != nil {
		return nil, fmt.Errorf("chart URL blocked: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	// Best-effort auth pass-through. Same credential the index sweep uses,
	// unwrapped from the migration-145 envelope by the same helper — this is a
	// third reader of auth_config and reading the raw column here would send
	// the stripped projection (no password) at a private repo.
	//
	// Same-host only, on the same shared helper as the sweep's chart-asset
	// fetch (internal/worker/tasks/catalog_sync.go). `target` comes from the
	// repository's own index.yaml and may be an absolute URL naming ANY host;
	// GuardPublicHost above blocks loopback/private/metadata, not third
	// parties. Without this guard a repo index pointing at attacker.example
	// collects the operator's credential on the first lazy hydrate.
	if catalog.SameHost(repo.Url, target) {
		h.applyRepoIndexAuth(req, repo)
	}

	client := httpclient.SafeClient(h.effectiveChartHydrationTimeout())
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
	cfg, err := h.resolveOCIAuthConfig(repo)
	if err != nil {
		return nil, err
	}
	rc, err := catalog.NewOCIRegistryClient(ctx, repo.Url, cfg, chartArchiveMaxBytes)
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

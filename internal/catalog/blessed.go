// Package catalog — blessed-catalog reconciliation.
//
// The astronomer-catalog repo (catalog.yaml) is the source of truth for the
// charts Astronomer ships as platform defaults. On boot the server fetches it
// from ASTRONOMER_CATALOG_URL and reconciles two tables: helm_repositories (the
// distinct repos, marked is_default) and catalog_blessed_charts (per-entry
// overlays — category, mgmt-cluster safety, version policy). Chart versions and
// values are NOT defined here; they are discovered live from each repo index.
package catalog

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// BlessedSource tags the catalog_blessed_charts rows this loader owns, so a
// reconcile only ever touches its own rows (never an operator's).
const BlessedSource = "catalog.yaml"

const catalogAPIVersion = "catalog.astronomer.io/v1"
const catalogKind = "Catalog"

var validCategories = map[string]bool{
	"security": true, "storage": true, "observability": true, "networking": true,
	"database": true, "gitops": true, "ai": true, "other": true,
}

var repoNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
var versionPolicyRe = regexp.MustCompile(`^last:[0-9]+$`)

// CatalogDoc mirrors catalog.yaml. sigs.k8s.io/yaml routes through JSON, so the
// struct tags are json tags.
type CatalogDoc struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   CatalogMeta    `json:"metadata"`
	Entries    []CatalogEntry `json:"entries"`
}

type CatalogMeta struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type CatalogEntry struct {
	Chart       string `json:"chart"`
	Repo        string `json:"repo"`
	RepoName    string `json:"repoName"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Icon        string `json:"icon"`
	MgmtSafe    *bool  `json:"mgmtSafe"` // pointer: absent => true
	Versions    string `json:"versions"`
}

// IsMgmtSafe defaults to true when the field is omitted.
func (e CatalogEntry) IsMgmtSafe() bool { return e.MgmtSafe == nil || *e.MgmtSafe }

// BlessedStore is the narrow DB surface the reconcile needs.
type BlessedStore interface {
	UpsertDefaultHelmRepository(context.Context, sqlc.UpsertDefaultHelmRepositoryParams) error
	DeleteBlessedChartsBySource(context.Context, string) error
	CreateBlessedChart(context.Context, sqlc.CreateBlessedChartParams) error
}

// ParseCatalog decodes and validates catalog.yaml. It enforces the same
// invariants as catalog.schema.json so a malformed catalog is rejected before
// it touches the DB, rather than half-applied.
func ParseCatalog(data []byte) (*CatalogDoc, error) {
	var doc CatalogDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse catalog: %w", err)
	}
	if doc.APIVersion != catalogAPIVersion {
		return nil, fmt.Errorf("unsupported apiVersion %q (want %q)", doc.APIVersion, catalogAPIVersion)
	}
	if doc.Kind != catalogKind {
		return nil, fmt.Errorf("unsupported kind %q (want %q)", doc.Kind, catalogKind)
	}
	if len(doc.Entries) == 0 {
		return nil, fmt.Errorf("catalog has no entries")
	}

	seenRepoName := map[string]string{}
	seenChart := map[string]bool{}
	for i, e := range doc.Entries {
		where := fmt.Sprintf("entry %d (%s)", i, e.Chart)
		if e.Chart == "" || e.Repo == "" || e.RepoName == "" || e.Category == "" {
			return nil, fmt.Errorf("%s: chart, repo, repoName and category are required", where)
		}
		if !strings.HasPrefix(e.Repo, "http://") && !strings.HasPrefix(e.Repo, "https://") {
			return nil, fmt.Errorf("%s: repo must be an http(s) URL, got %q", where, e.Repo)
		}
		if !repoNameRe.MatchString(e.RepoName) {
			return nil, fmt.Errorf("%s: repoName %q must match %s", where, e.RepoName, repoNameRe)
		}
		if !validCategories[e.Category] {
			return nil, fmt.Errorf("%s: unknown category %q", where, e.Category)
		}
		if e.Versions != "" && !versionPolicyRe.MatchString(e.Versions) {
			return nil, fmt.Errorf("%s: versions %q must match last:N", where, e.Versions)
		}
		if prev, ok := seenRepoName[e.RepoName]; ok && prev != e.Repo {
			return nil, fmt.Errorf("%s: repoName %q reused for a different repo URL", where, e.RepoName)
		}
		seenRepoName[e.RepoName] = e.Repo
		key := e.Repo + "\x00" + e.Chart
		if seenChart[key] {
			return nil, fmt.Errorf("%s: duplicate chart in repo %s", where, e.Repo)
		}
		seenChart[key] = true
	}
	return &doc, nil
}

// Reconcile upserts the default repos and replaces this source's blessed-chart
// rows with the catalog's. Not transactional: a crash mid-reconcile is healed
// by the next boot, which re-applies the same desired state.
func Reconcile(ctx context.Context, store BlessedStore, doc *CatalogDoc) error {
	// Distinct repos first, so the blessed rows always reference a seeded repo.
	seen := map[string]bool{}
	for _, e := range doc.Entries {
		if seen[e.RepoName] {
			continue
		}
		seen[e.RepoName] = true
		if err := store.UpsertDefaultHelmRepository(ctx, sqlc.UpsertDefaultHelmRepositoryParams{
			Name:        e.RepoName,
			Url:         e.Repo,
			Description: fmt.Sprintf("Project-maintained Helm repo. Seeded by astronomer-catalog (%s).", e.RepoName),
		}); err != nil {
			return fmt.Errorf("upsert repo %s: %w", e.RepoName, err)
		}
	}

	if err := store.DeleteBlessedChartsBySource(ctx, BlessedSource); err != nil {
		return fmt.Errorf("clear blessed charts: %w", err)
	}
	for _, e := range doc.Entries {
		if err := store.CreateBlessedChart(ctx, sqlc.CreateBlessedChartParams{
			RepoUrl:       e.Repo,
			ChartName:     e.Chart,
			DisplayName:   e.DisplayName,
			Description:   e.Description,
			Category:      e.Category,
			IconUrl:       e.Icon,
			MgmtSafe:      e.IsMgmtSafe(),
			VersionPolicy: e.Versions,
			Source:        BlessedSource,
		}); err != nil {
			return fmt.Errorf("insert blessed chart %s: %w", e.Chart, err)
		}
	}
	return nil
}

// Load fetches catalog.yaml from url, validates and reconciles it. A blank url
// is a no-op (returns 0). Any fetch/parse error is returned so the caller can
// log and keep the previously-reconciled rows.
func Load(ctx context.Context, store BlessedStore, client *http.Client, url string) (int, error) {
	if strings.TrimSpace(url) == "" {
		return 0, nil
	}
	// SSRF guard: the blessed-catalog URL is operator-supplied and fetched
	// server-side; refuse loopback/internal/metadata targets before dialing.
	if err := httpclient.GuardPublicHost(url); err != nil {
		return 0, fmt.Errorf("blessed catalog host is not a permitted public address")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch catalog %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("fetch catalog %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4MiB ceiling
	if err != nil {
		return 0, err
	}
	doc, err := ParseCatalog(body)
	if err != nil {
		return 0, err
	}
	if err := Reconcile(ctx, store, doc); err != nil {
		return 0, err
	}
	return len(doc.Entries), nil
}

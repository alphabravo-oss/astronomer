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
	"crypto/sha256"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// BlessedSource tags the catalog_blessed_charts rows this loader owns, so a
// reconcile only ever touches its own rows (never an operator's).
const BlessedSource = "catalog.yaml"

const catalogAPIVersion = "catalog.astronomer.io/v1"
const catalogKind = "Catalog"
const applicationCatalogAPIVersion = "catalog.astronomer.dev/v1alpha1"
const applicationCatalogKind = "ApplicationCatalog"

var validCategories = map[string]bool{
	"security": true, "storage": true, "observability": true, "networking": true,
	"database": true, "gitops": true, "ai": true, "other": true,
}

var repoNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
var versionPolicyRe = regexp.MustCompile(`^last:[0-9]+$`)
var immutableGitRevisionRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

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

// ApplicationCatalogDoc is the v1 application-store index. The complete
// application object is persisted as JSON by ReconcileApplicationCatalogV1;
// these typed fields are the minimum trust and lifecycle contract validated
// before any database mutation occurs.
type ApplicationCatalogDoc struct {
	APIVersion   string                    `json:"apiVersion"`
	Kind         string                    `json:"kind"`
	Metadata     ApplicationCatalogMeta    `json:"metadata"`
	Repositories []ApplicationCatalogRepo  `json:"repositories"`
	Applications []ApplicationCatalogEntry `json:"applications"`
}

type ApplicationCatalogMeta struct {
	SchemaVersion        int    `json:"schemaVersion"`
	MinimumReaderVersion int    `json:"minimumReaderVersion"`
	Name                 string `json:"name"`
	DisplayName          string `json:"displayName"`
	Description          string `json:"description"`
	Channel              string `json:"channel"`
}

type ApplicationCatalogRepo struct {
	Name string `json:"name"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

type ApplicationCatalogEntry struct {
	Slug        string                 `json:"slug"`
	Name        string                 `json:"name"`
	Category    string                 `json:"category"`
	SupportTier string                 `json:"supportTier"`
	Artifact    ApplicationCatalogHelm `json:"artifact"`
	Lifecycle   map[string]bool        `json:"lifecycle"`
}

type ApplicationCatalogHelm struct {
	Type       string `json:"type"`
	Repository string `json:"repository"`
	Chart      string `json:"chart"`
	Version    string `json:"version"`
}

type ApplicationCatalogStore interface {
	ReconcileApplicationCatalogV1(context.Context, sqlc.ReconcileApplicationCatalogV1Params) (int64, error)
}

type applicationCatalogFailureStore interface {
	RecordApplicationCatalogSyncFailure(context.Context, sqlc.RecordApplicationCatalogSyncFailureParams) error
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

func ParseApplicationCatalog(data []byte) (*ApplicationCatalogDoc, error) {
	var doc ApplicationCatalogDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse application catalog: %w", err)
	}
	if doc.APIVersion != applicationCatalogAPIVersion || doc.Kind != applicationCatalogKind {
		return nil, fmt.Errorf("unsupported application catalog %q/%q", doc.APIVersion, doc.Kind)
	}
	if strings.TrimSpace(doc.Metadata.Name) == "" {
		return nil, fmt.Errorf("application catalog metadata.name is required")
	}
	if doc.Metadata.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported application catalog schema version %d", doc.Metadata.SchemaVersion)
	}
	if doc.Metadata.MinimumReaderVersion > 1 {
		return nil, fmt.Errorf("application catalog requires reader version %d", doc.Metadata.MinimumReaderVersion)
	}
	if len(doc.Repositories) == 0 || len(doc.Applications) == 0 {
		return nil, fmt.Errorf("application catalog requires repositories and applications")
	}
	repositories := make(map[string]ApplicationCatalogRepo, len(doc.Repositories))
	for index, repository := range doc.Repositories {
		if !repoNameRe.MatchString(repository.Name) {
			return nil, fmt.Errorf("repository %d has invalid name %q", index, repository.Name)
		}
		if repository.Type != "helm" {
			return nil, fmt.Errorf("repository %q has unsupported type %q", repository.Name, repository.Type)
		}
		if !strings.HasPrefix(repository.URL, "https://") {
			return nil, fmt.Errorf("repository %q must use HTTPS", repository.Name)
		}
		if _, exists := repositories[repository.Name]; exists {
			return nil, fmt.Errorf("duplicate repository %q", repository.Name)
		}
		repositories[repository.Name] = repository
	}
	seen := make(map[string]struct{}, len(doc.Applications))
	for index, application := range doc.Applications {
		if !repoNameRe.MatchString(application.Slug) || application.Name == "" || application.Category == "" {
			return nil, fmt.Errorf("application %d has invalid identity or category", index)
		}
		if _, exists := seen[application.Slug]; exists {
			return nil, fmt.Errorf("duplicate application slug %q", application.Slug)
		}
		seen[application.Slug] = struct{}{}
		if application.Artifact.Type != "helm" || application.Artifact.Chart == "" || application.Artifact.Version == "" {
			return nil, fmt.Errorf("application %q must pin a Helm chart version", application.Slug)
		}
		if _, exists := repositories[application.Artifact.Repository]; !exists {
			return nil, fmt.Errorf("application %q references unknown repository %q", application.Slug, application.Artifact.Repository)
		}
		if !application.Lifecycle["install"] || !application.Lifecycle["uninstall"] {
			return nil, fmt.Errorf("application %q must declare install and uninstall lifecycle support", application.Slug)
		}
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
func Load(ctx context.Context, store BlessedStore, client *http.Client, url string) (count int, err error) {
	return LoadSource(ctx, store, SourceOptions{URL: url, Clients: SourceClients{Public: client, Mirror: client}})
}

// LoadSource retrieves a catalog from immutable HTTPS or OCI storage, applies
// an optional verified mirror rewrite, and only then reconciles the document.
func LoadSource(ctx context.Context, store BlessedStore, options SourceOptions) (count int, err error) {
	if strings.TrimSpace(options.URL) == "" {
		return 0, nil
	}
	startedAt := time.Now()
	defer func() {
		observeCatalogSync(options.URL, count, err, time.Since(startedAt))
		if err == nil {
			return
		}
		if recorder, ok := store.(applicationCatalogFailureStore); ok {
			// Failure recording is deliberately best effort: a remote outage must
			// never replace the useful fetch/validation error or stop startup.
			_ = recorder.RecordApplicationCatalogSyncFailure(ctx, sqlc.RecordApplicationCatalogSyncFailureParams{
				SourceUrl: options.URL,
				SyncError: err.Error(),
			})
		}
	}()
	body, revision, identity, verificationStatus, err := fetchCatalogSource(ctx, options)
	if err != nil {
		return 0, err
	}
	var header struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
	}
	if err := yaml.Unmarshal(body, &header); err != nil {
		return 0, fmt.Errorf("parse catalog header: %w", err)
	}
	if header.APIVersion == applicationCatalogAPIVersion && header.Kind == applicationCatalogKind {
		if _, err := ParseApplicationCatalog(body); err != nil {
			return 0, err
		}
		applicationStore, ok := store.(ApplicationCatalogStore)
		if !ok {
			return 0, fmt.Errorf("application catalog persistence is not configured")
		}
		if revision == "" || identity == "" {
			return 0, fmt.Errorf("application catalog source must have an immutable Git, document, or OCI digest identity")
		}
		// Persist the complete validated document, not the deliberately narrow
		// validation struct above. Re-marshalling doc drops presentation fields
		// (icons, summaries, keywords, UI hints, maintainers, and links) that are
		// intentionally not needed by ParseApplicationCatalog's trust checks.
		document, err := yaml.YAMLToJSON(body)
		if err != nil {
			return 0, fmt.Errorf("preserve application catalog document: %w", err)
		}
		digest := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
		count, err := applicationStore.ReconcileApplicationCatalogV1(ctx, sqlc.ReconcileApplicationCatalogV1Params{
			Document:             document,
			SourceUrl:            options.URL,
			SourceRevision:       revision,
			IndexDigest:          digest,
			VerificationStatus:   verificationStatus,
			VerificationIdentity: identity,
		})
		return int(count), err
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

func immutableCatalogIdentity(sourceURL string) (string, string) {
	parts := strings.Split(strings.Trim(sourceURL, "/"), "/")
	for index, part := range parts {
		if !immutableGitRevisionRe.MatchString(part) {
			continue
		}
		identity := "git:" + part
		if index >= 2 {
			identity = fmt.Sprintf("github:%s/%s@%s", parts[index-2], parts[index-1], part)
		}
		return part, identity
	}
	return "", ""
}

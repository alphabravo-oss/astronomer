package handler

import (
	"context"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/catalog"
	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// The OCI ingest itself now lives in internal/catalog so the unattended
// catalog:sync sweep (internal/worker/tasks/catalog_sync.go) can call the same
// implementation — it could not import this package (handler imports
// worker/tasks, so the reverse edge is a cycle). What remains here are the
// package-local names the rest of the handler already uses.

// OCIPrefix identifies an OCI Helm registry URL.
const OCIPrefix = catalog.OCIPrefix

// IsOCIRepo reports whether the given URL is an OCI Helm registry reference.
func IsOCIRepo(repoURL string) bool { return catalog.IsOCIURL(repoURL) }

// parseOCIAuthConfig decodes an already-resolved auth_config document for an
// OCI repository. It takes the DECRYPTED bytes — see resolveOCIAuthConfig for
// the path that starts from a stored row.
func parseOCIAuthConfig(raw []byte) catalog.OCIAuthConfig { return catalog.ParseOCIAuthConfig(raw) }

// resolveOCIAuthConfig unwraps the migration-145 Fernet envelope and decodes
// the OCI credential + chart selection.
func (h *CatalogHandler) resolveOCIAuthConfig(repo sqlc.HelmRepository) (catalog.OCIAuthConfig, error) {
	return catalog.ResolveOCIAuthConfig(repo, h.decryptor())
}

// splitOCIURL parses an oci://host[:port]/path URL into (host, path).
func splitOCIURL(repoURL string) (host, path string, err error) { return catalog.SplitOCIURL(repoURL) }

// fetchAndIngestOCIRepo is the OCI counterpart to fetchAndIngestRepoIndex.
func (h *CatalogHandler) fetchAndIngestOCIRepo(ctx context.Context, repo sqlc.HelmRepository) (chartCount, versionCount int, err error) {
	return catalog.IngestOCIRepo(ctx, h.queries, repo, h.decryptor(), h.log)
}

// applyRepoIndexAuth sets the Authorization header on an index.yaml (or
// test-connection) request from the repository's stored credentials. A
// credential that cannot be decrypted is logged and omitted — never sent as
// ciphertext.
func (h *CatalogHandler) applyRepoIndexAuth(req *http.Request, repo sqlc.HelmRepository) {
	catalog.ApplyIndexAuth(req, repo, h.decryptor(), h.log)
}

// isOCIRepoSpec reports whether the stored repository should be treated as
// an OCI artifact registry. We accept either an oci:// URL or an explicit
// repo_type='oci' marker so operators can override URL-based detection.
func isOCIRepoSpec(repo sqlc.HelmRepository) bool { return catalog.IsOCIRepo(repo) }

// compile-time proof that the handler's querier can drive the shared ingest.
var _ catalog.OCIQuerier = (CatalogQuerier)(nil)

// Repository-kind detection and index authentication, shared by the
// interactive catalog handler (internal/handler/catalog.go) and the
// unattended catalog:sync sweep (internal/worker/tasks/catalog_sync.go).
//
// These three predicates used to live only on the handler side, which is why
// the 6-hourly sweep was blind to repo_type: it appended /index.yaml to every
// stored URL, including oci:// and git ones, and the only observable effect
// was a worker log line. Anything that decides "which ingest does this row
// get" belongs here so both callers answer the question the same way.
package catalog

import (
	"strings"

	semver "github.com/Masterminds/semver/v3"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// OCIPrefix identifies an OCI Helm registry URL.
const OCIPrefix = "oci://"

// MaxIndexVersionsPerChart caps how many recent versions per chart an
// index.yaml ingest keeps. The form/install UI never offers ancient releases,
// and a large public index carries tens of thousands of them.
//
// It lives here because BOTH ingest paths must use it. When only the worker
// capped, the two disagreed destructively: interactive Sync inserted every
// version in the index, then the next 6-hourly sweep's GC deleted everything
// outside its own top-3 — the operator clicked Sync, saw 40 versions, and six
// hours later had 3, with nothing to explain it.
const MaxIndexVersionsPerChart = 3

// CompareVersionsDesc orders two version strings newest-first, for
// slices.SortStableFunc. Parseable semver sorts by precedence and always
// outranks unparseable tags; unparseable tags fall back to reverse
// lexicographic so the order is at least deterministic.
func CompareVersionsDesc(a, b string) int {
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
}

// IsOCIURL reports whether the given URL is an OCI Helm registry reference.
//
// Helm itself uses this same prefix check to dispatch between traditional
// HTTP-served chart repositories and OCI artifact registries.
func IsOCIURL(repoURL string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(repoURL)), OCIPrefix)
}

// IsOCIRepo reports whether the stored repository should be treated as an OCI
// artifact registry. We accept either an oci:// URL or an explicit
// repo_type='oci' marker so operators can override URL-based detection.
func IsOCIRepo(repo sqlc.HelmRepository) bool {
	if strings.EqualFold(strings.TrimSpace(repo.RepoType), "oci") {
		return true
	}
	return IsOCIURL(repo.Url)
}

// IsGitRepo reports whether the stored repository is a git-backed chart source
// (repo_type='git', accepted at create by CatalogHandler.CreateRepo).
//
// There is no clone+index implementation for these on either the interactive
// or the scheduled path. The predicate exists so the sweep can say so per
// repository instead of fetching `<git url>/index.yaml` and reporting a
// confusing 404.
func IsGitRepo(repo sqlc.HelmRepository) bool {
	return strings.EqualFold(strings.TrimSpace(repo.RepoType), "git")
}

// IndexAuthConfig mirrors the auth_config JSON we accept for classic (non-OCI)
// Helm repositories. All fields are optional. Basic auth uses
// username/password; a bearer token can be supplied under either "token" or
// "bearer".
//
// It describes the DECRYPTED document. Since migration 145 the credential is
// held as a Fernet envelope in auth_config_encrypted; construct this via
// ResolveIndexAuthConfig (authconfig.go) rather than unmarshalling
// repo.AuthConfig, which after sealing carries the non-secret projection only.
type IndexAuthConfig struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
	Bearer   string `json:"bearer,omitempty"`
}

package handler

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// catalogCodes is the set of error-code string literals that the
// internal/handler/apierror package has canonicalized. A RespondRequestError
// call whose 4th argument is a bare string literal NOT in this set is a
// candidate for migration to an apierror.* constant.
//
// Keep this in sync with internal/handler/apierror/codes.go.
var catalogCodes = map[string]bool{
	"invalid_body":            true,
	"invalid_id":              true,
	"validation_error":        true,
	"invalid_request":         true,
	"invalid_name":            true,
	"invalid_token":           true,
	"not_found":               true,
	"conflict":                true,
	"authentication_required": true,
	"forbidden":               true,
	"internal_error":          true,
	"db_error":                true,
	"list_error":              true,
	"count_error":             true,
	"create_error":            true,
	"update_error":            true,
	"delete_error":            true,
}

// respondRequestErrorCode matches a single-line RespondRequestError(...) call
// and captures the 4th positional argument when it is a double-quoted string
// literal. Multi-line calls and constant (non-literal) code arguments are not
// matched and are therefore ignored by this lint.
var respondRequestErrorCode = regexp.MustCompile(
	`RespondRequestError\([^,]+,[^,]+,[^,]+,\s*"([a-z0-9_]+)"`,
)

// TestApierrorCatalogCoverage flags RespondRequestError call sites that pass a
// bare error-code string literal which is not part of the apierror catalog.
//
// It is intentionally skipped: the repository still has ~2,000 legacy call
// sites across ~260 distinct code spellings, and this catalog deliberately
// migrates only one worked example (clusters.go Create/Get) for now. Enabling
// this assertion today would fail CI.
//
// TODO(apierror-codemod): once a codemod has (a) expanded the apierror catalog
// to cover the full canonical set and (b) rewritten every RespondRequestError
// call site to reference an apierror.* constant, delete the t.Skip below so
// this test enforces that no new bare-literal codes are introduced.
func TestApierrorCatalogCoverage(t *testing.T) {
	t.Skip("TODO(apierror-codemod): enable after the catalog is expanded and call sites are migrated; see internal/handler/apierror/codes.go")

	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob handler files: %v", err)
	}

	var offenders []string
	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, m := range respondRequestErrorCode.FindAllStringSubmatch(string(src), -1) {
			code := m[1]
			if !catalogCodes[code] {
				offenders = append(offenders, path+": "+code)
			}
		}
	}

	if len(offenders) > 0 {
		t.Errorf("RespondRequestError calls using non-catalog code literals:\n%s",
			strings.Join(offenders, "\n"))
	}
}

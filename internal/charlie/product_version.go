package charlie

import (
	"regexp"
	"strings"

	productversion "github.com/alphabravocompany/astronomer-go/pkg/version"
)

var gitDescribeProductVersion = regexp.MustCompile(`^v?([0-9]+\.[0-9]+\.[0-9]+)-[0-9]+-g[0-9a-f]+(?:-dirty)?$`)

// currentProductDocumentationVersion removes build provenance from a
// git-describe development version so Charlie can select the matching
// immutable product-documentation release. Full build provenance remains in
// Astronomer's operational telemetry and capability status.
func currentProductDocumentationVersion() string {
	return productDocumentationVersion(productversion.Version)
}

func productDocumentationVersion(value string) string {
	value = strings.TrimSpace(value)
	if match := gitDescribeProductVersion.FindStringSubmatch(value); len(match) == 2 {
		return match[1]
	}
	return strings.TrimPrefix(value, "v")
}

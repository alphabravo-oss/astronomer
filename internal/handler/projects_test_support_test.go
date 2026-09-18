package handler

import (
	"encoding/json"

	projectdomain "github.com/alphabravocompany/astronomer-go/internal/projects"
)

// decodeNamespaceList remains a test-only convenience while the production
// namespace decoder belongs to the projects domain package.
func decodeNamespaceList(raw json.RawMessage) []string {
	return projectdomain.Namespaces(raw)
}

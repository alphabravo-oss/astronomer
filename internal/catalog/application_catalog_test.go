package catalog

import (
	"strings"
	"testing"
)

const validApplicationCatalog = `
apiVersion: catalog.astronomer.dev/v1alpha1
kind: ApplicationCatalog
metadata:
  schemaVersion: 1
  minimumReaderVersion: 1
  name: test
  displayName: Test
  channel: development
repositories:
  - name: upstream
    type: helm
    url: https://charts.example.com
applications:
  - slug: example
    name: Example
    category: other
    supportTier: upstream
    artifact:
      type: helm
      repository: upstream
      chart: example
      version: 1.2.3
    lifecycle:
      install: true
      upgrade: true
      rollback: true
      uninstall: true
`

func TestParseApplicationCatalogAcceptsSupportedSchema(t *testing.T) {
	document, err := ParseApplicationCatalog([]byte(validApplicationCatalog))
	if err != nil {
		t.Fatalf("ParseApplicationCatalog: %v", err)
	}
	if document.Metadata.SchemaVersion != 1 || document.Metadata.MinimumReaderVersion != 1 {
		t.Fatalf("unexpected schema negotiation metadata: %+v", document.Metadata)
	}
}

func TestParseApplicationCatalogRejectsUnsupportedSchema(t *testing.T) {
	tests := map[string]string{
		"missing schema version": strings.Replace(validApplicationCatalog, "  schemaVersion: 1\n", "", 1),
		"new schema version":     strings.Replace(validApplicationCatalog, "schemaVersion: 1", "schemaVersion: 2", 1),
		"new minimum reader":     strings.Replace(validApplicationCatalog, "minimumReaderVersion: 1", "minimumReaderVersion: 2", 1),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseApplicationCatalog([]byte(input)); err == nil {
				t.Fatal("expected unsupported schema to be rejected")
			}
		})
	}
}

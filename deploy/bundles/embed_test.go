package builtinbundles

import (
	"strings"
	"testing"
)

func TestCatalogIsImmutableAndFullyPinned(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Components) != 2 {
		t.Fatalf("got %d built-ins, want the two explicitly opted baseline components", len(catalog.Components))
	}
	for _, component := range catalog.Components {
		if !component.DefaultEnabled || strings.Contains(component.Source.Version, "*") || !strings.HasPrefix(component.Source.ChartDigest, "sha256:") {
			t.Fatalf("component is not an exact default: %#v", component)
		}
		encoded := string(Bytes())
		if strings.Contains(encoded, `"tag":"latest"`) || strings.Contains(encoded, `"tag": "latest"`) {
			t.Fatal("built-in catalog contains a floating image tag")
		}
	}
}

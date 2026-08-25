package builtinbundles

import (
	"reflect"
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

func TestCatalogAcceptsDistinctCanonicalHTTPSSources(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	catalog.Components[1].Source.URL = "https://charts.example.test:8443/stable"
	if err := catalog.Validate(); err != nil {
		t.Fatalf("multi-source catalog rejected: %v", err)
	}
}

func TestNormalizeSourceURLIsDeterministic(t *testing.T) {
	want := "https://charts.example.test:8443/stable"
	first, err := NormalizeSourceURL(want)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NormalizeSourceURL(first)
	if err != nil || first != second || first != want {
		t.Fatalf("normalization = %q then %q, err=%v", first, second, err)
	}
}

func TestCatalogRejectsUnsafeOrAmbiguousSourceURLs(t *testing.T) {
	for _, raw := range []string{
		"http://charts.example.test/stable",
		"https://user@charts.example.test/stable",
		"https://charts.example.test/stable?channel=prod",
		"https://charts.example.test/stable#fragment",
		"https://127.0.0.1/stable",
		"https://charts.internal/stable",
		"https://CHARTS.example.test/stable",
		"https://charts.example.test:443/stable",
		"https://charts.example.test/stable/",
		"https://charts.example.test/a/../stable",
		"https://charts.example.test/%73table",
	} {
		t.Run(raw, func(t *testing.T) {
			catalog, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			catalog.Components[0].Source.URL = raw
			if err := catalog.Validate(); err == nil {
				t.Fatalf("unsafe or ambiguous source URL accepted: %q", raw)
			}
		})
	}
}

func TestCurrentCatalogSourceIdentityInputsRemainUnchanged(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(catalog.Components))
	for _, component := range catalog.Components {
		got = append(got, component.Source.URL)
	}
	want := []string{
		"https://prometheus-community.github.io/helm-charts",
		"https://prometheus-community.github.io/helm-charts",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("current catalog source inputs = %v, want %v", got, want)
	}
}

func TestCatalogRejectsConflictingArtifactIdentity(t *testing.T) {
	catalog, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	catalog.Components[1].Source.Chart = catalog.Components[0].Source.Chart
	catalog.Components[1].Source.Version = catalog.Components[0].Source.Version
	if err := catalog.Validate(); err == nil {
		t.Fatal("same source/chart/version with a different digest was accepted")
	}
}

func TestParseRejectsTrailingJSON(t *testing.T) {
	if _, err := Parse(append(Bytes(), []byte(` {}`)...)); err == nil {
		t.Fatal("catalog with trailing JSON was accepted")
	}
}

package distribution

import (
	"context"
	"strings"
	"testing"

	builtinbundles "github.com/alphabravocompany/astronomer-go/deploy/bundles"
)

func verifiedCatalogFixture(t *testing.T) builtinbundles.Catalog {
	t.Helper()
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	catalog.DistributionIdentity = "sha256:" + strings.Repeat("d", 64)
	for index := range catalog.Components {
		component := &catalog.Components[index]
		component.Source.OriginalURL = component.Source.URL
		component.Source.URL = "oci://ghcr.io/alphabravo-oss/astronomer-builtins/charts"
		component.Source.Kind = "helm_oci"
		image := component.Images[0]
		repository, digest, _ := strings.Cut(image, "@")
		host, path, _ := strings.Cut(repository, "/")
		component.Values = map[string]any{"image": map[string]any{"registry": host, "repository": path, "digest": digest}, "extra": []any{image}}
		component.Images = component.Images[:1]
	}
	return catalog
}

func TestProjectLocalRegistryRehostsChartsImagesAndValues(t *testing.T) {
	t.Parallel()
	const local = "astronomer-zot.astronomer.svc.cluster.local:5000"
	catalog, err := projectLocalRegistry(verifiedCatalogFixture(t), local)
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range catalog.Components {
		if component.Source.URL != "oci://"+local+"/alphabravo-oss/astronomer-builtins/charts" {
			t.Fatalf("chart source not localized: %s", component.Source.URL)
		}
		for _, image := range component.Images {
			if !strings.HasPrefix(image, local+"/") || !strings.Contains(image, "@sha256:") {
				t.Fatalf("image not localized with digest: %s", image)
			}
		}
		values := component.Values["image"].(map[string]any)
		if values["registry"] != local {
			t.Fatalf("values registry not localized: %v", values)
		}
		if extra := component.Values["extra"].([]any)[0].(string); !strings.HasPrefix(extra, local+"/") {
			t.Fatalf("values string subject not localized: %s", extra)
		}
	}
	if !strings.HasSuffix(catalog.DistributionIdentity, ";local:"+local) {
		t.Fatalf("identity does not record local projection: %s", catalog.DistributionIdentity)
	}
}

func TestProjectLocalRegistryFailsClosedForHTTPCharts(t *testing.T) {
	t.Parallel()
	catalog, err := builtinbundles.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projectLocalRegistry(catalog, "zot.ns.svc:5000"); err == nil {
		t.Fatal("helm_http chart sources must fail closed under a local registry")
	}
	same, err := projectLocalRegistry(catalog, "")
	if err != nil || same.Components[0].Source.URL != catalog.Components[0].Source.URL {
		t.Fatal("online profile must leave the catalog untouched")
	}
}

func TestDevelopmentCatalogUnavailableUnderLocalRegistry(t *testing.T) {
	t.Parallel()
	loader, err := New(Options{Development: true, LocalRegistry: "zot.ns.svc:5000"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Load(context.Background()); err == nil || !strings.Contains(err.Error(), "REGISTRY_LOCAL") {
		t.Fatalf("expected fail-closed development catalog, got %v", err)
	}
}

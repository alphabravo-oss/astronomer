package upgrade

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/releasecontract"
)

func TestRuntimeBindingsUseExactHistoricalOrCandidateChartSources(t *testing.T) {
	for _, postgres := range []string{"postgres:16-alpine", "postgres:18.6-alpine"} {
		t.Run(postgres, func(t *testing.T) {
			var manifest releasecontract.Manifest
			sources := map[string]string{"utilities.busybox": "busybox:owned", "postgres": postgres, "redis": "valkey/valkey:owned", "dex": "dexidp/dex:owned", "managementRestoreDrill.sidecar": postgres, "managementLogging": "fluent/fluent-bit:owned"}
			seen := map[string]bool{}
			for _, source := range sources {
				if !seen[source] {
					seen[source] = true
					manifest.Astronomer.RuntimeImages = append(manifest.Astronomer.RuntimeImages, releasecontract.RuntimeImage{SourceReference: source, Reference: "example.invalid/owned@sha256:" + strings.Repeat("a", 64)})
				}
			}
			binding, err := RuntimeBindings(manifest, sources)
			if err != nil || len(binding) != 6 || binding["postgres"] != binding["managementRestoreDrill.sidecar"] {
				t.Fatal("exact chart sources did not resolve")
			}
			sources["postgres"] = "postgres:latest"
			if _, err = RuntimeBindings(manifest, sources); err == nil {
				t.Fatal("ambient latest accepted in place of exact signed source")
			}
		})
	}
}
func TestRuntimeBindingsRejectDuplicateMissingMutableOrMalformedDigest(t *testing.T) {
	sources := map[string]string{"utilities.busybox": "owned", "postgres": "owned", "redis": "owned", "dex": "owned", "managementRestoreDrill.sidecar": "owned", "managementLogging": "owned"}
	for _, kind := range []string{"duplicate", "missing", "mutable", "digest"} {
		t.Run(kind, func(t *testing.T) {
			var manifest releasecontract.Manifest
			image := releasecontract.RuntimeImage{SourceReference: "owned", Reference: "example.invalid/owned@sha256:" + strings.Repeat("a", 64)}
			manifest.Astronomer.RuntimeImages = []releasecontract.RuntimeImage{image}
			switch kind {
			case "duplicate":
				manifest.Astronomer.RuntimeImages = append(manifest.Astronomer.RuntimeImages, image)
			case "missing":
				manifest.Astronomer.RuntimeImages = nil
			case "mutable":
				manifest.Astronomer.RuntimeImages[0].Reference = "example.invalid/owned:latest"
			case "digest":
				manifest.Astronomer.RuntimeImages[0].Reference = "example.invalid/owned@sha256:" + strings.Repeat("Z", 64)
			}
			if _, err := RuntimeBindings(manifest, sources); err == nil {
				t.Fatal("invalid runtime binding accepted")
			}
		})
	}
}

package server

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/alphabravocompany/astronomer-go/internal/config"
)

func TestDisconnectedFirstBootDoesNotScheduleLegacyRepositories(t *testing.T) {
	var output bytes.Buffer
	logger := zerolog.New(&output)
	composition := &productionComposition{} // nil query/queue exposes any legacy bootstrap access
	cfg := &config.Config{DistributionDestinationPolicy: `{"restricted":true,"origins":[{"url":"https://10.42.1.7","cidrs":["10.42.1.7/32"]}]}`}
	if err := composition.startApplicationCatalog(t.Context(), cfg, logger); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"blocked_category":"application_catalog"`) {
		t.Fatal("disabled catalog status absent")
	}
	for _, source := range []string{"https://1.1.1.1/catalog.json", "oci://10.42.1.7/catalog@sha256:" + strings.Repeat("a", 64)} {
		cfg.CatalogURL = source
		if err := composition.startApplicationCatalog(t.Context(), cfg, logger); err == nil {
			t.Fatal("unsigned/public fallback admitted in disconnected first boot")
		}
	}
}

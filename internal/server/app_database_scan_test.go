package server

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/config"
)

func TestDatabaseScanProductionCatalogRefusesIgnoredExplicitFeaturesBeforeLegacyLoader(t *testing.T) {
	for _, name := range []string{"scan-without-updates", "scan-without-delivery", "pull-without-scan", "pull-without-delivery"} {
		t.Run(name, func(t *testing.T) {
			cfg := &config.Config{DeliveryEnabled: true, DeliveryDatabaseUpdatesEnabled: true, DeliveryDatabaseScanEnabled: true, DeliverySourceCAFile: "/nonexistent/must-not-reach-trust"}
			expected := "requires enabled delivery"
			switch name {
			case "scan-without-updates":
				cfg.DeliveryDatabaseUpdatesEnabled = false
			case "scan-without-delivery":
				cfg.DeliveryEnabled = false
			case "pull-without-scan":
				cfg.DeliveryDatabaseScanEnabled = false
				cfg.DeliveryDatabaseUpdatesEnabled = false
				cfg.DeliveryDatabaseJobPullCredentialRef = "kubelet"
				expected = "requires explicit measured scanning"
			case "pull-without-delivery":
				cfg.DeliveryEnabled = false
				cfg.DeliveryDatabaseScanEnabled = false
				cfg.DeliveryDatabaseUpdatesEnabled = false
				cfg.DeliveryDatabaseJobPullCredentialRef = "kubelet"
				expected = "requires explicit measured scanning"
			}
			// The actual production constructor must reject before dereferencing its
			// pool/requester or opening trust material, including the old early branch.
			var composition *productionComposition
			loader, err := composition.prepareDeliveryCatalog(t.Context(), cfg)
			if err == nil || loader != nil || !strings.Contains(err.Error(), expected) {
				t.Fatal("explicit measured settings were ignored or reached an unrelated constructor")
			}
		})
	}
}
func TestDatabaseScanFeatureRelationshipsLeaveOrdinaryDefaultsAvailable(t *testing.T) {
	for _, cfg := range []*config.Config{{}, {DeliveryEnabled: true}, {DeliveryEnabled: true, DeliveryDatabaseUpdatesEnabled: true}, {DeliveryEnabled: true, DeliveryDatabaseUpdatesEnabled: true, DeliveryDatabaseScanEnabled: true}} {
		if err := validateOwnedDatabaseScanSettings(cfg); err != nil {
			t.Fatal("ordinary or coherent feature relationships rejected")
		}
	}
	if err := validateOwnedDatabaseScanSettings(nil); err == nil {
		t.Fatal("missing typed configuration accepted")
	}
}

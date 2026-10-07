package compatibility

import (
	"strings"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestNoncurrentObservationDoesNotInventOrHideIncompatibility(t *testing.T) {
	inventory := protocol.DeliveryControllerInventory{Observation: &protocol.DeliveryObservation{State: protocol.ObservationUnsynced}}
	if got := Evaluate(inventory); got.Status != Degraded || got.Code != "observation_unsynced" {
		t.Fatalf("never-observed identity: %#v", got)
	}
	inventory.FluxVersion = "v0.0.1"
	if got := Evaluate(inventory); got.Code != "flux_version_unsupported" {
		t.Fatalf("known mismatch hidden: %#v", got)
	}
	inventory.FluxVersion = ""
	inventory.DistributionDigest = "sha256:" + strings.Repeat("a", 64)
	if got := Evaluate(inventory); got.Code != "distribution_digest_unsupported" {
		t.Fatalf("known identity mismatch hidden: %#v", got)
	}
}

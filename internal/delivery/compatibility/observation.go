package compatibility

import (
	"strings"

	semver "github.com/Masterminds/semver/v3"
	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// Unknown identity is not incompatible identity. Preserve stronger, known
// release mismatches while reporting explicit freshness for never-observed or
// partially observed inventories. Missing fields cannot prove a mismatch.
func evaluateNoncurrentObservation(inventory protocol.DeliveryControllerInventory) Result {
	if inventory.FluxVersion != "" && inventory.FluxVersion != fluxdistribution.Version() {
		return Result{Status: UpgradeRequired, Code: "flux_version_unsupported"}
	}
	if inventory.KubernetesVersion != "" {
		kube, err := semver.NewVersion(strings.TrimPrefix(inventory.KubernetesVersion, "v"))
		if err != nil || kube.Major() != 1 || kube.Minor() < 33 || kube.Minor() > 35 {
			return Result{Status: Incompatible, Code: "kubernetes_version_unsupported"}
		}
	}
	components, err := requiredComponentVersions()
	if err != nil {
		return Result{Status: Incompatible, Code: "invalid_release_contract"}
	}
	for component, version := range components {
		if observed := inventory.Components[component]; observed != "" && observed != version {
			return Result{Status: UpgradeRequired, Code: "controller_version_unsupported"}
		}
	}
	if inventory.DistributionDigest != "" {
		expected, err := fluxdistribution.ControllerSetDigest()
		if err != nil {
			return Result{Status: Incompatible, Code: "invalid_release_contract"}
		}
		if inventory.DistributionDigest != expected {
			return Result{Status: UpgradeRequired, Code: "distribution_digest_unsupported"}
		}
	}
	return Result{Status: Degraded, Code: "observation_" + string(inventory.Observation.State)}
}

// Package compatibility evaluates downstream Flux inventory against the v1
// release contract. Keep these constants synchronized by the release contract
// test; runtime placement consumes only this normalized result.
package compatibility

import (
	"fmt"
	"strings"

	semver "github.com/Masterminds/semver/v3"

	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type Status string

const (
	Compatible      Status = "compatible"
	Incompatible    Status = "incompatible"
	UpgradeRequired Status = "upgrade_required"
	Degraded        Status = "degraded"
)

type Result struct {
	Status Status
	Code   string
}

var requiredAPIs = []string{
	"source.toolkit.fluxcd.io/v1",
	"kustomize.toolkit.fluxcd.io/v1",
	"helm.toolkit.fluxcd.io/v2",
}

func Evaluate(inventory protocol.DeliveryControllerInventory) Result {
	if err := inventory.Validate(); err != nil {
		return Result{Status: Incompatible, Code: "invalid_inventory"}
	}
	if !inventory.Ready {
		return Result{Status: Degraded, Code: "controllers_not_ready"}
	}
	if inventory.FluxVersion != fluxdistribution.Version() {
		return Result{Status: UpgradeRequired, Code: "flux_version_unsupported"}
	}
	kube, err := semver.NewVersion(strings.TrimPrefix(inventory.KubernetesVersion, "v"))
	if err != nil || kube.Major() != 1 || kube.Minor() < 33 || kube.Minor() > 35 {
		return Result{Status: Incompatible, Code: "kubernetes_version_unsupported"}
	}
	components, err := requiredComponentVersions()
	if err != nil {
		return Result{Status: Incompatible, Code: "invalid_release_contract"}
	}
	for component, version := range components {
		if inventory.Components[component] != version {
			return Result{Status: UpgradeRequired, Code: "controller_version_unsupported"}
		}
	}
	apiSet := make(map[string]struct{}, len(inventory.APIVersions))
	for _, api := range inventory.APIVersions {
		apiSet[api] = struct{}{}
	}
	for _, api := range requiredAPIs {
		if _, found := apiSet[api]; !found {
			return Result{Status: UpgradeRequired, Code: "flux_api_missing"}
		}
	}
	if inventory.DistributionDigest == "" {
		return Result{Status: Incompatible, Code: "distribution_identity_missing"}
	}
	expectedDigest, err := fluxdistribution.ControllerSetDigest()
	if err != nil {
		return Result{Status: Incompatible, Code: "invalid_release_contract"}
	}
	if inventory.DistributionDigest != expectedDigest {
		return Result{Status: UpgradeRequired, Code: "distribution_digest_unsupported"}
	}
	return Result{Status: Compatible}
}

func RequiredComponentVersions() map[string]string {
	result, _ := requiredComponentVersions()
	return result
}

func RequiredAPIs() []string {
	return append([]string(nil), requiredAPIs...)
}

func ContractSummary() string {
	return fmt.Sprintf("Flux %s, Kubernetes 1.33-1.35, protocol %s", fluxdistribution.Version(), protocol.DeliveryProtocolVersion)
}

func requiredComponentVersions() (map[string]string, error) {
	images, err := fluxdistribution.ControllerImages()
	if err != nil {
		return nil, err
	}
	result := make(map[string]string, len(images))
	for name, image := range images {
		result[name] = image.Version
	}
	return result, nil
}

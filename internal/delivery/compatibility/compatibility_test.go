package compatibility

import (
	"strings"
	"testing"

	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestEvaluate(t *testing.T) {
	expectedDigest, err := fluxdistribution.ControllerSetDigest()
	if err != nil {
		t.Fatal(err)
	}
	valid := protocol.DeliveryControllerInventory{
		FluxVersion: "v2.9.6", KubernetesVersion: "v1.34.2", Ready: true,
		DistributionDigest: expectedDigest,
		Components:         RequiredComponentVersions(),
		APIVersions: []string{
			"source.toolkit.fluxcd.io/v1", "kustomize.toolkit.fluxcd.io/v1", "helm.toolkit.fluxcd.io/v2",
		},
	}
	if result := Evaluate(valid); result.Status != Compatible || result.Code != "" {
		t.Fatalf("valid inventory: %#v", result)
	}
	tests := []struct {
		name string
		edit func(*protocol.DeliveryControllerInventory)
		want Status
		code string
	}{
		{"not ready", func(i *protocol.DeliveryControllerInventory) { i.Ready = false }, Degraded, "controllers_not_ready"},
		{"flux", func(i *protocol.DeliveryControllerInventory) { i.FluxVersion = "v2.8.0" }, UpgradeRequired, "flux_version_unsupported"},
		{"kubernetes low", func(i *protocol.DeliveryControllerInventory) { i.KubernetesVersion = "v1.32.9" }, Incompatible, "kubernetes_version_unsupported"},
		{"kubernetes high", func(i *protocol.DeliveryControllerInventory) { i.KubernetesVersion = "v1.36.0" }, Incompatible, "kubernetes_version_unsupported"},
		{"controller", func(i *protocol.DeliveryControllerInventory) { i.Components["helm-controller"] = "v1.5.0" }, UpgradeRequired, "controller_version_unsupported"},
		{"api", func(i *protocol.DeliveryControllerInventory) { i.APIVersions = i.APIVersions[:2] }, UpgradeRequired, "flux_api_missing"},
		{"identity", func(i *protocol.DeliveryControllerInventory) { i.DistributionDigest = "" }, Incompatible, "distribution_identity_missing"},
		{"wrong identity", func(i *protocol.DeliveryControllerInventory) {
			i.DistributionDigest = "sha256:" + strings.Repeat("a", 64)
		}, UpgradeRequired, "distribution_digest_unsupported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			candidate.Components = RequiredComponentVersions()
			candidate.APIVersions = append([]string(nil), valid.APIVersions...)
			test.edit(&candidate)
			if result := Evaluate(candidate); result.Status != test.want || result.Code != test.code {
				t.Fatalf("got %#v, want %s/%s", result, test.want, test.code)
			}
		})
	}
}

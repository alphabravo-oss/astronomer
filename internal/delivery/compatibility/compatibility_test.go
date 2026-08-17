package compatibility

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"gopkg.in/yaml.v3"
)

func TestEvaluate(t *testing.T) {
	expectedDigest, err := fluxdistribution.ControllerSetDigest()
	if err != nil {
		t.Fatal(err)
	}
	valid := protocol.DeliveryControllerInventory{
		FluxVersion: "v2.9.3", KubernetesVersion: "v1.34.2", Ready: true,
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

func TestRuntimeContractMatchesReleaseCompatibilityManifest(t *testing.T) {
	payload, err := os.ReadFile("../../../deploy/release/compatibility.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Flux struct {
			DistributionVersion string `yaml:"distribution_version"`
			Components          []struct {
				Name string `yaml:"name"`
				Tag  string `yaml:"tag"`
			} `yaml:"components"`
		} `yaml:"flux"`
		AgentProtocol struct {
			Ranges []struct {
				Minimum int `yaml:"minimum"`
				Maximum int `yaml:"maximum"`
			} `yaml:"supported_ranges"`
			Required []string `yaml:"required_capabilities"`
		} `yaml:"agent_protocol"`
	}
	if err := yaml.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Flux.DistributionVersion != fluxdistribution.Version() {
		t.Fatalf("release Flux version %q != embedded %q", manifest.Flux.DistributionVersion, fluxdistribution.Version())
	}
	components := make(map[string]string, len(manifest.Flux.Components))
	for _, component := range manifest.Flux.Components {
		components[component.Name] = component.Tag
	}
	if !reflect.DeepEqual(components, RequiredComponentVersions()) {
		t.Fatalf("release controller versions %#v != runtime %#v", components, RequiredComponentVersions())
	}
	if len(manifest.AgentProtocol.Ranges) != 1 || manifest.AgentProtocol.Ranges[0].Minimum != 2 || manifest.AgentProtocol.Ranges[0].Maximum != 2 {
		t.Fatalf("unexpected protocol range: %#v", manifest.AgentProtocol.Ranges)
	}
	wantCapabilities := []string{
		protocol.FeatureDeliveryAssignmentsV2,
		protocol.FeatureDeliveryStatusV2,
		protocol.FeatureDeliverySystemV2,
	}
	sort.Strings(wantCapabilities)
	sort.Strings(manifest.AgentProtocol.Required)
	if !reflect.DeepEqual(manifest.AgentProtocol.Required, wantCapabilities) {
		t.Fatalf("required capabilities %#v != runtime %#v", manifest.AgentProtocol.Required, wantCapabilities)
	}
}

package delivery

import (
	"context"
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"

	fluxdistribution "github.com/alphabravocompany/astronomer-go/deploy/flux"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

var expectedFluxAPIs = []string{
	"source.toolkit.fluxcd.io/v1",
	"kustomize.toolkit.fluxcd.io/v1",
	"helm.toolkit.fluxcd.io/v2",
}

// ClusterProbe derives advertised capabilities from APIs and the exact
// controller Deployments that are actually present. A missing controller or
// CRD is reported as degraded inventory, not papered over with compile-time
// capability claims.
type ClusterProbe struct {
	client        kubernetes.Interface
	discovery     discovery.DiscoveryInterface
	platformScope bool
}

func NewClusterProbe(client kubernetes.Interface, discoveryClient discovery.DiscoveryInterface, platformScope bool) (*ClusterProbe, error) {
	if client == nil || discoveryClient == nil {
		return nil, fmt.Errorf("Kubernetes and discovery clients are required for delivery capability probing")
	}
	return &ClusterProbe{client: client, discovery: discoveryClient, platformScope: platformScope}, nil
}

func (p *ClusterProbe) Inspect(ctx context.Context) (protocol.DeliveryControllerInventory, Capabilities, error) {
	expectedControllerImages, err := fluxdistribution.ControllerImages()
	if err != nil {
		return protocol.DeliveryControllerInventory{}, Capabilities{}, err
	}
	inventory := protocol.DeliveryControllerInventory{Components: make(map[string]string)}
	capabilities := Capabilities{PlatformScope: p.platformScope}
	if version, err := p.discovery.ServerVersion(); err == nil && version != nil {
		inventory.KubernetesVersion = version.GitVersion
	} else {
		inventory.CompatibilityMessage = "kubernetes_version_unavailable"
	}

	served := make(map[string]bool, len(expectedFluxAPIs))
	for _, apiVersion := range expectedFluxAPIs {
		if _, err := p.discovery.ServerResourcesForGroupVersion(apiVersion); err == nil {
			served[apiVersion] = true
			inventory.APIVersions = append(inventory.APIVersions, apiVersion)
		}
	}
	sort.Strings(inventory.APIVersions)
	capabilities.FluxAPIVersions = append([]string(nil), inventory.APIVersions...)
	if served["source.toolkit.fluxcd.io/v1"] {
		capabilities.SourceKinds = []protocol.DeliverySourceKind{
			protocol.DeliverySourceGit, protocol.DeliverySourceOCIArtifact,
			protocol.DeliverySourceHelmHTTP, protocol.DeliverySourceHelmOCI,
		}
	}
	if served["kustomize.toolkit.fluxcd.io/v1"] {
		capabilities.RendererKinds = append(capabilities.RendererKinds, protocol.DeliveryRendererKustomize)
	}
	if served["helm.toolkit.fluxcd.io/v2"] {
		capabilities.RendererKinds = append(capabilities.RendererKinds, protocol.DeliveryRendererHelm)
	}
	capabilities.NamespaceScope = len(capabilities.SourceKinds) != 0 && len(capabilities.RendererKinds) != 0

	controllerReady := true
	hardeningReady := true
	images := make([]string, 0, len(expectedControllerImages))
	fluxVersion := ""
	for _, name := range []string{"source-controller", "kustomize-controller", "helm-controller"} {
		deployment, err := p.client.AppsV1().Deployments(DeliverySystemNamespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			controllerReady = false
			hardeningReady = false
			if !apierrors.IsNotFound(err) && inventory.CompatibilityMessage == "" {
				inventory.CompatibilityMessage = "controller_inventory_unavailable"
			}
			continue
		}
		if !deploymentReady(deployment) {
			controllerReady = false
		}
		image, args := controllerImageAndArgs(deployment, name)
		if image == "" {
			controllerReady = false
			continue
		}
		images = append(images, name+"="+image)
		expected := expectedControllerImages[name]
		if !strings.HasSuffix(image, "@"+expected.Digest) {
			controllerReady = false
		} else {
			inventory.Components[name] = expected.Version
		}
		version := deployment.Labels["app.kubernetes.io/version"]
		if fluxVersion == "" {
			fluxVersion = version
		} else if version != fluxVersion {
			controllerReady = false
		}
		if !containsArgument(args, "--no-cross-namespace-refs=true") {
			hardeningReady = false
		}
		if name == "kustomize-controller" && !containsArgument(args, "--no-remote-bases=true") {
			hardeningReady = false
		}
	}
	inventory.FluxVersion = fluxVersion
	if len(images) == len(expectedControllerImages) {
		inventory.DistributionDigest, err = fluxdistribution.ControllerSetDigest()
		if err != nil {
			return protocol.DeliveryControllerInventory{}, Capabilities{}, err
		}
	}
	capabilities.NoCrossNamespaceRefs = hardeningReady
	capabilities.NoRemoteKustomizeBases = hardeningReady
	inventory.Ready = controllerReady && hardeningReady && len(inventory.APIVersions) == len(expectedFluxAPIs)
	if !inventory.Ready && inventory.CompatibilityMessage == "" {
		inventory.CompatibilityMessage = "flux_distribution_not_ready"
	}
	return inventory, capabilities, nil
}

func deploymentReady(deployment *appsv1.Deployment) bool {
	if deployment == nil || deployment.Generation < 1 || deployment.Status.ObservedGeneration < deployment.Generation || deployment.Status.AvailableReplicas < 1 {
		return false
	}
	for _, condition := range deployment.Status.Conditions {
		if condition.Type == appsv1.DeploymentAvailable && condition.Status == "True" {
			return true
		}
	}
	return false
}

func controllerImageAndArgs(deployment *appsv1.Deployment, name string) (string, []string) {
	for _, container := range deployment.Spec.Template.Spec.Containers {
		if container.Name == "manager" || container.Name == name {
			return container.Image, container.Args
		}
	}
	return "", nil
}

func containsArgument(arguments []string, wanted string) bool {
	for _, argument := range arguments {
		if argument == wanted {
			return true
		}
	}
	return false
}

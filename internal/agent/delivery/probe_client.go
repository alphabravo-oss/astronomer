package delivery

import (
	"fmt"
	"github.com/alphabravocompany/astronomer-go/internal/agent/kuberequests"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// NewClusterProbeForConfig shares the existing typed client and creates only a
// dedicated discovery client. Legacy discovery methods discard caller context;
// its default consumer therefore must be installed at the transport boundary.
func NewClusterProbeForConfig(client kubernetes.Interface, cfg *rest.Config, platformScope bool) (*ClusterProbe, error) {
	if client == nil || cfg == nil {
		return nil, fmt.Errorf("Kubernetes client and config are required for delivery probing")
	}
	discoveryConfig := kuberequests.DefaultConsumerConfig(kuberequests.Config(cfg), kuberequests.DeliveryInventory)
	// Clientsets synthesize a shared limiter in a copied config. Reuse the
	// existing discovery limiter rather than granting another burst budget.
	if existing := client.Discovery(); existing != nil {
		if restClient := existing.RESTClient(); restClient != nil && restClient.GetRateLimiter() != nil {
			discoveryConfig.RateLimiter = restClient.GetRateLimiter()
		}
	}
	// Match the clientset construction order: create HTTP client before
	// discovery applies its defaults, preserving the original HTTP timeout.
	httpClient, err := rest.HTTPClientFor(discoveryConfig)
	if err != nil {
		return nil, err
	}
	discoveryClient, err := discovery.NewDiscoveryClientForConfigAndClient(discoveryConfig, httpClient)
	if err != nil {
		return nil, err
	}
	return NewClusterProbe(client, discoveryClient, platformScope)
}

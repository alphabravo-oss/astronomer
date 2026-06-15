package kubeutil

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

func DiscoveryClientForConfig(config *rest.Config) (discovery.DiscoveryInterface, error) {
	if config == nil {
		return nil, fmt.Errorf("create Kubernetes discovery client: config is nil")
	}
	client, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes discovery client: %w", err)
	}
	return client, nil
}

func RESTMapperForConfig(config *rest.Config) (meta.RESTMapper, error) {
	client, err := DiscoveryClientForConfig(config)
	if err != nil {
		return nil, err
	}
	return RESTMapperForDiscovery(client)
}

func RESTMapperForDiscovery(client discovery.DiscoveryInterface) (meta.RESTMapper, error) {
	if client == nil {
		return nil, fmt.Errorf("create Kubernetes RESTMapper: discovery client is nil")
	}
	return restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(client)), nil
}

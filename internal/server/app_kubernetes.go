package server

import (
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// inClusterClients builds the optional management-cluster clients together so
// callers cannot accidentally mix clients created from different REST config.
func inClusterClients() (*kubernetes.Clientset, metricsv.Interface, dynamic.Interface) {
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, nil, nil
	}
	localDynamic, _ := dynamic.NewForConfig(restCfg)
	localK8s, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, nil, localDynamic
	}
	localMetrics, _ := metricsv.NewForConfig(restCfg)
	return localK8s, localMetrics, localDynamic
}

package server

import (
	"github.com/alphabravocompany/astronomer-go/internal/agent/kuberequests"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Return the same copied configuration used for the initial typed client so
// subsequent embedded-agent dynamic/metadata clients inherit instrumentation.
func newLocalAgentKubernetesClient(original *rest.Config) (*rest.Config, *kubernetes.Clientset, error) {
	cfg := kuberequests.Config(original)
	client, err := kubernetes.NewForConfig(cfg)
	return cfg, client, err
}

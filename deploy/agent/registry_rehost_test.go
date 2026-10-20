package agenttemplate

import (
	"strings"
	"testing"
)

func TestRenderInstallYAMLRehostsFluxControllersOntoLocalRegistry(t *testing.T) {
	t.Parallel()
	const local = "astronomer-zot.astronomer.svc.cluster.local:5000"
	online := RenderInstallYAML(InstallTemplateData{AgentImage: "a/b:1"})
	if !strings.Contains(online, "image: ghcr.io/fluxcd/helm-controller@sha256:") {
		t.Fatal("online profile must keep the signed Flux references")
	}
	rendered := RenderInstallYAML(InstallTemplateData{AgentImage: local + "/alphabravo-oss/astronomer-go-agent:1", ImageRegistry: local})
	for _, controller := range []string{"helm-controller", "kustomize-controller", "source-controller"} {
		if !strings.Contains(rendered, "image: "+local+"/fluxcd/"+controller+"@sha256:") {
			t.Errorf("%s not re-hosted onto %s", controller, local)
		}
	}
	if strings.Contains(rendered, "ghcr.io/fluxcd/") {
		t.Error("public Flux reference left in a local-registry install manifest")
	}
}

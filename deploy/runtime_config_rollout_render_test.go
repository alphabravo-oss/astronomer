package deploy

import "testing"

func TestRuntimeConfigChangesRollServerAndWorker(t *testing.T) {
	baseline := parseRenderedDocs(t, helmTemplate(t,
		"config.apiK8sProxyRateLimitRPS=1",
		"config.apiK8sProxyRateLimitBurst=20",
	))
	changed := parseRenderedDocs(t, helmTemplate(t,
		"config.apiK8sProxyRateLimitRPS=500",
		"config.apiK8sProxyRateLimitBurst=1000",
	))

	for _, component := range []string{"server", "worker"} {
		name := "astronomer-" + component
		before := runtimeConfigChecksum(t, findRenderedDoc(t, baseline, "Deployment", name))
		after := runtimeConfigChecksum(t, findRenderedDoc(t, changed, "Deployment", name))
		if before == after {
			t.Fatalf("%s pod template checksum did not change with runtime configuration", name)
		}
	}
}

func TestRuntimeConfigChecksumPreservesPodAnnotations(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t,
		"server.podAnnotations.example\\.com/server=value",
		"worker.podAnnotations.example\\.com/worker=value",
	))
	for _, test := range []struct {
		component string
		key       string
	}{
		{component: "server", key: "example.com/server"},
		{component: "worker", key: "example.com/worker"},
	} {
		deployment := findRenderedDoc(t, docs, "Deployment", "astronomer-"+test.component)
		annotations := nestedMap(deployment, "spec", "template", "metadata", "annotations")
		if stringValue(annotations["checksum/config"]) == "" {
			t.Fatalf("astronomer-%s has no runtime configuration checksum", test.component)
		}
		if got := stringValue(annotations[test.key]); got != "value" {
			t.Fatalf("astronomer-%s annotation %q = %q, want value", test.component, test.key, got)
		}
	}
}

func runtimeConfigChecksum(t *testing.T, deployment renderedDoc) string {
	t.Helper()
	annotations := nestedMap(deployment, "spec", "template", "metadata", "annotations")
	checksum := stringValue(annotations["checksum/config"])
	if checksum == "" {
		t.Fatalf("%s has no runtime configuration checksum", stringAt(deployment, "metadata", "name"))
	}
	return checksum
}

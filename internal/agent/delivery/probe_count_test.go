package delivery

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	discoveryfake "k8s.io/client-go/discovery/fake"
	clientfake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"testing"
)

// E04 local baseline only: production/live qualification remains separate.
// Keep direct legacy mode measurable while E05 adds negotiated shared sources.
func TestDirectProbeReadBaseline(t *testing.T) {
	client := clientfake.NewClientset(readyController(t, "source-controller", false), readyController(t, "kustomize-controller", true), readyController(t, "helm-controller", false))
	discovery := &discoveryfake.FakeDiscovery{Fake: &ktesting.Fake{}, FakedServerVersion: &version.Info{GitVersion: "v1.35.2"}}
	for _, api := range expectedFluxAPIs {
		discovery.Resources = append(discovery.Resources, &metav1.APIResourceList{GroupVersion: api})
	}
	probe, err := NewClusterProbe(client, discovery, true)
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if _, _, err := probe.Inspect(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	counts := map[string]int{}
	for _, action := range client.Actions() {
		counts[action.GetVerb()]++
	}
	if counts["list"] != 60 || counts["get"] != 30 {
		t.Fatalf("direct probe baseline changed: %v", counts)
	}
	t.Log("10 direct platform probes: 60 typed LISTs, 30 controller GETs (discovery tracked separately; no dynamic client)")
}

package deploy

import (
	"fmt"
	"reflect"
	"testing"
)

func TestInternalDenyBackendIsResolvedAndEndpointlessByConstruction(t *testing.T) {
	docs := parseRenderedDocs(t, helmTemplate(t))
	service := findRenderedDoc(t, docs, "Service", "astronomer-internal-deny")

	wantSelector := map[string]any{
		"app.kubernetes.io/name":      "astronomer",
		"app.kubernetes.io/instance":  "astronomer",
		"app.kubernetes.io/component": "internal-deny",
	}
	if got := nestedMap(service, "spec", "selector"); !reflect.DeepEqual(got, wantSelector) {
		t.Fatalf("internal-deny selector = %#v, want exactly %#v", got, wantSelector)
	}
	if got := stringAt(service, "spec", "type"); got != "ClusterIP" {
		t.Fatalf("internal-deny Service type = %q, want ClusterIP", got)
	}
	ports, _ := nestedMap(service, "spec")["ports"].([]any)
	if len(ports) != 1 {
		t.Fatalf("internal-deny Service ports = %#v, want exactly one", ports)
	}
	port, _ := ports[0].(map[string]any)
	if stringValue(port["protocol"]) != "TCP" || fmt.Sprint(port["port"]) != "8000" || fmt.Sprint(port["targetPort"]) != "8000" {
		t.Fatalf("internal-deny Service port contract = %#v, want TCP 8000 -> 8000", port)
	}

	route := findRenderedDoc(t, docs, "HTTPRoute", "astronomer-ui")
	rules, _ := nestedMap(route, "spec")["rules"].([]any)
	if len(rules) < 1 {
		t.Fatal("UI HTTPRoute has no rules")
	}
	denyRule, _ := rules[0].(map[string]any)
	backends, _ := denyRule["backendRefs"].([]any)
	if len(backends) != 1 {
		t.Fatalf("internal deny rule backends = %#v, want exactly one", backends)
	}
	backend, _ := backends[0].(map[string]any)
	if stringValue(backend["name"]) != "astronomer-internal-deny" || fmt.Sprint(backend["port"]) != "8000" {
		t.Fatalf("internal deny backend = %#v, want astronomer-internal-deny:8000", backend)
	}

	for _, doc := range docs {
		if labels, ok := renderedWorkloadPodLabels(doc); ok && labels["app.kubernetes.io/component"] == "internal-deny" {
			t.Fatalf("internal-deny must remain endpointless; rendered %s/%s with matching pod labels", stringValue(doc["kind"]), stringAt(doc, "metadata", "name"))
		}
		switch stringValue(doc["kind"]) {
		case "Pod", "Endpoints", "EndpointSlice":
			if stringAt(doc, "metadata", "name") == "astronomer-internal-deny" {
				t.Fatalf("internal-deny must remain endpointless; rendered %s/%s", stringValue(doc["kind"]), stringAt(doc, "metadata", "name"))
			}
		}
	}
}

func TestInternalDenyServiceFollowsPublicRoutingTopology(t *testing.T) {
	for _, tt := range []struct {
		name string
		sets []string
		want bool
	}{
		{name: "gateway", sets: []string{"gateway.enabled=true", "ingress.enabled=false"}, want: true},
		{name: "ingress", sets: []string{"gateway.enabled=false", "ingress.enabled=true"}, want: true},
		{name: "no public routing", sets: []string{"gateway.enabled=false", "ingress.enabled=false"}, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			docs := parseRenderedDocs(t, helmTemplate(t, tt.sets...))
			if got := renderedDocExists(docs, "Service", "astronomer-internal-deny"); got != tt.want {
				t.Fatalf("internal-deny Service rendered = %v, want %v", got, tt.want)
			}
		})
	}
}

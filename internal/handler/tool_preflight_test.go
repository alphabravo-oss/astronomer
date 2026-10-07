package handler

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

type toolPreflightRequester struct{ body string }

func (r toolPreflightRequester) Do(context.Context, string, string, string, []byte, map[string]string) (*protocol.K8sResponsePayload, error) {
	return &protocol.K8sResponsePayload{StatusCode: http.StatusOK, Body: base64.StdEncoding.EncodeToString([]byte(r.body))}, nil
}

func TestNodeExporterPreflightBlocksOccupiedHostPort(t *testing.T) {
	h := &ToolHandler{k8s: toolPreflightRequester{body: `{"items":[{"metadata":{"namespace":"monitoring","name":"existing-exporter"},"spec":{"containers":[{"ports":[{"hostPort":9100}]}]}}]}`}}
	checks := h.toolPreflightChecks(context.Background(), "prometheus-node-exporter", "cluster", nil, "")
	if len(checks) != 1 || checks[0].Status != "block" {
		t.Fatalf("checks=%#v", checks)
	}
	if blockingToolPreflight(checks) == "" {
		t.Fatal("expected blocking explanation")
	}
}

func TestUnrelatedToolHasNoHostPortPreflight(t *testing.T) {
	h := &ToolHandler{}
	if checks := h.toolPreflightChecks(context.Background(), "gatekeeper", "cluster", nil, ""); len(checks) != 0 {
		t.Fatalf("checks=%#v", checks)
	}
}

func TestFluentBitPreflightRequiresAnExplicitOutput(t *testing.T) {
	h := &ToolHandler{}
	checks := h.toolPreflightChecks(context.Background(), "fluent-bit", "cluster", []toolRelease{{ValuesYAML: "hotReload:\n  enabled: true\n"}}, "")
	if len(checks) != 1 || checks[0].Status != "block" {
		t.Fatalf("checks=%#v", checks)
	}
	checks = h.toolPreflightChecks(context.Background(), "fluent-bit", "cluster", []toolRelease{{ValuesYAML: "config:\n  outputs: |\n    [OUTPUT]\n        Name http\n        Match '*'\n        Host logs.example.test\n"}}, "")
	if len(checks) != 1 || checks[0].Status != "pass" {
		t.Fatalf("checks=%#v", checks)
	}
}

func TestNodeExporterPreflightUsesEffectiveValues(t *testing.T) {
	h := &ToolHandler{k8s: toolPreflightRequester{body: `{"items":[{"metadata":{"namespace":"system","name":"other"},"spec":{"containers":[{"ports":[{"hostPort":9200}]}]}}]}`}}
	checks := h.toolPreflightChecks(context.Background(), "prometheus-node-exporter", "cluster", []toolRelease{{ValuesYAML: "hostNetwork: true\nservice:\n  port: 9200\n"}}, "")
	if len(checks) != 1 || checks[0].Code != "host-port-9200" || checks[0].Status != "block" {
		t.Fatalf("checks=%#v", checks)
	}
}

func TestNodeExporterPreflightDoesNotReservePortWithoutHostNetwork(t *testing.T) {
	h := &ToolHandler{}
	checks := h.toolPreflightChecks(context.Background(), "prometheus-node-exporter", "cluster", []toolRelease{{ValuesYAML: "hostNetwork: false\nservice:\n  port: 9200\n"}}, "")
	if len(checks) != 1 || checks[0].Status != "pass" {
		t.Fatalf("checks=%#v", checks)
	}
}

func TestNodeExporterUpgradeIgnoresCurrentReleaseOwner(t *testing.T) {
	h := &ToolHandler{k8s: toolPreflightRequester{body: `{"items":[{"metadata":{"namespace":"monitoring","name":"managed-exporter","labels":{"app.kubernetes.io/instance":"prometheus-node-exporter"}},"spec":{"containers":[{"ports":[{"hostPort":9100}]}]}}]}`}}
	checks := h.toolPreflightChecks(context.Background(), "prometheus-node-exporter", "cluster", nil, "prometheus-node-exporter")
	if len(checks) != 1 || checks[0].Status != "pass" {
		t.Fatalf("checks=%#v", checks)
	}
}

package main

import (
	"reflect"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

func TestWorkerTelemetryConfigPreservesOperatorTransportAndWorkerIdentity(t *testing.T) {
	cfg := &config.Config{
		OTELExporterEndpoint: "https://collector.invalid/custom/traces", OTELExporterInsecure: true,
		OTELExporterHeaders: "Authorization=Bearer synthetic-header,X-Qualification=owned",
		OTELServiceName:     "operator-api-name", OTELServiceVersion: "operator-api-version",
		OTELSamplerRatio: 0.25, Env: "qualification", ProcessHostname: "owned-worker",
	}
	got := workerTelemetryConfig(cfg)
	if got.Endpoint != cfg.OTELExporterEndpoint || got.Insecure != cfg.OTELExporterInsecure || got.SampleRate != cfg.OTELSamplerRatio {
		t.Fatal("operator transport/sampling settings changed")
	}
	if !reflect.DeepEqual(got.Headers, map[string]string{"Authorization": "Bearer synthetic-header", "X-Qualification": "owned"}) {
		t.Fatal("operator exporter headers changed")
	}
	if got.ServiceName != "astronomer-worker" || got.ServiceVersion != version.Version || got.ServiceNamespace != "astronomer" || got.Environment != cfg.Env || got.ServiceInstanceID != cfg.ProcessHostname {
		t.Fatal("worker resource identity changed")
	}
	cfg.OTELExporterEndpoint = ""
	cfg.OTELSamplerRatio = 0
	if disabled := workerTelemetryConfig(cfg); disabled.Endpoint != "" || disabled.SampleRate != 0 {
		t.Fatal("disabled tracing/sampling values changed")
	}
}

package main

import (
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/pkg/telemetry"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

// workerTelemetryConfig retains operator transport settings while binding the
// resource identity to this worker binary rather than the API service settings.
func workerTelemetryConfig(cfg *config.Config) telemetry.Config {
	return telemetry.Config{
		Endpoint: cfg.OTELExporterEndpoint, Protocol: cfg.OTELExporterProtocol, Insecure: cfg.OTELExporterInsecure,
		Headers:     telemetry.ParseOTLPHeaders(cfg.OTELExporterHeaders),
		ServiceName: "astronomer-worker", ServiceVersion: version.Version,
		Environment: cfg.Env, ServiceNamespace: "astronomer", ServiceInstanceID: cfg.ProcessHostname,
		SampleRate: cfg.OTELSamplerRatio,
	}
}

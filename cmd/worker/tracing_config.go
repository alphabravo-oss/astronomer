package main

import (
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/version"
)

// workerTracingConfig retains operator transport settings while binding the
// resource identity to this worker binary rather than the API service settings.
func workerTracingConfig(cfg *config.Config) observability.TracingConfig {
	return observability.TracingConfig{
		Endpoint: cfg.OTELExporterEndpoint, Insecure: cfg.OTELExporterInsecure,
		Headers:     observability.ParseOTLPHeaders(cfg.OTELExporterHeaders),
		ServiceName: "astronomer-worker", ServiceVersion: version.Version,
		Environment: cfg.Env, ServiceNamespace: "astronomer", ServiceInstanceID: cfg.ProcessHostname,
		SamplerRatio: cfg.OTELSamplerRatio,
	}
}

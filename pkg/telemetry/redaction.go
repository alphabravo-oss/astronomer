package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/alphabravocompany/astronomer-go/pkg/logging"
)

// diagnosticExporter applies the same credential field policy before OTLP
// serialization, including event/link/resource attributes and error status.
// Trace IDs remain available for correlation; they are not metric labels.
type diagnosticExporter struct{ sdktrace.SpanExporter }

func (exporter diagnosticExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	clean := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, span := range spans {
		clean[i] = diagnosticSpan{span}
	}
	return exporter.SpanExporter.ExportSpans(ctx, clean)
}

type diagnosticSpan struct{ sdktrace.ReadOnlySpan }

func (span diagnosticSpan) Name() string { return logging.Scrub(span.ReadOnlySpan.Name()) }
func (span diagnosticSpan) Attributes() []attribute.KeyValue {
	return diagnosticAttributes(span.ReadOnlySpan.Attributes())
}
func (span diagnosticSpan) Status() sdktrace.Status {
	status := span.ReadOnlySpan.Status()
	// Error descriptions can embed arbitrary server-supplied SQL literals or
	// response bodies. Preserve the status code without exporting that text.
	if status.Description != "" {
		status.Description = logging.Redacted
	}
	return status
}
func (span diagnosticSpan) Resource() *resource.Resource {
	return resource.NewWithAttributes(span.ReadOnlySpan.Resource().SchemaURL(), diagnosticAttributes(span.ReadOnlySpan.Resource().Attributes())...)
}
func (span diagnosticSpan) Events() []sdktrace.Event {
	events := append([]sdktrace.Event(nil), span.ReadOnlySpan.Events()...)
	for i := range events {
		events[i].Name = logging.Scrub(events[i].Name)
		events[i].Attributes = diagnosticAttributes(events[i].Attributes)
	}
	return events
}
func (span diagnosticSpan) Links() []sdktrace.Link {
	links := append([]sdktrace.Link(nil), span.ReadOnlySpan.Links()...)
	for i := range links {
		links[i].Attributes = diagnosticAttributes(links[i].Attributes)
	}
	return links
}

func diagnosticAttributes(attributes []attribute.KeyValue) []attribute.KeyValue {
	clean := make([]attribute.KeyValue, 0, len(attributes))
	for _, attr := range attributes {
		if logging.SensitiveKey(string(attr.Key)) {
			continue
		}
		switch attr.Value.Type() {
		case attribute.STRING:
			attr.Value = attribute.StringValue(logging.Scrub(attr.Value.AsString()))
		case attribute.STRINGSLICE:
			values := append([]string(nil), attr.Value.AsStringSlice()...)
			for i := range values {
				values[i] = logging.Scrub(values[i])
			}
			attr.Value = attribute.StringSliceValue(values)
		}
		clean = append(clean, attr)
	}
	return clean
}

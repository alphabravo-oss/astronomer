package observability

import (
	"context"
	"testing"
)

func TestNormalizedProtocolDefaultsToGRPC(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{"": ProtocolGRPC, "grpc": ProtocolGRPC, "GRPC": ProtocolGRPC, "http/protobuf": ProtocolHTTPProtobuf, "http": ProtocolHTTPProtobuf, "bogus": ProtocolGRPC} {
		if got := normalizedProtocol(input); got != want {
			t.Fatalf("normalizedProtocol(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTraceExporterConstructsForBothTransports(t *testing.T) {
	t.Parallel()
	for name, cfg := range map[string]TracingConfig{"grpc-default": {Insecure: true}, "http-protobuf": {Protocol: ProtocolHTTPProtobuf}} {
		exporter, err := newTraceExporter(context.Background(), cfg, "http://collector.invalid:4317/v1/traces")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Cleanup(func() { _ = exporter.Shutdown(context.Background()) })
	}
}

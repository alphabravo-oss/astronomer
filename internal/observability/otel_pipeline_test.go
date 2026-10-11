package observability

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func preserveTracingGlobals(t *testing.T) {
	t.Helper()
	provider, propagator, errorHandler := otel.GetTracerProvider(), otel.GetTextMapPropagator(), otel.GetErrorHandler()
	t.Cleanup(func() {
		otel.SetTracerProvider(provider)
		otel.SetTextMapPropagator(propagator)
		otel.SetErrorHandler(errorHandler)
	})
}

func TestTracingRejectsMalformedEndpointWithoutCredentialDisclosure(t *testing.T) {
	preserveTracingGlobals(t)
	for _, endpoint := range []string{"not-a-url", "ftp://collector.invalid", "https://synthetic-user:synthetic-secret@collector.invalid", "https://collector.invalid?token=synthetic-secret", "https://collector.invalid#synthetic-secret"} {
		shutdown, err := InitTracing(context.Background(), nil, TracingConfig{Endpoint: endpoint})
		if shutdown != nil {
			_ = shutdown(context.Background())
		}
		if err == nil {
			t.Errorf("invalid endpoint accepted")
		} else if strings.Contains(err.Error(), "synthetic-secret") {
			t.Error("invalid endpoint error leaked credentials")
		}
	}
}

func TestTracingDisabledInstallsNoopProvider(t *testing.T) {
	preserveTracingGlobals(t)
	prior := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	defer func() { _ = prior.Shutdown(context.Background()) }()
	otel.SetTracerProvider(prior)
	shutdown, err := InitTracing(context.Background(), nil, TracingConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shutdown(context.Background()) }()
	_, span := otel.Tracer("disabled-probe").Start(context.Background(), "must-not-record")
	defer span.End()
	if span.IsRecording() {
		t.Fatal("explicit disabled configuration retained active global provider")
	}
}

func TestTracingFlushesRealOTLPResourceAndSpan(t *testing.T) {
	preserveTracingGlobals(t)
	received := make(chan *collector.ExportTraceServiceRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" || r.Header.Get("X-Fixture") != "synthetic-collector" {
			t.Error("OTLP endpoint or typed header not preserved")
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Error(err)
			return
		}
		request := new(collector.ExportTraceServiceRequest)
		if err := proto.Unmarshal(body, request); err != nil {
			t.Error(err)
			return
		}
		received <- request
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	shutdown, err := InitTracing(context.Background(), nil, TracingConfig{Endpoint: server.URL, SamplerRatio: 1, ServiceName: "astronomer-fixture", ServiceNamespace: "qualification", ServiceInstanceID: "owned-instance", ServiceVersion: "fixture", Environment: "owned", Headers: map[string]string{"X-Fixture": "synthetic-collector"}})
	if err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("pipeline-fixture").Start(context.Background(), "operation.commit")
	span.SetAttributes(attribute.String("operation.kind", "fixture"))
	span.End()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-received:
		if len(request.ResourceSpans) != 1 {
			t.Fatal("resource batches missing")
		}
		resource := request.ResourceSpans[0]
		attributes := map[string]string{}
		for _, attr := range resource.Resource.Attributes {
			attributes[attr.Key] = attr.Value.GetStringValue()
		}
		for key, want := range map[string]string{"service.name": "astronomer-fixture", "service.namespace": "qualification", "service.instance.id": "owned-instance", "service.version": "fixture", "deployment.environment": "owned"} {
			if attributes[key] != want {
				t.Errorf("resource %s missing", key)
			}
		}
		if len(resource.ScopeSpans) != 1 || len(resource.ScopeSpans[0].Spans) != 1 || resource.ScopeSpans[0].Spans[0].Name != "operation.commit" {
			t.Fatal("shutdown lost completed span")
		}
	case <-time.After(time.Second):
		t.Fatal("no actual OTLP protobuf export")
	}
}

func TestTracingShutdownBoundsUnresponsiveCollector(t *testing.T) {
	preserveTracingGlobals(t)
	entered, release := make(chan struct{}, 1), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-release }))
	defer server.Close()
	shutdown, err := InitTracing(context.Background(), nil, TracingConfig{Endpoint: server.URL, SamplerRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("pipeline-fixture").Start(context.Background(), "bounded-export")
	span.End()
	flushing := make(chan error, 1)
	go func() {
		flushing <- otel.GetTracerProvider().(*sdktrace.TracerProvider).ForceFlush(context.Background())
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("deadline test never exercised HTTP export")
	}
	defer func() {
		close(release)
		select {
		case <-flushing:
		case <-time.After(time.Second):
			t.Error("export did not join after fixture released response")
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = shutdown(ctx)
	if err == nil {
		t.Fatal("unresponsive collector shutdown falsely succeeded")
	}
	if time.Since(started) > time.Second {
		t.Fatal("collector retained shutdown beyond caller budget")
	}
}

func TestTracingEndpointPreservesExplicitPathAndLegacyHostPort(t *testing.T) {
	for _, test := range []struct {
		cfg  TracingConfig
		want string
	}{
		{TracingConfig{Endpoint: "http://collector.invalid:4318"}, "http://collector.invalid:4318/v1/traces"},
		{TracingConfig{Endpoint: "http://collector.invalid:4318/"}, "http://collector.invalid:4318/v1/traces"},
		{TracingConfig{Endpoint: "https://collector.invalid/custom/traces"}, "https://collector.invalid/custom/traces"},
		{TracingConfig{Endpoint: "collector.invalid:4318", Insecure: true}, "http://collector.invalid:4318/v1/traces"},
		{TracingConfig{Endpoint: "collector.invalid:4318"}, "https://collector.invalid:4318/v1/traces"},
	} {
		got, err := tracingEndpoint(test.cfg)
		if err != nil || got != test.want {
			t.Errorf("endpoint normalization: got %q, error%v", got, err)
		}
	}
}

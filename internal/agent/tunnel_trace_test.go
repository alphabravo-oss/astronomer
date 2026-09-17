package agent

import (
	"context"
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestAgentTunnelMessageRejoinsRemoteTrace(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	previousTracer := agentTunnelTracer
	agentTunnelTracer = provider.Tracer("astronomer/agent-tunnel")
	t.Cleanup(func() {
		agentTunnelTracer = previousTracer
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
		_ = provider.Shutdown(context.Background())
	})

	parentCtx, parent := provider.Tracer("test/server").Start(context.Background(), "server.tunnel request")
	traceparent, tracestate := observability.TraceContextFromContext(parentCtx)
	parent.End()

	client := &TunnelClient{config: &AgentConfig{ClusterID: "cluster-1"}}
	ctx, span := client.startMessageSpan(context.Background(), &protocol.Message{
		Type: protocol.MsgK8sRequest, Traceparent: traceparent, Tracestate: tracestate,
	})
	childContext := trace.SpanContextFromContext(ctx)
	span.End()

	if childContext.TraceID() != parent.SpanContext().TraceID() {
		t.Fatalf("agent trace ID = %s, want server trace ID %s", childContext.TraceID(), parent.SpanContext().TraceID())
	}
	ended := recorder.Ended()
	if len(ended) != 2 {
		t.Fatalf("ended spans = %d, want parent and agent child", len(ended))
	}
	child := ended[1]
	if child.Parent().SpanID() != parent.SpanContext().SpanID() || !child.Parent().IsRemote() {
		t.Fatalf("agent parent = %#v, want remote %s", child.Parent(), parent.SpanContext().SpanID())
	}
	if child.SpanKind() != trace.SpanKindConsumer {
		t.Fatalf("agent span kind = %v, want consumer", child.SpanKind())
	}
}

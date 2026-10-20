package observability

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

type sdkLogCapture struct {
	mu   sync.Mutex
	text strings.Builder
}

func (capture *sdkLogCapture) Write(value []byte) (int, error) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.text.Write(value)
}

func (capture *sdkLogCapture) String() string {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.text.String()
}

func TestBackgroundOTLPErrorBodyDoesNotBypassDiagnosticPolicy(t *testing.T) {
	preserveTracingGlobals(t)
	const secret = "synthetic-unlabelled-collector-response-body"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusBadRequest) // terminal, no SDK transport retry
		_, _ = io.WriteString(w, secret)
	}))
	defer server.Close()
	var capture sdkLogCapture
	shutdown, err := InitTracing(context.Background(), testSlog(&capture), TracingConfig{Endpoint: server.URL, Protocol: ProtocolHTTPProtobuf, SamplerRatio: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = shutdown(ctx)
	}()
	// A full batch exercises the real background processor and global SDK
	// diagnostic path, rather than manually calling an error-handler fixture.
	for i := 0; i < 512; i++ {
		_, span := otel.Tracer("sdk-diagnostic-fixture").Start(context.Background(), "safe-operation")
		span.End()
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(capture.String(), "otel.sdk.error") && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	raw := capture.String()
	if !strings.Contains(raw, "otel.sdk.error") {
		t.Fatal("actual background SDK error was not observed through safe handler")
	}
	if strings.Contains(raw, secret) {
		t.Fatal("collector-controlled response escaped to final diagnostic output")
	}
}

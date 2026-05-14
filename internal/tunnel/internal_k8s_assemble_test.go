package tunnel

// Regression tests for reassembleK8sResponse.
//
// The original InternalK8sHandler ate the chunked path entirely —
// reading a single frame off the stream and unmarshaling it as a
// K8sResponsePayload, which produced HTTP 200 with status_code +
// headers but a zero-length body. Live symptom: cluster pages on
// .247 showed "0 pods" half the time (whenever the request crossed
// a pod boundary). These tests pin both shapes so a future edit
// that drops back to the one-frame read fails loudly.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestReassembleK8sResponse_OneShot(t *testing.T) {
	want := protocol.K8sResponsePayload{
		StatusCode: 200,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       base64.StdEncoding.EncodeToString([]byte(`{"items":[{"name":"alpha"}]}`)),
	}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	dataCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	dataCh <- raw
	got, err := reassembleK8sResponse(context.Background(), dataCh, doneCh)
	if err != nil {
		t.Fatalf("reassemble: %v", err)
	}
	if got.StatusCode != 200 || got.Body != want.Body {
		t.Errorf("one-shot mismatch: %+v", got)
	}
}

func TestReassembleK8sResponse_Chunked(t *testing.T) {
	// The agent ships three frames for a chunked response:
	//   header → data → end
	// reassembleK8sResponse must concatenate the data frames into
	// out.Body. The original code returned the header frame as-is,
	// dropping all the data.
	header := mustMarshal(protocol.K8sStreamFrame{
		Kind:       protocol.K8sStreamFrameHeader,
		StatusCode: 200,
		Headers:    map[string]string{"Content-Type": "application/json"},
	})
	chunk1 := mustMarshal(protocol.K8sStreamFrame{
		Kind: protocol.K8sStreamFrameData,
		Body: base64.StdEncoding.EncodeToString([]byte(`{"items":[`)),
	})
	chunk2 := mustMarshal(protocol.K8sStreamFrame{
		Kind: protocol.K8sStreamFrameData,
		Body: base64.StdEncoding.EncodeToString([]byte(`{"name":"alpha"},{"name":"bravo"}]}`)),
	})
	end := mustMarshal(protocol.K8sStreamFrame{Kind: protocol.K8sStreamFrameEnd})

	dataCh := make(chan []byte, 4)
	doneCh := make(chan struct{})
	dataCh <- header
	dataCh <- chunk1
	dataCh <- chunk2
	dataCh <- end

	got, err := reassembleK8sResponse(context.Background(), dataCh, doneCh)
	if err != nil {
		t.Fatalf("reassemble: %v", err)
	}
	if got.StatusCode != 200 {
		t.Errorf("status_code = %d, want 200", got.StatusCode)
	}
	decoded, err := base64.StdEncoding.DecodeString(got.Body)
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	want := `{"items":[{"name":"alpha"},{"name":"bravo"}]}`
	if string(decoded) != want {
		t.Errorf("body = %q, want %q", string(decoded), want)
	}
}

func TestReassembleK8sResponse_StreamClosed(t *testing.T) {
	dataCh := make(chan []byte)
	doneCh := make(chan struct{})
	close(doneCh)
	_, err := reassembleK8sResponse(context.Background(), dataCh, doneCh)
	if err == nil {
		t.Errorf("expected error when stream closes before first frame")
	}
}

func TestReassembleK8sResponse_Timeout(t *testing.T) {
	dataCh := make(chan []byte)
	doneCh := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	_, err := reassembleK8sResponse(ctx, dataCh, doneCh)
	if err == nil {
		t.Errorf("expected timeout error")
	}
}

func TestReassembleK8sResponse_AgentReportedError(t *testing.T) {
	header := mustMarshal(protocol.K8sStreamFrame{Kind: protocol.K8sStreamFrameHeader, StatusCode: 200})
	end := mustMarshal(protocol.K8sStreamFrame{Kind: protocol.K8sStreamFrameEnd, Error: "upstream gone"})
	dataCh := make(chan []byte, 2)
	doneCh := make(chan struct{})
	dataCh <- header
	dataCh <- end
	_, err := reassembleK8sResponse(context.Background(), dataCh, doneCh)
	if err == nil || err.Error() == "" {
		t.Errorf("expected agent-reported-error surfaced, got %v", err)
	}
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

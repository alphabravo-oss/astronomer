package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// AssembleChunkedResponse must reconstruct a body
// from header + N data + end frames in agent-emit order.
func TestAssembleChunkedResponse_RoundTrip(t *testing.T) {
	// Simulate the agent emitting a 700KB body in three 256KB-ish chunks.
	const part1 = "header-data-"
	const part2 = "middle-chunk-"
	const part3 = "tail-bytes"
	originalBody := []byte(strings.Repeat(part1+part2+part3, 1000)) // ~33 KB; small enough to keep the test fast

	header := protocol.K8sStreamFrame{
		Kind:       protocol.K8sStreamFrameHeader,
		StatusCode: 200,
		Headers:    map[string]string{"Content-Type": "application/json"},
	}
	headerJSON, _ := json.Marshal(header)

	// Split originalBody into three frames to cover the multi-chunk path.
	third := len(originalBody) / 3
	data1, _ := json.Marshal(protocol.K8sStreamFrame{
		Kind: protocol.K8sStreamFrameData,
		Body: base64.StdEncoding.EncodeToString(originalBody[:third]),
	})
	data2, _ := json.Marshal(protocol.K8sStreamFrame{
		Kind: protocol.K8sStreamFrameData,
		Body: base64.StdEncoding.EncodeToString(originalBody[third : 2*third]),
	})
	data3, _ := json.Marshal(protocol.K8sStreamFrame{
		Kind: protocol.K8sStreamFrameData,
		Body: base64.StdEncoding.EncodeToString(originalBody[2*third:]),
	})
	end, _ := json.Marshal(protocol.K8sStreamFrame{Kind: protocol.K8sStreamFrameEnd})

	dataCh := make(chan []byte, 4)
	doneCh := make(chan struct{})
	// Don't enqueue the header here — the assembler receives it via the
	// `first` argument.
	dataCh <- data1
	dataCh <- data2
	dataCh <- data3
	dataCh <- end

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := assembleChunkedResponse(ctx, dataCh, doneCh, headerJSON)
	if err != nil {
		t.Fatalf("assembleChunkedResponse: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if got := resp.Headers["Content-Type"]; got != "application/json" {
		t.Errorf("header Content-Type = %q, want application/json", got)
	}
	decoded, err := base64.StdEncoding.DecodeString(resp.Body)
	if err != nil {
		t.Fatalf("decode reassembled body: %v", err)
	}
	if string(decoded) != string(originalBody) {
		t.Errorf("reassembled body length=%d, want %d (first 50: %q vs %q)",
			len(decoded), len(originalBody), string(decoded[:50]), string(originalBody[:50]))
	}
}

// Error-path frame: agent reports a stream error via the end frame's
// Error field.
func TestAssembleChunkedResponse_AgentError(t *testing.T) {
	header, _ := json.Marshal(protocol.K8sStreamFrame{
		Kind: protocol.K8sStreamFrameHeader, StatusCode: 200,
	})
	end, _ := json.Marshal(protocol.K8sStreamFrame{
		Kind: protocol.K8sStreamFrameEnd, Error: "upstream EOF",
	})
	dataCh := make(chan []byte, 2)
	doneCh := make(chan struct{})
	dataCh <- end

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := assembleChunkedResponse(ctx, dataCh, doneCh, header)
	if err == nil || !strings.Contains(err.Error(), "upstream EOF") {
		t.Errorf("expected error containing 'upstream EOF', got %v", err)
	}
}

// Probe correctly classifies frames.
func TestIsStreamFrame(t *testing.T) {
	headerJSON, _ := json.Marshal(protocol.K8sStreamFrame{Kind: protocol.K8sStreamFrameHeader})
	if !isStreamFrame(headerJSON) {
		t.Error("header frame should be classified as stream frame")
	}
	respJSON, _ := json.Marshal(protocol.K8sResponsePayload{StatusCode: 200})
	if isStreamFrame(respJSON) {
		t.Error("K8sResponsePayload should NOT be classified as stream frame")
	}
	if isStreamFrame([]byte("garbage")) {
		t.Error("malformed JSON should NOT be classified as stream frame")
	}
}

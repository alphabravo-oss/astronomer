package tunnel

import (
	"encoding/json"
	"testing"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func TestTranslateFromFrontend_Stdin(t *testing.T) {
	msg, skip := translateFromFrontend([]byte(`{"type":"stdin","data":"ls -l\n"}`), "stream-1", "cluster-1")
	if skip {
		t.Fatal("stdin frame should not be skipped")
	}
	if msg == nil {
		t.Fatal("expected message")
	}
	if msg.Type != protocol.MsgExecInput {
		t.Errorf("type = %q, want %q", msg.Type, protocol.MsgExecInput)
	}
	// Payload must be a JSON-encoded string so the parent protocol.Message
	// stays valid JSON when re-marshaled over the tunnel. The agent's
	// HandleExecInput unmarshals it before writing to stdin. Raw-byte
	// payloads (the previous behavior) produced invalid wire JSON for any
	// non-numeric keystroke and got dropped on the agent side, which is
	// what made "type into the terminal and nothing happens" fail in
	// production.
	var data string
	if err := json.Unmarshal(msg.Payload, &data); err != nil {
		t.Fatalf("payload %q is not valid JSON: %v", string(msg.Payload), err)
	}
	if data != "ls -l\n" {
		t.Errorf("decoded payload = %q, want %q", data, "ls -l\n")
	}
}

func TestTranslateFromFrontend_Resize(t *testing.T) {
	msg, skip := translateFromFrontend([]byte(`{"type":"resize","cols":120,"rows":40}`), "s", "c")
	if skip {
		t.Fatal("resize must not be skipped")
	}
	if msg.Type != protocol.MsgExecResize {
		t.Errorf("type = %q, want %q", msg.Type, protocol.MsgExecResize)
	}
	var got protocol.ExecResizePayload
	if err := json.Unmarshal(msg.Payload, &got); err != nil {
		t.Fatalf("payload not ExecResizePayload: %v", err)
	}
	if got.Width != 120 || got.Height != 40 {
		t.Errorf("width/height = %d/%d, want 120/40 (cols→Width, rows→Height)", got.Width, got.Height)
	}
}

func TestTranslateFromFrontend_AuthSkipped(t *testing.T) {
	msg, skip := translateFromFrontend([]byte(`{"type":"auth","token":"abc"}`), "s", "c")
	if !skip {
		t.Fatal("auth frame should be skipped")
	}
	if msg != nil {
		t.Errorf("auth frame should produce nil msg, got %+v", msg)
	}
}

func TestTranslateFromFrontend_RawFallback(t *testing.T) {
	// Non-JSON frames are forwarded as raw stdin so non-browser clients
	// work. As with the typed-envelope path, the bytes are JSON-encoded
	// here so the message envelope stays valid JSON over the wire.
	msg, skip := translateFromFrontend([]byte(`hello`), "s", "c")
	if skip {
		t.Fatal("raw frame should not be skipped")
	}
	if msg.Type != protocol.MsgExecInput {
		t.Errorf("type = %q, want %q", msg.Type, protocol.MsgExecInput)
	}
	var data string
	if err := json.Unmarshal(msg.Payload, &data); err != nil {
		t.Fatalf("payload %q is not valid JSON: %v", string(msg.Payload), err)
	}
	if data != "hello" {
		t.Errorf("decoded payload = %q, want %q", data, "hello")
	}
}

func TestTranslateFromFrontend_End(t *testing.T) {
	msg, _ := translateFromFrontend([]byte(`{"type":"end"}`), "s", "c")
	if msg.Type != protocol.MsgExecEnd {
		t.Errorf("type = %q, want %q", msg.Type, protocol.MsgExecEnd)
	}
}

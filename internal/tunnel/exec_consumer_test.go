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
	// Payload must be raw bytes, not the JSON envelope.
	if string(msg.Payload) != "ls -l\n" {
		t.Errorf("payload = %q, want %q", string(msg.Payload), "ls -l\n")
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
	// Non-JSON frames are forwarded as raw stdin so non-browser clients work.
	msg, skip := translateFromFrontend([]byte(`hello`), "s", "c")
	if skip {
		t.Fatal("raw frame should not be skipped")
	}
	if msg.Type != protocol.MsgExecInput {
		t.Errorf("type = %q, want %q", msg.Type, protocol.MsgExecInput)
	}
	if string(msg.Payload) != "hello" {
		t.Errorf("payload = %q, want %q", string(msg.Payload), "hello")
	}
}

func TestTranslateFromFrontend_End(t *testing.T) {
	msg, _ := translateFromFrontend([]byte(`{"type":"end"}`), "s", "c")
	if msg.Type != protocol.MsgExecEnd {
		t.Errorf("type = %q, want %q", msg.Type, protocol.MsgExecEnd)
	}
}

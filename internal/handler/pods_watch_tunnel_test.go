package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

func dataFrame(t *testing.T, body []byte) []byte {
	t.Helper()
	b, err := json.Marshal(protocol.K8sStreamFrame{
		Kind: protocol.K8sStreamFrameData,
		Body: base64.StdEncoding.EncodeToString(body),
	})
	if err != nil {
		t.Fatalf("marshal frame: %v", err)
	}
	return b
}

// A server that streams a huge unterminated line (no newline ever) must be
// terminated by the reassembly cap rather than buffering forever. We feed
// newline-free data chunks until the watch goroutine emits a terminal ERROR
// event and closes the channel.
func TestWatchPodsCapsReassemblyBuffer(t *testing.T) {
	hub := tunnel.NewHub(nil)
	sm := hub.RegisterAgentForTest("c1")
	r := NewTunnelK8sRequester(hub)

	// Pushing >64 MiB through full JSON+base64 marshal/unmarshal is slow under
	// the race detector; allow generous headroom so the cap (not the deadline)
	// is what terminates the stream.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	out, err := r.WatchPods(ctx, "c1", "ns")
	if err != nil {
		t.Fatalf("WatchPods: %v", err)
	}

	// Grab the stream the requester created and stuff it with newline-free
	// chunks. 256 KiB each; well over 64 MiB total forces the cap.
	stream := sm.SoleStreamForTest()
	if stream == nil {
		t.Fatal("expected exactly one active stream")
	}
	chunk := make([]byte, 256*1024)
	for i := range chunk {
		chunk[i] = 'x' // never a '\n', so emitLines never drains
	}

	go func() {
		// Enough chunks to exceed maxAssembledResponseBytes (64 MiB).
		for i := 0; i < (maxAssembledResponseBytes/len(chunk))+4; i++ {
			select {
			case stream.DataCh <- dataFrame(t, chunk):
			case <-ctx.Done():
				return
			}
		}
	}()

	// The first (and only) event must be the terminal ERROR; the channel must
	// then close rather than the goroutine buffering forever.
	select {
	case ev, ok := <-out:
		if !ok {
			t.Fatal("channel closed without emitting terminal ERROR")
		}
		if ev.Type != "ERROR" {
			t.Fatalf("first event type = %q, want ERROR", ev.Type)
		}
		var msg string
		_ = json.Unmarshal(ev.Object, &msg)
		if !strings.Contains(msg, "exceeded") {
			t.Errorf("ERROR object = %q, want overflow message", msg)
		}
	case <-ctx.Done():
		t.Fatal("timed out: buffer grew unbounded instead of capping")
	}

	// Channel must close after the terminal error.
	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("expected channel closed after terminal ERROR")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for channel close after ERROR")
	}
}

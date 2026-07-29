package tunnel

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// chunkReader hands back exactly one chunk per Read so a test can reproduce the
// arbitrary read boundaries a real watch body arrives on.
type chunkReader struct {
	chunks [][]byte
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.chunks[0])
	c.chunks[0] = c.chunks[0][n:]
	if len(c.chunks[0]) == 0 {
		c.chunks = c.chunks[1:]
	}
	return n, nil
}

func (c *chunkReader) Close() error { return nil }

// watchTerminator is how the agent side ends the stream once every data frame
// has been queued. The two production endings — an explicit end frame and a
// stream torn down without one (dropped tunnel) — must deliver the same bytes.
type watchTerminator func(stream *Stream)

// endWithFrame is the happy path: an explicit K8S_STREAM_FRAME_END.
func endWithFrame(stream *Stream) {
	end, _ := json.Marshal(protocol.K8sStreamFrame{Kind: protocol.K8sStreamFrameEnd})
	stream.DataCh <- end
}

// endWithDoneCh mimics a dropped tunnel: no end frame, the stream is just
// closed. It waits for the consumer to drain the queued data frames first,
// otherwise the consumer's select between DataCh and DoneCh is a coin flip and
// the test would be testing scheduling, not the flush.
func endWithDoneCh(stream *Stream) {
	for len(stream.DataCh) > 0 {
		time.Sleep(time.Millisecond)
	}
	stream.Close()
}

// runLocalWatch drives consumeStreamingResponse end to end for a
// namespace-scoped caller, feeding chunks as K8S_STREAM_FRAME data frames, and
// returns what the client received.
func runLocalWatch(t *testing.T, clusterID string, allowed map[string]struct{}, chunks [][]byte) (string, *AgentConnection) {
	t.Helper()
	return runLocalWatchEnding(t, clusterID, allowed, chunks, endWithFrame)
}

// runLocalWatchEnding is runLocalWatch with the stream ending under test.
func runLocalWatchEnding(t *testing.T, clusterID string, allowed map[string]struct{}, chunks [][]byte, end watchTerminator) (string, *AgentConnection) {
	t.Helper()

	hub := NewHub(slog.Default())
	agent := &AgentConnection{
		ClusterID: clusterID,
		Streams:   NewStreamManager(256),
		sendCh:    make(chan *protocol.Message, sendChannelSize),
		cancel:    func() {},
	}
	hub.agents.Set(clusterID, agent)

	proxy := NewProxyHandler(hub, slog.Default())
	router := chi.NewRouter()
	router.HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", proxy.HandleK8sProxy)

	go func() {
		msg := <-agent.sendCh // the K8S_STREAM_REQUEST
		stream, ok := agent.Streams.GetStream(msg.StreamID)
		if !ok {
			t.Errorf("stream %s not found", msg.StreamID)
			return
		}
		hdr, _ := json.Marshal(protocol.K8sStreamFrame{
			Kind:       protocol.K8sStreamFrameHeader,
			StatusCode: http.StatusOK,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})
		stream.DataCh <- hdr
		for _, c := range chunks {
			d, _ := json.Marshal(protocol.K8sStreamFrame{
				Kind: protocol.K8sStreamFrameData,
				Body: base64.StdEncoding.EncodeToString(c),
			})
			stream.DataCh <- d
		}
		end(stream)
	}()

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/clusters/"+clusterID+"/k8s/api/v1/pods?watch=true", nil)
	req = req.WithContext(WithNamespaceFilter(req.Context(), allowed))
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		router.ServeHTTP(w, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("watch consumer did not finish")
	}
	return w.Body.String(), agent
}

// TestConsumeStreamingResponseFiltersAcrossChunkBoundaries is the regression for
// namespace-filtered watches on the same-pod (agent) path. The agent emits
// whatever a single Read returned, so events coalesce into one frame and split
// across frames. Before the fix each decoded frame was handed whole to
// watchEventAllowed and dropped on any parse failure, so the coalesced frame and
// both halves of every split event were silently discarded — the client here
// received nothing at all.
func TestConsumeStreamingResponseFiltersAcrossChunkBoundaries(t *testing.T) {
	body := evtTeamA + "\n" + evtTeamB + "\n" + evtTeamA2 + "\n"
	// Boundaries: mid-way through the denied event, then mid-way through the
	// second allowed event.
	cut1 := len(evtTeamA) + 1 + 20
	cut2 := len(evtTeamA) + 1 + len(evtTeamB) + 1 + 15
	chunks := [][]byte{[]byte(body[:cut1]), []byte(body[cut1:cut2]), []byte(body[cut2:])}

	got, _ := runLocalWatch(t, "cluster-watch-split", allowSet("team-a"), chunks)

	want := evtTeamA + "\n" + evtTeamA2 + "\n"
	if got != want {
		t.Fatalf("filtered watch body:\n got %q\nwant %q", got, want)
	}
}

// TestConsumeStreamingResponseDeliversFinalUnterminatedEvent covers a stream
// whose last event arrives without a trailing newline before the end frame.
// Before the fix this event was delivered only by luck (json.Unmarshal tolerates
// a lone complete object); it must now survive reassembly too.
func TestConsumeStreamingResponseDeliversFinalUnterminatedEvent(t *testing.T) {
	chunks := [][]byte{[]byte(evtTeamA[:10]), []byte(evtTeamA[10:])}

	got, _ := runLocalWatch(t, "cluster-watch-tail", allowSet("team-a"), chunks)

	if got != evtTeamA+"\n" {
		t.Fatalf("final unterminated event:\n got %q\nwant %q", got, evtTeamA+"\n")
	}
}

// TestConsumeStreamingResponseTerminatesOnOversizedWatchEvent asserts the bound
// on the carry-over buffer is enforced and observable: the stream is torn down
// (agent told to stop) and the failure is counted, rather than the events being
// dropped in silence — which is the failure mode this whole fix removes.
func TestConsumeStreamingResponseTerminatesOnOversizedWatchEvent(t *testing.T) {
	counter := k8sProxyErrorsTotal.WithLabelValues(
		observability.MetricValues("watch", "watch_event_too_large")...)
	before := testutil.ToFloat64(counter)

	chunks := [][]byte{[]byte(evtTeamA + "\n")}
	for sent := 0; sent <= watchLineMaxBytes; sent += 512 * 1024 {
		chunks = append(chunks, bytes.Repeat([]byte("x"), 512*1024))
	}

	got, agent := runLocalWatch(t, "cluster-watch-huge", allowSet("team-a"), chunks)

	if got != evtTeamA+"\n" {
		t.Fatalf("events before the oversized one must still be delivered:\n got %q", got)
	}
	if after := testutil.ToFloat64(counter); after <= before {
		t.Fatalf("watch_event_too_large not recorded (before=%v after=%v)", before, after)
	}
	var sawStop bool
	for len(agent.sendCh) > 0 {
		if m := <-agent.sendCh; m.Type == protocol.MsgK8sStreamStop {
			sawStop = true
		}
	}
	if !sawStop {
		t.Fatal("oversized event did not tear the stream down (no MsgK8sStreamStop)")
	}
}

// TestConsumeStreamingResponseFlushesTailOnStreamTeardown covers the OTHER way
// an agent stream ends: the tunnel drops and the stream is closed with no end
// frame. The cross-pod path flushes its carry-over on any read termination, so
// an agent path that only flushed on the end frame would silently drop a
// complete final event that the cross-pod path delivers — the same
// replica-dependent divergence this fix removes.
func TestConsumeStreamingResponseFlushesTailOnStreamTeardown(t *testing.T) {
	chunks := [][]byte{[]byte(evtTeamA + "\n" + evtTeamA2[:12]), []byte(evtTeamA2[12:])}

	got, _ := runLocalWatchEnding(t, "cluster-watch-teardown", allowSet("team-a"), chunks, endWithDoneCh)

	want := evtTeamA + "\n" + evtTeamA2 + "\n"
	if got != want {
		t.Fatalf("tail dropped on stream teardown:\n got %q\nwant %q", got, want)
	}
}

// TestLocalAndCrossPodWatchFilterAgree feeds the identical byte sequence, cut at
// the identical boundaries, through the agent path and the cross-pod path. They
// had drifted — the cross-pod path reassembled with a bufio.Scanner while the
// agent path filtered raw frames — so the same request returned different data
// depending on which replica nginx happened to pin it to.
func TestLocalAndCrossPodWatchFilterAgree(t *testing.T) {
	body := evtTeamA + "\n" + evtTeamB + "\n" + evtTeamA2 + "\n" +
		`{"type":"BOOKMARK","object":{"kind":"Pod","metadata":{"resourceVersion":"42"}}}` + "\n"
	chunks := splitIrregular([]byte(body), 7, 31, 1, 100)

	local, _ := runLocalWatch(t, "cluster-watch-agree", allowSet("team-a"), chunks)

	proxy := NewProxyHandler(NewHub(slog.Default()), slog.Default())
	w := httptest.NewRecorder()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       &chunkReader{chunks: append([][]byte(nil), chunks...)},
	}
	if !proxy.forwardFilteredOwnerWatch(w, resp, allowSet("team-a")) {
		t.Fatal("forwardFilteredOwnerWatch reported a header failure")
	}
	crossPod := w.Body.String()

	if local != crossPod {
		t.Fatalf("paths disagree:\n  local     %q\n  cross-pod %q", local, crossPod)
	}
	want := evtTeamA + "\n" + evtTeamA2 + "\n" +
		`{"type":"BOOKMARK","object":{"kind":"Pod","metadata":{"resourceVersion":"42"}}}` + "\n"
	if local != want {
		t.Fatalf("filtered body:\n got %q\nwant %q", local, want)
	}
}

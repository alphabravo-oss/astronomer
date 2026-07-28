package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/client-go/rest"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// newTalkingTunnel brings up an agent connected to a test server that can push
// frames INTO the agent, which is what makes readLoop's dispatch observable.
// Returns the client and the server; the connection is torn down by t.Cleanup.
func newTalkingTunnel(t *testing.T, cfg *AgentConfig) (*TunnelClient, *tunnelTestServer) {
	t.Helper()
	ts := newTunnelTestServer(t)
	ts.push = make(chan *protocol.Message, 512)

	cfg.ServerURL = ts.wsURL()
	cfg.ReconnectBackoff = 1
	cfg.MaxReconnect = 1
	tc := NewTunnelClient(cfg, testLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	connectDone := make(chan struct{})
	go func() {
		defer close(connectDone)
		_ = tc.Connect(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-connectDone:
		case <-time.After(10 * time.Second):
			t.Error("Connect did not return after context cancellation")
		}
	})

	waitFor(t, 10*time.Second, "the session to establish", func() bool {
		return ts.sessions.Load() == 1 && tc.IsConnected()
	})
	return tc, ts
}

// k8sRequestFrame builds a well-formed K8S_REQUEST the k8s proxy will act on.
func k8sRequestFrame(streamID string) *protocol.Message {
	payload, _ := json.Marshal(protocol.K8sRequestPayload{
		Method: http.MethodGet,
		Path:   "/api/v1/pods",
	})
	return &protocol.Message{
		Type:      protocol.MsgK8sRequest,
		StreamID:  streamID,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

// peak tracks a high-water mark under concurrency.
type peak struct {
	cur atomic.Int64
	max atomic.Int64
}

func (p *peak) enter() {
	v := p.cur.Add(1)
	for {
		m := p.max.Load()
		if v <= m || p.max.CompareAndSwap(m, v) {
			return
		}
	}
}

func (p *peak) exit()       { p.cur.Add(-1) }
func (p *peak) high() int64 { return p.max.Load() }

// withSmallResponseBudget swaps the process-wide buffered-response budget (and
// the per-request cap derived from it) for a small one, restoring both after the
// test. Package-level state, so the tests that use it must not run in parallel.
func withSmallResponseBudget(t *testing.T, limit int64) *memBudget {
	t.Helper()
	oldBudget, oldCap := agentResponseBudget, maxK8sResponseBodyBytes
	agentResponseBudget = newMemBudget(limit)
	maxK8sResponseBodyBytes = limit
	t.Cleanup(func() {
		agentResponseBudget, maxK8sResponseBodyBytes = oldBudget, oldCap
	})
	return agentResponseBudget
}

// TestInflightRequestsAreBounded is the core item-2 regression, run through the
// production wiring (AdaptStreamingHandler + K8sProxy.HandleRequestStreaming)
// rather than a stand-in handler, because the defect is about what the real
// handler buffers.
//
// A hundred K8S_REQUESTs arrive while every upstream response is held open. Two
// things must stay bounded: the number of handler goroutines running at once,
// and the number of BYTES those goroutines have buffered — a goroutine cap
// alone is not a memory bound, since N goroutines each holding the per-request
// cap still OOMs at any N.
//
// Before the fix readLoop dispatched `go func(m)` unconditionally: all 100
// handlers ran concurrently, all 100 buffered a body at once, and the agent's
// only limit was the 64 MiB per-request cap — two of which already exceed the
// 512Mi container limit.
func TestInflightRequestsAreBounded(t *testing.T) {
	const (
		burst       = 100
		maxInflight = 4
		bodyBytes   = 512 * 1024 // > K8sChunkSizeBytes, so replies take the chunked path
	)
	// The budget is deliberately smaller than maxInflight x the per-response
	// footprint, so it is an INDEPENDENT bound: even with permits available, the
	// bytes already resident decide whether one more body may be read.
	budget := withSmallResponseBudget(t, 2*1024*1024)

	// Upstream that writes a large body and then refuses to finish until
	// released, so every admitted handler is provably still holding its buffer
	// when the assertions run.
	hold := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(hold) }) }
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, bodyBytes))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-hold:
		case <-r.Context().Done():
		}
	}))
	// Cleanups run LIFO, so the release registered second runs first: closing
	// the server while requests are still held would make Close wait them all
	// out before the test could report a failure.
	t.Cleanup(upstream.Close)
	t.Cleanup(release)

	proxy := &K8sProxy{
		restConfig: &rest.Config{Host: upstream.URL},
		httpClient: upstream.Client(),
		log:        testLogger(),
		streams:    make(map[string]context.CancelFunc),
	}

	cfg := testConfig()
	cfg.MaxInflightRequests = maxInflight
	tc, ts := newTalkingTunnel(t, cfg)

	var handlers peak
	tc.RegisterHandler(protocol.MsgK8sRequest, AdaptStreamingHandler(tc,
		func(ctx context.Context, msg *protocol.Message, sendFn func(*protocol.Message) error) error {
			handlers.enter()
			defer handlers.exit()
			return proxy.HandleRequestStreaming(ctx, msg, sendFn)
		}))

	for i := 0; i < burst; i++ {
		ts.push <- k8sRequestFrame("req-" + string(rune('a'+i%26)) + string(rune('0'+i/26)))
	}

	// Every request past the bound must be answered, not silently queued: the
	// originator is blocked on a stream that would otherwise never produce a
	// frame. Successful replies take the chunked path (K8S_STREAM_FRAME), so a
	// K8S_RESPONSE here is unambiguously a rejection.
	waitFor(t, 20*time.Second, "the burst to be either admitted past the bound or shed with a reply each", func() bool {
		return handlers.high() > maxInflight || ts.countReceived(protocol.MsgK8sResponse) >= burst-maxInflight
	})

	if got := handlers.high(); got > maxInflight {
		t.Fatalf("peak concurrent handlers = %d, want <= %d; readLoop is still dispatching without a bound", got, maxInflight)
	}
	if got := budget.Peak(); got > budget.limit {
		t.Fatalf("peak buffered response bytes = %d, want <= the %d-byte budget", got, budget.limit)
	}
	if budget.Peak() == 0 {
		t.Fatal("no bytes were ever charged to the budget; the test never exercised the buffered path")
	}
	if !tc.IsConnected() {
		t.Fatal("the tunnel closed under a request burst; shedding must cost one request, not the connection")
	}

	// The rejection has to be a shape the k8s consumer understands. An
	// ErrorPayload would unmarshal into K8sResponsePayload as StatusCode 0,
	// which ensureSuccess reads as SUCCESS with an empty body.
	var rejection *protocol.K8sResponsePayload
	for _, msg := range ts.receivedMessages() {
		if msg.Type != protocol.MsgK8sResponse {
			continue
		}
		var parsed protocol.K8sResponsePayload
		if err := json.Unmarshal(msg.Payload, &parsed); err != nil {
			t.Fatalf("rejection payload is not a K8sResponsePayload: %v", err)
		}
		rejection = &parsed
		break
	}
	if rejection == nil {
		t.Fatal("no rejection frame captured")
	}
	if rejection.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("rejection status = %d, want 429 so the originator retries instead of reading an empty success", rejection.StatusCode)
	}
	if rejection.Headers["Retry-After"] == "" {
		t.Fatal("rejection carries no Retry-After; the retryable signal is what makes shedding safe")
	}
	body, err := base64.StdEncoding.DecodeString(rejection.Body)
	if err != nil {
		t.Fatalf("decode rejection body: %v", err)
	}
	var status struct {
		Kind   string `json:"kind"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(body, &status); err != nil || status.Kind != "Status" {
		t.Fatalf("rejection body is not an apiserver Status object: %q", body)
	}

	// Release the held handlers and confirm the permits come back: a bound that
	// never refills is an outage, not a limit.
	release()
	waitFor(t, 20*time.Second, "every admitted handler to finish and return its permit", func() bool {
		return tc.inflightActive(dispatchBuffered) == 0
	})
	waitFor(t, 10*time.Second, "the buffered-response budget to return to zero", func() bool {
		return agentResponseBudget.Used() == 0
	})
}

// TestControlFramesBypassInflightLimit locks the exemption that keeps the bound
// from deadlocking the session. With every permit held, the agent must still
// answer a heartbeat and must still process the messages that TERMINATE
// long-running work — EXEC_INPUT/RESIZE/END, K8S_STREAM_STOP, LOG_STOP. If
// those queued behind the bound, the only thing that could free the bound would
// be the very messages the bound was blocking.
//
// It also proves readLoop itself never stalls: the pong is produced on
// readLoop's own goroutine, so a reader parked waiting for a permit could not
// emit one, and the server's ping deadline would reap the agent.
func TestControlFramesBypassInflightLimit(t *testing.T) {
	const maxInflight = 2

	cfg := testConfig()
	cfg.MaxInflightRequests = maxInflight
	tc, ts := newTalkingTunnel(t, cfg)

	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	tc.RegisterHandler(protocol.MsgK8sRequest, func(ctx context.Context, _ *protocol.Message) (*protocol.Message, error) {
		select {
		case <-block:
		case <-ctx.Done():
		}
		return nil, nil
	})

	var execInputs atomic.Int32
	tc.RegisterHandler(protocol.MsgExecInput, AdaptVoidHandler(func(*protocol.Message) error {
		execInputs.Add(1)
		return nil
	}))

	// Saturate, and prove it: not "send enough and hope".
	for i := 0; i < maxInflight+2; i++ {
		ts.push <- k8sRequestFrame("saturate")
	}
	waitFor(t, 10*time.Second, "the buffered in-flight bound to saturate", func() bool {
		return tc.inflightActive(dispatchBuffered) == maxInflight
	})

	// A heartbeat must still get a pong, out of readLoop, while saturated.
	ts.push <- &protocol.Message{Type: protocol.MsgHeartbeat, Timestamp: time.Now().UTC()}
	waitFor(t, 10*time.Second, "a PONG while the in-flight bound is saturated", func() bool {
		return ts.countReceived(protocol.MsgPong) >= 1
	})

	// An exempt control message must still be dispatched while saturated.
	ts.push <- &protocol.Message{Type: protocol.MsgExecInput, StreamID: "exec-1", Timestamp: time.Now().UTC()}
	waitFor(t, 10*time.Second, "an exempt EXEC_INPUT to be handled while saturated", func() bool {
		return execInputs.Load() >= 1
	})

	if got := tc.inflightActive(dispatchBuffered); got != maxInflight {
		t.Fatalf("exempt traffic consumed %d permits, want the bound untouched at %d", got, maxInflight)
	}
	if !tc.IsConnected() {
		t.Fatal("the tunnel closed while the in-flight bound was saturated")
	}
}

// TestInflightPermitsAreNeverParkedOnADeadWriteLoop is the cross-item deadlock
// check. A handler holds its permit across the sends it makes, which is correct
// — the work is genuinely still in flight — but it means a permit parked on a
// send that never resolves would take the bound down with it, and every permit
// parking at once is a total stall of the request path.
//
// Two properties make that impossible, and both are asserted here:
//   - every handler send is bound to the CONNECTION context, and writeLoop
//     cancels that context when it dies, so a dead writer aborts waiting
//     producers immediately rather than after the full sendQueueWait; and
//   - the wait is bounded anyway, so even a live-but-wedged socket returns the
//     permit rather than holding it forever.
func TestInflightPermitsAreNeverParkedOnADeadWriteLoop(t *testing.T) {
	cfg := testConfig()
	cfg.MaxInflightRequests = 8
	tc := NewTunnelClient(cfg, testLogger())
	tc.setConnected(true)

	// A permanently saturated data queue with no writeLoop draining it: the
	// exact state a dead writer leaves behind.
	for i := 0; i < sendQueueSize; i++ {
		if err := tc.Send(&protocol.Message{Type: protocol.MsgK8sStreamFrame, StreamID: "wedge"}); err != nil {
			t.Fatalf("filling the data queue at %d: %v", i, err)
		}
	}

	connCtx, connCancel := context.WithCancel(context.Background())
	done := make(chan struct{}, cfg.MaxInflightRequests)
	for i := 0; i < cfg.MaxInflightRequests; i++ {
		if !tc.acquireDispatch(dispatchBuffered) {
			t.Fatalf("could not acquire permit %d of %d", i, cfg.MaxInflightRequests)
		}
		go func() {
			defer tc.releaseDispatch(dispatchBuffered)
			_ = tc.SendBlocking(connCtx, &protocol.Message{Type: protocol.MsgLogData, StreamID: "blocked"})
			done <- struct{}{}
		}()
	}

	// Every permit is now held by a goroutine blocked on the send path.
	waitFor(t, 5*time.Second, "all permits to be held by blocked senders", func() bool {
		return tc.inflightActive(dispatchBuffered) == cfg.MaxInflightRequests
	})

	// writeLoop dying cancels the connection context. Permits must come back
	// immediately, not after sendQueueWait: 8 permits x 2s would be a 16s stall
	// of the whole request path on a connection that is already gone.
	connCancel()
	deadline := time.After(sendQueueWait / 4)
	for i := 0; i < cfg.MaxInflightRequests; i++ {
		select {
		case <-done:
		case <-deadline:
			t.Fatalf("only %d of %d permits were released within %v of the connection dying; a dead writeLoop parks the in-flight bound",
				i, cfg.MaxInflightRequests, sendQueueWait/4)
		}
	}
	waitFor(t, 5*time.Second, "the in-flight bound to be fully reclaimed", func() bool {
		return tc.inflightActive(dispatchBuffered) == 0
	})
	if !tc.IsConnected() {
		t.Fatal("a saturated data queue force-closed the tunnel")
	}
}

// TestResponseBudgetRefusesRatherThanDeadlocking pins the byte budget's
// non-blocking contract. If reserve() waited for room, two readers each holding
// half the budget and each needing more would wait on each other forever, with
// no timeout that could safely break it — releasing early would hand out memory
// that is still in use. Failing the reservation is what makes the budget
// deadlock-free, so the failure has to be a first-class, retryable outcome.
func TestResponseBudgetRefusesRatherThanDeadlocking(t *testing.T) {
	budget := newMemBudget(64 * 1024)

	first, releaseFirst, err := readBodyWithinBudget(newRepeatReader(48*1024), 1024*1024, budget)
	if err != nil {
		t.Fatalf("first body: %v", err)
	}
	if len(first) != 48*1024 {
		t.Fatalf("first body = %d bytes, want %d", len(first), 48*1024)
	}

	// The budget is now mostly spoken for. A second large body must be refused
	// outright, and must leave nothing behind.
	usedBefore := budget.Used()
	if _, _, err := readBodyWithinBudget(newRepeatReader(48*1024), 1024*1024, budget); err == nil {
		t.Fatal("second body was admitted; the aggregate byte bound does not hold")
	} else if !errors.Is(err, errResponseBudgetExhausted) {
		t.Fatalf("second body failed with %v, want the retryable budget error", err)
	}
	if got := budget.Used(); got != usedBefore {
		t.Fatalf("refused read leaked %d bytes", got-usedBefore)
	}

	releaseFirst()
	if got := budget.Used(); got != 0 {
		t.Fatalf("budget = %d after release, want 0", got)
	}
	// Release is idempotent: callers defer it and may also call it early.
	releaseFirst()
	if got := budget.Used(); got != 0 {
		t.Fatalf("double release drove the budget to %d", got)
	}

	// With room again, the same read succeeds — a refusal is backpressure, not
	// a permanent failure.
	third, releaseThird, err := readBodyWithinBudget(newRepeatReader(48*1024), 1024*1024, budget)
	if err != nil {
		t.Fatalf("third body after release: %v", err)
	}
	if len(third) != 48*1024 {
		t.Fatalf("third body = %d bytes, want %d", len(third), 48*1024)
	}
	releaseThird()
}

// newRepeatReader returns a reader yielding n bytes then EOF, in small pieces so
// the budgeted reader exercises its growth loop rather than one big read.
func newRepeatReader(n int) *repeatReader { return &repeatReader{remaining: n} }

type repeatReader struct{ remaining int }

func (r *repeatReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.remaining {
		n = r.remaining
	}
	if n > 4096 {
		n = 4096
	}
	r.remaining -= n
	return n, nil
}

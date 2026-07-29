package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/observability"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// dispatchClass is the concurrency class of an INBOUND message, deciding
// whether readLoop's dispatch of it is gated and against which bound. It is the
// read-side counterpart of frameClass: the same idea (policy keyed off what the
// frame costs, not off who sent it) applied to the goroutine and memory a
// message consumes rather than to the queue it joins.
type dispatchClass string

const (
	// dispatchExempt is a cheap message that steers work already in progress,
	// or an operational command that must never be shed. Gating these is not
	// merely wasteful, it deadlocks: EXEC_INPUT/EXEC_RESIZE/EXEC_END,
	// K8S_STREAM_STOP and LOG_STOP are precisely how the server tells a
	// long-running handler to stop — if they queued behind the bound those
	// handlers hold, the only thing that could free the bound would be the
	// messages the bound is blocking. DECOMMISSION and AGENT_UPGRADE are
	// once-per-lifetime commands whose rejection would strand an operator, and
	// DESIRED_STATE_RESPONSE is the answer to a request the agent itself made.
	dispatchExempt dispatchClass = "exempt"
	// dispatchStream is a long-lived, low-memory handler: a watch, an exec
	// session, a log tail, or a Helm operation that can legitimately run for
	// minutes. These hold their goroutine for the life of the session, so they
	// cannot share a bound with short unary requests — sixteen open watches
	// would otherwise starve every subsequent API call. They get a generous cap
	// that exists to stop unbounded goroutine growth, not to ration ordinary
	// use.
	dispatchStream dispatchClass = "stream"
	// dispatchBuffered is a unary handler that reads an entire upstream
	// response into memory before framing it (K8S_REQUEST, SERVICE_PROXY_REQUEST,
	// RBAC_SYNC_REQUEST). This is the class the 512Mi limit is actually spent
	// on, so it gets the tight bound AND the byte budget in membudget.go.
	dispatchBuffered dispatchClass = "buffered"
)

const (
	// defaultMaxInflightRequests bounds concurrent buffering handlers. Sixteen
	// concurrent LISTs is far more parallelism than a single cluster's UI
	// generates, while being small enough that the per-request work (an
	// upstream round trip plus its buffered body) stays comfortably inside the
	// byte budget. Override with ASTRONOMER_MAX_INFLIGHT_REQUESTS.
	defaultMaxInflightRequests = 16
	// defaultMaxInflightStreams bounds concurrent long-lived handlers. Sized
	// for real fan-out — a busy dashboard opens tens of watches per user — and
	// matched to the data queue's 256 slots, since each stream is a producer
	// into it. Override with ASTRONOMER_MAX_INFLIGHT_STREAMS.
	//
	// Note what this bounds now that log/exec permits live for the SESSION and
	// not the handler call (see dispatchPermit): a fully saturated class is 256
	// live sessions, and a log session's bufio.Scanner starts at 64 KiB and
	// grows to at most 1 MiB (logs.go). Steady state is therefore ~16 MiB
	// against the 512Mi container limit, with a pathological all-long-lines
	// worst case of 256 MiB. Lowering the bound would ration ordinary watch
	// traffic to protect against that worst case; operators who need the
	// tighter memory ceiling more than the fan-out have the env override.
	defaultMaxInflightStreams = 256
)

// classifyDispatch maps an inbound message type to its concurrency class.
//
// Unlisted types default to dispatchBuffered: a message type whose handler this
// function has never seen has unknown memory behaviour, and the cost of being
// wrong in that direction is a retryable rejection under load, whereas the cost
// of being wrong in the other is an OOMKill that takes the cluster unmanaged.
func classifyDispatch(t protocol.MessageType) dispatchClass {
	switch t {
	case protocol.MsgExecInput, protocol.MsgExecResize, protocol.MsgExecEnd,
		protocol.MsgK8sStreamStop, protocol.MsgLogStop,
		protocol.MsgDecommission, protocol.MsgAgentUpgrade,
		protocol.MsgDesiredStateResponse:
		return dispatchExempt
	case protocol.MsgK8sStreamRequest, protocol.MsgExecStart, protocol.MsgLogStart,
		protocol.MsgHelmInstall, protocol.MsgHelmUpgrade, protocol.MsgHelmUninstall,
		protocol.MsgHelmRollback, protocol.MsgHelmStatus, protocol.MsgHelmHistory:
		return dispatchStream
	default:
		return dispatchBuffered
	}
}

// inflightLimit resolves the configured bound for a class, clamping unset or
// nonsensical values to the default. Callers that build an AgentConfig by hand
// (localcluster.go, tests) therefore get the same protection as the agent
// binary without having to know these fields exist.
func inflightLimit(cfg *AgentConfig, class dispatchClass) int {
	fallback := defaultMaxInflightRequests
	configured := 0
	if class == dispatchStream {
		fallback = defaultMaxInflightStreams
	}
	if cfg != nil {
		if class == dispatchStream {
			configured = cfg.MaxInflightStreams
		} else {
			configured = cfg.MaxInflightRequests
		}
	}
	if configured <= 0 {
		return fallback
	}
	return configured
}

// dispatchSlots returns the semaphore for a class, or nil for dispatchExempt.
func (tc *TunnelClient) dispatchSlots(class dispatchClass) chan struct{} {
	switch class {
	case dispatchStream:
		return tc.inflightStreams
	case dispatchBuffered:
		return tc.inflightBuffered
	default:
		return nil
	}
}

// acquireDispatch takes a permit for class without ever blocking. readLoop is
// the process's only reader: blocking here would stop pongs and heartbeats and
// get the agent reaped by the server long before the permit freed up, so
// backpressure on this path has to be a rejection, not a wait.
func (tc *TunnelClient) acquireDispatch(class dispatchClass) bool {
	slots := tc.dispatchSlots(class)
	if slots == nil {
		return true
	}
	select {
	case slots <- struct{}{}:
		agentInflightActive.WithLabelValues(observability.MetricValues(string(class))...).Set(float64(len(slots)))
		return true
	default:
		return false
	}
}

func (tc *TunnelClient) releaseDispatch(class dispatchClass) {
	slots := tc.dispatchSlots(class)
	if slots == nil {
		return
	}
	<-slots
	agentInflightActive.WithLabelValues(observability.MetricValues(string(class))...).Set(float64(len(slots)))
}

// dispatchPermitKey is the context key under which readLoop hands a dispatch's
// permit to the handler it is about to run.
type dispatchPermitKey struct{}

// dispatchPermit is the ownership token for one readLoop dispatch's in-flight
// permit. It exists because a permit's natural lifetime is the WORK, not the
// handler CALL, and for two message types those differ by minutes.
//
// LOG_START and EXEC_START are session handlers: they open an upstream stream,
// hand it to a goroutine, and return immediately (logs.go, exec.go — the
// goroutine outlives the call by the whole life of the tail or the shell).
// AdaptStreamingHandler returns as soon as they do, so readLoop's release fires
// within milliseconds while the session — its goroutine, its open kubelet
// stream, and for logs a 1 MiB scanner buffer — is still fully live. A peer
// sending N LOG_STARTs would get N of them however small the bound was, which
// is exactly the unbounded growth dispatchStream exists to stop.
//
// So the permit is transferable: a handler that spawns a session calls
// adoptDispatchPermit and defers the returned func from inside the session
// goroutine, next to the defer that already deletes the session and emits its
// end frame. Handlers that block for their whole duration (K8S_STREAM_REQUEST,
// every Helm op) adopt nothing and keep the original behaviour.
type dispatchPermit struct {
	mu       sync.Mutex
	adopted  bool
	released bool
	release  func()
}

func newDispatchPermit(release func()) *dispatchPermit {
	return &dispatchPermit{release: release}
}

// withDispatchPermit binds a permit to the context readLoop passes the handler.
func withDispatchPermit(ctx context.Context, permit *dispatchPermit) context.Context {
	return context.WithValue(ctx, dispatchPermitKey{}, permit)
}

// adoptDispatchPermit transfers this dispatch's permit to the caller and
// returns the func that releases it. It MUST be called before the handler
// returns, and the returned func MUST be deferred from the goroutine that owns
// the session — otherwise the permit is held for the life of the connection.
//
// A context with no permit (a handler invoked directly by a test, or a
// dispatchExempt type that was never gated) yields a no-op, so callers need no
// special case.
func adoptDispatchPermit(ctx context.Context) func() {
	permit, _ := ctx.Value(dispatchPermitKey{}).(*dispatchPermit)
	if permit == nil {
		return func() {}
	}
	permit.mu.Lock()
	permit.adopted = true
	permit.mu.Unlock()
	return permit.done
}

// done releases the permit, at most once however many owners call it.
func (p *dispatchPermit) done() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return
	}
	p.released = true
	p.release()
}

// finishHandler is readLoop's own defer: it releases the permit unless the
// handler adopted it, in which case the session now owns it.
func (p *dispatchPermit) finishHandler() {
	if p == nil {
		return
	}
	p.mu.Lock()
	adopted := p.adopted
	p.mu.Unlock()
	if adopted {
		return
	}
	p.done()
}

// inflightActive reports the permits currently held for a class.
func (tc *TunnelClient) inflightActive(class dispatchClass) int {
	slots := tc.dispatchSlots(class)
	if slots == nil {
		return 0
	}
	return len(slots)
}

// shedInbound answers a message that could not get a permit, immediately and
// from readLoop's own goroutine, so the server-side originator fails fast
// instead of waiting out its context (ten minutes for Helm).
//
// The reply is queued NON-BLOCKING and onto the DATA queue, deliberately, even
// though its own message type is control-class:
//
//   - Non-blocking because this runs on readLoop. Waiting here for queue room
//     would reintroduce exactly the reader stall the rejection exists to avoid.
//   - Data queue because shed replies are unbounded by construction — one per
//     inbound frame, at whatever rate the peer sends. The control queue's
//     fail-close policy is only sound for the bounded producers it was sized
//     for; letting a request flood in would convert an overload into a
//     force-closed tunnel, which is the failure mode the send-path work just
//     removed. On the data queue a shed reply that cannot be queued is counted
//     and dropped, leaving that one originator on its pre-existing timeout —
//     strictly no worse than today, and it costs no other stream anything.
func (tc *TunnelClient) shedInbound(msg *protocol.Message, class dispatchClass) {
	agentInflightRejectedTotal.WithLabelValues(observability.MetricValues(string(class), string(msg.Type))...).Inc()
	observability.RecordDroppedEvent("agent_inflight_dispatch", "limit_exceeded")
	// Debug, not Warn: shedding fires once per over-limit message, so at the
	// rate that triggers it a line per message is its own outage. The counter
	// above is the operator-facing signal; this is here for reproducing a
	// specific incident.
	tc.log.Debug("shedding inbound request: in-flight limit reached",
		"type", msg.Type, "class", string(class), "stream_id", msg.StreamID,
		"limit", cap(tc.dispatchSlots(class)))

	reply := overloadReply(msg)
	if reply == nil {
		return
	}
	// context.TODO: this is the non-blocking path (wait == 0), so enqueueClass
	// never selects on ctx.Done — and this runs on readLoop, which must not
	// block on a connection context here.
	if err := tc.enqueueClass(context.TODO(), reply, frameStream, 0); err != nil {
		tc.log.Warn("could not queue overload rejection", "type", msg.Type, "error", err)
	}
}

// overloadReplyCode is the retryable error code carried by shed rejections that
// have no richer protocol-level representation.
const overloadReplyCode = "AGENT_OVERLOADED"

// overloadReplyMessage is shared by every rejection shape so operators see one
// string regardless of which handler was shed.
const overloadReplyMessage = "agent at its in-flight request limit; retry"

// overloadReply builds the rejection frame for a shed message.
//
// It is NOT a bare MsgError for every type, because MsgError is not safely
// interpretable on every consumer path. The k8s requester probes the first
// frame of the stream and, if it is not a chunk header, unmarshals it straight
// into a K8sResponsePayload (internal/handler/k8s_requester.go, and the
// cross-pod assembler in internal/tunnel/internal_k8s_assemble.go do the same).
// An ErrorPayload unmarshals into that struct without error, yielding
// StatusCode 0 and an empty body — which ensureSuccess treats as SUCCESS. A
// shed request would then surface to the user as an empty list, silently. So
// each family gets the rejection shape its own consumer already understands,
// and only families with no such shape fall back to MsgError.
func overloadReply(msg *protocol.Message) *protocol.Message {
	reply := &protocol.Message{
		StreamID:  msg.StreamID,
		RequestID: msg.RequestID,
		ClusterID: msg.ClusterID,
		Timestamp: time.Now().UTC(),
	}

	switch msg.Type {
	case protocol.MsgK8sRequest:
		reply.Type = protocol.MsgK8sResponse
		reply.Payload = mustMarshal(protocol.K8sResponsePayload{
			StatusCode: http.StatusTooManyRequests,
			Headers: map[string]string{
				"Content-Type": "application/json",
				// 429 + Retry-After is the apiserver's own overload contract,
				// so every client in the chain — browser, client-go, the
				// server's requester — already knows to back off and retry.
				"Retry-After": "1",
			},
			Body: base64.StdEncoding.EncodeToString(
				[]byte(statusBody(http.StatusTooManyRequests, "TooManyRequests", overloadReplyMessage))),
		})
	case protocol.MsgServiceProxyRequest:
		reply.Type = protocol.MsgServiceProxyResponse
		reply.Payload = mustMarshal(protocol.ServiceProxyResponsePayload{
			StatusCode: http.StatusTooManyRequests,
			Headers:    map[string]string{"Retry-After": "1"},
			Error:      overloadReplyMessage,
		})
	case protocol.MsgHelmInstall, protocol.MsgHelmUpgrade, protocol.MsgHelmUninstall,
		protocol.MsgHelmRollback, protocol.MsgHelmStatus, protocol.MsgHelmHistory:
		// HelmResultPayload.Success=false is the failure signal the helm
		// requester already checks; an ErrorPayload would decode into it as
		// Success=false too, but with an empty Error string.
		reply.Type = protocol.MsgHelmResult
		reply.Payload = mustMarshal(protocol.HelmResultPayload{
			Success: false,
			Error:   overloadReplyMessage,
		})
	default:
		reply.Type = protocol.MsgError
		reply.Payload = mustMarshal(protocol.ErrorPayload{
			Code:    overloadReplyCode,
			Message: overloadReplyMessage,
		})
	}
	return reply
}

// mustMarshal marshals a payload the agent constructs itself. Every value
// passed here is a plain struct of scalars and string maps, so encoding cannot
// fail; a nil payload on the impossible path still produces a routable frame
// rather than a lost reply.
func mustMarshal(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return raw
}

// statusBody renders a metav1.Status JSON document the way the kube-apiserver
// would, so clients parse an agent-generated refusal exactly as they parse a
// real one. Shared by the shed-request 429 and the response-cap 413 so the
// proxy path has one response shape for "the agent refused to do this".
func statusBody(code int, reason, message string) string {
	return fmt.Sprintf(
		`{"kind":"Status","apiVersion":"v1","metadata":{},"status":"Failure","message":%q,"reason":%q,"code":%d}`,
		message, reason, code,
	)
}

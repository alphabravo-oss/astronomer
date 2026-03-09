package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// ExecHandler manages pod exec sessions over the tunnel.
type ExecHandler struct {
	client     *kubernetes.Clientset
	restConfig *rest.Config
	log        *slog.Logger

	// Active sessions keyed by stream ID.
	mu       sync.Mutex
	sessions map[string]*execSession
}

type execSession struct {
	stdinW  io.WriteCloser
	cancel  context.CancelFunc
	resizeC chan remotecommand.TerminalSize
}

// NewExecHandler creates a new ExecHandler.
func NewExecHandler(client *kubernetes.Clientset, restConfig *rest.Config, log *slog.Logger) *ExecHandler {
	return &ExecHandler{
		client:     client,
		restConfig: restConfig,
		log:        log,
		sessions:   make(map[string]*execSession),
	}
}

// HandleExecStart initiates a pod exec session. It decodes the ExecStartPayload,
// creates an SPDY connection to the K8s API server, and starts goroutines to relay
// stdin/stdout via tunnel messages.
func (h *ExecHandler) HandleExecStart(ctx context.Context, msg *protocol.Message, sendFn func(*protocol.Message) error) error {
	var payload protocol.ExecStartPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return fmt.Errorf("decode exec start payload: %w", err)
	}

	streamID := msg.StreamID
	h.log.Info("starting exec session",
		"stream_id", streamID,
		"namespace", payload.Namespace,
		"pod", payload.Pod,
		"container", payload.Container,
		"command", payload.Command,
	)

	// Build the exec request.
	req := h.client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(payload.Pod).
		Namespace(payload.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: payload.Container,
			Command:   payload.Command,
			Stdin:     payload.Stdin,
			Stdout:    true,
			Stderr:    true,
			TTY:       payload.TTY,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(h.restConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("create SPDY executor: %w", err)
	}

	// Create stdin pipe.
	stdinR, stdinW := io.Pipe()

	// Create cancellable context for the session.
	sessionCtx, cancel := context.WithCancel(ctx)

	resizeC := make(chan remotecommand.TerminalSize, 4)

	sess := &execSession{
		stdinW:  stdinW,
		cancel:  cancel,
		resizeC: resizeC,
	}

	h.mu.Lock()
	h.sessions[streamID] = sess
	h.mu.Unlock()

	// Stdout/stderr writers that relay data back through the tunnel.
	stdoutW := &tunnelWriter{streamID: streamID, stream: "stdout", sendFn: sendFn}
	stderrW := &tunnelWriter{streamID: streamID, stream: "stderr", sendFn: sendFn}

	// Run the exec in a goroutine.
	go func() {
		defer func() {
			h.mu.Lock()
			delete(h.sessions, streamID)
			h.mu.Unlock()
			stdinW.Close()

			endPayload, _ := json.Marshal(map[string]string{"reason": "completed"})
			_ = sendFn(&protocol.Message{
				Type:     protocol.MsgExecEnd,
				StreamID: streamID,
				Payload:  endPayload,
			})
			h.log.Info("exec session ended", "stream_id", streamID)
		}()

		opts := remotecommand.StreamOptions{
			Stdout: stdoutW,
			Stderr: stderrW,
		}
		if payload.Stdin {
			opts.Stdin = stdinR
		}
		if payload.TTY {
			opts.Tty = true
			opts.TerminalSizeQueue = &terminalSizeQueue{resizeC: resizeC, ctx: sessionCtx}
		}

		if err := executor.StreamWithContext(sessionCtx, opts); err != nil {
			h.log.Error("exec stream error", "stream_id", streamID, "error", err)
			errPayload, _ := json.Marshal(map[string]string{"error": err.Error()})
			_ = sendFn(&protocol.Message{
				Type:     protocol.MsgExecEnd,
				StreamID: streamID,
				Payload:  errPayload,
			})
		}
	}()

	return nil
}

// HandleExecInput delivers stdin data to an active exec session.
func (h *ExecHandler) HandleExecInput(msg *protocol.Message) error {
	h.mu.Lock()
	sess, ok := h.sessions[msg.StreamID]
	h.mu.Unlock()
	if !ok {
		return fmt.Errorf("no active exec session for stream %s", msg.StreamID)
	}

	_, err := sess.stdinW.Write(msg.Payload)
	return err
}

// HandleExecResize sends a terminal resize event to an active exec session.
func (h *ExecHandler) HandleExecResize(msg *protocol.Message) error {
	var payload protocol.ExecResizePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return fmt.Errorf("decode exec resize payload: %w", err)
	}

	h.mu.Lock()
	sess, ok := h.sessions[msg.StreamID]
	h.mu.Unlock()
	if !ok {
		return fmt.Errorf("no active exec session for stream %s", msg.StreamID)
	}

	select {
	case sess.resizeC <- remotecommand.TerminalSize{
		Width:  uint16(payload.Width),
		Height: uint16(payload.Height),
	}:
	default:
		h.log.Warn("resize channel full, dropping resize event", "stream_id", msg.StreamID)
	}

	return nil
}

// CloseSession terminates an active exec session.
func (h *ExecHandler) CloseSession(streamID string) {
	h.mu.Lock()
	sess, ok := h.sessions[streamID]
	h.mu.Unlock()
	if ok {
		sess.cancel()
	}
}

// tunnelWriter relays data written to it as EXEC_OUTPUT messages.
type tunnelWriter struct {
	streamID string
	stream   string // "stdout" or "stderr"
	sendFn   func(*protocol.Message) error
}

func (w *tunnelWriter) Write(p []byte) (int, error) {
	payload, _ := json.Marshal(map[string]string{
		"stream": w.stream,
		"data":   string(p),
	})
	if err := w.sendFn(&protocol.Message{
		Type:     protocol.MsgExecOutput,
		StreamID: w.streamID,
		Payload:  payload,
	}); err != nil {
		return 0, err
	}
	return len(p), nil
}

// terminalSizeQueue implements remotecommand.TerminalSizeQueue.
type terminalSizeQueue struct {
	resizeC <-chan remotecommand.TerminalSize
	ctx     context.Context
}

func (q *terminalSizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case size, ok := <-q.resizeC:
		if !ok {
			return nil
		}
		return &size
	case <-q.ctx.Done():
		return nil
	}
}

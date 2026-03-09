package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// LogHandler streams pod logs via the tunnel.
type LogHandler struct {
	client *kubernetes.Clientset
	log    *slog.Logger
}

// NewLogHandler creates a new LogHandler.
func NewLogHandler(client *kubernetes.Clientset, log *slog.Logger) *LogHandler {
	return &LogHandler{
		client: client,
		log:    log,
	}
}

// HandleLogStart initiates log streaming for a pod/container. It decodes the
// LogStartPayload, opens a log stream, and sends each line as a LOG_DATA message.
func (h *LogHandler) HandleLogStart(ctx context.Context, msg *protocol.Message, sendFn func(*protocol.Message) error) error {
	var payload protocol.LogStartPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return fmt.Errorf("decode log start payload: %w", err)
	}

	streamID := msg.StreamID
	h.log.Info("starting log stream",
		"stream_id", streamID,
		"namespace", payload.Namespace,
		"pod", payload.Pod,
		"container", payload.Container,
		"follow", payload.Follow,
	)

	opts := &corev1.PodLogOptions{
		Container:  payload.Container,
		Follow:     payload.Follow,
		Timestamps: payload.Timestamps,
	}
	if payload.TailLines > 0 {
		lines := int64(payload.TailLines)
		opts.TailLines = &lines
	}

	req := h.client.CoreV1().Pods(payload.Namespace).GetLogs(payload.Pod, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("open log stream: %w", err)
	}

	// Stream logs in a goroutine.
	go func() {
		defer stream.Close()
		defer func() {
			endPayload, _ := json.Marshal(map[string]string{"reason": "stream_closed"})
			_ = sendFn(&protocol.Message{
				Type:     protocol.MsgLogEnd,
				StreamID: streamID,
				Payload:  endPayload,
			})
			h.log.Info("log stream ended", "stream_id", streamID)
		}()

		scanner := bufio.NewScanner(stream)
		// Allow up to 1MB lines (some log lines can be very long).
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			linePayload, _ := json.Marshal(map[string]string{
				"line": line,
			})
			if err := sendFn(&protocol.Message{
				Type:     protocol.MsgLogData,
				StreamID: streamID,
				Payload:  linePayload,
			}); err != nil {
				h.log.Error("failed to send log data", "stream_id", streamID, "error", err)
				return
			}
		}
		if err := scanner.Err(); err != nil {
			h.log.Error("log scanner error", "stream_id", streamID, "error", err)
		}
	}()

	return nil
}

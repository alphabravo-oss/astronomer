package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// WatchPods opens a Kubernetes pod watch through the agent tunnel and emits
// decoded watch events on the returned channel. It satisfies the PodWatcher
// interface used by WorkloadHandler.WatchPods.
//
// The k8s watch response is a stream of newline-delimited JSON objects, each
// {"type":"ADDED|MODIFIED|DELETED|...","object":{...}}. The agent forwards it
// as K8sStreamFrame data chunks whose boundaries do NOT align with event
// boundaries, so we reassemble lines here before decoding.
func (r *TunnelK8sRequester) WatchPods(ctx context.Context, clusterID, namespace string) (<-chan PodWatchEvent, error) {
	if r == nil || r.hub == nil {
		return nil, fmt.Errorf("tunnel requester not configured")
	}
	agent := r.hub.GetAgent(clusterID)
	if agent == nil {
		return nil, fmt.Errorf("cluster agent not connected")
	}

	path := "/api/v1/pods?watch=true"
	if namespace != "" {
		path = "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods?watch=true"
	}

	streamID := uuid.NewString()
	stream, err := agent.Streams.CreateStream(streamID)
	if err != nil {
		return nil, err
	}

	payload := protocol.K8sRequestPayload{Method: "GET", Path: path, Headers: requestHeaders("")}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		agent.Streams.CloseStream(streamID)
		return nil, err
	}
	if err := r.hub.SendToAgent(clusterID, &protocol.Message{
		Type:      protocol.MsgK8sStreamRequest,
		StreamID:  streamID,
		ClusterID: clusterID,
		Timestamp: time.Now().UTC(),
		Payload:   payloadBytes,
	}); err != nil {
		agent.Streams.CloseStream(streamID)
		return nil, err
	}

	out := make(chan PodWatchEvent, 16)
	go func() {
		defer close(out)
		defer agent.Streams.CloseStream(streamID)

		var buf bytes.Buffer
		// errStatus is set from a header frame whose StatusCode >= 400; while
		// set, the following data frames are the error body (a k8s Status JSON)
		// rather than watch events, so we surface them as a single ERROR event.
		var errStatus bool
		emitErr := func(obj json.RawMessage) bool {
			select {
			case out <- PodWatchEvent{Type: "ERROR", Object: obj}:
				return true
			case <-ctx.Done():
				return false
			}
		}
		emitLines := func() bool {
			for {
				idx := bytes.IndexByte(buf.Bytes(), '\n')
				if idx < 0 {
					return true
				}
				line := make([]byte, idx)
				copy(line, buf.Bytes()[:idx])
				buf.Next(idx + 1)
				if len(bytes.TrimSpace(line)) == 0 {
					continue
				}
				var ev PodWatchEvent
				if err := json.Unmarshal(line, &ev); err != nil {
					continue // skip malformed line, keep the watch alive
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					return false
				}
			}
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-stream.DoneCh:
				return
			case data, ok := <-stream.DataCh:
				if !ok {
					return
				}
				var frame protocol.K8sStreamFrame
				if err := json.Unmarshal(data, &frame); err != nil {
					return
				}
				switch frame.Kind {
				case protocol.K8sStreamFrameHeader:
					// The agent always sends a header frame first carrying the
					// real upstream status. A >= 400 status (e.g. 403 from an
					// RBAC-restricted token) means the data frames that follow
					// are an error body, not watch events.
					if frame.StatusCode >= 400 {
						errStatus = true
					}
				case protocol.K8sStreamFrameData:
					if frame.Body == "" {
						continue
					}
					decoded, derr := base64.StdEncoding.DecodeString(frame.Body)
					if derr != nil {
						decoded = []byte(frame.Body)
					}
					if errStatus {
						emitErr(json.RawMessage(decoded))
						return
					}
					buf.Write(decoded)
					// A server streaming a single huge unterminated line would
					// grow buf without bound (emitLines only drains on '\n'). Cap
					// the unparsed remainder: a single watch event is far smaller
					// than maxAssembledResponseBytes, so crossing it means the
					// peer is misbehaving — surface a terminal error and stop
					// rather than buffer forever.
					if buf.Len() > maxAssembledResponseBytes {
						msg, _ := json.Marshal(fmt.Sprintf("watch reassembly buffer exceeded %d-byte cap", maxAssembledResponseBytes))
						emitErr(json.RawMessage(msg))
						return
					}
					if !emitLines() {
						return
					}
				case protocol.K8sStreamFrameEnd:
					if frame.Error != "" {
						msg, _ := json.Marshal(frame.Error)
						emitErr(json.RawMessage(msg))
					}
					return
				}
			}
		}
	}()

	return out, nil
}

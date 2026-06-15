package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
)

// HelmRequester is the API-layer interface for dispatching Helm operations
// through the agent tunnel.
type HelmRequester interface {
	Do(ctx context.Context, clusterID string, msgType protocol.MessageType, payload protocol.HelmRequestPayload) (*protocol.HelmResultPayload, error)
	Status(ctx context.Context, clusterID, releaseName, namespace string) (*protocol.HelmResultPayload, error)
}

// TunnelHelmRequester implements HelmRequester by forwarding to the
// per-cluster Hub originator (Hub.SendHelmRequest), which handles correlation,
// timeouts, and stream cleanup.
//
// In multi-replica server deployments the local hub may not own the
// agent's WS. When that's the case and a PSK is configured, the requester
// reverse-proxies the helm op to whichever sibling pod owns the WS
// (looked up via the redis-backed locator), via the
// /internal/tunnel/helm/{cluster_id} endpoint on that sibling.
type TunnelHelmRequester struct {
	hub *tunnel.Hub
	// psk authenticates cross-pod calls to a sibling's internal helm
	// endpoint. Empty disables the fallback (single-replica install) —
	// the requester falls through to a local-hub call which returns
	// "cluster agent not connected" if our pod doesn't own the WS.
	psk string
}

func NewTunnelHelmRequester(hub *tunnel.Hub) *TunnelHelmRequester {
	return &TunnelHelmRequester{hub: hub}
}

// SetInternalPSK wires the shared-secret PSK used to authenticate to
// sibling pods' internal helm endpoint. Pass the same value the
// InternalHelmHandler is configured with (typically
// tunnel.DerivePSK(cfg.EncryptionKey)). Empty leaves the fallback off.
func (r *TunnelHelmRequester) SetInternalPSK(psk string) {
	if r == nil {
		return
	}
	r.psk = psk
}

func (r *TunnelHelmRequester) Do(ctx context.Context, clusterID string, msgType protocol.MessageType, payload protocol.HelmRequestPayload) (*protocol.HelmResultPayload, error) {
	if r == nil || r.hub == nil {
		return nil, fmt.Errorf("helm requester not configured")
	}

	// Local hub owns the WS: dispatch directly. Skipping the locator
	// check here also avoids forwarding when a stale entry points
	// elsewhere while our pod actually has the WS.
	if agent := r.hub.GetAgent(clusterID); agent != nil {
		return r.hub.SendHelmRequest(ctx, clusterID, msgType, payload)
	}
	// Cross-pod fallback: ok==false (no locator, no PSK, no entry, or a
	// stale self-pointer) falls through to SendHelmRequest, which
	// returns "cluster agent not connected" — same behaviour we had
	// before the fallback existed.
	if resp, ok, err := r.forwardToOwner(ctx, clusterID, msgType, payload); ok {
		return resp, err
	}
	return r.hub.SendHelmRequest(ctx, clusterID, msgType, payload)
}

func (r *TunnelHelmRequester) Status(ctx context.Context, clusterID, releaseName, namespace string) (*protocol.HelmResultPayload, error) {
	return r.Do(ctx, clusterID, protocol.MsgHelmStatus, protocol.HelmRequestPayload{
		ReleaseName: releaseName,
		Namespace:   namespace,
	})
}

// forwardToOwner POSTs the helm request to whichever sibling pod owns the
// cluster's WS. ok==false means "no forward attempted" (no locator, no
// entry, locator points back at us, or PSK unset); the caller falls back
// to the local hub path. ok==true with retErr non-nil means we forwarded
// but the sibling failed.
func (r *TunnelHelmRequester) forwardToOwner(ctx context.Context, clusterID string, msgType protocol.MessageType, payload protocol.HelmRequestPayload) (resp *protocol.HelmResultPayload, ok bool, retErr error) {
	if r == nil || r.hub == nil || r.psk == "" {
		return nil, false, nil
	}
	loc := r.hub.Locator()
	if loc == nil {
		return nil, false, nil
	}
	addr, err := loc.Lookup(ctx, clusterID)
	if err != nil || addr == "" || addr == loc.Address() {
		return nil, false, nil
	}

	body, err := json.Marshal(tunnel.InternalHelmRequest{MsgType: msgType, Payload: payload})
	if err != nil {
		return nil, true, err
	}
	target := "http://" + addr + "/internal/tunnel/helm/" + clusterID
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, true, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(tunnel.InternalPSKHeader, r.psk)
	// Defense-in-depth in-band marker proving sibling-pod origin; the
	// receiver rejects requests without it even with a valid PSK.
	req.Header.Set(tunnel.InternalSourceHeader, tunnel.InternalSourceValue)

	httpResp, err := internalHelmForwardClient.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() {
		_ = httpResp.Body.Close()
	}()
	// Helm result bodies are small (HelmResultPayload has a few strings and
	// an error string), but cap the read at 16 MiB so a misbehaving sibling
	// can't OOM us.
	respBytes, err := io.ReadAll(io.LimitReader(httpResp.Body, 16<<20))
	if err != nil {
		return nil, true, err
	}
	if httpResp.StatusCode >= 400 {
		return nil, true, fmt.Errorf("sibling internal helm endpoint %d: %s", httpResp.StatusCode, string(respBytes))
	}
	var out protocol.HelmResultPayload
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return nil, true, fmt.Errorf("decode sibling response: %w", err)
	}
	// Mirror SendHelmRequest's failure surfacing: if the agent reported
	// Success==false with an Error string, return that as the error so
	// callers don't have to re-check the embedded field.
	if !out.Success && out.Error != "" {
		return &out, true, errors.New(out.Error)
	}
	return &out, true, nil
}

// internalHelmForwardClient is the HTTP client for cross-pod helm forwarding.
// Helm operations can take 10+ minutes (kube-prom-stack install with --wait),
// so there's no global timeout — the per-request context governs lifetime.
// ResponseHeaderTimeout is generous for the same reason: the sibling may not
// flush headers until the agent's first response frame arrives.
var internalHelmForwardClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:          16,
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: 11 * time.Minute,
	},
}

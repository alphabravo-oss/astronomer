package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultSSOBackchannelClient is the production SSOBackchannelClient
// used by admin force-logout to fire upstream OIDC end-session POSTs.
//
// OIDC RP-initiated logout supports two transports for the id_token_hint:
//
//  1. GET redirect — the Logout handler uses this for the user-driven
//     flow because the browser is right there and can follow it. We
//     construct the URL with id_token_hint in the query string.
//
//  2. POST form-encoded — what we use here, because there is no
//     browser in the admin force-logout flow. The id_token sits in
//     the POST body so it doesn't end up in upstream access logs.
//
// Some IdPs only support one or the other; some support neither. We
// pick the POST shape because it's the right transport for a
// back-channel call, and we return a structured error on non-2xx so
// the caller can record the upstream rejection in the audit row /
// metric.
type defaultSSOBackchannelClient struct {
	client *http.Client
}

// NewDefaultSSOBackchannelClient builds the production backchannel
// client with a sensible 10s timeout. Long enough for a sluggish
// upstream IdP, short enough that a force-logout for a user with a
// dozen sessions doesn't tie up a worker for minutes.
func NewDefaultSSOBackchannelClient() SSOBackchannelClient {
	return &defaultSSOBackchannelClient{
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// PostEndSession fires a POST against the IdP's end_session_endpoint
// with the form-encoded id_token_hint in the body. Returns the
// rejection error verbatim — every error path is best-effort from
// the caller's perspective (audit + metric, no 5xx).
func (c *defaultSSOBackchannelClient) PostEndSession(ctx context.Context, endpoint, idTokenHint string) error {
	if endpoint == "" {
		return fmt.Errorf("backchannel logout: empty endpoint")
	}
	if idTokenHint == "" {
		return fmt.Errorf("backchannel logout: empty id_token_hint")
	}
	form := url.Values{}
	form.Set("id_token_hint", idTokenHint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("backchannel logout: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json, text/plain")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("backchannel logout: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	// Drain the body so the conn can be reused; cap so a 1GB
	// response from a misconfigured IdP can't OOM us.
	_, _ = io.CopyN(io.Discard, resp.Body, 1<<20)

	// Most IdPs return 200 or 204 on a successful end-session POST.
	// Dex returns a 303 redirect when post_logout_redirect_uri is
	// supplied (which we deliberately don't supply here — there's
	// no browser to redirect). Treat 2xx and 303 as success; the
	// rest is a soft failure that the caller surfaces as a metric.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode == http.StatusSeeOther || resp.StatusCode == http.StatusFound {
		return nil
	}
	return fmt.Errorf("backchannel logout: %s returned %d", endpoint, resp.StatusCode)
}

package tunnel

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"

	"github.com/alphabravocompany/astronomer-go/internal/callerid"
	"github.com/alphabravocompany/astronomer-go/pkg/protocol"
	"github.com/alphabravocompany/astronomer-go/pkg/proxyhdr"
)

// buildK8sRequestPayload constructs a K8sRequestPayload from an HTTP request.
func buildK8sRequestPayload(r *http.Request) (*protocol.K8sRequestPayload, error) {
	// Authorization, auditing, and forwarding consume the same validated
	// path. The route middleware stores it in context; direct callers run the
	// same strict validator here.
	path, err := CanonicalK8sProxyPath(r)
	if err != nil {
		return nil, err
	}

	// Include query string if present.
	if r.URL.RawQuery != "" {
		path = path + "?" + r.URL.RawQuery
	}

	// Forward only the small allowlist of headers that the kubernetes API
	// actually needs (see proxyhdr.ShouldForwardRequestHeader). Everything
	// else is dropped — the allowlist fails closed against header-spoofing,
	// including Authorization (caller's Astronomer JWT, not a k8s bearer),
	// Cookie/Host/X-Forwarded-*, user-controlled Impersonate-* headers, and
	// the front-proxy identity headers X-Remote-User/X-Remote-Group/
	// X-Remote-Extra-* honored by clusters using --requestheader auth.
	headers := make(map[string]string)
	for key, values := range r.Header {
		if len(values) == 0 {
			continue
		}
		if !proxyhdr.ShouldForwardRequestHeader(key) {
			continue
		}
		headers[key] = values[0]
	}

	// Read and base64-encode the body, capped so a huge payload can't OOM the
	// shared replica. Read one byte past the limit to distinguish "exactly at
	// the cap" from "over".
	var body string
	if r.Body != nil {
		bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, k8sProxyMaxBodyBytes+1))
		if err != nil {
			return nil, fmt.Errorf("reading request body: %w", err)
		}
		if int64(len(bodyBytes)) > k8sProxyMaxBodyBytes {
			return nil, errRequestBodyTooLarge
		}
		if len(bodyBytes) > 0 {
			body = base64.StdEncoding.EncodeToString(bodyBytes)
		}
	}

	return &protocol.K8sRequestPayload{
		Method:  r.Method,
		Path:    path,
		Headers: headers,
		Body:    body,
		// Typed caller identity, resolved from the authenticated session (or
		// from a positive machine marker stamped by a trusted gate — never
		// from a header. Note this runs AFTER the allowlist loop above, which
		// has already dropped any Impersonate-* / X-Remote-* the client sent, so
		// there is no path by which a caller-supplied header can influence it.
		//
		// PHASE 0: populated, unused.
		CallerIdentity: callerid.Resolve(r.Context()),
	}, nil
}

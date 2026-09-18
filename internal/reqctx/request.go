package reqctx

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type requestMetadataKey struct{}

type requestMetadata struct {
	requestID          string
	requestIDGenerated bool
	forwardedProto     string
	forwardedHost      string
	totpEnrollOnly     bool
	secureCookies      bool
}

func metadata(ctx context.Context) requestMetadata {
	if ctx == nil {
		return requestMetadata{}
	}
	value, _ := ctx.Value(requestMetadataKey{}).(requestMetadata)
	return value
}

func withMetadata(ctx context.Context, update func(*requestMetadata)) context.Context {
	value := metadata(ctx)
	update(&value)
	return context.WithValue(ctx, requestMetadataKey{}, value)
}

// WithRequestID records the request identifier and whether the server minted
// it. Client-supplied identifiers are safe for logs and audit correlation but
// are never returned by GeneratedRequestID.
func WithRequestID(ctx context.Context, id string, generated bool) context.Context {
	return withMetadata(ctx, func(value *requestMetadata) {
		value.requestID = id
		value.requestIDGenerated = generated
	})
}

func RequestID(ctx context.Context) string { return metadata(ctx).requestID }

func CorrelationID(ctx context.Context) string { return RequestID(ctx) }

func GeneratedRequestID(ctx context.Context) string {
	value := metadata(ctx)
	if !value.requestIDGenerated {
		return ""
	}
	return value.requestID
}

// WithTrustedForwarding stores forwarding values only after the caller has
// authenticated the socket peer against the configured trusted-proxy CIDRs.
func WithTrustedForwarding(ctx context.Context, proto, host string) context.Context {
	return withMetadata(ctx, func(value *requestMetadata) {
		value.forwardedProto = strings.ToLower(strings.TrimSpace(proto))
		value.forwardedHost = strings.TrimSpace(host)
	})
}

// ClientIP returns the canonical client address established by the trusted
// proxy middleware. It never reads request headers.
func ClientIP(request *http.Request) *netip.Addr {
	if request == nil {
		return nil
	}
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	address, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return nil
	}
	address = address.Unmap()
	return &address
}

func RequestHost(request *http.Request) string {
	if request == nil {
		return ""
	}
	if host := metadata(request.Context()).forwardedHost; host != "" {
		return host
	}
	return request.Host
}

func RequestIsHTTPS(request *http.Request) bool {
	if request == nil {
		return false
	}
	value := metadata(request.Context())
	return request.TLS != nil || value.forwardedProto == "https" || value.secureCookies
}

// WithSecureCookiesRequired forces Secure session cookies even when TLS is
// terminated before the request reaches the server.
func WithSecureCookiesRequired(ctx context.Context, required bool) context.Context {
	return withMetadata(ctx, func(value *requestMetadata) { value.secureCookies = required })
}

func WithTOTPEnrollOnly(ctx context.Context) context.Context {
	return withMetadata(ctx, func(value *requestMetadata) { value.totpEnrollOnly = true })
}

func IsTOTPEnrollOnly(ctx context.Context) bool { return metadata(ctx).totpEnrollOnly }

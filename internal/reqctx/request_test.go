package reqctx

import (
	"context"
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

func TestRequestMetadata(t *testing.T) {
	ctx := WithRequestID(context.Background(), "caller-id", false)
	if RequestID(ctx) != "caller-id" || CorrelationID(ctx) != "caller-id" {
		t.Fatal("shared request identity was not retained")
	}
	if GeneratedRequestID(ctx) != "" {
		t.Fatal("caller-supplied request identity was treated as generated")
	}
	ctx = WithRequestID(ctx, "server-id", true)
	ctx = WithTOTPEnrollOnly(ctx)
	if GeneratedRequestID(ctx) != "server-id" || !IsTOTPEnrollOnly(ctx) {
		t.Fatal("server provenance or TOTP enrollment state was not retained")
	}
}

func TestTrustedRequestMetadata(t *testing.T) {
	request := httptest.NewRequest("GET", "http://internal/", nil)
	request.RemoteAddr = "[::ffff:203.0.113.8]:443"
	request.Host = "internal"
	request = request.WithContext(WithTrustedForwarding(request.Context(), "HTTPS", "astronomer.example"))
	if address := ClientIP(request); address == nil || address.String() != "203.0.113.8" {
		t.Fatalf("ClientIP = %v", address)
	}
	if !RequestIsHTTPS(request) || RequestHost(request) != "astronomer.example" {
		t.Fatal("trusted forwarding metadata was not applied")
	}

	direct := httptest.NewRequest("GET", "https://direct.example/", nil)
	direct.TLS = &tls.ConnectionState{}
	if !RequestIsHTTPS(direct) || RequestHost(direct) != "direct.example" {
		t.Fatal("direct TLS request metadata was not preserved")
	}
}

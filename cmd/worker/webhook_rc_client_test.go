package main

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/config"
)

func TestWebhookHTTPClientPrivateReceiverIsDevelopmentRCOnly(t *testing.T) {
	if !allowPrivateRCWebhooks(&config.Config{Env: "development", RCAllowPrivateWebhooks: true}) {
		t.Fatal("development RC with explicit opt-in must allow the private proof receiver")
	}
	if allowPrivateRCWebhooks(&config.Config{Env: "production"}) {
		t.Fatal("production must ignore the RC private-receiver opt-in")
	}
	if allowPrivateRCWebhooks(&config.Config{Env: "development"}) {
		t.Fatal("development without explicit RC opt-in must retain the public-only guard")
	}
}

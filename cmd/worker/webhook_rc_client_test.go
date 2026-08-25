package main

import (
	"testing"

	"github.com/alphabravocompany/astronomer-go/internal/config"
)

func TestWebhookHTTPClientPrivateReceiverIsDevelopmentRCOnly(t *testing.T) {
	t.Setenv("ASTRONOMER_RC_ALLOW_PRIVATE_WEBHOOKS", "true")
	if !allowPrivateRCWebhooks(&config.Config{Env: "development"}) {
		t.Fatal("development RC with explicit opt-in must allow the private proof receiver")
	}
	if allowPrivateRCWebhooks(&config.Config{Env: "production"}) {
		t.Fatal("production must ignore the RC private-receiver opt-in")
	}
	t.Setenv("ASTRONOMER_RC_ALLOW_PRIVATE_WEBHOOKS", "false")
	if allowPrivateRCWebhooks(&config.Config{Env: "development"}) {
		t.Fatal("development without explicit RC opt-in must retain the public-only guard")
	}
}

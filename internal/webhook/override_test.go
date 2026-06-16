package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestWebhookDispatch_UsesOverride proves the override path in
// buildBody: with no subscription template, the notify override
// closure's body wins over the JSON-marshal default.
func TestWebhookDispatch_UsesOverride(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewSender(srv.Client())
	s.SetOverrideLookup(func(_ context.Context, key string) (string, bool) {
		if key == "webhook.audit.event" {
			return `{"override":true,"name":"{{ .event_name }}"}`, true
		}
		return "", false
	})

	outcome, _, err := s.Send(context.Background(),
		Subscription{URL: srv.URL, Secret: "k"},
		Event{EventName: "audit.user.login", Timestamp: time.Now()},
	)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !outcome.IsSuccess() {
		t.Fatalf("expected success, got %+v", outcome)
	}
	if !strings.Contains(string(gotBody), `"override":true`) {
		t.Errorf("override body not used; got: %s", gotBody)
	}
}

// TestWebhookDispatch_FallsBackToJSONWhenNoOverride proves the
// byte-identical fallback: with no override and no subscription
// template, the body is the canonical json.Marshal(event) output.
func TestWebhookDispatch_FallsBackToJSONWhenNoOverride(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewSender(srv.Client())
	// No override lookup wired — behaviour should be identical to
	// pre-migration.

	ev := Event{EventName: "audit.user.login", Timestamp: time.Unix(1700000000, 0).UTC()}
	if _, _, err := s.Send(context.Background(), Subscription{URL: srv.URL, Secret: "k"}, ev); err != nil {
		t.Fatalf("Send: %v", err)
	}
	want, _ := json.Marshal(ev)
	if string(gotBody) != string(want) {
		t.Errorf("body diverged from json.Marshal(event):\n got: %s\nwant: %s", gotBody, want)
	}
}

// TestWebhookDispatch_SubscriptionTemplateBeatsOverride proves the
// override does NOT clobber a subscription-supplied template (which
// is the highest-precedence layer).
func TestWebhookDispatch_SubscriptionTemplateBeatsOverride(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewSender(srv.Client())
	s.SetOverrideLookup(func(_ context.Context, _ string) (string, bool) {
		return `{"from":"override"}`, true
	})

	_, _, err := s.Send(context.Background(),
		Subscription{URL: srv.URL, Secret: "k", PayloadTemplate: `{"from":"subscription"}`},
		Event{EventName: "audit.user.login", Timestamp: time.Now()},
	)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if string(gotBody) != `{"from":"subscription"}` {
		t.Errorf("subscription template should win over override; got %s", gotBody)
	}
}

// TestOverrideKeyForEvent maps the well-known event prefixes to
// registry keys. Lock the precedence table.
func TestOverrideKeyForEvent(t *testing.T) {
	cases := map[string]string{
		"audit.user.login":                          "webhook.audit.event",
		"audit.admin.webhook.create":                "webhook.audit.event",
		"cluster.connected":                         "webhook.cluster.connected",
		"cluster.disconnected":                      "webhook.cluster.disconnected",
		"cluster.status_changed":                    "webhook.cluster.status_changed",
		"cluster.created":                           "webhook.cluster.created",
		"cluster.updated":                           "webhook.cluster.updated",
		"cluster.deleted":                           "webhook.cluster.deleted",
		"cluster.decommission.cleanup_managed_side": "webhook.cluster.decommissioned",
		"alert.fired":                               "webhook.alert.fired",
		"alert.resolved":                            "webhook.alert.resolved",
		"unknown.event":                             "",
	}
	for ev, want := range cases {
		got := overrideKeyForEvent(ev)
		if got != want {
			t.Errorf("overrideKeyForEvent(%q) = %q, want %q", ev, got, want)
		}
	}
}

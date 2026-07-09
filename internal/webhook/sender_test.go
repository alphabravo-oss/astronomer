package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSender_PostsSignedPayload(t *testing.T) {
	const secret = "topsecret"
	var (
		gotSig         string
		gotEvent       string
		gotBody        []byte
		gotContentType string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get(SignatureHeader)
		gotEvent = r.Header.Get(EventNameHeader)
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := NewSender(srv.Client())
	event := Event{
		EventName: "audit.user.login",
		EventID:   "evt-42",
		Timestamp: time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC),
		Detail:    json.RawMessage(`{"user":"alice"}`),
	}
	outcome, _, err := s.Send(context.Background(), Subscription{
		URL:    srv.URL,
		Secret: secret,
	}, event)
	if err != nil {
		t.Fatalf("Send returned err: %v", err)
	}
	if !outcome.IsSuccess() {
		t.Fatalf("expected success outcome, got status=%d err=%v", outcome.Status, outcome.Err)
	}

	// Verify the HMAC matches the body we received. This is the same
	// transform the receiver-side recipe in the README documents.
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(gotBody)
	want := "sha256=" + hex.EncodeToString(h.Sum(nil))
	if gotSig != want {
		t.Errorf("signature mismatch:\n want %s\n  got %s", want, gotSig)
	}
	if gotEvent != "audit.user.login" {
		t.Errorf("expected X-Astronomer-Event header, got %q", gotEvent)
	}
	if gotContentType != "application/json" {
		t.Errorf("expected default Content-Type application/json, got %q", gotContentType)
	}
	// Default (no template) ships the event verbatim.
	if !strings.Contains(string(gotBody), `"event_name":"audit.user.login"`) {
		t.Errorf("body missing event_name field: %s", gotBody)
	}
	if !strings.Contains(string(gotBody), `"event_id":"evt-42"`) {
		t.Errorf("body missing event_id field: %s", gotBody)
	}
}

func TestSender_AppliesTemplate(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewSender(srv.Client())
	const slackTpl = `{"text":"{{ .event_name }}: {{ .detail.user }}"}`
	event := Event{
		EventName: "auth.login_failed",
		Timestamp: time.Now().UTC(),
		Detail:    json.RawMessage(`{"user":"bob"}`),
	}
	outcome, _, err := s.Send(context.Background(), Subscription{
		URL:             srv.URL,
		Secret:          "k",
		PayloadTemplate: slackTpl,
	}, event)
	if err != nil {
		t.Fatalf("Send err: %v", err)
	}
	if !outcome.IsSuccess() {
		t.Fatalf("expected success, got status=%d", outcome.Status)
	}
	if string(gotBody) != `{"text":"auth.login_failed: bob"}` {
		t.Errorf("template body mismatch: %s", gotBody)
	}
}

func TestSender_RespectsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewSender(srv.Client())
	start := time.Now()
	outcome, _, _ := s.Send(context.Background(), Subscription{
		URL:            srv.URL,
		Secret:         "k",
		TimeoutSeconds: 1, // 1s wall clock; the server sleeps 500ms so completes in time
	}, Event{EventName: "x"})
	if outcome.Status != http.StatusOK {
		// On a slow CI box this might miss; the assertion below is the
		// load-bearing one — timeout actually triggers.
		t.Logf("ok-path elapsed=%v status=%d", time.Since(start), outcome.Status)
	}

	// Now a request that takes 2s with a 1s timeout MUST fail with a
	// transport error (context deadline exceeded surfaced through
	// http.Client). We use a small inner sleep to keep CI snappy.
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(3 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer slowSrv.Close()

	start = time.Now()
	outcome, _, _ = s.Send(context.Background(), Subscription{
		URL:            slowSrv.URL,
		Secret:         "k",
		TimeoutSeconds: 1,
	}, Event{EventName: "x"})
	elapsed := time.Since(start)
	if outcome.Err == nil {
		t.Errorf("expected transport error on timed-out POST, got status=%d", outcome.Status)
	}
	if elapsed > 2*time.Second {
		t.Errorf("Send did not honor the 1s timeout; elapsed=%v", elapsed)
	}
}

func TestSender_RetriesOn5xx_NotOn4xx(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		wantSuccess bool
		wantRetry   bool
	}{
		{"2xx — delivered, no retry", http.StatusOK, true, false},
		{"204 — delivered, no retry", http.StatusNoContent, true, false},
		{"400 — operator error, do not retry", http.StatusBadRequest, false, false},
		{"404 — bad URL, do not retry", http.StatusNotFound, false, false},
		{"500 — transient, retry", http.StatusInternalServerError, false, true},
		{"502 — bad gateway, retry", http.StatusBadGateway, false, true},
		{"503 — overloaded, retry", http.StatusServiceUnavailable, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			s := NewSender(srv.Client())
			outcome, _, err := s.Send(context.Background(), Subscription{URL: srv.URL, Secret: "k"}, Event{EventName: "x"})
			if err != nil {
				t.Fatalf("Send returned err: %v", err)
			}
			if outcome.IsSuccess() != tc.wantSuccess {
				t.Errorf("IsSuccess(): got %v, want %v (status=%d)", outcome.IsSuccess(), tc.wantSuccess, outcome.Status)
			}
			if outcome.IsRetryable() != tc.wantRetry {
				t.Errorf("IsRetryable(): got %v, want %v (status=%d)", outcome.IsRetryable(), tc.wantRetry, outcome.Status)
			}
		})
	}

	// Transport error always retries.
	s := NewSender(&errClient{err: errors.New("dial tcp: no such host")})
	outcome, _, _ := s.Send(context.Background(), Subscription{URL: "http://invalid.invalid", Secret: "k"}, Event{EventName: "x"})
	if outcome.Err == nil {
		t.Errorf("expected transport error, got status=%d", outcome.Status)
	}
	if !outcome.IsRetryable() {
		t.Errorf("transport error must be retryable")
	}
}

func TestSender_TruncatesResponseBody(t *testing.T) {
	huge := strings.Repeat("X", MaxResponseBodyBytes+1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(huge))
	}))
	defer srv.Close()
	s := NewSender(srv.Client())
	outcome, _, err := s.Send(context.Background(), Subscription{URL: srv.URL, Secret: "k"}, Event{EventName: "x"})
	if err != nil {
		t.Fatalf("Send err: %v", err)
	}
	if len(outcome.ResponseBody) > MaxResponseBodyBytes+32 {
		t.Errorf("expected response_body truncated near %d, got %d", MaxResponseBodyBytes, len(outcome.ResponseBody))
	}
	if !strings.Contains(outcome.ResponseBody, "[truncated]") {
		t.Errorf("expected truncation marker on overlarge body")
	}
}

func TestSender_RejectsOversizedPayload(t *testing.T) {
	// A template producing > 1 MiB of output must be rejected.
	huge := strings.Repeat("Y", MaxPayloadBytes+16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s := NewSender(srv.Client())
	outcome, _, err := s.Send(context.Background(), Subscription{
		URL:             srv.URL,
		Secret:          "k",
		PayloadTemplate: `{"x":"` + huge + `"}`,
	}, Event{EventName: "x"})
	if err == nil {
		t.Fatalf("expected oversized-payload error, got success: %+v", outcome)
	}
}

func TestSender_AppliesExtraHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s := NewSender(srv.Client())
	_, _, _ = s.Send(context.Background(), Subscription{
		URL:    srv.URL,
		Secret: "k",
		ExtraHeaders: map[string]string{
			"X-API-Key":     "abc123",
			"Content-Type":  "application/vnd.custom+json",
			SignatureHeader: "spoofed",
		},
	}, Event{EventName: "x"})
	if got.Get("X-Api-Key") != "abc123" {
		t.Errorf("X-API-Key not forwarded: %q", got.Get("X-Api-Key"))
	}
	if got.Get("Content-Type") != "application/vnd.custom+json" {
		t.Errorf("Content-Type override not honored: %q", got.Get("Content-Type"))
	}
	// The signature header must not be overridable by extra_headers.
	if strings.Contains(got.Get(SignatureHeader), "spoofed") {
		t.Errorf("extra_headers must NOT override the HMAC signature: %q", got.Get(SignatureHeader))
	}
}

func TestNextBackoff_MatchesSchedule(t *testing.T) {
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 30 * time.Second}, // never-attempted maps to first slot
		{1, 30 * time.Second}, // first retry
		{2, 2 * time.Minute},  // second retry
		{3, 10 * time.Minute}, // third retry
		{4, 1 * time.Hour},    // fourth retry
		{5, 6 * time.Hour},    // fifth retry
		{6, 6 * time.Hour},    // past the schedule clamps to the last slot
		{100, 6 * time.Hour},
	}
	for _, tc := range cases {
		got := NextBackoff(tc.attempts)
		if got != tc.want {
			t.Errorf("NextBackoff(%d) = %v; want %v", tc.attempts, got, tc.want)
		}
	}
}

// errClient is a deterministic transport error stub.
type errClient struct{ err error }

func (e *errClient) Do(req *http.Request) (*http.Response, error) { return nil, e.err }

// SEC-R02: NewSender(nil) must ship a dial-guarded SafeClient so loopback
// destinations are refused when the SSRF guard is enabled.
func TestNewSender_NilUsesSafeClient(t *testing.T) {
	s := NewSender(nil)
	if s == nil || s.client == nil {
		t.Fatal("NewSender(nil) must install a non-nil client")
	}
	client, ok := s.client.(*http.Client)
	if !ok {
		t.Fatalf("default client type = %T, want *http.Client", s.client)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client.Timeout = 2 * time.Second
	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("default NewSender client dialed loopback; want SafeClient block")
	}
}

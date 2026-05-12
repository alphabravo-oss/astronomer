package tunnel

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTranslateLogLine exercises the agent → frontend payload translation in
// HandleLogs. The agent sends `{"line":"..."}` envelopes; the frontend expects
// a PodLog `{timestamp, message, container}`. When the agent line carries a
// kubelet RFC3339Nano timestamp prefix (which we always request via
// timestamps=true), the translator must split it out so the UI can show per-
// line times rather than the moment the line arrived at the server.
func TestTranslateLogLine(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantMessage string
		// wantTimestampPrefix is empty when we don't care about the value
		// (fallback to time.Now()) and we only assert the message.
		wantTimestampPrefix string
	}{
		{
			name:                "timestamped line",
			raw:                 `{"line":"2026-05-11T19:30:00.123456789Z hello world"}`,
			wantMessage:         "hello world",
			wantTimestampPrefix: "2026-05-11T19:30:00",
		},
		{
			name:        "untimestamped line falls back to now",
			raw:         `{"line":"plain log content"}`,
			wantMessage: "plain log content",
		},
		{
			name:        "empty line",
			raw:         `{"line":""}`,
			wantMessage: "",
		},
		{
			name:        "non-envelope payload passes through as message",
			raw:         `not json`,
			wantMessage: "not json",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := translateLogLine([]byte(tc.raw), "server")
			var got struct {
				Timestamp string `json:"timestamp"`
				Message   string `json:"message"`
				Container string `json:"container"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("translateLogLine produced invalid JSON: %v (%s)", err, out)
			}
			if got.Message != tc.wantMessage {
				t.Errorf("message: got %q want %q", got.Message, tc.wantMessage)
			}
			if got.Container != "server" {
				t.Errorf("container: got %q want %q", got.Container, "server")
			}
			if tc.wantTimestampPrefix != "" && !strings.HasPrefix(got.Timestamp, tc.wantTimestampPrefix) {
				t.Errorf("timestamp: got %q want prefix %q", got.Timestamp, tc.wantTimestampPrefix)
			}
			if got.Timestamp == "" {
				t.Errorf("timestamp should never be empty")
			}
		})
	}
}

// Note: the small `bearerFromHeader` helper that used to live in this package
// was extracted to internal/auth as auth.BearerFromHeader so the three
// long-lived stream endpoints (WS logs, WS exec, SSE events) share a single
// implementation. The unit test for the helper now lives next to it in
// internal/auth/streamauth_test.go.

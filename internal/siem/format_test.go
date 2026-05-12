package siem

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fixedTime is the timestamp every format test uses. Pinned so a
// future TZ-handling tweak surfaces in the golden output rather than
// hiding behind time.Now.
var fixedTime = time.Date(2026, 5, 12, 13, 55, 22, 0, time.UTC)

func auditEvent() SIEMEvent {
	return SIEMEvent{
		EventName:    "audit.cluster.delete",
		EventID:      "audit-1234",
		Timestamp:    fixedTime,
		Severity:     "warning",
		Hostname:     "astro-1",
		ActorUserID:  "user-abc",
		ResourceID:   "cluster-xyz",
		ResourceType: "cluster",
		Message:      "cluster decommissioned",
		Detail:       json.RawMessage(`{"action":"cluster.delete","correlation_id":"c-1"}`),
	}
}

func TestFormat_RFC5424_AuditEvent(t *testing.T) {
	out := string(FormatRFC5424(auditEvent()))
	wants := []string{
		// PRI = facility 1 << 3 | severity 4 (warning) = 12. So <12>.
		"<12>1 2026-05-12T13:55:22Z astro-1 astronomer - audit-1234",
		// STRUCTURED-DATA carries the audit fields.
		`[astronomer@1`,
		`event="audit.cluster.delete"`,
		`user_id="user-abc"`,
		`resource_id="cluster-xyz"`,
		`resource_type="cluster"`,
		`severity="warning"`,
		// The free-text MSG holds the human message.
		"cluster decommissioned",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("rfc5424 output missing %q\nfull output: %s", w, out)
		}
	}
}

func TestFormat_RFC5424_EscapesQuotesAndBackslashes(t *testing.T) {
	ev := auditEvent()
	ev.Message = `payload has " quote and ] bracket`
	ev.ActorUserID = `user"id\here`
	out := string(FormatRFC5424(ev))
	if !strings.Contains(out, `user_id="user\"id\\here"`) {
		t.Errorf("rfc5424 escape failed for ActorUserID: %s", out)
	}
}

func TestFormat_RFC3164_AuditEvent(t *testing.T) {
	out := string(FormatRFC3164(auditEvent()))
	wants := []string{
		// PRI same as RFC 5424.
		"<12>",
		// BSD-syslog date: "Mmm _d HH:MM:SS"
		"May 12 13:55:22",
		"astro-1",
		// Tag includes the event name (truncated to fit RFC 3164's 32-char cap)
		"astronomer/audit.cluster.delete",
		"user=user-abc",
		"resource=cluster-xyz",
		"cluster decommissioned",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("rfc3164 output missing %q\nfull output: %s", w, out)
		}
	}
}

func TestFormat_CEF_LoginFailed(t *testing.T) {
	ev := SIEMEvent{
		EventName:    "audit.auth.login_failed",
		EventID:      "evt-99",
		Timestamp:    fixedTime,
		Severity:     "error",
		ActorUserID:  "alice@example.com",
		ResourceType: "user",
		ResourceID:   "alice@example.com",
		Message:      "login rejected: bad password",
		Detail:       json.RawMessage(`{"reason":"bad_password"}`),
	}
	out := string(FormatCEF(ev))
	// CEF header is the 7 pipe-delimited prefix before the extension k=v
	// pairs start. Splitting on '|' gives 8 fields when we include the
	// trailing empty before the extensions; the prefix carries 7 pipes.
	headerEnd := strings.Index(out, "|3|") // severity=3 marks end of header
	if headerEnd < 0 || strings.Count(out[:headerEnd+3], "|") != 7 {
		t.Errorf("CEF header should have 7 pipes (8 fields): %s", out)
	}
	wants := []string{
		"CEF:0|AlphaBravo|Astronomer|1.0|audit.auth.login_failed|login rejected: bad password|3|",
		"rt=1778594122000", // ms since epoch for 2026-05-12T13:55:22Z
		"suser=alice@example.com",
		"cs1Label=resource_type cs1=user",
		"cs2Label=resource_id cs2=alice@example.com",
		"externalId=evt-99",
		`msg={"reason":"bad_password"}`,
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("CEF output missing %q\nfull output: %s", w, out)
		}
	}
}

func TestFormat_CEF_EscapesPipeAndEquals(t *testing.T) {
	ev := auditEvent()
	ev.Message = `bad|name=evil`
	out := string(FormatCEF(ev))
	// Header field pipes must be escaped.
	if !strings.Contains(out, `bad\|name=evil`) {
		t.Errorf("CEF header pipe not escaped: %s", out)
	}
}

func TestFormat_NDJSON_ClusterDecommission(t *testing.T) {
	out := FormatNDJSON(auditEvent())
	if len(out) == 0 || out[len(out)-1] != '\n' {
		t.Fatalf("NDJSON output must terminate with newline; got: %q", out)
	}
	// Round-trip the JSON to confirm key set + key naming.
	var decoded map[string]any
	if err := json.Unmarshal(out[:len(out)-1], &decoded); err != nil {
		t.Fatalf("NDJSON output is not valid JSON: %v\noutput: %s", err, out)
	}
	for _, key := range []string{
		"event_name", "event_id", "timestamp", "severity",
		"hostname", "actor_user_id", "resource_id", "resource_type",
		"message", "detail",
	} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("NDJSON missing key %q (full: %v)", key, decoded)
		}
	}
	if decoded["event_name"] != "audit.cluster.delete" {
		t.Errorf("NDJSON event_name = %v, want audit.cluster.delete", decoded["event_name"])
	}
	if decoded["timestamp"] != "2026-05-12T13:55:22Z" {
		t.Errorf("NDJSON timestamp = %v, want 2026-05-12T13:55:22Z", decoded["timestamp"])
	}
}

func TestFormat_NDJSON_DefaultSeverityIsInfo(t *testing.T) {
	ev := SIEMEvent{
		EventName: "cluster.connected",
		Timestamp: fixedTime,
	}
	out := FormatNDJSON(ev)
	var decoded map[string]any
	if err := json.Unmarshal(out[:len(out)-1], &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["severity"] != "info" {
		t.Errorf("default severity should be info; got %v", decoded["severity"])
	}
}

func TestFormat_ForID_DispatchesCorrectly(t *testing.T) {
	ev := auditEvent()
	cases := []struct {
		id      string
		prefix  []byte
		isJSON  bool
	}{
		{FormatRFC5424ID, []byte("<"), false},
		{FormatRFC3164ID, []byte("<"), false},
		{FormatCEFID, []byte("CEF:"), false},
		{FormatNDJSONID, []byte("{"), true},
		{"", []byte("<"), false}, // unknown → rfc5424
	}
	for _, c := range cases {
		got := FormatForID(c.id, ev)
		if len(got) == 0 {
			t.Fatalf("FormatForID(%q) returned empty", c.id)
		}
		if c.isJSON && got[0] != '{' {
			t.Errorf("FormatForID(%q) JSON output should start with {: %q", c.id, got[:1])
		}
		if !c.isJSON && got[0] != c.prefix[0] {
			t.Errorf("FormatForID(%q) should start with %q: got %q", c.id, c.prefix, got[:1])
		}
	}
}

func TestFormat_DefaultFormatForTransport(t *testing.T) {
	cases := map[string]string{
		TransportSyslogUDP:   FormatRFC5424ID,
		TransportSyslogTCP:   FormatRFC5424ID,
		TransportSyslogTLS:   FormatRFC5424ID,
		TransportSplunkHEC:   FormatNDJSONID,
		TransportNDJSONHTTPS: FormatNDJSONID,
		"unknown":            FormatRFC5424ID,
	}
	for transport, want := range cases {
		if got := DefaultFormatForTransport(transport); got != want {
			t.Errorf("DefaultFormatForTransport(%q) = %q, want %q", transport, got, want)
		}
	}
}

func TestFormat_TruncationCapAndMarker(t *testing.T) {
	// Build a giant message that forces truncation.
	huge := strings.Repeat("A", MaxSingleEventBytes*2)
	ev := SIEMEvent{
		EventName: "audit.test",
		Timestamp: fixedTime,
		Message:   huge,
	}
	out := FormatRFC5424(ev)
	if len(out) > MaxSingleEventBytes {
		t.Fatalf("truncate cap not honored: len=%d > cap %d", len(out), MaxSingleEventBytes)
	}
	if !strings.HasSuffix(string(out), "[truncated]") {
		t.Errorf("truncation marker missing: tail=%q", string(out[len(out)-32:]))
	}
}

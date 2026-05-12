package siem

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Format identifiers stored in siem_forwarders.format. The empty string
// means "auto-derive from transport" (syslog_* → rfc5424, splunk_hec /
// ndjson_https → ndjson, anywhere else → rfc5424 as the safest default).
const (
	FormatRFC5424ID = "rfc5424"
	FormatRFC3164ID = "rfc3164"
	FormatCEFID     = "cef"
	FormatNDJSONID  = "ndjson"
)

// AppName is the syslog APP-NAME / CEF Device Product field. Hard-coded
// rather than tunable because every operator-deployed receiver expects
// to filter on "astronomer" — making it configurable would invite
// fingerprint drift across an org's pipelines.
const AppName = "astronomer"

// Vendor / Product / Version surfaces for CEF. The version field is
// pinned to a constant so a CEF parser doesn't need to handle every
// patch-release variant in its dictionary; CEF parsers tend to key on
// vendor+product+sigID so the version churn is largely cosmetic.
const (
	CEFVendor  = "AlphaBravo"
	CEFProduct = "Astronomer"
	CEFVersion = "1.0"
)

// MaxSingleEventBytes is the per-event truncation cap. Modern syslogd
// implementations handle 64 KiB but operators using older relays will
// see drops past ~8 KiB; 60K is a permissive default that still keeps
// us well under the practical max. The format functions truncate the
// rendered tail and append "[truncated]" so the lossiness is visible.
const MaxSingleEventBytes = 60 * 1024

// SIEMEvent is the input each formatter consumes. The fields mirror the
// JSONB column the bus tap writes onto siem_forward_queue.payload so
// the dispatcher can rehydrate without an extra type conversion.
type SIEMEvent struct {
	EventName    string          `json:"event_name"`
	EventID      string          `json:"event_id,omitempty"`
	Timestamp    time.Time       `json:"timestamp"`
	Severity     string          `json:"severity,omitempty"`
	Hostname     string          `json:"hostname,omitempty"`
	ActorUserID  string          `json:"actor_user_id,omitempty"`
	ResourceID   string          `json:"resource_id,omitempty"`
	ResourceType string          `json:"resource_type,omitempty"`
	Message      string          `json:"message,omitempty"`
	Detail       json.RawMessage `json:"detail,omitempty"`
}

// severityToFacility maps a human severity label to the numeric syslog
// severity (0–7). The facility is fixed at 1 (user-level messages) so
// the priority (PRI) field encodes facility*8 + severity. Operators
// commonly filter on severity in their syslog router.
func severityToCode(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "emerg", "emergency":
		return 0
	case "alert":
		return 1
	case "crit", "critical":
		return 2
	case "err", "error":
		return 3
	case "warn", "warning":
		return 4
	case "notice":
		return 5
	case "info":
		return 6
	case "debug":
		return 7
	default:
		// Unknown severity → notice. Higher than info so an operator
		// scanning at warn+ doesn't miss a missing-severity event.
		return 5
	}
}

// FormatRFC5424 renders the event as RFC 5424 syslog. Wire shape:
//
//	<PRI>1 TIMESTAMP HOSTNAME APP-NAME PROCID MSGID STRUCTURED-DATA MSG
//
// e.g.
//
//	<14>1 2026-05-12T13:55:22Z astronomer audit - audit-1234
//	     [astronomer@1 user_id="..." action="cluster.delete"] cluster decommissioned
//
// The STRUCTURED-DATA element id is "astronomer@1" — a private enterprise
// number stub keyed on @1; receivers don't validate the PEN, they key
// off the SD-ID for routing. The "-" PROCID is the "no procid" sentinel.
func FormatRFC5424(event SIEMEvent) []byte {
	severity := severityToCode(event.Severity)
	// Facility 1 (user-level messages); PRI = 8*facility + severity.
	pri := 8 + severity

	ts := event.Timestamp.UTC().Format(time.RFC3339)
	host := event.Hostname
	if host == "" {
		host = AppName
	}
	msgID := event.EventID
	if msgID == "" {
		msgID = "-"
	}
	sd := renderRFC5424StructuredData(event)
	msg := event.Message
	if msg == "" {
		msg = event.EventName
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "<%d>1 %s %s %s - %s %s %s", pri, ts, host, AppName, msgID, sd, msg)
	return truncate(buf.Bytes())
}

// renderRFC5424StructuredData emits the STRUCTURED-DATA element for
// FormatRFC5424. We collapse the high-level event fields (event_name,
// actor_user_id, resource_*) into key="value" params so receivers can
// route on them without parsing MSG. The detail blob is dropped into a
// "detail" param as a single quoted JSON string — splunk + others
// accept that and parse it on the search side.
func renderRFC5424StructuredData(event SIEMEvent) string {
	var b strings.Builder
	b.WriteString("[astronomer@1")
	writeSDParam(&b, "event", event.EventName)
	writeSDParam(&b, "user_id", event.ActorUserID)
	writeSDParam(&b, "resource_id", event.ResourceID)
	writeSDParam(&b, "resource_type", event.ResourceType)
	writeSDParam(&b, "severity", event.Severity)
	if len(event.Detail) > 0 {
		writeSDParam(&b, "detail", string(event.Detail))
	}
	b.WriteString("]")
	return b.String()
}

// writeSDParam writes ` key="escaped-value"` if val is non-empty. RFC
// 5424 requires escaping `"`, `\`, and `]` inside PARAM-VALUE; we don't
// support BOM/UCS-2 because every receiver we care about reads ASCII.
func writeSDParam(b *strings.Builder, key, val string) {
	if val == "" {
		return
	}
	b.WriteByte(' ')
	b.WriteString(key)
	b.WriteString(`="`)
	b.WriteString(escapeSDValue(val))
	b.WriteByte('"')
}

func escapeSDValue(s string) string {
	if !strings.ContainsAny(s, `"\]`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for _, r := range s {
		switch r {
		case '"', '\\', ']':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// FormatRFC3164 is the older BSD syslog format:
//
//	<PRI>TIMESTAMP HOSTNAME TAG: MSG
//
// Some legacy SIEM relays accept only this. The TIMESTAMP is the
// classic "Mmm dd hh:mm:ss" with no year (RFC 3164 §4.1.2), so the year
// is inferred by the receiver from arrival time. We accept that
// limitation because operators forced onto RFC 3164 have already
// accepted it elsewhere.
func FormatRFC3164(event SIEMEvent) []byte {
	pri := 8 + severityToCode(event.Severity)
	// "Jan _2 15:04:05" — Go's reference format with a space-padded day.
	ts := event.Timestamp.UTC().Format("Jan _2 15:04:05")
	host := event.Hostname
	if host == "" {
		host = AppName
	}
	tag := AppName
	if event.EventName != "" {
		// Tag includes the event name so a router can dispatch on it
		// without re-parsing MSG. Cap at 32 chars (RFC 3164 §4.1.3).
		tag = AppName + "/" + truncateString(event.EventName, 32-len(AppName)-1)
	}
	msg := event.Message
	if msg == "" {
		msg = event.EventName
	}
	if event.ActorUserID != "" {
		msg = "user=" + event.ActorUserID + " " + msg
	}
	if event.ResourceID != "" {
		msg = "resource=" + event.ResourceID + " " + msg
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "<%d>%s %s %s: %s", pri, ts, host, tag, msg)
	return truncate(buf.Bytes())
}

// FormatCEF renders the ArcSight CEF format:
//
//	CEF:Version|Device Vendor|Device Product|Device Version|Signature ID|Name|Severity|Extension
//
// Extension is space-separated key=value pairs. Per the ArcSight spec
// the pipe, backslash, and equals must be escaped in field values; the
// Extension keys use the standard "dvc", "src", "suser" set where
// possible so existing CEF parsers light up.
func FormatCEF(event SIEMEvent) []byte {
	sevCEF := severityToCode(event.Severity)
	// CEF severity is 0–10 in the spec; we map syslog 0–7 directly and
	// clamp at 10 (which we'll never hit from the syslog mapper).
	if sevCEF > 10 {
		sevCEF = 10
	}
	name := event.Message
	if name == "" {
		name = event.EventName
	}
	signatureID := event.EventName

	var buf bytes.Buffer
	buf.WriteString("CEF:0|")
	buf.WriteString(cefEscapeHeader(CEFVendor))
	buf.WriteByte('|')
	buf.WriteString(cefEscapeHeader(CEFProduct))
	buf.WriteByte('|')
	buf.WriteString(cefEscapeHeader(CEFVersion))
	buf.WriteByte('|')
	buf.WriteString(cefEscapeHeader(signatureID))
	buf.WriteByte('|')
	buf.WriteString(cefEscapeHeader(name))
	buf.WriteByte('|')
	buf.WriteString(strconv.Itoa(sevCEF))
	buf.WriteByte('|')

	// Extension k=v pairs. Order is stable for golden-file tests.
	writeCEFExt(&buf, "rt", strconv.FormatInt(event.Timestamp.UTC().UnixMilli(), 10))
	writeCEFExt(&buf, "dvc", event.Hostname)
	writeCEFExt(&buf, "suser", event.ActorUserID)
	writeCEFExt(&buf, "cs1Label", "resource_type")
	writeCEFExt(&buf, "cs1", event.ResourceType)
	writeCEFExt(&buf, "cs2Label", "resource_id")
	writeCEFExt(&buf, "cs2", event.ResourceID)
	if event.EventID != "" {
		writeCEFExt(&buf, "externalId", event.EventID)
	}
	if len(event.Detail) > 0 {
		// "msg" is the canonical free-text extension. We dump the
		// detail JSON in there so downstream parsers can pull it out
		// with a JSON tokenizer.
		writeCEFExt(&buf, "msg", string(event.Detail))
	}
	return truncate(buf.Bytes())
}

// writeCEFExt writes ` key=val` if val is non-empty, applying the
// CEF extension escape rules (pipe and backslash escaped in keys; the
// `=` and `\r\n` in values).
func writeCEFExt(b *bytes.Buffer, key, val string) {
	if val == "" {
		return
	}
	if b.Len() > 0 && b.Bytes()[b.Len()-1] != '|' {
		b.WriteByte(' ')
	}
	b.WriteString(key)
	b.WriteByte('=')
	b.WriteString(cefEscapeExtensionValue(val))
}

// cefEscapeHeader escapes the CEF prefix-pipe-separated header fields.
// Per spec: backslash + pipe.
func cefEscapeHeader(s string) string {
	if !strings.ContainsAny(s, "\\|") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for _, r := range s {
		if r == '\\' || r == '|' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// cefEscapeExtensionValue escapes the value half of an extension
// key=value pair: equals and backslash escape, CR/LF as their CEF
// escapes (\r / \n).
func cefEscapeExtensionValue(s string) string {
	if !strings.ContainsAny(s, "\\=\r\n") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for _, r := range s {
		switch r {
		case '\\', '=':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\r':
			b.WriteString(`\r`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// FormatNDJSON renders a single line of JSON terminated by \n. Used for
// generic HTTPS sinks (Loki, Vector, Datadog, etc.) and inside the
// Splunk HEC POST body. The JSON shape is stable so receivers can pin
// schemas; new fields are additive.
func FormatNDJSON(event SIEMEvent) []byte {
	if event.Severity == "" {
		event.Severity = "info"
	}
	// Marshal a synthetic envelope so the JSON keys mirror the wire
	// contract (timestamp goes out as RFC3339 string rather than the
	// time.Time zero-value encoding).
	out := map[string]any{
		"event_name": event.EventName,
		"timestamp":  event.Timestamp.UTC().Format(time.RFC3339Nano),
		"severity":   event.Severity,
	}
	if event.EventID != "" {
		out["event_id"] = event.EventID
	}
	if event.Hostname != "" {
		out["hostname"] = event.Hostname
	}
	if event.ActorUserID != "" {
		out["actor_user_id"] = event.ActorUserID
	}
	if event.ResourceID != "" {
		out["resource_id"] = event.ResourceID
	}
	if event.ResourceType != "" {
		out["resource_type"] = event.ResourceType
	}
	if event.Message != "" {
		out["message"] = event.Message
	}
	if len(event.Detail) > 0 {
		out["detail"] = json.RawMessage(event.Detail)
	}
	buf, err := json.Marshal(out)
	if err != nil {
		// json.Marshal of a known-good map shape can't fail in
		// practice; emit a minimal fallback envelope so the dispatcher
		// has something to ship.
		return []byte(fmt.Sprintf(`{"event_name":%q,"error":"marshal failed"}`+"\n", event.EventName))
	}
	buf = append(buf, '\n')
	return truncate(buf)
}

// FormatForID dispatches to the appropriate formatter. Empty / unknown
// values fall back to RFC 5424.
func FormatForID(id string, event SIEMEvent) []byte {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case FormatRFC3164ID:
		return FormatRFC3164(event)
	case FormatCEFID:
		return FormatCEF(event)
	case FormatNDJSONID:
		return FormatNDJSON(event)
	default:
		return FormatRFC5424(event)
	}
}

// DefaultFormatForTransport returns the natural format for a transport.
// Operators can override via the forwarder's `format` column; this is
// the fallback the handler applies when the column is empty.
func DefaultFormatForTransport(transport string) string {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case TransportSyslogUDP, TransportSyslogTCP, TransportSyslogTLS:
		return FormatRFC5424ID
	case TransportSplunkHEC, TransportNDJSONHTTPS:
		return FormatNDJSONID
	default:
		return FormatRFC5424ID
	}
}

// truncate is the per-event cap. We append a literal "[truncated]"
// marker so a receiver scanning for the tail sees that data was lost.
func truncate(b []byte) []byte {
	if len(b) <= MaxSingleEventBytes {
		return b
	}
	marker := []byte("[truncated]")
	cap := MaxSingleEventBytes - len(marker)
	if cap < 0 {
		cap = 0
	}
	out := make([]byte, 0, MaxSingleEventBytes)
	out = append(out, b[:cap]...)
	out = append(out, marker...)
	return out
}

func truncateString(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return s[:n]
}

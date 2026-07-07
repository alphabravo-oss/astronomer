package siem

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

// Transport identifiers stored in siem_forwarders.transport.
const (
	TransportSyslogUDP   = "syslog_udp"
	TransportSyslogTCP   = "syslog_tcp"
	TransportSyslogTLS   = "syslog_tls"
	TransportSplunkHEC   = "splunk_hec"
	TransportNDJSONHTTPS = "ndjson_https"
)

// Transport is the per-protocol sender abstraction the dispatcher
// drives. A single Transport instance is created per (forwarder, tick)
// — the connect-on-send / close-after-send pattern keeps the dispatcher
// stateless across ticks at the cost of a TCP/TLS handshake per tick.
// For sinks that lean on a long-lived connection (high event rate) the
// transport can hold a pooled connection internally; the syslog TCP /
// TLS senders do so.
type Transport interface {
	// Send delivers one batch of pre-formatted event bytes. Each slice
	// is one event; the transport decides how to frame the wire
	// envelope (newline separators for syslog TCP, ND-JSON for HEC,
	// individual UDP datagrams for syslog UDP).
	Send(ctx context.Context, batch [][]byte) error

	// Close releases any underlying connection. Safe to call multiple
	// times; safe to call on a transport that was never Send-ed.
	Close() error
}

// ErrUnsupportedTransport is returned by the dispatcher when a
// forwarder's transport column doesn't match a known sender.
var ErrUnsupportedTransport = errors.New("siem: unsupported transport")

// ---- syslog UDP --------------------------------------------------------

// NewSyslogUDP builds a connectionless UDP sender. Each event in the
// batch is sent as a single datagram; the practical MTU for an
// in-the-clear UDP datagram on a typical LAN is 1500 bytes so the
// format layer's truncation cap (60K) is too permissive for UDP. We
// log a warning at the dispatcher level on first overflow rather than
// truncating here — operators on UDP have explicitly opted out of
// reliability, and we shouldn't silently mangle their data.
func NewSyslogUDP(endpoint string) Transport {
	return &syslogUDP{endpoint: endpoint}
}

type syslogUDP struct {
	endpoint string
	mu       sync.Mutex
	conn     net.Conn
}

func (s *syslogUDP) Send(ctx context.Context, batch [][]byte) error {
	if len(batch) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		c, err := net.Dial("udp", s.endpoint)
		if err != nil {
			return fmt.Errorf("syslog udp dial: %w", err)
		}
		s.conn = c
	}
	deadline, ok := ctx.Deadline()
	if ok {
		_ = s.conn.SetWriteDeadline(deadline)
	} else {
		_ = s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	}
	for _, ev := range batch {
		if _, err := s.conn.Write(ev); err != nil {
			// UDP write should rarely fail at the kernel layer; when
			// it does (network unreachable, buffer full), recreate the
			// connection on the next Send and surface the error.
			_ = s.conn.Close()
			s.conn = nil
			return fmt.Errorf("syslog udp write: %w", err)
		}
	}
	return nil
}

func (s *syslogUDP) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn = nil
	return err
}

// ---- syslog TCP --------------------------------------------------------

// NewSyslogTCP builds a TCP framed-syslog sender. Frames are
// newline-delimited (the "non-transparent framing" of RFC 6587 §3.4.2)
// — it's the format every modern syslogd accepts and the only one
// rsyslog / syslog-ng support with zero extra config. We don't
// implement octet-counting framing (the alternative in §3.4.1) because
// no operator in this audit-forward use case has asked for it.
func NewSyslogTCP(endpoint string, dialTimeout time.Duration) Transport {
	return &syslogTCP{endpoint: endpoint, dialTimeout: dialTimeout}
}

type syslogTCP struct {
	endpoint    string
	dialTimeout time.Duration

	mu   sync.Mutex
	conn net.Conn
}

func (s *syslogTCP) Send(ctx context.Context, batch [][]byte) error {
	if len(batch) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		dialer := &net.Dialer{Timeout: s.dialTimeout}
		c, err := dialer.DialContext(ctx, "tcp", s.endpoint)
		if err != nil {
			return fmt.Errorf("syslog tcp dial: %w", err)
		}
		s.conn = c
	}
	deadline, ok := ctx.Deadline()
	if ok {
		_ = s.conn.SetWriteDeadline(deadline)
	}
	var buf bytes.Buffer
	for _, ev := range batch {
		buf.Write(ev)
		if len(ev) == 0 || ev[len(ev)-1] != '\n' {
			buf.WriteByte('\n')
		}
	}
	if _, err := s.conn.Write(buf.Bytes()); err != nil {
		_ = s.conn.Close()
		s.conn = nil
		return fmt.Errorf("syslog tcp write: %w", err)
	}
	return nil
}

func (s *syslogTCP) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn = nil
	return err
}

// ---- syslog TLS --------------------------------------------------------

// NewSyslogTLS wraps syslogTCP semantics inside a TLS dial. The caller
// supplies the *tls.Config; the dispatcher builds it from the
// forwarder's tls_skip_verify + ca_cert_pem columns.
func NewSyslogTLS(endpoint string, tlsCfg *tls.Config, dialTimeout time.Duration) Transport {
	return &syslogTLS{endpoint: endpoint, tlsCfg: tlsCfg, dialTimeout: dialTimeout}
}

type syslogTLS struct {
	endpoint    string
	tlsCfg      *tls.Config
	dialTimeout time.Duration

	mu   sync.Mutex
	conn net.Conn
}

func (s *syslogTLS) Send(ctx context.Context, batch [][]byte) error {
	if len(batch) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		dialer := &tls.Dialer{
			NetDialer: &net.Dialer{Timeout: s.dialTimeout},
			Config:    s.tlsCfg,
		}
		c, err := dialer.DialContext(ctx, "tcp", s.endpoint)
		if err != nil {
			return fmt.Errorf("syslog tls dial: %w", err)
		}
		s.conn = c
	}
	deadline, ok := ctx.Deadline()
	if ok {
		_ = s.conn.SetWriteDeadline(deadline)
	}
	var buf bytes.Buffer
	for _, ev := range batch {
		buf.Write(ev)
		if len(ev) == 0 || ev[len(ev)-1] != '\n' {
			buf.WriteByte('\n')
		}
	}
	if _, err := s.conn.Write(buf.Bytes()); err != nil {
		_ = s.conn.Close()
		s.conn = nil
		return fmt.Errorf("syslog tls write: %w", err)
	}
	return nil
}

func (s *syslogTLS) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn = nil
	return err
}

// ---- Splunk HEC --------------------------------------------------------

// NewSplunkHEC builds a Splunk HEC sender. The HEC "/services/collector/event"
// endpoint accepts newline-delimited JSON objects, each wrapping the
// payload in a Splunk envelope: { "event": {...}, "time": ..., "host": ... }.
// We unmarshal each event line (FormatNDJSON output) into the envelope's
// "event" key. Auth is `Authorization: Splunk <token>`.
func NewSplunkHEC(endpoint, token string, client *http.Client) Transport {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &splunkHEC{endpoint: endpoint, token: token, client: client}
}

type splunkHEC struct {
	endpoint string
	token    string
	client   *http.Client
}

func (s *splunkHEC) Send(ctx context.Context, batch [][]byte) error {
	if len(batch) == 0 {
		return nil
	}
	target := s.collectorURL()
	// SSRF guard: the collector endpoint is operator-supplied, so refuse to
	// dial a loopback/internal/metadata address. Do not echo the target.
	if err := httpclient.GuardPublicHost(target); err != nil {
		return fmt.Errorf("splunk hec: destination is not a permitted public address")
	}
	var body bytes.Buffer
	for _, ev := range batch {
		// Strip trailing newlines from FormatNDJSON output so the
		// envelope's "event" carries a clean JSON object.
		clean := bytes.TrimRight(ev, "\n")
		body.WriteString(`{"event":`)
		body.Write(clean)
		body.WriteString("}\n")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, &body)
	if err != nil {
		return fmt.Errorf("splunk hec: build request: %w", err)
	}
	req.Header.Set("Authorization", "Splunk "+s.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("splunk hec: do: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Capture a small slice of the body so the operator can debug
		// the rejection (HEC returns a JSON error blob with text).
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("splunk hec: status %d: %s", resp.StatusCode, strings.TrimSpace(string(preview)))
	}
	return nil
}

func (s *splunkHEC) Close() error { return nil }

// collectorURL composes the HEC event endpoint from the operator-
// supplied base. We accept both "https://splunk:8088" and
// "https://splunk:8088/services/collector/event" — the latter is the
// fully-qualified form some operators paste from the HEC UI.
func (s *splunkHEC) collectorURL() string {
	endpoint := strings.TrimRight(s.endpoint, "/")
	if strings.Contains(endpoint, "/services/collector") {
		return endpoint
	}
	return endpoint + "/services/collector/event"
}

// ---- generic NDJSON over HTTPS ----------------------------------------

// NewNDJSONHTTPS builds a generic NDJSON-over-HTTPS sender. The body
// is the concatenation of formatted lines (each terminated by \n as
// FormatNDJSON already emits). Headers (Authorization, Content-Type
// override) come from the forwarder's auth blob + transport defaults.
func NewNDJSONHTTPS(endpoint string, client *http.Client, headers http.Header) Transport {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &ndjsonHTTPS{endpoint: endpoint, client: client, headers: headers}
}

type ndjsonHTTPS struct {
	endpoint string
	client   *http.Client
	headers  http.Header
}

func (s *ndjsonHTTPS) Send(ctx context.Context, batch [][]byte) error {
	if len(batch) == 0 {
		return nil
	}
	if _, err := url.Parse(s.endpoint); err != nil {
		return fmt.Errorf("ndjson https: parse endpoint: %w", err)
	}
	// SSRF guard: the endpoint is operator-supplied, so refuse to dial a
	// loopback/internal/metadata address. Do not echo the endpoint.
	if err := httpclient.GuardPublicHost(s.endpoint); err != nil {
		return fmt.Errorf("ndjson https: destination is not a permitted public address")
	}
	var body bytes.Buffer
	for _, ev := range batch {
		body.Write(ev)
		if len(ev) == 0 || ev[len(ev)-1] != '\n' {
			body.WriteByte('\n')
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, &body)
	if err != nil {
		return fmt.Errorf("ndjson https: build request: %w", err)
	}
	if s.headers != nil {
		for k, vals := range s.headers {
			for _, v := range vals {
				req.Header.Add(k, v)
			}
		}
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/x-ndjson")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("ndjson https: do: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ndjson https: status %d: %s", resp.StatusCode, strings.TrimSpace(string(preview)))
	}
	return nil
}

func (s *ndjsonHTTPS) Close() error { return nil }

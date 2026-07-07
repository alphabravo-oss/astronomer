package siem

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

func TestTransport_SyslogUDP_RoundTrip(t *testing.T) {
	// Bind a UDP listener on an ephemeral port and read what the
	// transport writes. The test is racy in theory (UDP datagrams can
	// be reordered or lost on loopback), but in practice loopback UDP
	// is reliable enough to validate the wire shape without a real
	// syslogd.
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	received := make(chan []byte, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64*1024)
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			out := make([]byte, n)
			copy(out, buf[:n])
			received <- out
		}
	}()

	transport := NewSyslogUDP(conn.LocalAddr().String())
	defer func() {
		_ = transport.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	batch := [][]byte{
		[]byte("<14>1 2026-05-12T13:55:22Z host astronomer - - - hello"),
		[]byte("<14>1 2026-05-12T13:55:23Z host astronomer - - - world"),
	}
	if err := transport.Send(ctx, batch); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Wait for two datagrams.
	got := make([][]byte, 0, 2)
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case b := <-received:
			got = append(got, b)
		case <-timeout:
			t.Fatalf("only received %d datagrams; expected 2", len(got))
		}
	}
	for i, want := range batch {
		if !bytes.Equal(got[i], want) {
			t.Errorf("datagram %d mismatch:\n  got:  %q\n  want: %q", i, got[i], want)
		}
	}
}

func TestTransport_SplunkHEC_PostsExpectedShape(t *testing.T) {
	defer httpclient.DisableGuardForTest()()
	var (
		mu        sync.Mutex
		gotHeader string
		gotBody   string
		gotPath   string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotHeader = r.Header.Get("Authorization")
		gotBody = string(body)
		gotPath = r.URL.Path
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"text":"Success","code":0}`))
	}))
	defer srv.Close()

	transport := NewSplunkHEC(srv.URL, "abc-token", srv.Client())
	defer func() {
		_ = transport.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	batch := [][]byte{
		[]byte(`{"event_name":"audit.cluster.delete","severity":"warn"}` + "\n"),
		[]byte(`{"event_name":"audit.user.login","severity":"info"}` + "\n"),
	}
	if err := transport.Send(ctx, batch); err != nil {
		t.Fatalf("send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotHeader != "Splunk abc-token" {
		t.Errorf("auth header = %q, want %q", gotHeader, "Splunk abc-token")
	}
	if !strings.HasSuffix(gotPath, "/services/collector/event") {
		t.Errorf("expected HEC collector URL path, got %q", gotPath)
	}
	// Body should be two newline-delimited HEC envelopes, each with an "event" key.
	lines := strings.Split(strings.TrimRight(gotBody, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), gotBody)
	}
	for i, line := range lines {
		var env struct {
			Event map[string]any `json:"event"`
		}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("line %d not valid JSON: %v\n%s", i, err, line)
		}
		if env.Event["event_name"] == "" {
			t.Errorf("line %d HEC envelope missing event_name", i)
		}
	}
}

func TestTransport_SplunkHEC_NonOKStatusReturnsError(t *testing.T) {
	defer httpclient.DisableGuardForTest()()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"text":"Invalid token","code":4}`))
	}))
	defer srv.Close()
	transport := NewSplunkHEC(srv.URL, "bad", srv.Client())
	defer func() {
		_ = transport.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := transport.Send(ctx, [][]byte{[]byte(`{"event_name":"x"}`)})
	if err == nil {
		t.Fatalf("expected error for HTTP 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401: %v", err)
	}
}

func TestTransport_NDJSONHTTPS_AddsCustomHeaders(t *testing.T) {
	defer httpclient.DisableGuardForTest()()
	var (
		mu      sync.Mutex
		gotAuth string
		gotCT   string
		gotBody string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotBody = string(body)
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer abc")
	transport := NewNDJSONHTTPS(srv.URL, srv.Client(), hdr)
	defer func() {
		_ = transport.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := transport.Send(ctx, [][]byte{
		[]byte(`{"a":1}` + "\n"),
		[]byte(`{"b":2}` + "\n"),
	}); err != nil {
		t.Fatalf("send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer abc" {
		t.Errorf("Authorization = %q, want Bearer abc", gotAuth)
	}
	if gotCT != "application/x-ndjson" {
		t.Errorf("Content-Type = %q, want application/x-ndjson", gotCT)
	}
	if !strings.HasPrefix(gotBody, `{"a":1}`) || !strings.Contains(gotBody, `{"b":2}`) {
		t.Errorf("body shape wrong: %q", gotBody)
	}
}

func TestTransport_SyslogTCP_FramesNewlineDelimited(t *testing.T) {
	// Listen on a TCP socket, read until close, validate every line.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() {
		_ = ln.Close()
	}()

	received := make(chan []string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			received <- nil
			return
		}
		defer func() {
			_ = conn.Close()
		}()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		scanner := bufio.NewScanner(conn)
		var out []string
		for scanner.Scan() {
			out = append(out, scanner.Text())
		}
		received <- out
	}()

	transport := NewSyslogTCP(ln.Addr().String(), 1*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := transport.Send(ctx, [][]byte{
		[]byte("<14>1 t a app - - - one"),
		[]byte("<14>1 t a app - - - two"),
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	_ = transport.Close() // close half so the server sees EOF
	got := <-received
	if len(got) != 2 || got[0] != "<14>1 t a app - - - one" || got[1] != "<14>1 t a app - - - two" {
		t.Errorf("received lines = %#v", got)
	}
}

// TestTransport_SplunkHEC_RejectsPrivateHost verifies the SSRF guard blocks a
// loopback collector endpoint before the HEC POST is dialed.
func TestTransport_SplunkHEC_RejectsPrivateHost(t *testing.T) {
	transport := NewSplunkHEC("http://127.0.0.1:8088", "token", http.DefaultClient)
	err := transport.Send(context.Background(), [][]byte{[]byte(`{"a":1}`)})
	if err == nil {
		t.Fatal("expected SSRF guard to reject loopback collector URL")
	}
	if strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error leaks internal address: %v", err)
	}
}

// TestTransport_NDJSONHTTPS_RejectsPrivateHost verifies the SSRF guard blocks a
// link-local (cloud metadata) endpoint before the NDJSON POST is dialed.
func TestTransport_NDJSONHTTPS_RejectsPrivateHost(t *testing.T) {
	transport := NewNDJSONHTTPS("http://169.254.169.254/ingest", http.DefaultClient, nil)
	err := transport.Send(context.Background(), [][]byte{[]byte("line")})
	if err == nil {
		t.Fatal("expected SSRF guard to reject link-local endpoint")
	}
	if strings.Contains(err.Error(), "169.254") {
		t.Fatalf("error leaks internal address: %v", err)
	}
}

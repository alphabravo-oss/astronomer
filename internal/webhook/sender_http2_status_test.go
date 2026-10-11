package webhook

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

// Exercise the real TLS HTTP/2 client, including parsers that accept numeric
// :status values wider than an HTTP status or the database int32 field.
func TestSenderRejectsOversizedHTTP2ResponseStatus(t *testing.T) {
	defer httpclient.DisableGuardForTest()()
	certServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert := certServer.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(certServer.Certificate())
	certServer.Close()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2"}, MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		result <- serveOversizedHTTP2Status(listener)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			t.Error("HTTP/2 fixture failed to join")
		}
	})
	client := httpclient.SafeClientWithTLS(5*time.Second, &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	outcome, size, err := NewSender(client).Send(ctx, Subscription{
		URL: "https://" + listener.Addr().String() + "/hook", Secret: "fixture-key", TimeoutSeconds: 5,
	}, Event{EventName: "audit.fixture"})
	select {
	case fixtureErr := <-result:
		if fixtureErr != nil {
			t.Fatalf("HTTP/2 fixture did not send malformed status: %v", fixtureErr)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("HTTP/2 fixture result timed out")
	}
	if err != nil || size == 0 {
		t.Fatalf("request build failed: size=%d err=%v", size, err)
	}
	if outcome.Status != 0 || outcome.Err == nil || outcome.IsSuccess() || !outcome.IsRetryable() {
		t.Fatalf("oversized HTTP/2 response status accepted: %+v", outcome)
	}
}

func serveOversizedHTTP2Status(listener net.Listener) error {
	raw, err := listener.Accept()
	if err != nil {
		return err
	}
	defer raw.Close()
	if err := raw.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	conn, ok := raw.(*tls.Conn)
	if !ok {
		return fmt.Errorf("fixture connection is not TLS")
	}
	if err := conn.Handshake(); err != nil {
		return err
	}
	if conn.ConnectionState().NegotiatedProtocol != "h2" {
		return fmt.Errorf("HTTP/2 was not negotiated")
	}
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(conn, preface); err != nil {
		return err
	}
	if string(preface) != http2.ClientPreface {
		return fmt.Errorf("invalid client preface")
	}
	framer := http2.NewFramer(conn, conn)
	if err := framer.WriteSettings(); err != nil {
		return err
	}
	var stream uint32
	bodyBytes := 0
	complete := false
	for !complete {
		frame, err := framer.ReadFrame()
		if err != nil {
			return err
		}
		switch f := frame.(type) {
		case *http2.SettingsFrame:
			if !f.IsAck() {
				if err := framer.WriteSettingsAck(); err != nil {
					return err
				}
			}
		case *http2.HeadersFrame:
			stream = f.StreamID
			complete = f.StreamEnded()
		case *http2.DataFrame:
			if f.StreamID != stream {
				return fmt.Errorf("unexpected request stream")
			}
			bodyBytes += len(f.Data())
			complete = f.StreamEnded()
		}
	}
	if stream == 0 || bodyBytes == 0 {
		return fmt.Errorf("fixture received no POST body")
	}
	var block bytes.Buffer
	if err := hpack.NewEncoder(&block).WriteField(hpack.HeaderField{Name: ":status", Value: "4294967496"}); err != nil {
		return err
	}
	return framer.WriteHeaders(http2.HeadersFrameParam{StreamID: stream, BlockFragment: block.Bytes(), EndHeaders: true, EndStream: true})
}

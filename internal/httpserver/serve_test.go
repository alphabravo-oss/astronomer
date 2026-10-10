package httpserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServePreservesResponseAndClosesOwnedListener(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})
	result := make(chan error, 1)
	var listener net.Listener
	launched := false
	transport := &http.Transport{}
	t.Cleanup(func() {
		cancel()
		if listener != nil {
			_ = listener.Close()
		}
		transport.CloseIdleConnections()
		if launched {
			select {
			case <-returned:
			case <-time.After(time.Second):
				t.Error("owned HTTP serving did not join fixture cleanup")
			}
		}
	})
	var err error
	listener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("listener setup failed")
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("owned-response")) })}
	launched = true
	go func() { defer close(returned); result <- Serve(ctx, server, listener, 100*time.Millisecond, nil) }()
	client := &http.Client{Timeout: time.Second, Transport: transport}
	response, err := client.Get("http://" + listener.Addr().String() + "/ready")
	if err != nil {
		t.Fatal("owned response request failed")
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK || string(body) != "owned-response" {
		t.Fatal("shared lifecycle changed a legitimate HTTP response")
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal("normal cancellation returned serving failure")
		}
	case <-time.After(time.Second):
		t.Fatal("normal cancellation retained serving owner")
	}
	if conn, err := net.DialTimeout("tcp", listener.Addr().String(), 100*time.Millisecond); err == nil {
		_ = conn.Close()
		t.Fatal("canceled serving retained its owned listener")
	}
}

func TestServeFailureReturnsWithoutCallerCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("listener setup failed")
	}
	if err := listener.Close(); err != nil {
		t.Fatal("listener close setup failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})
	result := make(chan error, 1)
	launched := false
	t.Cleanup(func() {
		cancel()
		if launched {
			select {
			case <-returned:
			case <-time.After(time.Second):
				t.Error("failed serving owner did not join fixture cleanup")
			}
		}
	})
	launched = true
	go func() {
		defer close(returned)
		result <- Serve(ctx, &http.Server{}, listener, 100*time.Millisecond, nil)
	}()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("closed listener failure was hidden")
		}
	case <-time.After(time.Second):
		t.Fatal("serving failure retained shutdown owner until unrelated caller cancellation")
	}
	if ctx.Err() != nil {
		t.Fatal("fixture caller was canceled before serving failure returned")
	}
}

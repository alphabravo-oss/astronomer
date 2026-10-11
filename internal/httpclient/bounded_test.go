package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBoundedResponseChecksHeaderBeforeReadingAndAllocation(t *testing.T) {
	body := &boundedReadFixture{err: errors.New("must not read declared oversized body")}
	response := &http.Response{ContentLength: 9, Body: body}
	value, err := ReadBoundedResponse(context.Background(), response, 8)
	if !errors.Is(err, ErrResponseBodyLimit) || value != nil || body.reads != 0 {
		t.Fatal("declared size limit was not enforced before reading")
	}
	if body.closed {
		t.Fatal("body ownership moved from caller")
	}
	_ = response.Body.Close()
	if !body.closed {
		t.Fatal("caller could not close response")
	}
	for _, maximum := range []int64{0, -1, 1<<63 - 1} {
		if _, err := ReadBoundedResponse(context.Background(), response, maximum); err == nil {
			t.Fatal("invalid or overflowing sentinel bound admitted")
		}
	}
}

func TestBoundedResponseChunkedSentinelAndExactLimit(t *testing.T) {
	for _, fixture := range []struct {
		name string
		text string
		want error
	}{{"exact", "1234", nil}, {"overflow", "12345", ErrResponseBodyLimit}} {
		t.Run(fixture.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.(http.Flusher).Flush() // Do not supply a Content-Length.
				_, _ = io.WriteString(w, fixture.text)
			}))
			defer server.Close()
			response, err := server.Client().Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer func(close func() error) { _ = close() }(response.Body.Close)
			value, err := ReadBoundedResponse(context.Background(), response, 4)
			if !errors.Is(err, fixture.want) || (err == nil && string(value) != fixture.text) || (err != nil && value != nil) {
				t.Fatal("chunked transfer did not enforce its exact sentinel bound")
			}
		})
	}
}

func TestBoundedResponseCancellationAfterHeadersJoinsRead(t *testing.T) {
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(serverDone)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func(close func() error) { _ = close() }(response.Body.Close)
	if value, err := ReadBoundedResponse(ctx, response, 8); value != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("whole-body cancellation identity was lost")
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("request server did not observe cancellation")
	}
}

func TestBoundedResponseErrorsOmitUpstreamContentButRetainCancellation(t *testing.T) {
	secret := "fixture-untrusted-response-text"
	for _, fixture := range []struct {
		cause error
		want  error
	}{{errors.New(secret), ErrResponseBodyRead}, {fmt.Errorf("%s: %w", secret, context.Canceled), context.Canceled}, {fmt.Errorf("%s: %w", secret, context.DeadlineExceeded), context.DeadlineExceeded}} {
		response := &http.Response{ContentLength: -1, Body: &boundedReadFixture{err: fixture.cause}}
		value, err := ReadBoundedResponse(context.Background(), response, 8)
		if value != nil || !errors.Is(err, fixture.want) || strings.Contains(err.Error(), secret) {
			t.Fatal("read error lost safe category or retained upstream text")
		}
	}
}

type boundedReadFixture struct {
	err    error
	reads  int
	closed bool
}

func (r *boundedReadFixture) Read([]byte) (int, error) {
	r.reads++
	return 0, r.err
}

func (r *boundedReadFixture) Close() error {
	r.closed = true
	return nil
}

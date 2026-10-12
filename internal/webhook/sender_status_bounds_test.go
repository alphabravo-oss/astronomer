package webhook

import (
	"context"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

type invalidStatusHTTPDoer struct {
	status int
	closed atomic.Bool
}

func (d *invalidStatusHTTPDoer) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: d.status, Body: &invalidStatusResponseBody{owner: d, Reader: strings.NewReader("remote body")}}, nil
}

type invalidStatusResponseBody struct {
	owner *invalidStatusHTTPDoer
	io.Reader
}

func (b *invalidStatusResponseBody) Close() error {
	b.owner.closed.Store(true)
	return nil
}

func TestSenderRejectsInvalidRemoteResponseStatus(t *testing.T) {
	for _, status := range []int{-1, 0, 99, 1000, math.MaxInt} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			doer := &invalidStatusHTTPDoer{status: status}
			sender := NewSender(doer)
			outcome, size, err := sender.Send(context.Background(), Subscription{URL: "https://example.invalid/hook", Secret: "fixture-key"}, Event{EventName: "audit.fixture"})
			if err != nil || size == 0 {
				t.Fatalf("sent request classified as build failure: size=%d err=%v", size, err)
			}
			if outcome.Status != 0 || outcome.Err == nil || !strings.Contains(outcome.Err.Error(), "invalid webhook response status") || outcome.IsSuccess() || !outcome.IsRetryable() {
				t.Fatalf("invalid remote status was accepted: %+v", outcome)
			}
			if !doer.closed.Load() {
				t.Fatal("invalid response body was not closed")
			}
		})
	}
}

package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

type signedGetResponseBody struct {
	reader       io.Reader
	reads, bytes int
	closed       bool
}

func (b *signedGetResponseBody) Read(p []byte) (int, error) {
	b.reads++
	n, err := b.reader.Read(p)
	b.bytes += n
	return n, err
}
func (b *signedGetResponseBody) Close() error { b.closed = true; return nil }

type signedGetRoundTrip func(*http.Request) (*http.Response, error)

func (f signedGetRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type signedGetErrorReader struct{}

func (signedGetErrorReader) Read([]byte) (int, error) {
	return 0, errors.New("unlabelled_synthetic_upstream_secret")
}
func signedGetForBody(ctx context.Context, b *signedGetResponseBody, length int64) ([]byte, int, error) {
	client := &http.Client{Transport: signedGetRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, ContentLength: length, Body: b, Header: make(http.Header), Request: r}, nil
	})}
	return signedGet(ctx, client, "synthetic-access", "synthetic-secret", "synthetic-session", "us-east-1", "sts", "https://sts.amazonaws.com/", url.Values{"Action": {"GetCallerIdentity"}, "Version": {"2011-06-15"}})
}
func TestSignedAWSIdentityResponsePreservesSignedRequestAndExactLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	body := &signedGetResponseBody{reader: strings.NewReader(strings.Repeat("x", int(maximumAWSIdentityResponseBytes)))}
	client := &http.Client{Transport: signedGetRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Host != "sts.amazonaws.com" || r.URL.Query().Get("Action") != "GetCallerIdentity" || !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=synthetic-access/") || r.Header.Get("X-Amz-Security-Token") != "synthetic-session" {
			t.Fatal("canonical signed request changed")
		}
		return &http.Response{StatusCode: http.StatusOK, ContentLength: maximumAWSIdentityResponseBytes, Body: body, Header: make(http.Header), Request: r}, nil
	})}
	raw, status, err := signedGet(ctx, client, "synthetic-access", "synthetic-secret", "synthetic-session", "us-east-1", "sts", "https://sts.amazonaws.com/", url.Values{"Action": {"GetCallerIdentity"}})
	if err != nil || status != http.StatusOK || int64(len(raw)) != maximumAWSIdentityResponseBytes || !body.closed {
		t.Fatal("bounded exact-length response changed or body not closed", err)
	}
}
func TestSignedAWSIdentityResponseRejectsOversizeBeforeAllocationAndChunkedSentinel(t *testing.T) {
	for _, length := range []int64{maximumAWSIdentityResponseBytes + 1, -1, 0} {
		t.Run(strings.ReplaceAll(time.Duration(length).String(), "/", "_"), func(t *testing.T) {
			body := &signedGetResponseBody{reader: strings.NewReader(strings.Repeat("x", int(maximumAWSIdentityResponseBytes+4096)))}
			raw, status, err := signedGetForBody(context.Background(), body, length)
			if !errors.Is(err, httpclient.ErrResponseBodyLimit) || len(raw) != 0 || status != http.StatusOK || !body.closed {
				t.Fatal("oversized STS response published/truncated or body unclosed", err)
			}
			if length > maximumAWSIdentityResponseBytes && body.reads != 0 {
				t.Fatal("declared oversized response read before rejection")
			}
			if int64(body.bytes) > maximumAWSIdentityResponseBytes+1 {
				t.Fatal("chunked/lied-header sentinel exceeded cap")
			}
		})
	}
}
func TestSignedAWSIdentityResponseReadFailureCannotReturnSuccessOrUpstreamSecret(t *testing.T) {
	body := &signedGetResponseBody{reader: io.MultiReader(strings.NewReader("partial XML"), signedGetErrorReader{})}
	raw, status, err := signedGetForBody(context.Background(), body, -1)
	if !errors.Is(err, httpclient.ErrResponseBodyRead) || len(raw) != 0 || status != http.StatusOK || !body.closed || strings.Contains(err.Error(), "unlabelled_synthetic_upstream_secret") {
		t.Fatal("read failure ignored or leaked upstream text", err)
	}
}
func TestSignedAWSIdentityResponseCancellationAfterEstablishedBodyJoinsRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	entered, stopped := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(stopped)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial XML"))
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
	}))
	type outcome struct {
		raw []byte
		err error
	}
	result := make(chan outcome, 1)
	go func() {
		raw, _, err := signedGet(ctx, server.Client(), "synthetic-access", "synthetic-secret", "", "us-east-1", "sts", server.URL, url.Values{"Action": {"GetCallerIdentity"}})
		result <- outcome{raw, err}
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-result:
			case <-time.After(time.Second):
				t.Error("signed request did not join during failure cleanup")
			}
		}
		server.Close()
		server.Client().CloseIdleConnections()
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("response establishment failed")
	}
	cancel()
	select {
	case got := <-result:
		joined = true
		if !errors.Is(got.err, context.Canceled) || len(got.raw) != 0 {
			t.Fatal("body cancellation published partial success", got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled request failed finite join")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("server body writer survived canceled request")
	}
}

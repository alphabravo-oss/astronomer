package httpclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
)

// ErrResponseBodyLimit is shared with the canonical transport's header and
// streaming response limits. It carries no URL or upstream response text.
var ErrResponseBodyLimit = errResponseBodyLimit

var ErrResponseBodyRead = errors.New("destination response could not be read")

// ReadBoundedResponse collects at most maximum bytes, checking the declared
// size before allocation and unknown/chunked sizes with one sentinel byte.
// The caller owns status handling and Body.Close. The request transport must
// bind cancellation for the whole body; this function starts no worker and
// cannot cancel an arbitrary context-ignoring Reader. Only cancellation
// identity is retained, never credential-bearing upstream read errors.
func ReadBoundedResponse(ctx context.Context, response *http.Response, maximum int64) ([]byte, error) {
	if ctx == nil || response == nil || response.Body == nil || maximum <= 0 || maximum == 1<<63-1 {
		return nil, errors.New("destination response read contract is invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if response.ContentLength > maximum {
		return nil, ErrResponseBodyLimit
	}
	initial := maximum
	if initial > 1<<20 {
		initial = 1 << 20
	}
	buffer := bytes.NewBuffer(make([]byte, 0, int(initial)))
	written, err := io.CopyN(buffer, response.Body, maximum+1)
	if ownerErr := ctx.Err(); ownerErr != nil {
		clear(buffer.Bytes())
		return nil, ownerErr
	}
	if written > maximum || errors.Is(err, ErrResponseBodyLimit) {
		clear(buffer.Bytes())
		return nil, ErrResponseBodyLimit
	}
	if err != nil && !errors.Is(err, io.EOF) {
		clear(buffer.Bytes())
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, ErrResponseBodyRead
	}
	return buffer.Bytes(), nil
}

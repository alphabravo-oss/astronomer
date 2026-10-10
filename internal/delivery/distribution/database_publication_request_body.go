package distribution

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

// The HTTP transport may finish a response before its body writer finishes.
// Close serializes with Read, seals further underlying-reader access, then
// acknowledges that ownership handoff. It deliberately does not close the
// pinned supplier reader: only its synchronous caller does that AFTER joining.
type databasePublicationRequestBody struct {
	mutex  sync.Mutex
	ctx    context.Context
	input  io.Reader
	closed bool
	copied int64
	joined chan struct{}
}

func newDatabasePublicationRequestBody(ctx context.Context, input io.Reader) *databasePublicationRequestBody {
	return &databasePublicationRequestBody{ctx: ctx, input: input, joined: make(chan struct{})}
}
func (body *databasePublicationRequestBody) Read(output []byte) (int, error) {
	body.mutex.Lock()
	defer body.mutex.Unlock()
	if body.closed {
		return 0, io.ErrClosedPipe
	}
	if err := body.ctx.Err(); err != nil {
		return 0, err
	}
	count, err := body.input.Read(output)
	body.copied += int64(count)
	return count, err
}
func (body *databasePublicationRequestBody) Close() error {
	body.mutex.Lock()
	defer body.mutex.Unlock()
	if !body.closed {
		body.closed = true
		close(body.joined)
	}
	return nil
}
func (body *databasePublicationRequestBody) waitJoined() (int64, error) {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-body.joined:
	case <-timer.C:
		return 0, errors.New("database publication request body remains unjoined")
	}
	body.mutex.Lock()
	defer body.mutex.Unlock()
	return body.copied, nil
}

package tunnel

import (
	"bytes"
	"sync"
)

// syncBuffer is a tiny mutex-guarded bytes.Buffer for use as an
// io.Writer passed to slog.NewJSONHandler in tests. The hub's
// background goroutines log while the test goroutine reads the buffer
// at the end of the test — without the mutex, -race rightly flags the
// access pattern. Production code doesn't share buffers like this so
// the helper lives in _test.go only.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newSyncBuffer() *syncBuffer { return &syncBuffer{} }

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

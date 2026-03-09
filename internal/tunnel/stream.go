package tunnel

import (
	"fmt"
	"sync"
)

// StreamManager manages multiplexed streams over a single agent connection.
type StreamManager struct {
	mu         sync.RWMutex
	streams    map[string]*Stream
	maxStreams int
}

// Stream represents a single multiplexed stream (e.g., one K8s request or exec session).
type Stream struct {
	ID       string
	DataCh   chan []byte
	DoneCh   chan struct{}
	mu       sync.Mutex
	isClosed bool
}

// NewStreamManager creates a StreamManager with the given max concurrent streams.
// If maxStreams <= 0, defaults to 256.
func NewStreamManager(maxStreams int) *StreamManager {
	if maxStreams <= 0 {
		maxStreams = 256
	}
	return &StreamManager{
		streams:    make(map[string]*Stream),
		maxStreams: maxStreams,
	}
}

// CreateStream creates a new stream with the given ID.
// Returns an error if the stream already exists or max streams is reached.
func (sm *StreamManager) CreateStream(id string) (*Stream, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, exists := sm.streams[id]; exists {
		return nil, fmt.Errorf("stream %q already exists", id)
	}
	if len(sm.streams) >= sm.maxStreams {
		return nil, fmt.Errorf("max streams (%d) reached", sm.maxStreams)
	}

	s := &Stream{
		ID:     id,
		DataCh: make(chan []byte, 64),
		DoneCh: make(chan struct{}),
	}
	sm.streams[id] = s
	return s, nil
}

// GetStream returns the stream with the given ID, or false if not found.
func (sm *StreamManager) GetStream(id string) (*Stream, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	s, ok := sm.streams[id]
	return s, ok
}

// CloseStream closes and removes the stream with the given ID.
func (sm *StreamManager) CloseStream(id string) {
	sm.mu.Lock()
	s, ok := sm.streams[id]
	if ok {
		delete(sm.streams, id)
	}
	sm.mu.Unlock()

	if ok {
		s.Close()
	}
}

// CloseAll closes all streams.
func (sm *StreamManager) CloseAll() {
	sm.mu.Lock()
	streams := sm.streams
	sm.streams = make(map[string]*Stream)
	sm.mu.Unlock()

	for _, s := range streams {
		s.Close()
	}
}

// Count returns the number of active streams.
func (sm *StreamManager) Count() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.streams)
}

// Close marks the stream as closed and signals DoneCh.
func (s *Stream) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.isClosed {
		s.isClosed = true
		close(s.DoneCh)
	}
}

// IsClosed returns whether the stream has been closed.
func (s *Stream) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isClosed
}

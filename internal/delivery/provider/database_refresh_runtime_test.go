package provider

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type databaseRefreshLogWriter struct {
	buffer   bytes.Buffer
	once     sync.Once
	observed chan struct{}
}

func (writer *databaseRefreshLogWriter) Write(data []byte) (int, error) {
	n, err := writer.buffer.Write(data)
	writer.once.Do(func() { close(writer.observed) })
	return n, err
}

func TestDatabaseRefreshFailedRuntimeWaitIsJoinedOnCancellation(t *testing.T) {
	// A zero physical owner cannot accept or fabricate authority. This exercises
	// the actual failure/retry timer and joined cancellation only, not PG state.
	owner := &DatabaseRefreshOwner{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	output := &databaseRefreshLogWriter{observed: make(chan struct{})}
	logger := slog.New(slog.NewTextHandler(output, nil))
	joined := make(chan struct{})
	go func() { defer close(joined); owner.Run(ctx, logger) }()
	defer func() {
		cancel()
		select {
		case <-joined:
		case <-time.After(2 * time.Second):
			t.Error("database refresh cleanup remains unjoined")
		}
	}()
	select {
	case <-output.observed:
	case <-time.After(2 * time.Second):
		t.Fatal("failed refresh did not reach actual retry log")
	}
	// Cancellation need not wait for the 30-minute retry timer.
	cancel()
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("database refresh runtime unjoined after cancellation")
	}
	if strings.Contains(output.buffer.String(), "acceptance sweep completed") || strings.Contains(output.buffer.String(), "measured") {
		t.Fatal("failed runtime fabricated successful acceptance or scan use")
	}
}

func TestDatabaseRefreshConstructorCannotUseLocatorAsPhysicalAuthority(t *testing.T) {
	for _, repository := range []string{"", "registry.corp/updates", "registry.corp/updates:current", "https://registry.corp/updates", "registry.corp/updates@sha256:abc"} {
		if _, err := NewDatabaseRefreshOwner(t.Context(), nil, nil, nil, repository, t.TempDir(), nil); err == nil {
			t.Fatal("locator created refresh authority without owned pool/base")
		}
	}
	if err := (&DatabaseRefreshOwner{}).Refresh(t.Context()); err == nil {
		t.Fatal("zero refresh owner accepted a sweep")
	}
}

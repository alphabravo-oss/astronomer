package distribution

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// This internal fixture isolates the copy-loop contract. Canonical proof
// construction is exercised separately by the actual signed producer fixture.
func databaseCopyFixture(data []byte) (*VerifiedOfflineDatabaseGraph, string) {
	subject := contentDigest(data)
	graph := &VerifiedOfflineDatabaseGraph{status: Status{Verification: "verified"}, layers: map[string]DatabaseDescriptor{subject: {Digest: subject, Size: int64(len(data)), MediaType: "application/vnd.aquasec.trivy.db.layer.v1.tar+gzip"}}}
	return graph, subject
}

func TestDatabaseLayerCopyPreservesContextSentinels(t *testing.T) {
	data := []byte("complete copy fixture")
	graph, subject := databaseCopyFixture(data)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	expired, expire := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer expire()
	for name, test := range map[string]struct {
		context  context.Context
		sentinel error
	}{"canceled": {canceled, context.Canceled}, "deadline": {expired, context.DeadlineExceeded}} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			err := graph.CopyDatabaseLayer(test.context, subject, bytes.NewReader(data), &output)
			if !errors.Is(err, test.sentinel) || output.Len() != 0 {
				t.Fatalf("context signal lost or cancellation wrote bytes: %v", err)
			}
		})
	}
	for _, signal := range []error{context.Canceled, context.DeadlineExceeded} {
		err := graph.CopyDatabaseLayer(t.Context(), subject, databaseErrorReader{fmt.Errorf("untrusted I/O detail: %w", signal)}, nil)
		if err != signal {
			t.Fatal("wrapped context signal was lost or untrusted detail propagated")
		}
	}
}

func TestDatabaseLayerCopySanitizesOrdinaryIOFailures(t *testing.T) {
	data := []byte("complete copy fixture")
	graph, subject := databaseCopyFixture(data)
	err := graph.CopyDatabaseLayer(t.Context(), subject, databaseErrorReader{errors.New("untrusted I/O detail")}, nil)
	if err == nil || strings.Contains(err.Error(), "untrusted") {
		t.Fatal("raw reader error reached caller")
	}
	err = graph.CopyDatabaseLayer(t.Context(), subject, bytes.NewReader(data), databaseErrorWriter{})
	if err == nil || strings.Contains(err.Error(), "untrusted") {
		t.Fatal("raw writer error reached caller")
	}
}

func TestDatabaseLayerCopyRecognizesCancellationDuringRead(t *testing.T) {
	data := []byte("complete copy fixture")
	graph, subject := databaseCopyFixture(data)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := graph.CopyDatabaseLayer(ctx, subject, databaseCancelReader{cancel}, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("read cancellation replaced by digest error")
	}
}

type databaseErrorReader struct{ err error }

func (reader databaseErrorReader) Read([]byte) (int, error) { return 0, reader.err }

type databaseErrorWriter struct{}

func (databaseErrorWriter) Write([]byte) (int, error) { return 0, errors.New("untrusted I/O detail") }

type databaseCancelReader struct{ cancel context.CancelFunc }

func (reader databaseCancelReader) Read([]byte) (int, error) {
	reader.cancel()
	return 0, context.Canceled
}

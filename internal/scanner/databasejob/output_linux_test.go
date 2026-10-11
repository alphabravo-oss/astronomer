//go:build linux

package databasejob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDatabaseJobMeasurementPipeIsFiniteAndOwnsOnlyDuplicate(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func(close func() error) { _ = close() }(read.Close)
	defer func(close func() error) { _ = close() }(write.Close)
	originalDescriptor := int(write.Fd())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	output, err := NewMeasurementPipe(ctx, write)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	record := []byte("bounded measurement record\n")
	if n, err := output.Write(record); err != nil || n != len(record) {
		t.Fatalf("write: %v", err)
	}
	received := make([]byte, len(record))
	if _, err := io.ReadFull(read, received); err != nil || !bytes.Equal(record, received) {
		t.Fatal("pipe record changed")
	}
	// Fill only the owned pipe with nonblocking writes. No goroutine may remain
	// blocked while the cancellation case or close assertion runs.
	for {
		_, err := unix.Write(originalDescriptor, bytes.Repeat([]byte{'x'}, 4096))
		if errors.Is(err, unix.EAGAIN) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	canceled, cancelWrite := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancelWrite()
	limited, err := NewMeasurementPipe(canceled, write)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	_, err = limited.Write(record)
	limited.Close()
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(before) > 2*time.Second {
		t.Fatalf("blocked log consumer not bounded: %v", err)
	}
	output.Close()
	if _, err := output.Write(record); err == nil {
		t.Fatal("closed captured pipe reused")
	}
	// Closing the captured owner must not close the supplied original descriptor.
	var stat unix.Stat_t
	if unix.Fstat(originalDescriptor, &stat) != nil {
		t.Fatal("original stdout descriptor closed")
	}
	file, err := os.Create(filepath.Join(t.TempDir(), "regular-output"))
	if err != nil {
		t.Fatal(err)
	}
	defer func(close func() error) { _ = close() }(file.Close)
	if _, err := NewMeasurementPipe(t.Context(), file); err == nil {
		t.Fatal("regular output credited as bounded container pipe")
	}
}

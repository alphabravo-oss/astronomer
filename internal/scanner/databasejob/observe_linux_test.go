//go:build linux

package databasejob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func privateRecordRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	// Sealed fixtures stay read-only during assertions. Restore directory write
	// permission only at teardown so TempDir can remove them as an unprivileged user.
	t.Cleanup(func() {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return os.Chmod(path, 0700)
			}
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("restore fixture directory permissions: %v", err)
		}
	})
	return root
}
func measuredFile(t *testing.T, path string, body []byte) string {
	t.Helper()
	if err := os.WriteFile(path, body, 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func syntheticPreparedCache(t *testing.T) (string, string, Preparation) {
	t.Helper()
	root, coord := privateRecordRoot(t), privateRecordRoot(t)
	value := preparationData()
	child := filepath.Join(root, "database-cache-"+value.ClaimID.String())
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{"db", "java-db"} {
		directory := filepath.Join(child, name)
		if err := os.Mkdir(directory, 0700); err != nil {
			t.Fatal(err)
		}
		database, metadata := []byte("synthetic-main-bytes"), []byte(`{"Version":2}`)
		content, meta := &value.Cache.Main, &value.MainMetadata
		if index == 1 {
			database, metadata = []byte("synthetic-java-bytes"), []byte(`{"Version":1}`)
			content, meta = &value.Cache.Java, &value.JavaMetadata
		}
		content.Digest = measuredFile(t, filepath.Join(directory, content.File), database)
		content.Size = int64(len(database))
		meta.Digest = measuredFile(t, filepath.Join(directory, "metadata.json"), metadata)
		meta.Size = int64(len(metadata))
		if err := os.Chmod(directory, 0555); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(child, 0555); err != nil {
		t.Fatal(err)
	}
	// This fixture supplies measurement DATA only. These arbitrary bytes are NOT
	// a usable upstream Trivy/Java DB or signed preparation/authentication proof.
	if err := PublishPreparation(t.Context(), root, value); err != nil {
		t.Fatal(err)
	}
	return root, coord, value
}

type measurementOutput struct {
	mutex   sync.Mutex
	body    bytes.Buffer
	initial chan struct{}
	once    sync.Once
}

func (writer *measurementOutput) Write(body []byte) (int, error) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	n, err := writer.body.Write(body)
	if bytes.Contains(body, []byte(`"phase":"initial"`)) {
		writer.once.Do(func() { close(writer.initial) })
	}
	return n, err
}
func (writer *measurementOutput) bytes() []byte {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	return append([]byte(nil), writer.body.Bytes()...)
}
func joinedObserver(t *testing.T, done <-chan error, cancel context.CancelFunc) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		cancel()
	}
	select {
	case err := <-done:
		t.Fatalf("observer exceeded fixture deadline but cleanup joined: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("observer cleanup UNJOINED; no closure qualified")
	}
	return nil
}

func joinObserverCleanup(t *testing.T, done <-chan error, cancel context.CancelFunc) func() {
	t.Helper()
	joined := false
	t.Cleanup(func() {
		if joined {
			return
		}
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("observer fixture cleanup UNJOINED; no closure qualified")
		}
	})
	return func() { joined = true }
}

func TestDatabaseJobObserverWaitsForReadyThenFinalSignal(t *testing.T) {
	root, coord, value := syntheticPreparedCache(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	signal := make(chan struct{})
	writer := &measurementOutput{initial: make(chan struct{})}
	done := make(chan error, 1)
	readinessStart := time.Now()
	go func() { defer close(done); done <- ObserveCache(ctx, root, coord, writer, signal) }()
	markJoined := joinObserverCleanup(t, done, cancel)
	if err := WaitInitialMeasurement(ctx, coord, value); err != nil {
		cancel()
		_ = joinedObserver(t, done, cancel)
		markJoined()
		t.Fatal(err)
	}
	if time.Since(readinessStart) < 2*time.Second {
		t.Fatal("initial readiness did not preserve monotonic precision separation")
	}
	// Readiness publication follows the completed initial output/hash. A prepare
	// init process can now exit, which is the first chance the scanner can start.
	select {
	case <-writer.initial:
	default:
		t.Fatal("readiness appeared before initial log")
	}
	select {
	case err := <-done:
		markJoined()
		t.Fatalf("observer exited before final signal: %v", err)
	default:
	}
	finalStart := time.Now()
	close(signal)
	err := joinedObserver(t, done, cancel)
	markJoined()
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(finalStart) < 2*time.Second {
		t.Fatal("final measurement did not preserve monotonic precision separation")
	}
	rows := bytes.Split(bytes.TrimSpace(writer.bytes()), []byte{'\n'})
	if len(rows) != 2 {
		t.Fatalf("measurement count %d", len(rows))
	}
	for index, line := range rows {
		row, err := DecodeMeasurement(line, value)
		if err != nil {
			t.Fatal(err)
		}
		expected := "initial"
		if index == 1 {
			expected = "final"
		}
		if row.Phase != expected {
			t.Fatal("measurement ordering")
		}
	}
}

func TestDatabaseJobObserverRefusesChangedFinalOrCancellation(t *testing.T) {
	for _, mode := range []string{"changed", "cancel", "missing-preparation"} {
		t.Run(mode, func(t *testing.T) {
			root, coord, value := syntheticPreparedCache(t)
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			signal := make(chan struct{})
			writer := &measurementOutput{initial: make(chan struct{})}
			done := make(chan error, 1)
			if mode == "missing-preparation" {
				if err := os.Remove(filepath.Join(root, "prepare.json")); err != nil {
					t.Fatal(err)
				}
			}
			go func() { defer close(done); done <- ObserveCache(ctx, root, coord, writer, signal) }()
			markJoined := joinObserverCleanup(t, done, cancel)
			if mode == "missing-preparation" {
				close(signal)
			} else {
				select {
				case <-writer.initial:
				case <-time.After(5 * time.Second):
					cancel()
					_ = joinedObserver(t, done, cancel)
					markJoined()
					t.Fatal("initial measurement unavailable")
				}
				switch mode {
				case "changed":
					path := filepath.Join(root, "database-cache-"+value.ClaimID.String(), "db", "trivy.db")
					if err := os.Chmod(path, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(strings.Repeat("x", int(value.Cache.Main.Size))), 0444); err != nil {
						t.Fatal(err)
					}
					if err := os.Chmod(path, 0444); err != nil {
						t.Fatal(err)
					}
					close(signal)
				case "cancel":
					cancel()
				}
			}
			err := joinedObserver(t, done, cancel)
			markJoined()
			if err == nil {
				t.Fatal("invalid final/stop qualified")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation identity: %v", err)
			}
			if bytes.Contains(writer.bytes(), []byte(`"phase":"final"`)) {
				t.Fatal("failed observer emitted final")
			}
		})
	}
}

func TestDatabaseJobOwnedRecordsRejectReplacementLinksAndModes(t *testing.T) {
	root, coord, value := syntheticPreparedCache(t)
	before, err := os.ReadFile(filepath.Join(root, "prepare.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := PublishPreparation(t.Context(), root, value); err == nil {
		t.Fatal("existing record overwritten")
	}
	after, _ := os.ReadFile(filepath.Join(root, "prepare.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("old record changed")
	}
	if err := publishInitialMeasurement(t.Context(), coord, value, value.Cache, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	wrong := value
	wrong.ScannerDigest = "sha256:" + strings.Repeat("b", 64)
	if err := WaitInitialMeasurement(t.Context(), coord, wrong); err == nil {
		t.Fatal("wrong readiness accepted")
	}
	for _, mode := range []string{"symlink", "hardlink", "public", "fifo", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			destination := privateRecordRoot(t)
			record := filepath.Join(destination, "prepare.json")
			switch mode {
			case "symlink":
				if err := os.Symlink(filepath.Join(root, "prepare.json"), record); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(filepath.Join(root, "prepare.json"), record); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = os.Remove(record) }()
			case "public":
				if err := os.WriteFile(record, before, 0644); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := unix.Mkfifo(record, 0600); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(record, bytes.Repeat([]byte{'x'}, MaximumRecordBytes+1), 0444); err != nil {
					t.Fatal(err)
				}
			}
			if _, ready, err := readPreparation(t.Context(), destination); err == nil || ready {
				t.Fatal("unowned member accepted")
			}
		})
	}
	canceled, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	cancel()
	if _, _, err := readPreparation(canceled, root); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled record read lost identity")
	}
}

func TestDatabaseJobMeasurementSeparationCancellationAndEarlyStop(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := waitMeasurementSeparation(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation became a successful measurement")
	}
	early := make(chan struct{})
	close(early)
	if err := waitMeasurementSeparation(t.Context(), early); err == nil {
		t.Fatal("early shutdown became successful readiness")
	}
	ctx, cancel = context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := waitMeasurementSeparation(ctx, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("measurement delay lost actual deadline")
	}
}

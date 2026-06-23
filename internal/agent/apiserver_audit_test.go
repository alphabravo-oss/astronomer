package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// recordingAuditSender captures every batch it is asked to send so tests can assert
// on batching and ordering. failNext makes the next Send return an error.
type recordingAuditSender struct {
	mu       sync.Mutex
	batches  [][]json.RawMessage
	failNext bool
}

func (s *recordingAuditSender) Send(_ context.Context, events []json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNext {
		s.failNext = false
		return io.ErrUnexpectedEOF
	}
	cp := make([]json.RawMessage, len(events))
	copy(cp, events)
	s.batches = append(s.batches, cp)
	return nil
}

func (s *recordingAuditSender) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, b := range s.batches {
		n += len(b)
	}
	return n
}

func newTestTailer(t *testing.T, logPath string, batchSize int, sender AuditEventSender) *AuditTailer {
	t.Helper()
	cfg := &AgentConfig{
		AuditLogPath:   logPath,
		AuditBatchSize: batchSize,
	}
	tl, err := NewAuditTailer(cfg, sender, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewAuditTailer: %v", err)
	}
	return tl
}

func auditLine(id string) string {
	return `{"auditID":"` + id + `","stage":"ResponseComplete","verb":"get"}`
}

func TestAuditTailer_ParseAndBatch(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	// 5 events, plus a blank line and a malformed line that must be skipped.
	content := strings.Join([]string{
		auditLine("a"),
		auditLine("b"),
		"",
		"not json at all",
		auditLine("c"),
		auditLine("d"),
		auditLine("e"),
	}, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	sender := &recordingAuditSender{}
	tl := newTestTailer(t, logPath, 2, sender)
	if err := tl.poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}

	if got := sender.total(); got != 5 {
		t.Fatalf("forwarded %d events, want 5 (blank + malformed skipped)", got)
	}
	// batchSize=2 over 5 valid events => batches of 2, 2, 1.
	if len(sender.batches) != 3 {
		t.Fatalf("got %d batches, want 3", len(sender.batches))
	}
	if len(sender.batches[0]) != 2 || len(sender.batches[1]) != 2 || len(sender.batches[2]) != 1 {
		t.Fatalf("unexpected batch sizes: %d/%d/%d",
			len(sender.batches[0]), len(sender.batches[1]), len(sender.batches[2]))
	}
}

func TestAuditTailer_CheckpointResumes(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	if err := os.WriteFile(logPath, []byte(auditLine("a")+"\n"+auditLine("b")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sender := &recordingAuditSender{}
	tl := newTestTailer(t, logPath, 100, sender)
	if err := tl.poll(context.Background()); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if sender.total() != 2 {
		t.Fatalf("first poll forwarded %d, want 2", sender.total())
	}

	// Second poll with no new data must forward nothing.
	if err := tl.poll(context.Background()); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if sender.total() != 2 {
		t.Fatalf("idempotent poll forwarded %d, want 2", sender.total())
	}

	// Append two new events; only those should be forwarded.
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(auditLine("c") + "\n" + auditLine("d") + "\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := tl.poll(context.Background()); err != nil {
		t.Fatalf("third poll: %v", err)
	}
	if sender.total() != 4 {
		t.Fatalf("after append forwarded %d total, want 4", sender.total())
	}

	// Checkpoint must equal the file size now.
	fi, _ := os.Stat(logPath)
	cp, err := tl.readCheckpoint()
	if err != nil {
		t.Fatal(err)
	}
	if cp != fi.Size() {
		t.Fatalf("checkpoint = %d, want file size %d", cp, fi.Size())
	}
}

// A fresh tailer reading an existing checkpoint must not re-send old events:
// this simulates an agent restart.
func TestAuditTailer_RestartUsesPersistedCheckpoint(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	if err := os.WriteFile(logPath, []byte(auditLine("a")+"\n"+auditLine("b")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sender1 := &recordingAuditSender{}
	tl1 := newTestTailer(t, logPath, 100, sender1)
	if err := tl1.poll(context.Background()); err != nil {
		t.Fatalf("poll 1: %v", err)
	}
	if sender1.total() != 2 {
		t.Fatalf("poll 1 forwarded %d, want 2", sender1.total())
	}

	// New tailer instance (restart) over the same log + checkpoint path.
	sender2 := &recordingAuditSender{}
	tl2 := newTestTailer(t, logPath, 100, sender2)
	if err := tl2.poll(context.Background()); err != nil {
		t.Fatalf("poll 2: %v", err)
	}
	if sender2.total() != 0 {
		t.Fatalf("post-restart forwarded %d, want 0 (checkpoint should suppress re-send)", sender2.total())
	}
}

// A send failure must NOT advance the checkpoint, so the next poll re-delivers
// the batch.
func TestAuditTailer_SendFailureDoesNotAdvanceCheckpoint(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	if err := os.WriteFile(logPath, []byte(auditLine("a")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sender := &recordingAuditSender{failNext: true}
	tl := newTestTailer(t, logPath, 100, sender)
	if err := tl.poll(context.Background()); err == nil {
		t.Fatal("expected poll error on send failure, got nil")
	}
	cp, _ := tl.readCheckpoint()
	if cp != 0 {
		t.Fatalf("checkpoint advanced to %d despite send failure, want 0", cp)
	}

	// Retry: send now succeeds and the event is delivered.
	if err := tl.poll(context.Background()); err != nil {
		t.Fatalf("retry poll: %v", err)
	}
	if sender.total() != 1 {
		t.Fatalf("retry forwarded %d, want 1", sender.total())
	}
}

// A partial trailing line (no newline yet) must not be forwarded or counted; it
// gets picked up once the apiserver finishes writing the line.
func TestAuditTailer_PartialTrailingLineDeferred(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	// Complete line + a partial line still being written.
	partial := auditLine("a") + "\n" + `{"auditID":"b",`
	if err := os.WriteFile(logPath, []byte(partial), 0o600); err != nil {
		t.Fatal(err)
	}

	sender := &recordingAuditSender{}
	tl := newTestTailer(t, logPath, 100, sender)
	if err := tl.poll(context.Background()); err != nil {
		t.Fatalf("poll 1: %v", err)
	}
	if sender.total() != 1 {
		t.Fatalf("poll 1 forwarded %d, want 1 (partial line deferred)", sender.total())
	}

	// Finish the partial line.
	f, _ := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`"verb":"list"}` + "\n")
	f.Close()

	if err := tl.poll(context.Background()); err != nil {
		t.Fatalf("poll 2: %v", err)
	}
	if sender.total() != 2 {
		t.Fatalf("after completion forwarded %d total, want 2", sender.total())
	}
}

// Log rotation/truncation: if the file shrinks below the checkpoint, restart
// from offset 0 rather than seeking past EOF.
func TestAuditTailer_HandlesRotation(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	if err := os.WriteFile(logPath, []byte(auditLine("old1")+"\n"+auditLine("old2")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sender := &recordingAuditSender{}
	tl := newTestTailer(t, logPath, 100, sender)
	if err := tl.poll(context.Background()); err != nil {
		t.Fatalf("poll 1: %v", err)
	}

	// Rotate: replace with a smaller file.
	if err := os.WriteFile(logPath, []byte(auditLine("new1")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tl.poll(context.Background()); err != nil {
		t.Fatalf("poll 2: %v", err)
	}
	// old1, old2, then new1.
	if sender.total() != 3 {
		t.Fatalf("after rotation forwarded %d total, want 3", sender.total())
	}
}

func TestAuditTailer_CorruptCheckpointTreatedAsZero(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "audit.log")
	if err := os.WriteFile(logPath, []byte(auditLine("a")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tl := newTestTailer(t, logPath, 100, &recordingAuditSender{})
	if err := os.WriteFile(tl.checkpointPath, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	cp, err := tl.readCheckpoint()
	if err != nil {
		t.Fatalf("readCheckpoint: %v", err)
	}
	if cp != 0 {
		t.Fatalf("corrupt checkpoint = %d, want 0", cp)
	}
}

func TestParseAuditLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"valid", auditLine("x"), true},
		{"blank", "   ", false},
		{"garbage", "not json", false},
		{"json array", `[1,2,3]`, false},
		{"json scalar", `42`, false},
		{"trailing spaces", "  " + auditLine("y") + "  ", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ok := parseAuditLine([]byte(c.in))
			if ok != c.ok {
				t.Fatalf("parseAuditLine(%q) ok=%v, want %v", c.in, ok, c.ok)
			}
		})
	}
}

func TestAuditTailer_DefaultCheckpointPath(t *testing.T) {
	cfg := &AgentConfig{AuditLogPath: "/var/log/audit.log"}
	tl, err := NewAuditTailer(cfg, &recordingAuditSender{}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if tl.checkpointPath != "/var/log/audit.log.checkpoint" {
		t.Fatalf("checkpointPath = %q, want default suffix", tl.checkpointPath)
	}
	// Defaults applied.
	if tl.batchSize != 100 {
		t.Fatalf("batchSize = %d, want default 100", tl.batchSize)
	}
}

// Sanity: a written checkpoint round-trips through strconv as expected.
func TestAuditTailer_CheckpointRoundTrip(t *testing.T) {
	dir := t.TempDir()
	tl := newTestTailer(t, filepath.Join(dir, "audit.log"), 100, &recordingAuditSender{})
	if err := tl.writeCheckpoint(12345); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(tl.checkpointPath)
	if strings.TrimSpace(string(raw)) != strconv.Itoa(12345) {
		t.Fatalf("checkpoint file = %q", string(raw))
	}
	got, _ := tl.readCheckpoint()
	if got != 12345 {
		t.Fatalf("readCheckpoint = %d, want 12345", got)
	}
}

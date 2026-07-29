package tunnel

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// Watch bodies are newline-delimited JSON. The producers on both sides of the
// filter (the agent's 16 KiB Read frames, an owner pod's TCP-sized reads) cut
// the byte stream wherever a Read happened to end, so these tests drive the
// filter with boundaries that fall in every interesting place.

const (
	evtTeamA  = `{"type":"ADDED","object":{"kind":"Pod","metadata":{"namespace":"team-a","name":"a"}}}`
	evtTeamB  = `{"type":"ADDED","object":{"kind":"Pod","metadata":{"namespace":"team-b","name":"b"}}}`
	evtTeamA2 = `{"type":"MODIFIED","object":{"kind":"Pod","metadata":{"namespace":"team-a","name":"a2"}}}`
)

// filterAll pushes every chunk through the filter and returns the concatenated
// output plus the first error seen, mimicking a stream consumer.
func filterAll(f *watchLineFilter, chunks [][]byte) ([]byte, error) {
	var out []byte
	for _, c := range chunks {
		got, err := f.filter(c)
		out = append(out, got...)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

// splitIrregular cuts b at a repeating cycle of sizes, so chunk boundaries land
// in a different place inside every event.
func splitIrregular(b []byte, sizes ...int) [][]byte {
	var chunks [][]byte
	for i := 0; len(b) > 0; i++ {
		n := sizes[i%len(sizes)]
		if n > len(b) {
			n = len(b)
		}
		chunks = append(chunks, b[:n])
		b = b[n:]
	}
	return chunks
}

func splitBytes(b []byte, n int) [][]byte {
	var chunks [][]byte
	for len(b) > n {
		chunks = append(chunks, b[:n])
		b = b[n:]
	}
	return append(chunks, b)
}

// TestWatchLineFilterReassemblesSplitEvents is the core regression: before the
// fix the same bytes were handed to watchEventAllowed one raw chunk at a time,
// so any event that spanned a chunk boundary failed json.Unmarshal and was
// dropped — the whole stream vanished for the 3-chunk and byte-at-a-time cases.
func TestWatchLineFilterReassemblesSplitEvents(t *testing.T) {
	body := []byte(evtTeamA + "\n")
	for _, tc := range []struct {
		name  string
		split int
	}{
		{"two frames", len(body) / 2},
		{"three frames", len(body)/3 + 1},
		{"n frames, one byte each", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWatchLineFilter(allowSet("team-a"))
			out, err := filterAll(f, splitBytes(body, tc.split))
			if err != nil {
				t.Fatalf("filter: %v", err)
			}
			if !bytes.Equal(out, body) {
				t.Fatalf("split event not delivered intact:\n got %q\nwant %q", out, body)
			}
			if n := strings.Count(string(out), `"ADDED"`); n != 1 {
				t.Fatalf("event delivered %d times, want exactly once", n)
			}
			if len(f.buf) != 0 {
				t.Fatalf("carry-over buffer not drained: %q", f.buf)
			}
		})
	}
}

// TestWatchLineFilterMultipleEventsInOneFrame covers the coalescing case: a
// single Read that returned several events. Before the fix the concatenated
// frame failed to unmarshal and every event in it was dropped.
func TestWatchLineFilterMultipleEventsInOneFrame(t *testing.T) {
	f := newWatchLineFilter(allowSet("team-a"))
	frame := []byte(evtTeamA + "\n" + evtTeamB + "\n" + evtTeamA2 + "\n")
	out, err := f.filter(frame)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	want := evtTeamA + "\n" + evtTeamA2 + "\n"
	if string(out) != want {
		t.Fatalf("coalesced frame mis-filtered:\n got %q\nwant %q", out, want)
	}
}

// TestWatchLineFilterBoundaryOnNewline pins the off-by-one cases: a frame that
// ends exactly on the newline, and one that starts with it.
func TestWatchLineFilterBoundaryOnNewline(t *testing.T) {
	body := evtTeamA + "\n" + evtTeamA2 + "\n"
	for _, tc := range []struct {
		name string
		at   int
	}{
		{"boundary after the newline", len(evtTeamA) + 1},
		{"boundary before the newline", len(evtTeamA)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWatchLineFilter(allowSet("team-a"))
			out, err := filterAll(f, [][]byte{[]byte(body[:tc.at]), []byte(body[tc.at:])})
			if err != nil {
				t.Fatalf("filter: %v", err)
			}
			if string(out) != body {
				t.Fatalf("newline-boundary split mis-filtered:\n got %q\nwant %q", out, body)
			}
		})
	}
}

// TestWatchLineFilterDeniedEventDoesNotCorruptFollowing asserts a dropped event
// is still consumed from the carry-over buffer: leaving it behind would prefix
// the next event and make it unparseable, dropping allowed data as well.
func TestWatchLineFilterDeniedEventDoesNotCorruptFollowing(t *testing.T) {
	f := newWatchLineFilter(allowSet("team-a"))
	// The denied event is itself split across the two frames.
	body := evtTeamB + "\n" + evtTeamA + "\n"
	out, err := filterAll(f, [][]byte{[]byte(body[:len(evtTeamB)-5]), []byte(body[len(evtTeamB)-5:])})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if string(out) != evtTeamA+"\n" {
		t.Fatalf("denied event corrupted the next parse:\n got %q\nwant %q", out, evtTeamA+"\n")
	}
	if strings.Contains(string(out), "team-b") {
		t.Fatal("denied namespace leaked to the client")
	}
}

// TestWatchLineFilterUnterminatedTailIsFlushed covers a stream that ends without
// a trailing newline; bufio.Scanner emits that final token, so the filter must
// too (and must not emit it twice).
func TestWatchLineFilterUnterminatedTailIsFlushed(t *testing.T) {
	f := newWatchLineFilter(allowSet("team-a"))
	out, err := f.filter([]byte(evtTeamA))
	if err != nil || len(out) != 0 {
		t.Fatalf("partial event emitted early: out=%q err=%v", out, err)
	}
	if got := string(f.flush()); got != evtTeamA+"\n" {
		t.Fatalf("flush = %q, want %q", got, evtTeamA+"\n")
	}
	if got := f.flush(); len(got) != 0 {
		t.Fatalf("second flush re-emitted %q", got)
	}
}

// TestWatchLineFilterOversizedEventIsObservable pins the bound: an event that
// never terminates must not grow the buffer without limit, and hitting the cap
// must be reported (the caller terminates the stream) rather than silently
// swallowed — a silent drop would reintroduce the bug this filter fixes.
func TestWatchLineFilterOversizedEventIsObservable(t *testing.T) {
	f := newWatchLineFilter(allowSet("team-a"))
	// One allowed event, then a runaway line with no newline in sight.
	if _, err := f.filter([]byte(evtTeamA + "\n")); err != nil {
		t.Fatalf("first frame: %v", err)
	}
	var err error
	for i := 0; i < (watchLineMaxBytes/(64*1024))+2 && err == nil; i++ {
		_, err = f.filter(bytes.Repeat([]byte("x"), 64*1024))
	}
	if !errors.Is(err, errWatchLineTooLong) {
		t.Fatalf("oversized event: err = %v, want errWatchLineTooLong", err)
	}
	if len(f.buf) != 0 {
		t.Fatalf("buffer retained %d bytes after the cap was hit", len(f.buf))
	}
}

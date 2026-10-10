//go:build unix

package distribution

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDatabasePayloadOpenRejectsDeterministicFIFOAndSymlinkSwaps(t *testing.T) {
	for _, replacement := range []string{"fifo", "symlink"} {
		t.Run(replacement, func(t *testing.T) {
			payload := []byte("operator-owned bytes")
			path := databasePayloadOwnedFile(t, payload)
			type outcome struct {
				err, swapError error
				opened         bool
			}
			result := make(chan outcome, 1)
			// The hook runs after successful Lstat and immediately before open,
			// reproducing the exact finite-time window without timing guesses.
			go func() {
				var swapError error
				file, _, err := openDatabaseInspectionFileWithHook(path, int64(len(payload)), func() {
					if err := os.Rename(path, path+".original"); err != nil {
						swapError = err
						return
					}
					if replacement == "fifo" {
						swapError = unix.Mkfifo(path, 0600)
					} else {
						swapError = os.Symlink(path+".original", path)
					}
				})
				if file != nil {
					_ = file.Close()
				}
				result <- outcome{err: err, swapError: swapError, opened: file != nil}
			}()
			select {
			case actual := <-result:
				if actual.swapError != nil {
					t.Fatalf("path-substitution fixture failed: %v", actual.swapError)
				}
				if actual.err == nil || actual.opened {
					t.Fatal("substituted nonregular path qualified")
				}
			case <-time.After(time.Second):
				// Release a regressed blocking reader so the negative never leaves
				// an owned fixture goroutine waiting indefinitely after failure.
				if descriptor, err := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK, 0); err == nil {
					_ = unix.Close(descriptor)
				}
				select {
				case <-result:
				case <-time.After(time.Second):
				}
				t.Fatal("substituted FIFO blocked before context-aware inspection")
			}
		})
	}
}

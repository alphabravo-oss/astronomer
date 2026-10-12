//go:build unix

package resolver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestTrustReaderRejectsDeterministicFIFOAndSymlinkReplacement(t *testing.T) {
	for _, replacement := range []string{"fifo", "symlink", "same-size-regular", "mtime-changed"} {
		t.Run(replacement, func(t *testing.T) {
			directory, material := mountedTrustKey(t)
			path := filepath.Join(directory, "qa.pub")
			verifier, err := NewExecVerifier(directory)
			if err != nil {
				t.Fatal(err)
			}
			type result struct {
				err, swapError error
				data           []byte
			}
			completed := make(chan result, 1)
			go func() {
				var swapError error
				data, _, readErr := verifier.readTrustKeyWithHook(t.Context(), "qa", ".pub", func() {
					if replacement == "mtime-changed" {
						info, err := os.Lstat(path)
						if err != nil {
							swapError = err
							return
						}
						swapError = os.Chtimes(path, info.ModTime().Add(time.Second), info.ModTime().Add(time.Second))
						return
					}
					if err := os.Rename(path, path+".original"); err != nil {
						swapError = err
						return
					}
					switch replacement {
					case "fifo":
						swapError = unix.Mkfifo(path, 0600)
					case "symlink":
						swapError = os.Symlink(path+".original", path)
					case "same-size-regular":
						swapError = os.WriteFile(path, material, 0600)
					}
				})
				completed <- result{err: readErr, swapError: swapError, data: data}
			}()
			select {
			case actual := <-completed:
				if actual.swapError != nil {
					t.Fatal("controlled trust replacement failed")
				}
				if actual.err == nil || len(actual.data) != 0 {
					t.Fatal("substituted trust file qualified")
				}
			case <-time.After(time.Second):
				// Release a regressed blocking reader so failure leaves no goroutine.
				if descriptor, err := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK, 0); err == nil {
					_ = unix.Close(descriptor)
				}
				select {
				case <-completed:
				case <-time.After(time.Second):
				}
				t.Fatal("substituted trust FIFO blocked before fstat")
			}
		})
	}
}

func TestTrustReaderStableRegularBytesAndCanonicalCancellation(t *testing.T) {
	directory, expected := mountedTrustKey(t)
	verifier, err := NewExecVerifier(directory)
	if err != nil {
		t.Fatal(err)
	}
	data, _, err := verifier.readTrustKeyWithHook(t.Context(), "qa", ".pub", nil)
	if err != nil || string(data) != string(expected) {
		t.Fatal("regular mounted trust bytes differ")
	}
	ctx, cancel := context.WithCancel(t.Context())
	_, _, err = verifier.readTrustKeyWithHook(ctx, "qa", ".pub", cancel)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("trust reader lost cancellation after successful stat")
	}
}

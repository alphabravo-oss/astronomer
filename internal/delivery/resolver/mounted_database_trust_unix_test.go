//go:build unix

package resolver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"golang.org/x/sys/unix"
)

func TestMountedSigningInspectorClassifiesPreOpenSwapWithoutBlocking(t *testing.T) {
	for _, mutation := range []string{"fifo", "symlink", "regular-replacement", "cancel"} {
		t.Run(mutation, func(t *testing.T) {
			directory, material := mountedTrustKey(t)
			path := filepath.Join(directory, "qa.pub")
			inspector, err := NewMountedCosignTrustInspector(directory, model.TrustPolicy{Provider: model.SignatureCosignKey, KeyRef: "qa"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type result struct{ read, swap error }
			completed := make(chan result, 1)
			go func() {
				var swapError error
				_, readError := inspector.fingerprintWithHook(ctx, func() {
					if mutation == "cancel" {
						cancel()
						return
					}
					if err := os.Rename(path, path+".old"); err != nil {
						swapError = err
						return
					}
					switch mutation {
					case "fifo":
						swapError = unix.Mkfifo(path, 0600)
					case "symlink":
						swapError = os.Symlink(path+".old", path)
					case "regular-replacement":
						swapError = os.WriteFile(path, material, 0600)
					}
				})
				completed <- result{readError, swapError}
			}()
			select {
			case actual := <-completed:
				if actual.swap != nil {
					t.Fatal("controlled pre-open replacement failed")
				}
				if mutation == "cancel" {
					if !errors.Is(actual.read, context.Canceled) || errors.Is(actual.read, ErrMountedDatabaseTrustLost) {
						t.Fatal("pre-open cancellation became physical loss")
					}
				} else if mutation == "regular-replacement" {
					if actual.read == nil || errors.Is(actual.read, ErrMountedDatabaseTrustLost) {
						t.Fatal("unstable regular-file read became definite content loss")
					}
				} else if !errors.Is(actual.read, ErrMountedDatabaseTrustLost) {
					t.Fatal("actual current nonregular material was not known loss")
				}
			case <-time.After(time.Second):
				// Cancel, release a controlled FIFO open/read, then explicitly
				// distinguish joined cleanup from an owned unjoined failure.
				cancel()
				if descriptor, err := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK, 0); err == nil {
					if err := unix.Close(descriptor); err != nil {
						t.Error("controlled FIFO release failed")
					}
				}
				select {
				case <-completed:
					t.Fatal("nonregular substitution blocked inspection; controlled reader joined after release")
				case <-time.After(time.Second):
					t.Fatal("nonregular substitution blocked inspection; controlled reader remains unjoined in failed test process")
				}
			}
		})
	}
}

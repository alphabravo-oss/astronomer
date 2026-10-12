//go:build unix

package resolver

import (
	"os"

	"golang.org/x/sys/unix"
)

const strictTrustOpeningAvailable = true

// Refuse symlink substitution and avoid a blocking FIFO open before fstat.
func openTrustRegular(path string) (*os.File, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(descriptor), "delivery-trust")
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, os.ErrInvalid
	}
	return file, nil
}

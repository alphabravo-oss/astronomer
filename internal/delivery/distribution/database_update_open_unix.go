//go:build unix

package distribution

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// O_NOFOLLOW closes the symlink substitution window. O_NONBLOCK prevents a
// regular-file path swapped to a FIFO from hanging before immediate fstat.
func openDatabaseInspectionRegular(path string) (*os.File, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("database payload nonblocking open failed")
	}
	file := os.NewFile(uintptr(descriptor), "database-payload")
	if file == nil {
		_ = unix.Close(descriptor)
		return nil, errors.New("database payload descriptor unavailable")
	}
	return file, nil
}

//go:build linux

package databasejob

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// MeasurementPipe owns a duplicate of the container's stdout pipe. Nonblocking
// writes plus finite polling prevent a stalled log consumer from keeping the
// helper alive until an unbounded Write completes. Other output descriptors are
// unqualified for this optional container role and fail closed.
type MeasurementPipe struct {
	mutex  sync.Mutex
	ctx    context.Context
	fd     int
	closed bool
}

func NewMeasurementPipe(ctx context.Context, output *os.File) (*MeasurementPipe, error) {
	if ctx == nil || output == nil {
		return nil, errors.New("database measurement pipe unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	if unix.Fstat(int(output.Fd()), &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFIFO {
		return nil, errors.New("database measurement output is not a container pipe")
	}
	fd, err := unix.FcntlInt(output.Fd(), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return nil, errors.New("database measurement pipe capture failed")
	}
	if unix.SetNonblock(fd, true) != nil {
		_ = unix.Close(fd)
		return nil, errors.New("database measurement pipe cannot be bounded")
	}
	return &MeasurementPipe{ctx: ctx, fd: fd}, nil
}
func (writer *MeasurementPipe) Write(payload []byte) (int, error) {
	if writer == nil {
		return 0, errors.New("database measurement pipe unavailable")
	}
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if writer.closed || len(payload) == 0 || len(payload) > MaximumRecordBytes+1 {
		return 0, errors.New("database measurement pipe closed or record oversized")
	}
	bounded, cancel := context.WithTimeout(writer.ctx, 15*time.Second)
	defer cancel()
	copied := 0
	for copied < len(payload) {
		if err := bounded.Err(); err != nil {
			return copied, err
		}
		n, err := unix.Write(writer.fd, payload[copied:min(copied+4096, len(payload))])
		if n > 0 {
			copied += n
		}
		if err == nil {
			if n == 0 {
				return copied, errors.New("database measurement pipe made no progress")
			}
			continue
		}
		if !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			return copied, errors.New("database measurement pipe write failed")
		}
		descriptors := []unix.PollFd{{Fd: int32(writer.fd), Events: unix.POLLOUT}}
		if _, err := unix.Poll(descriptors, 100); err != nil && !errors.Is(err, unix.EINTR) {
			return copied, errors.New("database measurement pipe poll failed")
		}
	}
	return copied, nil
}
func (writer *MeasurementPipe) Close() {
	if writer == nil {
		return
	}
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if !writer.closed {
		writer.closed = true
		_ = unix.Close(writer.fd)
	}
}

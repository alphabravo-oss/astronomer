//go:build unix

package distribution

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func openSupplierTransferDirectory(path string, size int64) (*os.File, os.FileInfo, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path || size <= 0 || size > MaximumDatabaseLayerBytes {
		return nil, nil, errors.New("supplier database owned destination directory invalid")
	}
	before, err := os.Lstat(path)
	if err != nil || !before.IsDir() || before.Mode().Perm()&0077 != 0 {
		return nil, nil, errors.New("supplier database destination directory not private")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, errors.New("supplier database destination directory open failed")
	}
	file := os.NewFile(uintptr(fd), "owned supplier database directory")
	if file == nil {
		_ = unix.Close(fd)
		return nil, nil, errors.New("supplier database destination directory handle invalid")
	}
	var opened unix.Stat_t
	if unix.Fstat(fd, &opened) != nil || int(opened.Uid) != os.Geteuid() || opened.Mode&unix.S_IFMT != unix.S_IFDIR {
		_ = file.Close()
		return nil, nil, errors.New("supplier database destination directory owner invalid")
	}
	current, err := file.Stat()
	if err != nil || !os.SameFile(before, current) || current.Mode().Perm()&0077 != 0 {
		_ = file.Close()
		return nil, nil, errors.New("supplier database destination directory changed")
	}
	if err := supplierTransferCapacity(fd, size); err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return file, current, nil
}

func supplierTransferCapacity(fd int, size int64) error {
	var filesystem unix.Statfs_t
	if unix.Fstatfs(fd, &filesystem) != nil || filesystem.Bsize <= 0 || filesystem.Bavail > ^uint64(0)/uint64(filesystem.Bsize) {
		return errors.New("supplier database destination capacity unavailable")
	}
	available := filesystem.Bavail * uint64(filesystem.Bsize)
	if uint64(size)+uint64(SupplierDatabaseTransferReserveBytes) > available {
		return errors.New("supplier database destination has insufficient reserve")
	}
	return nil
}

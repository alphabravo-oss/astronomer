//go:build unix

package distribution

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func openSealedDatabaseCache(ctx context.Context, rootPath, child string, expected DatabaseCacheSnapshot, mainMetadata, javaMetadata DatabaseUpdateMetadata) (*MaterializedDatabaseCache, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The canonical private root opener retains its existing no-follow, UID,
	// original-path and finite reserve checks. No write is performed here.
	root, before, err := openSupplierTransferDirectory(rootPath, 1)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	directory, err := openSealedDatabaseCacheDirectory(int(root.Fd()), child)
	if err != nil {
		return nil, err
	}
	cache := &MaterializedDatabaseCache{directory: filepath.Join(rootPath, child), directoryFD: directory, snapshot: expected}
	accepted := false
	defer func() {
		if !accepted {
			cache.Close()
		}
	}()
	cache.claims[0].Metadata, cache.claims[1].Metadata = mainMetadata, javaMetadata
	for index, name := range []string{"db", "java-db"} {
		cache.directories[index], err = openSealedDatabaseCacheDirectory(int(directory.Fd()), name)
		if err != nil {
			return nil, err
		}
		content := expected.Main
		if index == 1 {
			content = expected.Java
		}
		for offset, member := range []string{content.File, "metadata.json"} {
			cache.files[index*2+offset], err = openSealedDatabaseCacheMember(int(cache.directories[index].Fd()), member)
			if err != nil {
				return nil, err
			}
		}
	}
	current, err := os.Lstat(rootPath)
	if err != nil || !os.SameFile(before, current) || current.Mode().Perm()&0077 != 0 {
		return nil, errors.New("database cache measurement root changed")
	}
	if _, err := cache.Measure(ctx); err != nil {
		return nil, err
	}
	accepted = true
	return cache, nil
}

func openSealedDatabaseCacheDirectory(parent int, name string) (*os.File, error) {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("database cache sealed directory open failed")
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || int(stat.Uid) != os.Geteuid() || stat.Mode&07777 != 0555 {
		_ = unix.Close(fd)
		return nil, errors.New("database cache sealed directory owner invalid")
	}
	return os.NewFile(uintptr(fd), "owned sealed database cache directory"), nil
}

func openSealedDatabaseCacheMember(parent int, name string) (*os.File, error) {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("database cache sealed member open failed")
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || int(stat.Uid) != os.Geteuid() || stat.Nlink != 1 || stat.Mode&07777 != 0444 {
		_ = unix.Close(fd)
		return nil, errors.New("database cache sealed member owner invalid")
	}
	return os.NewFile(uintptr(fd), "owned sealed database cache member"), nil
}

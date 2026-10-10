//go:build unix

package distribution

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

func createDatabaseCacheDestination(rootPath string, size int64) (*MaterializedDatabaseCache, error) {
	return createDatabaseCacheDestinationForJob(rootPath, size, "")
}

func createDatabaseCacheDestinationForJob(rootPath string, size int64, jobName string) (*MaterializedDatabaseCache, error) {
	if jobName != "" {
		claim, err := uuid.Parse(strings.TrimPrefix(jobName, "database-cache-"))
		if err != nil || claim.Version() != 4 || claim.Variant() != uuid.RFC4122 || jobName != "database-cache-"+claim.String() {
			return nil, errors.New("database cache job child invalid")
		}
	}
	if size <= 0 || size > MaximumDatabaseExpandedBytes {
		return nil, errors.New("database cache aggregate expansion invalid")
	}
	root, before, err := openSupplierTransferDirectory(rootPath, min(size, MaximumDatabaseLayerBytes))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := supplierRefreshCapacity(root, size); err != nil {
		return nil, err
	}
	name := jobName
	if name == "" {
		name = "database-cache-" + rand.Text()
	}
	if err := unix.Mkdirat(int(root.Fd()), name, 0700); err != nil {
		return nil, errors.New("database cache private staging creation failed")
	}
	directory, err := openDatabaseCacheDirectory(int(root.Fd()), name)
	if err != nil {
		return nil, err
	}
	cache := &MaterializedDatabaseCache{directory: filepath.Join(rootPath, name), directoryFD: directory}
	accepted := false
	defer func() {
		if !accepted {
			cache.Close()
		}
	}()
	current, err := os.Lstat(rootPath)
	if err != nil || !os.SameFile(before, current) || current.Mode().Perm()&0077 != 0 {
		return nil, errors.New("database cache root changed while staging")
	}
	for index, child := range []string{"db", "java-db"} {
		if err := unix.Mkdirat(int(directory.Fd()), child, 0700); err != nil {
			return nil, errors.New("database cache role directory creation failed")
		}
		cache.directories[index], err = openDatabaseCacheDirectory(int(directory.Fd()), child)
		if err != nil {
			return nil, err
		}
	}
	accepted = true
	return cache, nil
}

func openDatabaseCacheDirectory(parent int, name string) (*os.File, error) {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("database cache directory open failed")
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || int(stat.Uid) != os.Geteuid() || stat.Mode&0077 != 0 {
		_ = unix.Close(fd)
		return nil, errors.New("database cache directory ownership invalid")
	}
	return os.NewFile(uintptr(fd), "owned database cache directory"), nil
}

func (cache *MaterializedDatabaseCache) createMember(index int, name string, size int64, snapshot DatabaseUpdateSnapshot, content DatabaseCacheContent) (io.Writer, error) {
	if index < 0 || index >= len(cache.directories) || cache.directories[index] == nil {
		return nil, errors.New("database cache role destination unavailable")
	}
	slot := index * 2
	expected := content.Size
	if name == "metadata.json" {
		slot++
		expected = snapshot.Metadata.Size
	} else if name != content.File {
		return nil, errors.New("database cache member outside exact owned names")
	}
	if cache.files[slot] != nil || size != expected || size <= 0 {
		return nil, errors.New("database cache member size or ownership differs")
	}
	fd, err := unix.Openat(int(cache.directories[index].Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, errors.New("database cache member creation failed")
	}
	file := os.NewFile(uintptr(fd), "owned database cache member")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("database cache member handle unavailable")
	}
	cache.files[slot] = file
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || int(stat.Uid) != os.Geteuid() || stat.Nlink != 1 || stat.Mode&0777 != 0600 {
		return nil, errors.New("database cache member owner invalid")
	}
	return file, nil
}

func (cache *MaterializedDatabaseCache) sealAndMeasure(ctx context.Context) error {
	for _, file := range cache.files {
		if file == nil || file.Sync() != nil || file.Chmod(0444) != nil {
			return errors.New("database cache member seal failed")
		}
	}
	for _, directory := range cache.directories {
		if directory == nil || directory.Chmod(0555) != nil {
			return errors.New("database cache role seal failed")
		}
	}
	if cache.directoryFD.Chmod(0555) != nil {
		return errors.New("database cache directory seal failed")
	}
	_, err := cache.measure(ctx)
	return err
}

// Measure reads the actual owned cache again without changing files or clocks.
// The result is still DATA; job identity, scanner use and current authority must
// be established by the separate running-job/report commit path.
func (cache *MaterializedDatabaseCache) Measure(ctx context.Context) (DatabaseCacheSnapshot, error) {
	if ctx == nil || cache == nil {
		return DatabaseCacheSnapshot{}, errors.New("database cache measurement unavailable")
	}
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	if cache.closed {
		return DatabaseCacheSnapshot{}, errors.New("database cache measurement owner closed")
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	return cache.measure(bounded)
}
func (cache *MaterializedDatabaseCache) measure(ctx context.Context) (DatabaseCacheSnapshot, error) {
	if err := cache.validatePaths(); err != nil {
		return DatabaseCacheSnapshot{}, err
	}
	for index, file := range cache.files {
		role := index / 2
		content := cache.snapshot.Main
		directory, name := cache.directories[role], content.File
		if role == 1 {
			content = cache.snapshot.Java
			name = content.File
		}
		digest, size := content.Digest, content.Size
		if index%2 != 0 {
			name = "metadata.json"
			digest, size = cache.claims[role].Metadata.Digest, cache.claims[role].Metadata.Size
		}
		if file == nil || directory == nil {
			return DatabaseCacheSnapshot{}, errors.New("database cache member unavailable")
		}
		if err := measureDatabaseCacheMember(ctx, directory, name, file, digest, size); err != nil {
			return DatabaseCacheSnapshot{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return DatabaseCacheSnapshot{}, err
	}
	if err := cache.validatePaths(); err != nil {
		return DatabaseCacheSnapshot{}, err
	}
	return cache.snapshot, nil
}

func (cache *MaterializedDatabaseCache) validatePaths() error {
	if cache.directoryFD == nil {
		return errors.New("database cache directory unavailable")
	}
	before, err := cache.directoryFD.Stat()
	current, currentError := os.Lstat(cache.directory)
	if err != nil || currentError != nil || !current.IsDir() || !os.SameFile(before, current) || current.Mode().Perm() != 0555 || current.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return errors.New("database cache path changed during measurement")
	}
	var sealed unix.Stat_t
	if unix.Fstat(int(cache.directoryFD.Fd()), &sealed) != nil || sealed.Mode&unix.S_IFMT != unix.S_IFDIR || sealed.Mode&07777 != 0555 || int(sealed.Uid) != os.Geteuid() {
		return errors.New("database cache sealed directory owner changed")
	}
	for index, name := range []string{"db", "java-db"} {
		if cache.directories[index] == nil {
			return errors.New("database cache role directory unavailable")
		}
		var actual, pinned unix.Stat_t
		if unix.Fstatat(int(cache.directoryFD.Fd()), name, &actual, unix.AT_SYMLINK_NOFOLLOW) != nil || unix.Fstat(int(cache.directories[index].Fd()), &pinned) != nil || actual.Mode&unix.S_IFMT != unix.S_IFDIR || actual.Mode&07777 != 0555 || int(actual.Uid) != os.Geteuid() || pinned.Mode&unix.S_IFMT != unix.S_IFDIR || pinned.Mode&07777 != 0555 || int(pinned.Uid) != os.Geteuid() || actual.Dev != pinned.Dev || actual.Ino != pinned.Ino {
			return errors.New("database cache role directory changed during measurement")
		}
	}
	return nil
}

func measureDatabaseCacheMember(ctx context.Context, directory *os.File, name string, original *os.File, expectedDigest string, expectedSize int64) error {
	fd, err := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("database cache sealed member open failed")
	}
	file := os.NewFile(uintptr(fd), "owned sealed database cache member")
	if file == nil {
		_ = unix.Close(fd)
		return errors.New("database cache sealed member handle unavailable")
	}
	defer file.Close()
	if !sealedDatabaseCacheMemberOwner(fd) || !sealedDatabaseCacheMemberOwner(int(original.Fd())) {
		return errors.New("database cache sealed member owner changed")
	}
	before, err := original.Stat()
	opened, openedError := file.Stat()
	if err != nil || openedError != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Mode().Perm() != 0444 || opened.Size() != expectedSize || !opened.ModTime().Equal(before.ModTime()) {
		return errors.New("database cache sealed member identity differs")
	}
	hash := sha256.New()
	input := &io.LimitedReader{R: databaseInspectionReader{ctx: ctx, input: file}, N: expectedSize + 1}
	copied, err := io.CopyBuffer(hash, input, make([]byte, 64<<10))
	after, afterError := file.Stat()
	if err != nil || afterError != nil || !sealedDatabaseCacheMemberOwner(fd) || !sealedDatabaseCacheMemberOwner(int(original.Fd())) || copied != expectedSize || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != expectedDigest || !os.SameFile(opened, after) || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return databaseInspectionError(ctx, "database cache sealed member hash or identity changed")
	}
	// The literal directory entry must still designate the same regular inode.
	var current unix.Stat_t
	if unix.Fstatat(int(directory.Fd()), name, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || current.Mode&unix.S_IFMT != unix.S_IFREG || current.Mode&07777 != 0444 || current.Nlink != 1 || int(current.Uid) != os.Geteuid() {
		return errors.New("database cache sealed member path changed")
	}
	var pinned unix.Stat_t
	if unix.Fstat(fd, &pinned) != nil || pinned.Dev != current.Dev || pinned.Ino != current.Ino {
		return errors.New("database cache sealed member was replaced")
	}
	return ctx.Err()
}

// The initial/reopened owner and every before/after read obey the same sealed
// regular-file contract. Byte equality alone cannot renew replaced permissions,
// an extra hard link or a changed file owner.
func sealedDatabaseCacheMemberOwner(fd int) bool {
	var stat unix.Stat_t
	return unix.Fstat(fd, &stat) == nil && stat.Mode&unix.S_IFMT == unix.S_IFREG && stat.Mode&07777 == 0444 && stat.Nlink == 1 && int(stat.Uid) == os.Geteuid()
}

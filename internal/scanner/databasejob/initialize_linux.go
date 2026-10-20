//go:build linux

package databasejob

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/safeconv"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

// InitializeJobInput creates an EXCLUSIVE claim child beneath the owner's new
// PVC. It does not adopt/chown the filesystem root or erase entries such as
// lost+found. The worker independently binds the actual newly-created PVC UID.
func InitializeJobInput(ctx context.Context, claim uuid.UUID) error {
	return initializeClaimVolume(ctx, InputVolumeRoot, claim)
}

// InitializeJobCoordination seals the scan Pod's NEW emptyDir only. No scanner
// mount contains coordination. Kubernetes default emptyDir0777 is normalized
// by this explicit root init; canonical readers still require private0700.
func InitializeJobCoordination(ctx context.Context) error {
	return initializeEmptyVolume(ctx, CoordinationRoot, JobProcessUID, 0700)
}

// InitializeJobMaterialDestination prepares an empty root-owned destination
// before the separate closed projected-material adapter. It grants no trust.
func InitializeJobMaterialDestination(ctx context.Context) error {
	return initializeEmptyVolume(ctx, PrivateJobMaterialRoot, 0, 0755)
}

func initializationContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if ctx == nil || os.Geteuid() != 0 {
		return nil, nil, errors.New("owned Job initialization requires root init")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	return bounded, cancel, nil
}

func openInitializationVolume(path string, allowEmptyDir bool) (*os.File, unix.Stat_t, error) {
	fd, err := unix.Open(path, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, unix.Stat_t{}, errors.New("owned Job initialization volume unavailable")
	}
	file := os.NewFile(uintptr(fd), "owned Job initialization")
	var before, location unix.Stat_t
	if unix.Fstat(fd, &before) != nil || unix.Lstat(path, &location) != nil || before.Mode&unix.S_IFMT != unix.S_IFDIR || before.Uid != 0 || (before.Mode&07777 != 0755 && (!allowEmptyDir || before.Mode&07777 != 0777)) || !sameInitializationIdentity(before, location) {
		_ = file.Close()
		return nil, unix.Stat_t{}, errors.New("owned Job initialization volume identity invalid")
	}
	return file, before, nil
}
func sameInitializationIdentity(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino && left.Mode == right.Mode && left.Uid == right.Uid && left.Gid == right.Gid
}
func checkInitializationLocation(path string, file *os.File, before unix.Stat_t) error {
	var current, location unix.Stat_t
	if unix.Fstat(int(file.Fd()), &current) != nil || unix.Lstat(path, &location) != nil || !sameInitializationIdentity(before, current) || !sameInitializationIdentity(current, location) {
		return errors.New("owned Job initialization volume replaced")
	}
	return nil
}
func initializeEmptyVolume(ctx context.Context, path string, uid int, mode uint32) error {
	bounded, cancel, err := initializationContext(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	if (uid != 0 || mode != 0755) && (uid != JobProcessUID || mode != 0700) {
		return errors.New("owned Job initialization owner invalid")
	}
	file, before, err := openInitializationVolume(path, true)
	if err != nil {
		return err
	}
	defer func(close func() error) { _ = close() }(file.Close)
	// One name is enough to refuse existing content; this census never grows.
	names, err := file.Readdirnames(1)
	if len(names) != 0 || !errors.Is(err, io.EOF) {
		return errors.New("owned Job initialization refuses existing volume content")
	}
	if err := bounded.Err(); err != nil {
		return err
	}
	if err := checkInitializationLocation(path, file, before); err != nil {
		return err
	}
	if unix.Fchown(int(file.Fd()), uid, -1) != nil || unix.Fchmod(int(file.Fd()), mode) != nil || file.Sync() != nil {
		return errors.New("owned Job initialization volume ownership failed")
	}
	var final unix.Stat_t
	if unix.Fstat(int(file.Fd()), &final) != nil || final.Dev != before.Dev || final.Ino != before.Ino || final.Mode&unix.S_IFMT != unix.S_IFDIR || final.Mode&07777 != mode || final.Uid != safeconv.Uint32(uid) {
		return errors.New("owned Job initialized volume changed")
	}
	if err := checkInitializationLocation(path, file, final); err != nil {
		return err
	}
	return bounded.Err()
}

func initializeClaimVolume(ctx context.Context, path string, claim uuid.UUID) error {
	bounded, cancel, err := initializationContext(ctx)
	if err != nil {
		return err
	}
	defer cancel()
	if claim.Version() != 4 || claim.Variant() != uuid.RFC4122 {
		return errors.New("owned Job initialization claim invalid")
	}
	file, before, err := openInitializationVolume(path, false)
	if err != nil {
		return err
	}
	defer func(close func() error) { _ = close() }(file.Close)
	name := "input-" + claim.String()
	if unix.Mkdirat(int(file.Fd()), name, 0700) != nil {
		return errors.New("owned Job initialization refuses existing claim directory")
	}
	fd, err := unix.Openat(int(file.Fd()), name, unix.O_DIRECTORY|unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("owned Job initialization claim directory unavailable")
	}
	defer func() { _ = unix.Close(fd) }()
	for _, child := range []string{"staging", "cache", "workload"} {
		if err := bounded.Err(); err != nil {
			return err
		}
		if unix.Mkdirat(fd, child, 0700) != nil {
			return errors.New("owned Job initialization exclusive child failed")
		}
		childFD, err := unix.Openat(fd, child, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			return errors.New("owned Job initialization child unavailable")
		}
		valid := sealInitializedDirectory(childFD)
		var current, entry unix.Stat_t
		valid = valid && unix.Fstat(childFD, &current) == nil && unix.Fstatat(fd, child, &entry, unix.AT_SYMLINK_NOFOLLOW) == nil && sameInitializationIdentity(current, entry)
		_ = unix.Close(childFD)
		if !valid {
			return errors.New("owned Job initialization child ownership failed")
		}
	}
	if err := bounded.Err(); err != nil {
		return err
	}
	if err := checkInitializationLocation(path, file, before); err != nil {
		return err
	}
	if !sealInitializedDirectory(fd) || file.Sync() != nil {
		return errors.New("owned Job initialization claim ownership failed")
	}
	var current, entry unix.Stat_t
	if unix.Fstat(fd, &current) != nil || unix.Fstatat(int(file.Fd()), name, &entry, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameInitializationIdentity(current, entry) || current.Mode&07777 != 0700 || current.Uid != JobProcessUID {
		return errors.New("owned Job initialized claim replaced")
	}
	if err := checkInitializationLocation(path, file, before); err != nil {
		return err
	}
	// Partial originals remain on failure. Existing root entries are untouched;
	// future execution must create a new claim/PVC rather than adopt the output.
	return bounded.Err()
}
func sealInitializedDirectory(fd int) bool {
	var before, after unix.Stat_t
	if unix.Fstat(fd, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFDIR || before.Uid != 0 || before.Mode&07777 != 0700 {
		return false
	}
	return unix.Fchown(fd, JobProcessUID, -1) == nil && unix.Fchmod(fd, 0700) == nil && unix.Fsync(fd) == nil && unix.Fstat(fd, &after) == nil && after.Dev == before.Dev && after.Ino == before.Ino && after.Mode&07777 == 0700 && after.Uid == JobProcessUID
}

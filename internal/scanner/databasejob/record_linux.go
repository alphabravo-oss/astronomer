//go:build linux

package databasejob

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

// PublishPreparation publishes comparison DATA only. PrepareCache supplies
// actual opaque update/cache owners before this call; this exported serializer
// cannot grant authentication from caller-created DATA. The private root must already exist;
// no path/adoption/network authority is derived from record DATA.
func PublishPreparation(ctx context.Context, rootPath string, value Preparation) error {
	if ctx == nil || value.validate() != nil {
		return errors.New("database scan preparation publication invalid")
	}
	payload, err := json.Marshal(value)
	if err != nil || len(payload) > MaximumRecordBytes {
		return errors.New("database scan preparation encoding failed")
	}
	return publishOwnedRecord(ctx, rootPath, "prepare.json", value.ClaimID, payload)
}
func publishInitialMeasurement(ctx context.Context, rootPath string, value Preparation, snapshot distribution.DatabaseCacheSnapshot, clock time.Time) error {
	row, err := measurementData(value, "initial", snapshot, clock)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(row)
	if err != nil || len(payload) > MaximumRecordBytes {
		return errors.New("database scan initial acknowledgement encoding failed")
	}
	return publishOwnedRecord(ctx, rootPath, "initial.json", value.ClaimID, payload)
}
func publishOwnedRecord(ctx context.Context, rootPath, name string, claimID uuid.UUID, payload []byte) error {
	if ctx == nil || (name != "prepare.json" && name != "initial.json") || len(payload) == 0 || len(payload) > MaximumRecordBytes || claimID.Version() != 4 || claimID.Variant() != uuid.RFC4122 {
		return errors.New("database scan owned record publication invalid")
	}
	root, before, ready, err := openPreparationRoot(ctx, rootPath)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("database scan preparation root unavailable")
	}
	defer root.Close()
	temporary := "." + name + "-" + claimID.String()
	fd, err := unix.Openat(int(root.Fd()), temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return errors.New("database scan preparation exclusive creation failed")
	}
	file := os.NewFile(uintptr(fd), "owned database scan preparation")
	defer file.Close()
	// Failures retain the exact owned partial original. No deletion can remove a
	// path replacement; a future Job must use a fresh volume/claim instead.
	if err := ctx.Err(); err != nil {
		return err
	}
	n, err := file.Write(payload)
	if err != nil || n != len(payload) || file.Sync() != nil || file.Chmod(0444) != nil {
		return errors.New("database scan preparation write failed")
	}
	var pinned, current unix.Stat_t
	if unix.Fstat(fd, &pinned) != nil || unix.Fstatat(int(root.Fd()), temporary, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || pinned.Dev != current.Dev || pinned.Ino != current.Ino || pinned.Mode&unix.S_IFMT != unix.S_IFREG || pinned.Nlink != 1 || int(pinned.Uid) != os.Geteuid() || pinned.Mode&07777 != 0444 || pinned.Size != int64(len(payload)) {
		return errors.New("database scan preparation file changed")
	}
	if err := validatePreparationRoot(rootPath, root, before); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Linux-only opt-in helper: RENAME_NOREPLACE cannot replace a record selected
	// by another init attempt, and readers never see partial record bytes.
	if unix.Renameat2(int(root.Fd()), temporary, int(root.Fd()), name, unix.RENAME_NOREPLACE) != nil || root.Sync() != nil {
		return errors.New("database scan preparation publication failed")
	}
	return validatePreparationRoot(rootPath, root, before)
}

func readPreparation(ctx context.Context, rootPath string) (Preparation, bool, error) {
	payload, ready, err := readOwnedRecord(ctx, rootPath, "prepare.json")
	if err != nil || !ready {
		return Preparation{}, false, err
	}
	value, err := DecodePreparation(payload)
	return value, err == nil, err
}
func readOwnedRecord(ctx context.Context, rootPath, name string) ([]byte, bool, error) {
	if name != "prepare.json" && name != "initial.json" {
		return nil, false, errors.New("database scan record name outside owned set")
	}
	root, before, ready, err := openPreparationRoot(ctx, rootPath)
	if err != nil || !ready {
		return nil, false, err
	}
	defer root.Close()
	fd, err := unix.Openat(int(root.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errors.New("database scan preparation read open failed")
	}
	file := os.NewFile(uintptr(fd), "owned database scan preparation")
	defer file.Close()
	var pinned unix.Stat_t
	if unix.Fstat(fd, &pinned) != nil || pinned.Mode&unix.S_IFMT != unix.S_IFREG || pinned.Mode&07777 != 0444 || int(pinned.Uid) != os.Geteuid() || pinned.Nlink != 1 || pinned.Size <= 0 || pinned.Size > MaximumRecordBytes {
		return nil, false, errors.New("database scan preparation file ownership invalid")
	}
	opened, err := file.Stat()
	if err != nil {
		return nil, false, errors.New("database scan preparation stat failed")
	}
	payload, err := io.ReadAll(io.LimitReader(file, MaximumRecordBytes+1))
	if err != nil {
		return nil, false, errors.New("database scan preparation read failed")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	after, err := file.Stat()
	var entry unix.Stat_t
	if err != nil || !os.SameFile(opened, after) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) || len(payload) != int(opened.Size()) || unix.Fstatat(int(root.Fd()), name, &entry, unix.AT_SYMLINK_NOFOLLOW) != nil || entry.Dev != pinned.Dev || entry.Ino != pinned.Ino || entry.Mode&unix.S_IFMT != unix.S_IFREG || entry.Mode&07777 != 0444 || entry.Nlink != 1 {
		return nil, false, errors.New("database scan preparation file changed while reading")
	}
	if err := validatePreparationRoot(rootPath, root, before); err != nil {
		return nil, false, err
	}
	return payload, true, nil
}

func openPreparationRoot(ctx context.Context, path string) (*os.File, os.FileInfo, bool, error) {
	if ctx == nil || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, nil, false, errors.New("database scan preparation root invalid")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, false, err
	}
	before, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, false, nil
	}
	if err != nil || !before.IsDir() || before.Mode().Perm() != 0700 {
		return nil, nil, false, errors.New("database scan preparation root ownership invalid")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, false, errors.New("database scan preparation root open failed")
	}
	file := os.NewFile(uintptr(fd), "owned database scan preparation root")
	var current unix.Stat_t
	if unix.Fstat(fd, &current) != nil || current.Mode&unix.S_IFMT != unix.S_IFDIR || current.Mode&07777 != 0700 || int(current.Uid) != os.Geteuid() || validatePreparationRoot(path, file, before) != nil {
		file.Close()
		return nil, nil, false, errors.New("database scan preparation root changed")
	}
	return file, before, true, nil
}
func validatePreparationRoot(path string, file *os.File, before os.FileInfo) error {
	opened, err := file.Stat()
	current, currentError := os.Lstat(path)
	if err != nil || currentError != nil || !current.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, current) || opened.Mode().Perm() != 0700 || current.Mode().Perm() != 0700 {
		return errors.New("database scan preparation root changed")
	}
	return nil
}

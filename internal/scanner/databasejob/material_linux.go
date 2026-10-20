//go:build linux

package databasejob

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/pkg/safeconv"

	"golang.org/x/sys/unix"
)

type projectedMaterialMember struct {
	name, relative string
	maximum        int64
}

func projectedPreparationMembers(privatePull bool) []projectedMaterialMember {
	result := []projectedMaterialMember{
		{"release.json", "release.json", 1 << 20},
		{"mirror-plan.json", "mirror-plan.json", 1 << 20},
		{"mirror-signature.json", "mirror-signature.json", 1 << 20},
		{"trusted_root.json", "trust/trusted_root.json", 1 << 20},
		{"mirror.pub", "trust/mirror.pub", 1 << 20},
		{"registry.pem", "trust/registry.pem", 256 << 10},
	}
	if privatePull {
		result = append(result, projectedMaterialMember{"pull.json", "trust/pull.json", 32 << 10})
	}
	return result
}
func copyProjectedPreparationMaterial(ctx context.Context, projected, destination string, uid int, privatePull bool, copied func()) error {
	return copyProjectedPreparationMaterialWithMapping(ctx, projected, destination, uid, privatePull, false, copied)
}
func copyProjectedPreparationMaterialWithMapping(ctx context.Context, projected, destination string, uid int, privatePull, mappedRelease bool, copied func()) error {
	if ctx == nil || os.Geteuid() != 0 || uid < 0 || uid > 65534 || !filepath.IsAbs(projected) || filepath.Clean(projected) != projected || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination || projected == destination {
		return errors.New("owned Job material bootstrap inputs invalid")
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := bounded.Err(); err != nil {
		return err
	}
	source, before, err := openRootProjection(projected)
	if err != nil {
		return err
	}
	defer func(close func() error) { _ = close() }(source.Close)
	target, targetBefore, err := openRootProjection(destination)
	if err != nil {
		return err
	}
	defer func(close func() error) { _ = close() }(target.Close)
	names, err := target.Readdirnames(1)
	if err != io.EOF || len(names) != 0 {
		return errors.New("owned Job material target not empty")
	}
	epochName, err := projectionEpoch(int(source.Fd()))
	if err != nil {
		return err
	}
	epochFD, err := unix.Openat(int(source.Fd()), epochName, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("owned Job projection epoch unavailable")
	}
	epoch := os.NewFile(uintptr(epochFD), "owned Job projection epoch")
	defer func(close func() error) { _ = close() }(epoch.Close)
	var epochBefore unix.Stat_t
	if unix.Fstat(epochFD, &epochBefore) != nil || epochBefore.Mode&unix.S_IFMT != unix.S_IFDIR || epochBefore.Uid != 0 || epochBefore.Mode&07777 != 0755 {
		return errors.New("owned Job projection epoch owner invalid")
	}
	members := projectedPreparationMembers(privatePull)
	if mappedRelease {
		members = append(members, projectedMaterialMember{"release-mapping.json", "release-mapping.json", 1 << 20})
	}
	allowed := map[string]bool{}
	for _, member := range members {
		allowed[member.name] = true
	}
	actual, err := epoch.Readdirnames(len(members) + 1)
	if err != io.EOF && err != nil || len(actual) != len(members) {
		return errors.New("owned Job projected member set differs")
	}
	for _, name := range actual {
		if !allowed[name] {
			return errors.New("owned Job projected member outside closed set")
		}
	}
	if err := unix.Mkdirat(int(target.Fd()), "trust", 0700); err != nil {
		return errors.New("owned Job private trust creation failed")
	}
	trustFD, err := unix.Openat(int(target.Fd()), "trust", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("owned Job private trust unavailable")
	}
	trust := os.NewFile(uintptr(trustFD), "owned Job private trust")
	defer func(close func() error) { _ = close() }(trust.Close)
	for _, member := range members {
		if err := bounded.Err(); err != nil {
			return err
		}
		out, name := target, member.relative
		if strings.HasPrefix(name, "trust/") {
			out, name = trust, strings.TrimPrefix(name, "trust/")
		}
		if err := copyProjectedMember(bounded, epoch, source, epochName, out, name, member, uid); err != nil {
			return err
		}
		if copied != nil {
			copied()
		}
	}
	if err := bounded.Err(); err != nil {
		return err
	}
	var epochAfter unix.Stat_t
	if unix.Fstat(epochFD, &epochAfter) != nil || epochBefore.Dev != epochAfter.Dev || epochBefore.Ino != epochAfter.Ino || epochBefore.Mode != epochAfter.Mode || epochBefore.Uid != epochAfter.Uid || epochBefore.Mtim != epochAfter.Mtim {
		return errors.New("owned Job projection epoch changed")
	}
	if name, err := projectionEpoch(int(source.Fd())); err != nil || name != epochName {
		return errors.New("owned Job projection rotated during bootstrap")
	}
	if err := rootProjectionUnchanged(projected, source, before); err != nil {
		return err
	}
	if err := rootProjectionUnchanged(destination, target, targetBefore); err != nil {
		return err
	}
	if unix.Fchown(trustFD, uid, -1) != nil || unix.Fchmod(trustFD, 0700) != nil || trust.Sync() != nil || unix.Fchown(int(target.Fd()), uid, -1) != nil || unix.Fchmod(int(target.Fd()), 0700) != nil || target.Sync() != nil {
		return errors.New("owned Job private root sealing failed")
	}

	var sealedRoot, namedRoot, sealedTrust, namedTrust unix.Stat_t
	if unix.Fstat(int(target.Fd()), &sealedRoot) != nil || unix.Lstat(destination, &namedRoot) != nil || sealedRoot.Dev != targetBefore.Dev || sealedRoot.Ino != targetBefore.Ino || !projectedMaterialStatSame(sealedRoot, namedRoot) || sealedRoot.Mode&07777 != 0700 || sealedRoot.Uid != uint32(uid) || unix.Fstat(trustFD, &sealedTrust) != nil || unix.Fstatat(int(target.Fd()), "trust", &namedTrust, unix.AT_SYMLINK_NOFOLLOW) != nil || !projectedMaterialStatSame(sealedTrust, namedTrust) || sealedTrust.Mode&unix.S_IFMT != unix.S_IFDIR || sealedTrust.Mode&07777 != 0700 || sealedTrust.Uid != uint32(uid) {
		return errors.New("owned Job sealed root identity changed")
	}
	// All reads/writes are synchronous. Failures retain owned partial files on a
	// fresh volume; bootstrap never adopts/overwrites/deletes an existing target.
	return bounded.Err()
}
func openRootProjection(path string) (*os.File, unix.Stat_t, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, unix.Stat_t{}, errors.New("owned Job material root unavailable")
	}
	file := os.NewFile(uintptr(fd), "owned Job material root")
	var before unix.Stat_t
	if unix.Fstat(fd, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFDIR || before.Uid != 0 || before.Mode&07777 != 0755 {
		_ = file.Close()
		return nil, unix.Stat_t{}, errors.New("owned Job material root owner invalid")
	}
	return file, before, nil
}
func projectionEpoch(fd int) (string, error) {
	buffer := make([]byte, 256)
	count, err := unix.Readlinkat(fd, "..data", buffer)
	if err != nil || count <= 0 || count >= len(buffer) {
		return "", errors.New("owned Job projection epoch invalid")
	}
	name := string(buffer[:count])
	if !strings.HasPrefix(name, "..20") || strings.ContainsAny(name, "/\\\x00") || name == ".." {
		return "", errors.New("owned Job projection epoch outside literal directory")
	}
	return name, nil
}
func rootProjectionUnchanged(path string, file *os.File, before unix.Stat_t) error {
	var pinned, current unix.Stat_t
	if unix.Fstat(int(file.Fd()), &pinned) != nil || unix.Lstat(path, &current) != nil || pinned.Dev != before.Dev || pinned.Ino != before.Ino || pinned.Mode != before.Mode || pinned.Uid != before.Uid || current.Dev != before.Dev || current.Ino != before.Ino || current.Mode != before.Mode || current.Uid != before.Uid {
		return errors.New("owned Job material root changed")
	}
	return nil
}
func copyProjectedMember(ctx context.Context, epoch, source *os.File, epochName string, target *os.File, name string, member projectedMaterialMember, uid int) error {
	fd, err := unix.Openat(int(epoch.Fd()), member.name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return errors.New("owned Job projected material unavailable")
	}
	input := os.NewFile(uintptr(fd), "owned Job projected material")
	defer func(close func() error) { _ = close() }(input.Close)
	var before unix.Stat_t
	if unix.Fstat(fd, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Uid != 0 || before.Mode&07777 != 0444 || before.Nlink != 1 || before.Size <= 0 || before.Size > member.maximum {
		return errors.New("owned Job projected material owner or bounds invalid")
	}
	payload, err := io.ReadAll(io.LimitReader(workloadContextReader{ctx: ctx, reader: input}, member.maximum+1))
	defer clear(payload)
	var after, current unix.Stat_t
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || int64(len(payload)) != before.Size || unix.Fstat(fd, &after) != nil || unix.Fstatat(int(epoch.Fd()), member.name, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || !projectedMaterialStatSame(after, before) || !projectedMaterialStatSame(current, before) {
		return errors.New("owned Job projected material changed")
	}
	if actual, err := projectionEpoch(int(source.Fd())); err != nil || actual != epochName {
		return errors.New("owned Job projection rotated during copy")
	}
	outFD, err := unix.Openat(int(target.Fd()), name, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return errors.New("owned Job material exclusive output failed")
	}
	output := os.NewFile(uintptr(outFD), "owned Job private material")
	defer func(close func() error) { _ = close() }(output.Close)
	copied, err := output.Write(payload)
	if err != nil || copied != len(payload) || output.Sync() != nil || unix.Fchown(outFD, uid, -1) != nil || unix.Fchmod(outFD, 0400) != nil {
		return errors.New("owned Job material copy failed")
	}
	var sealed, entry unix.Stat_t
	if unix.Fstat(outFD, &sealed) != nil || unix.Fstatat(int(target.Fd()), name, &entry, unix.AT_SYMLINK_NOFOLLOW) != nil || !projectedMaterialStatSame(sealed, entry) || sealed.Mode&unix.S_IFMT != unix.S_IFREG || sealed.Mode&07777 != 0400 || sealed.Uid != safeconv.Uint32(uid) || sealed.Nlink != 1 || sealed.Size != before.Size {
		return errors.New("owned Job private material owner changed")
	}
	readback, err := io.ReadAll(io.LimitReader(io.NewSectionReader(output, 0, member.maximum+1), member.maximum+1))
	defer clear(readback)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || len(readback) != len(payload) || sha256.Sum256(readback) != sha256.Sum256(payload) {
		return errors.New("owned Job private material readback changed")
	}
	var final unix.Stat_t
	if unix.Fstat(outFD, &final) != nil || !projectedMaterialStatSame(final, sealed) {
		return errors.New("owned Job private material changed during readback")
	}
	return nil
}

func projectedMaterialStatSame(left, right unix.Stat_t) bool {
	// atime is intentionally excluded: an authenticated read may update it.
	return left.Dev == right.Dev && left.Ino == right.Ino && left.Mode == right.Mode && left.Uid == right.Uid && left.Gid == right.Gid && left.Nlink == right.Nlink && left.Size == right.Size && left.Mtim == right.Mtim && left.Ctim == right.Ctim
}

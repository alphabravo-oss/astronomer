//go:build linux

package upgradecli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"syscall"
)

// toolkitExecutableInput is separate from the public metadata reader. Only
// regular current-user executable0700/0755 files are admitted; this reader does
// not execute them or grant signing authority. Build info is parsed from these
// exact bounded retained bytes, rather than reopening an unbound pathname.
type toolkitExecutableInput struct {
	path    string
	maximum int64
	bound   metadataInput
}

func sameToolkitExecutable(before, after os.FileInfo) bool {
	if before == nil || after == nil {
		return false
	}
	mode := before.Mode().Perm()
	return (mode == 0700 || mode == 0755) && before.Mode().IsRegular() && after.Mode().IsRegular() && before.Mode() == after.Mode() && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) && before.Sys().(*syscall.Stat_t).Uid == uint32(os.Getuid()) && after.Sys().(*syscall.Stat_t).Uid == uint32(os.Getuid()) && before.Sys().(*syscall.Stat_t).Nlink == 1 && after.Sys().(*syscall.Stat_t).Nlink == 1
}

func readToolkitExecutable(ctx context.Context, path string, maximum int64) ([]byte, toolkitExecutableInput, error) {
	var empty toolkitExecutableInput
	if ctx == nil || !filepath.IsAbs(path) || maximum <= 0 || maximum > toolkitExecutableMaximum {
		return nil, empty, errors.New("exact bounded toolkit executable input required")
	}
	if err := ctx.Err(); err != nil {
		return nil, empty, err
	}
	parent := filepath.Dir(path)
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != parent {
		return nil, empty, errors.New("toolkit executable directory alias forbidden")
	}
	before, err := os.Lstat(path)
	if err != nil || !sameToolkitExecutable(before, before) || before.Size() <= 0 || before.Size() > maximum {
		return nil, empty, errors.New("toolkit executable mode/owner/budget differs")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, empty, errors.New("open toolkit executable input")
	}
	file := os.NewFile(uintptr(fd), "toolkit-executable-input")
	defer func(close func() error) { _ = close() }(file.Close)
	opened, err := file.Stat()
	if err != nil || !sameToolkitExecutable(before, opened) {
		return nil, empty, errors.New("opened toolkit executable identity differs")
	}
	raw := make([]byte, 0, before.Size())
	buffer := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, empty, err
		}
		n, readErr := file.Read(buffer)
		if int64(n) > maximum-int64(len(raw)) {
			return nil, empty, errors.New("toolkit executable exceeds byte bound")
		}
		raw = append(raw, buffer[:n]...)
		if readErr == io.EOF {
			break
		}
		if readErr != nil || n == 0 {
			return nil, empty, errors.New("read toolkit executable input")
		}
	}
	after, err := file.Stat()
	named, namedErr := os.Lstat(path)
	if err != nil || namedErr != nil || !sameToolkitExecutable(before, after) || !sameToolkitExecutable(before, named) || int64(len(raw)) != before.Size() {
		return nil, empty, errors.New("toolkit executable changed while reading")
	}
	if err := ctx.Err(); err != nil {
		return nil, empty, err
	}
	return raw, toolkitExecutableInput{path, maximum, metadataInput{before, sha256.Sum256(raw)}}, nil
}

func recheckToolkitExecutable(ctx context.Context, initial toolkitExecutableInput) error {
	_, after, err := readToolkitExecutable(ctx, initial.path, initial.maximum)
	if err != nil {
		return err
	}
	if !sameToolkitExecutable(initial.bound.identity, after.bound.identity) || initial.bound.hash != after.bound.hash {
		return errors.New("initial toolkit executable changed")
	}
	return ctx.Err()
}

func toolkitExecutablePlatform(raw []byte, source string) (string, error) {
	info, err := buildinfo.Read(bytes.NewReader(raw))
	if err != nil {
		return "", errors.New("toolkit executable build information unavailable")
	}
	return validateToolkitBuildInfo(info, source)
}

func validateToolkitBuildInfo(info *debug.BuildInfo, source string) (string, error) {
	if info == nil || !bootstrapSourceCommit.MatchString(source) || info.Path != "github.com/alphabravocompany/astronomer-go/cmd/upgrade-release" || info.Main.Path != "github.com/alphabravocompany/astronomer-go" {
		return "", errors.New("toolkit executable is not the canonical upgrade-release command")
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision", "vcs.modified", "GOOS", "GOARCH":
			if _, duplicate := settings[setting.Key]; duplicate {
				return "", errors.New("toolkit executable identity settings are ambiguous")
			}
			settings[setting.Key] = setting.Value
		}
	}
	if settings["vcs.revision"] != source || settings["vcs.modified"] != "false" || settings["GOOS"] != "linux" || (settings["GOARCH"] != "amd64" && settings["GOARCH"] != "arm64") {
		return "", errors.New("toolkit executable clean VCS source/platform differs")
	}
	return "linux/" + settings["GOARCH"], nil
}

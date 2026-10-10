//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// PublishedReport records the filesystem acknowledgement separately from Run.
// A link may be published even if a later directory sync/cancellation fails.
// Report publication never changes the retained upgrade result or authorizes
// replay, replacement, rollback, cleanup of backup files or another command.
type PublishedReport struct {
	Path      string
	SHA256    string
	SizeBytes int64
	Published bool
}

// PublishSanitizedReport preserves the shell's release-timestamp-tag.json name,
// 0755 public directory and 0644 complete report. The caller supplies only the
// explicit existing directory; all public bytes and identities derive from the
// completed invocation. Publication is exclusive, never an overwrite. Failed
// private partials remain named .partial; no complete public .json is linked
// until the bounded bytes have been written and synced. Filesystem I/O itself
// is not claimed interruptible; context is checked at every transition.
func (i *Invocation) PublishSanitizedReport(ctx context.Context, directory string) (PublishedReport, error) {
	raw, err := i.SanitizedReport(ctx)
	if err != nil {
		return PublishedReport{}, err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed || !i.completed {
		return PublishedReport{}, errors.New("completed invocation closed before public publication")
	}
	bounded, cancel := context.WithTimeout(ctx, i.plan.CleanupTimeout)
	defer cancel()
	if !kubeName.MatchString(i.plan.Identity.Release) || i.plan.Validate() != nil {
		return PublishedReport{}, errors.New("validated release identity required for public filename")
	}
	name := i.plan.Identity.Release + "-" + time.Now().UTC().Format("20060102T150405Z") + "-" + i.plan.Target + ".json"
	return publishPublicReport(bounded, directory, name, raw)
}

func publishPublicReport(ctx context.Context, directory, name string, raw []byte) (result PublishedReport, returned error) {
	if ctx == nil || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || filepath.Base(name) != name || name == "." || name == ".." || len(name) > 240 || len(raw) == 0 || len(raw) > 16<<10 {
		return result, errors.New("explicit bounded public report path and bytes required")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	before, err := os.Lstat(directory)
	if err != nil || !before.IsDir() || before.Mode().Perm() != 0755 || before.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return result, errors.New("owned public directory must already exist with mode0755")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != directory {
		return result, errors.New("public evidence directory aliases another path")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return result, errors.New("open owned public evidence directory")
	}
	defer root.Close()
	parentStable := func() error {
		opened, e := root.Stat(".")
		named, n := os.Lstat(directory)
		if e != nil || n != nil || !os.SameFile(before, opened) || !os.SameFile(before, named) || named.Mode().Perm() != 0755 || named.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
			return errors.New("public evidence directory identity changed")
		}
		return ctx.Err()
	}
	if err := parentStable(); err != nil {
		return result, err
	}
	partial := name + ".partial"
	file, err := root.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return result, errors.New("exclusive public report staging path unavailable")
	}
	result.Path = filepath.Join(directory, partial)
	defer func() {
		if e := file.Close(); e != nil && returned == nil {
			returned = errors.New("close public report file")
		}
	}()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Mode().Perm() != 0600 || opened.Sys().(*syscall.Stat_t).Nlink != 1 || opened.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return result, errors.New("public report staging identity differs")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	n, err := file.Write(raw)
	result.SizeBytes = int64(n)
	if err != nil || n != len(raw) {
		return result, errors.New("write bounded public report staging bytes")
	}
	hash := sha256.Sum256(raw)
	result.SHA256 = "sha256:" + hex.EncodeToString(hash[:])
	if err := parentStable(); err != nil {
		return result, err
	}
	if err := file.Sync(); err != nil {
		return result, errors.New("sync complete report staging bytes")
	}
	if err := parentStable(); err != nil {
		return result, err
	}
	named, err := root.Lstat(partial)
	current, statErr := file.Stat()
	if err != nil || statErr != nil || !os.SameFile(opened, named) || !os.SameFile(opened, current) || current.Size() != int64(len(raw)) || named.Sys().(*syscall.Stat_t).Nlink != 1 || current.Mode().Perm() != 0600 {
		return result, errors.New("public report staging identity changed before publication")
	}
	if err := file.Chmod(0644); err != nil {
		return result, errors.New("set complete public report mode")
	}
	if err := file.Sync(); err != nil {
		return result, errors.New("sync public report mode")
	}
	if err := parentStable(); err != nil {
		return result, err
	}
	if err := root.Link(partial, name); err != nil {
		return result, errors.New("exclusive public report destination unavailable")
	}
	result.Path = filepath.Join(directory, name)
	result.Published = true
	if err := root.Remove(partial); err != nil {
		return result, errors.New("published report retained with staging alias")
	}
	dir, err := root.Open(".")
	if err != nil {
		return result, errors.New("open public directory for durability acknowledgement")
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil || closeErr != nil {
		return result, errors.New("published report directory durability acknowledgement failed")
	}
	if err := parentStable(); err != nil {
		return result, err
	}
	return result, nil
}

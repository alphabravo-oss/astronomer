//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/alphabravocompany/astronomer-go/internal/ops/process"
)

// ArtifactPolicy is an explicit operator byte budget. It does not grant database
// or release mutation authority. The local preflight reserves the maintained
// default 20 GiB/20% after the maximum capture, without relaxing a later guard.
// Other host writers can consume space after this observation; the enclosing
// maintained runner still owns continuous capacity cancellation.
type ArtifactPolicy struct{ MaximumBytes int64 }

// CaptureArtifact retains bulk stdout only in an exclusive private file. It
// independently reopens and streams the hash after joined success; no backup is
// read into memory. A failed/partial artifact remains in Result for recovery.
// Validation acknowledges retained bytes, not archive readability/restorability.
func (c *Commands) CaptureArtifact(ctx context.Context, tool Tool, args []string, policy ArtifactPolicy) (*process.ArtifactCapture, process.Result, error) {
	if c == nil || ctx == nil {
		return nil, process.Result{}, errors.New("explicit artifact composition and context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, process.Result{}, err
	}
	if policy.MaximumBytes <= 0 {
		return nil, process.Result{}, errors.New("positive explicit artifact byte budget required")
	}
	if tool.Kind != Kubectl {
		return nil, process.Result{}, errors.New("bulk capture requires the bound database command adapter")
	}
	c.mu.Lock()
	_, verified := c.verified[tool]
	c.mu.Unlock()
	if !verified {
		return nil, process.Result{}, errors.New("actual tool version preflight required")
	}
	if c.artifactCapacity == nil {
		return nil, process.Result{}, errors.New("explicit artifact capacity composition required")
	}
	if err := c.artifactCapacity(c.options.CaptureDirectory, policy.MaximumBytes); err != nil {
		return nil, process.Result{}, err
	}
	_, result, err := c.executeWithOutput(ctx, tool, args, process.ArtifactOutput{MaximumBytes: policy.MaximumBytes})
	if err != nil {
		return nil, result, err
	}
	captured := *result.StdoutArtifact
	return &captured, result, nil
}

func artifactCapacity(directory string, maximum int64) error {
	if maximum <= 0 {
		return errors.New("positive explicit artifact byte budget required")
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(directory, &fs); err != nil || fs.Bsize <= 0 || fs.Blocks == 0 || fs.Bavail > fs.Blocks {
		return errors.New("fresh artifact capacity observation unavailable")
	}
	// Validate multiplication before converting a kernel unsigned count to int64.
	const maxInt64 = uint64(1<<63 - 1)
	block := uint64(fs.Bsize)
	if fs.Blocks > maxInt64/block || fs.Bavail > maxInt64/block {
		return errors.New("artifact capacity arithmetic exceeds supported range")
	}
	return artifactReserve(int64(fs.Blocks*block), int64(fs.Bavail*block), maximum)
}

func artifactReserve(total, available, maximum int64) error {
	if total <= 0 || available < 0 || available > total || maximum <= 0 {
		return errors.New("invalid artifact capacity observation or budget")
	}
	reserve := int64(20 << 30)
	// Ceiling(total/5) preserves at least20% rather than rounding down a guard.
	fifth := total / 5
	if total%5 != 0 {
		fifth++
	}
	if fifth > reserve {
		reserve = fifth
	}
	if available < reserve || maximum > available-reserve {
		return errors.New("artifact capture would exhaust maintained capacity reserve")
	}
	return nil
}

func verifyArtifactCapture(ctx context.Context, directory, name string, maximum int64, result process.Result) error {
	if ctx == nil {
		return errors.New("explicit artifact validation context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	metadata := result.StdoutArtifact
	path := filepath.Join(directory, name)
	if maximum <= 0 || filepath.Base(name) != name || metadata == nil || !metadata.Acknowledged || metadata.Path != path || metadata.SizeBytes < 0 || metadata.SizeBytes > maximum || metadata.SHA256 != result.StdoutSHA256 {
		return errors.New("joined artifact metadata differs")
	}
	parent, err := os.Lstat(directory)
	if err != nil || !parent.IsDir() || parent.Mode().Perm() != 0o700 || parent.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		return errors.New("private artifact parent differs")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != directory {
		return errors.New("private artifact parent aliases another path")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return errors.New("open private artifact scope")
	}
	defer func(close func() error) { _ = close() }(root.Close)
	currentParent, err := root.Stat(".")
	if err != nil || !os.SameFile(parent, currentParent) {
		return errors.New("private artifact parent identity changed")
	}
	before, err := root.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0o600 || before.Size() != metadata.SizeBytes || before.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) || before.Sys().(*syscall.Stat_t).Nlink != 1 {
		return errors.New("private artifact file identity differs")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("open private artifact capture")
	}
	defer func(close func() error) { _ = close() }(file.Close)
	opened, err := file.Stat()
	if err != nil || !sameArtifact(before, opened) {
		return errors.New("opened private artifact identity changed")
	}
	hash := sha256.New()
	buffer := make([]byte, 32<<10)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := file.Read(buffer)
		if int64(n) > maximum-size {
			return errors.New("private artifact byte budget exceeded")
		}
		size += int64(n)
		_, _ = hash.Write(buffer[:n])
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return errors.New("read private artifact capture")
		}
		if n == 0 {
			return errors.New("private artifact reader made no progress")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	after, err := file.Stat()
	if err != nil || !sameArtifact(before, after) {
		return errors.New("private artifact changed while hashing")
	}
	named, err := root.Lstat(name)
	if err != nil || !sameArtifact(before, named) {
		return errors.New("private artifact name identity changed")
	}
	if size != metadata.SizeBytes || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != metadata.SHA256 {
		return errors.New("private artifact content binding differs")
	}
	return ctx.Err()
}

func sameArtifact(before, after os.FileInfo) bool {
	return after != nil && after.Mode().IsRegular() && after.Mode().Perm() == 0o600 && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime()) && after.Sys().(*syscall.Stat_t).Uid == uint32(os.Getuid()) && after.Sys().(*syscall.Stat_t).Nlink == 1
}

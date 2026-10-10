//go:build linux

package upgradecli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// FreshBackupBudget derives a finite default from the actual available-space
// observation. An explicit positive override is admitted without clipping. The
// 512 MiB allowance leaves room for recovery captures and initial metadata;
// each later bulk capture still enforces its own fresh stronger reserve.
func FreshBackupBudget(directory string, requested int64) (int64, error) {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(directory, &fs); err != nil || fs.Bsize <= 0 || fs.Blocks == 0 || fs.Bavail > fs.Blocks {
		return 0, errors.New("fresh private backup capacity unavailable")
	}
	const maximum = uint64(1<<63 - 1)
	block := uint64(fs.Bsize)
	if fs.Blocks > maximum/block || fs.Bavail > maximum/block {
		return 0, errors.New("private backup capacity arithmetic exceeds supported range")
	}
	return backupBudget(int64(fs.Blocks*block), int64(fs.Bavail*block), requested)
}

func backupBudget(total, available, requested int64) (int64, error) {
	if total <= 0 || available <= 0 || available > total || requested < 0 {
		return 0, errors.New("invalid backup capacity or requested budget")
	}
	reserve := int64(20 << 30)
	fifth := total / 5
	if total%5 != 0 {
		fifth++
	}
	if fifth > reserve {
		reserve = fifth
	}
	const recoveryAllowance = int64(512 << 20)
	if available <= reserve || available-reserve <= recoveryAllowance {
		return 0, errors.New("backup has no capacity beyond protected reserve and recovery allowance")
	}
	maximum := available - reserve - recoveryAllowance
	if requested > maximum {
		return 0, errors.New("requested backup budget exceeds fresh admission")
	}
	if requested != 0 {
		return requested, nil
	}
	return maximum, nil
}

// createPrivateBootstrapParent preserves nested BACKUP_ROOT creation without
// following directory aliases. Every next directory is opened under an owned
// descriptor and compared with its observed entry before writes below it. New
// parents are private; existing ancestors are retained and never chmodded.
func createPrivateBootstrapParent(directory string) error {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return errors.New("absolute clean private upgrade parent required")
	}
	components := strings.Split(strings.TrimPrefix(directory, "/"), "/")
	if len(components) > 128 || directory == "/" {
		return errors.New("bounded private upgrade parent path required")
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return errors.New("open upgrade parent anchor")
	}
	defer func() { root.Close() }()
	for _, name := range components {
		before, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			if err := root.Mkdir(name, 0700); err != nil {
				return errors.New("create exclusive private upgrade parent")
			}
			before, err = root.Lstat(name)
		}
		if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
			return errors.New("upgrade parent alias or nondirectory forbidden")
		}
		next, err := root.OpenRoot(name)
		if err != nil {
			return errors.New("open upgrade parent component")
		}
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(before, opened) {
			next.Close()
			return errors.New("upgrade parent component identity changed")
		}
		root.Close()
		root = next
	}
	return privateBootstrapDirectory(directory)
}

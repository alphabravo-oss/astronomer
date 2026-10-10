package gitops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// WalkCheckout keeps every filesystem operation inside one opened repository
// directory. The callback receives the existing canonical repository path.
// Failure is never converted into an empty successful desired snapshot.
func WalkCheckout(ctx context.Context, directory, prefix string, visit func(string, []byte) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return errors.New("repository root absolute path unavailable")
	}
	before, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("repository root unreadable: %w", err)
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return errors.New("repository root must be a real directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return fmt.Errorf("open repository root: %w", err)
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil {
		return fmt.Errorf("stat opened repository root: %w", err)
	}
	current, err := os.Lstat(directory)
	if err != nil || !os.SameFile(before, opened) || !os.SameFile(before, current) {
		return errors.New("repository root changed during open")
	}
	// Preserve contained path_prefix aliases and canonical RepoPath identity.
	walkRoot, err := ResolvePathPrefix(directory, prefix)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("walk root unreadable (check path_prefix %q): %w", prefix, err)
		}
		return fmt.Errorf("invalid path_prefix %q: %w", prefix, err)
	}
	relative, err := filepath.Rel(directory, walkRoot)
	if err != nil || ValidatePathPrefix(relative) != nil {
		return errors.New("resolved path_prefix escapes repository root")
	}
	relative = filepath.ToSlash(relative)
	info, err := root.Stat(relative)
	if err != nil {
		return fmt.Errorf("walk root unreadable (check path_prefix %q): %w", prefix, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("walk root is not a directory (check path_prefix %q)", prefix)
	}
	walkErr := fs.WalkDir(root.FS(), relative, func(name string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return fmt.Errorf("walk %s: %w", name, walkErr)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		extension := strings.ToLower(filepath.Ext(name))
		if extension != ".yaml" && extension != ".yml" {
			return nil
		}
		enumerated, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", name, err)
		}
		content, err := readCheckoutFile(ctx, root, name, enumerated)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		return visit(name, content)
	})
	if err := ctx.Err(); err != nil {
		return err
	}
	return walkErr
}

func readCheckoutFile(ctx context.Context, root *os.Root, name string, enumerated os.FileInfo) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !enumerated.Mode().IsRegular() || enumerated.Size() < 0 || enumerated.Size() == math.MaxInt64 {
		return nil, errors.New("repository YAML is not a regular readable file")
	}
	file, err := openCheckoutRegular(root, name)
	if err != nil {
		return nil, errors.New("repository YAML no-follow open failed")
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return nil, errors.New("repository YAML descriptor stat failed")
	}
	if !checkoutSingleRegular(before) || !sameCheckoutFile(enumerated, before) {
		return nil, errors.New("repository YAML changed after enumeration")
	}
	// Preserve valid historical file sizes while refusing growth beyond the
	// captured descriptor size. This is not a new universal YAML size ceiling.
	content, err := io.ReadAll(io.LimitReader(file, before.Size()+1))
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		return nil, errors.New("repository YAML read failed")
	}
	after, err := file.Stat()
	if err != nil {
		return nil, errors.New("repository YAML final descriptor stat failed")
	}
	named, err := root.Lstat(name)
	if err != nil || !checkoutSingleRegular(after) || !sameCheckoutFile(before, after) || !checkoutSingleRegular(named) || !sameCheckoutFile(after, named) || int64(len(content)) != before.Size() {
		return nil, errors.New("repository YAML changed during read")
	}
	return content, nil
}

func sameCheckoutFile(before, after os.FileInfo) bool {
	return before != nil && after != nil && os.SameFile(before, after) && before.Mode() == after.Mode() && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

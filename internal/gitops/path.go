package gitops

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrInvalidPathPrefix = errors.New("gitops: invalid path_prefix")

// ValidatePathPrefix accepts a repository-relative directory and rejects any
// value that can escape the checked-out repository. Empty means the repo root.
func ValidatePathPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if filepath.IsAbs(prefix) {
		return fmt.Errorf("%w: must be relative to the repository root", ErrInvalidPathPrefix)
	}
	clean := filepath.Clean(prefix)
	if clean == "." {
		return nil
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: must stay within the repository root", ErrInvalidPathPrefix)
	}
	return nil
}

// ResolvePathPrefix returns a real path contained by root. Both lexical
// traversal and symlink escapes are rejected. The target must already exist;
// callers clone/fetch before resolving it.
func ResolvePathPrefix(root, prefix string) (string, error) {
	if err := ValidatePathPrefix(prefix); err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	candidate := rootAbs
	if clean := filepath.Clean(prefix); clean != "." && clean != "" {
		candidate = filepath.Join(rootAbs, clean)
	}
	if !pathContainedBy(rootAbs, candidate) {
		return "", fmt.Errorf("%w: must stay within the repository root", ErrInvalidPathPrefix)
	}

	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("resolve repository root symlinks: %w", err)
	}
	candidateReal, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return "", fmt.Errorf("resolve path_prefix symlinks: %w", err)
	}
	if !pathContainedBy(rootReal, candidateReal) {
		return "", fmt.Errorf("%w: symlink escapes the repository root", ErrInvalidPathPrefix)
	}
	return candidateReal, nil
}

func pathContainedBy(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

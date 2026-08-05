package contract

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func verifyChecksums(root string, manifest []byte) error {
	expected := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(manifest)))
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) != 2 || len(parts[0]) != sha256.Size*2 {
			return fmt.Errorf("invalid Charlie checksum manifest line %q", scanner.Text())
		}
		if _, err := hex.DecodeString(parts[0]); err != nil {
			return fmt.Errorf("invalid Charlie checksum %q: %w", parts[0], err)
		}
		clean := filepath.Clean(parts[1])
		if filepath.IsAbs(clean) || clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe Charlie checksum path %q", parts[1])
		}
		if _, duplicate := expected[clean]; duplicate {
			return fmt.Errorf("duplicate Charlie checksum path %q", clean)
		}
		expected[clean] = parts[0]
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	seen := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "pinned"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		want, exists := expected[rel]
		if !exists {
			return fmt.Errorf("unpinned Charlie contract file %q", rel)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		got := fmt.Sprintf("%x", sha256.Sum256(raw))
		if got != want {
			return fmt.Errorf("Charlie contract drift in %s: got %s want %s", rel, got, want)
		}
		seen[rel] = true
		return nil
	})
	if err != nil {
		return err
	}
	for name := range expected {
		if !seen[name] {
			return fmt.Errorf("missing pinned Charlie contract file %q", name)
		}
	}
	return nil
}

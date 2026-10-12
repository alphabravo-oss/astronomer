package tasks

import (
	"fmt"
	"os"
)

func protectCloneParent(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("mkdir clone parent: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("protect clone parent: %w", err)
	}
	return nil
}

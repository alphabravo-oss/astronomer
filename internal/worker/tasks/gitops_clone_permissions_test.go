package tasks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCloneParentProtectsNewAndExistingPrivateData(t *testing.T) {
	for _, existing := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "clones")
		if existing {
			if err := os.Mkdir(path, 0755); err != nil {
				t.Fatal(err)
			}
		}
		if err := protectCloneParent(path); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
			t.Fatalf("private traversable clone directory: info=%v err=%v", info, err)
		}
		if err := os.WriteFile(filepath.Join(path, "fixture"), []byte("private registration fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

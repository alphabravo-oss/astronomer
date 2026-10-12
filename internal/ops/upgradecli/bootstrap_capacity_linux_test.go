//go:build linux

package upgradecli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapBackupDefaultUsesFreshCapacityWithoutFixedArchiveCeiling(t *testing.T) {
	want := int64((10 << 30) - (512 << 20))
	got, err := backupBudget(100<<30, 30<<30, 0)
	if err != nil || got != want {
		t.Fatal("default budget does not preserve observed reserve and recovery allowance")
	}
	larger, err := backupBudget(100<<30, 40<<30, 0)
	if err != nil || larger <= got {
		t.Fatal("default archive size was clipped to an invented fixed ceiling")
	}
}
func TestBootstrapBackupExplicitBudgetIsNeverClipped(t *testing.T) {
	got, err := backupBudget(100<<30, 30<<30, 1<<30)
	if err != nil || got != 1<<30 {
		t.Fatal("explicit requested budget changed")
	}
	for _, value := range []int64{-1, 10 << 30} {
		if _, err := backupBudget(100<<30, 30<<30, value); err == nil {
			t.Fatal("invalid/over-budget request admitted")
		}
	}
}
func TestBootstrapBackupRejectsNoMarginAndRoundedPercentageBoundary(t *testing.T) {
	for _, available := range []int64{0, 20 << 30, (20 << 30) + (512 << 20)} {
		if _, err := backupBudget(100<<30, available, 0); err == nil {
			t.Fatal("default archive budget admitted without room")
		}
	}
	got, err := backupBudget((100<<30)+1, (30<<30)+1, 0)
	if err != nil || got != int64((10<<30)-(512<<20)) {
		t.Fatal("percentage reserve was rounded down or overflowed")
	}
}

func TestCreatePrivateBootstrapParentCreatesNestedPathWithoutChangingAncestor(t *testing.T) {
	ancestor := t.TempDir()
	if err := os.Chmod(ancestor, 0755); err != nil {
		t.Fatal(err)
	}
	selected := filepath.Join(ancestor, "new", "nested", "backups")
	if err := createPrivateBootstrapParent(selected); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(ancestor, "new"), filepath.Join(ancestor, "new", "nested"), selected} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("private created parent %s = %v %v", path, info, err)
		}
	}
	info, err := os.Stat(ancestor)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatal("existing public ancestor changed")
	}
	if err := createPrivateBootstrapParent(selected); err != nil {
		t.Fatal(err)
	}
}

func TestCreatePrivateBootstrapParentRejectsExistingAliasBeforeWritingBelowIt(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	alias := filepath.Join(directory, "alias")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	if err := createPrivateBootstrapParent(filepath.Join(alias, "backups")); err == nil {
		t.Fatal("alias admitted")
	}
	if _, err := os.Stat(filepath.Join(target, "backups")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("write occurred through rejected alias")
	}
}

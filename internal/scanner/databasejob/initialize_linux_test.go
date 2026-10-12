//go:build linux

package databasejob

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

func requireInitializationRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("owned init UID and mode mutation requires actual root; no nonroot qualification")
	}
}
func newInitializationRoot(t *testing.T, mode os.FileMode) string {
	t.Helper()
	path := t.TempDir()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}
func initializationStat(t *testing.T, path string, uid uint32, mode uint32) unix.Stat_t {
	t.Helper()
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		t.Fatal(err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Uid != uid || stat.Mode&07777 != mode {
		t.Fatalf("unexpected owned directory uid=%d mode=%o", stat.Uid, stat.Mode&07777)
	}
	return stat
}
func TestOwnedJobInitializationCreatesExclusiveNonrootClaimWithoutAdoptingRoot(t *testing.T) {
	requireInitializationRoot(t)
	path := newInitializationRoot(t, 0755)
	if err := os.Mkdir(filepath.Join(path, "lost+found"), 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(path, "lost+found", "preserve")
	if err := os.WriteFile(marker, []byte("existing filesystem metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	before := initializationStat(t, path, 0, 0755)
	claim := uuid.MustParse("5782d7dc-c1b4-4d1c-96bb-68c05af8a01e")
	if err := initializeClaimVolume(context.Background(), path, claim); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(path, "input-"+claim.String())
	initializationStat(t, root, JobProcessUID, 0700)
	for _, child := range []string{"cache", "staging", "workload"} {
		initializationStat(t, filepath.Join(root, child), JobProcessUID, 0700)
	}
	after := initializationStat(t, path, 0, 0755)
	if before.Dev != after.Dev || before.Ino != after.Ino {
		t.Fatal("filesystem root adopted or replaced")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "existing filesystem metadata" {
		t.Fatal("existing root content erased")
	}
	if err := initializeClaimVolume(context.Background(), path, claim); err == nil {
		t.Fatal("existing claim adopted")
	}
	for _, child := range []string{"cache", "staging", "workload"} {
		initializationStat(t, filepath.Join(root, child), JobProcessUID, 0700)
	}
}
func TestOwnedJobInitializationNormalizesOnlyNewEmptyDirectories(t *testing.T) {
	requireInitializationRoot(t)
	for _, target := range []struct {
		uid  int
		mode uint32
	}{{0, 0755}, {JobProcessUID, 0700}} {
		path := newInitializationRoot(t, 0777)
		if err := initializeEmptyVolume(context.Background(), path, target.uid, target.mode); err != nil {
			t.Fatal(err)
		}
		initializationStat(t, path, uint32(target.uid), target.mode)
	}
	nonempty := newInitializationRoot(t, 0777)
	marker := filepath.Join(nonempty, "keep")
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := initializeEmptyVolume(context.Background(), nonempty, JobProcessUID, 0700); err == nil {
		t.Fatal("existing volume normalized")
	}
	initializationStat(t, nonempty, 0, 0777)
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "preserve" {
		t.Fatal("existing volume changed")
	}
	for _, target := range []struct {
		uid  int
		mode uint32
	}{{1, 0700}, {JobProcessUID, 0777}, {0, 0700}} {
		path := newInitializationRoot(t, 0777)
		if err := initializeEmptyVolume(context.Background(), path, target.uid, target.mode); err == nil {
			t.Fatal("arbitrary owner/mode selected")
		}
		initializationStat(t, path, 0, 0777)
	}
}
func TestOwnedJobInitializationRefusesUnsafeIdentityAndPreservesCancellation(t *testing.T) {
	requireInitializationRoot(t)
	claim := uuid.New()
	for _, mode := range []os.FileMode{0700, 0777, 0755 | os.ModeSetgid} {
		path := newInitializationRoot(t, mode)
		if err := initializeClaimVolume(context.Background(), path, claim); err == nil {
			t.Fatal("unsafe PVC root accepted")
		}
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			t.Fatal("unsafe PVC root mutated")
		}
	}
	path := newInitializationRoot(t, 0755)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := initializeClaimVolume(context.Background(), link, claim); err == nil {
		t.Fatal("symlink root accepted")
	}
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if err := initializeClaimVolume(context.Background(), fifo, claim); err == nil {
		t.Fatal("nonregular root accepted")
	}
	if err := initializeClaimVolume(context.Background(), path, uuid.Nil); err == nil {
		t.Fatal("invalid claim accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := initializeClaimVolume(ctx, path, claim); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost")
	}
	if err := initializeEmptyVolume(ctx, path, JobProcessUID, 0700); !errors.Is(err, context.Canceled) {
		t.Fatal("empty-volume cancellation lost")
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed initialization wrote claim")
	}
	if err := initializeClaimVolume(nil, path, claim); err == nil { //nolint:staticcheck // Deliberately tests rejection of a missing context.
		t.Fatal("nil context accepted")
	}
}
func TestOwnedJobInitializationDetectsReplacedPinnedRoot(t *testing.T) {
	requireInitializationRoot(t)
	path := newInitializationRoot(t, 0755)
	file, before, err := openInitializationVolume(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func(close func() error) { _ = close() }(file.Close)
	saved := path + "-original"
	if err := os.Rename(path, saved); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(saved) })
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := checkInitializationLocation(path, file, before); err == nil {
		t.Fatal("same-owner replacement adopted")
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatal("replacement mutated")
	}
}

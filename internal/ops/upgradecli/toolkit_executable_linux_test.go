//go:build linux

package upgradecli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestToolkitExecutableReaderPreservesActualBuildModesAndInitialIdentity(t *testing.T) {
	for _, mode := range []os.FileMode{0700, 0755} {
		t.Run(mode.String(), func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "upgrade-release")
			body := []byte("bounded synthetic executable; never run")
			if err := os.WriteFile(path, body, mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
			raw, input, err := readToolkitExecutable(context.Background(), path, int64(len(body)))
			if err != nil || !bytes.Equal(raw, body) {
				t.Fatal("actual go-build mode refused by dedicated reader", err)
			}
			if err := recheckToolkitExecutable(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			if _, _, err := readOfflineReleaseInput(context.Background(), path, int64(len(body))); err == nil {
				t.Fatal("executable mode leaked into public metadata admission")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, body, mode); err != nil {
				t.Fatal(err)
			}
			if err := recheckToolkitExecutable(context.Background(), input); err == nil {
				t.Fatal("recreated same-name executable re-admitted")
			}
		})
	}
}

func TestToolkitExecutableReaderRejectsAliasesSpecialInputsBoundsAndCancellation(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo", "directory", "metadata-mode", "writable-executable", "overflow-bound", "nil-context", "canceled-read"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "upgrade-release")
			body := bytes.Repeat([]byte("x"), 96<<10)
			if err := os.WriteFile(path, body, 0700); err != nil {
				t.Fatal(err)
			}
			maximum := int64(len(body))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var owner = ctx
			switch kind {
			case "symlink":
				alias := filepath.Join(directory, "alias")
				if err := os.Symlink(path, alias); err != nil {
					t.Fatal(err)
				}
				path = alias
			case "hardlink":
				if err := os.Link(path, filepath.Join(directory, "second")); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Mkfifo(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "metadata-mode":
				if err := os.Chmod(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "writable-executable":
				if err := os.Chmod(path, 0777); err != nil {
					t.Fatal(err)
				}
			case "overflow-bound":
				maximum--
			case "nil-context":
				owner = nil
			case "canceled-read":
				owner = &toolkitCancelContext{Context: ctx, cancel: cancel, after: 3}
			}
			raw, input, err := readToolkitExecutable(owner, path, maximum)
			if err == nil || raw != nil || input.path != "" {
				t.Fatal("invalid executable published retained input", kind, err)
			}
			if kind == "canceled-read" && !errors.Is(err, context.Canceled) {
				t.Fatal("actual read cancellation identity lost", err)
			}
		})
	}
}

func TestToolkitExecutableBuildIdentityRequiresExactUpgradeCommand(t *testing.T) {
	source := strings.Repeat("a", 40)
	valid := func() *debug.BuildInfo {
		return &debug.BuildInfo{Path: "github.com/alphabravocompany/astronomer-go/cmd/upgrade-release", Main: debug.Module{Path: "github.com/alphabravocompany/astronomer-go"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: source}, {Key: "vcs.modified", Value: "false"}, {Key: "GOOS", Value: "linux"}, {Key: "GOARCH", Value: "amd64"}}}
	}
	platform, err := validateToolkitBuildInfo(valid(), source)
	if err != nil || platform != "linux/amd64" {
		t.Fatal(err)
	}
	for _, kind := range []string{"wrong-command", "wrong-module", "wrong-source", "modified", "wrong-os", "wrong-architecture", "duplicate-setting", "missing-setting"} {
		t.Run(kind, func(t *testing.T) {
			info := valid()
			switch kind {
			case "wrong-command":
				info.Path = "github.com/alphabravocompany/astronomer-go/cmd/server"
			case "wrong-module":
				info.Main.Path = "other/module"
			case "wrong-source":
				info.Settings[0].Value = strings.Repeat("b", 40)
			case "modified":
				info.Settings[1].Value = "true"
			case "wrong-os":
				info.Settings[2].Value = "darwin"
			case "wrong-architecture":
				info.Settings[3].Value = "386"
			case "duplicate-setting":
				info.Settings = append(info.Settings, info.Settings[0])
			case "missing-setting":
				info.Settings = info.Settings[:3]
			}
			if got, err := validateToolkitBuildInfo(info, source); err == nil || got != "" {
				t.Fatal("non-upgrade executable admitted", kind)
			}
		})
	}
	if _, err := toolkitExecutablePlatform([]byte("not a Go binary"), source); err == nil {
		t.Fatal("unparsed bytes granted executable identity")
	}
}

func TestOfflineReleaseMetadataPreservesPublic0644WithoutExecutableAdmission(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "release-manifest.json")
	body := []byte("public metadata fixture")
	if err := os.WriteFile(path, body, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	raw, input, err := readOfflineReleaseInput(context.Background(), path, int64(len(body)))
	if err != nil || !bytes.Equal(raw, body) {
		t.Fatal("supported public original mode rejected", err)
	}
	if err := recheckOfflineReleaseInputs(context.Background(), []offlineReleaseInput{input}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := recheckOfflineReleaseInputs(context.Background(), []offlineReleaseInput{input}); err == nil {
		t.Fatal("original metadata mode change re-admitted")
	}
}

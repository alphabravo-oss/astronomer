//go:build linux

package upgradecli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func publicDirectoryFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "public")
	if err := os.Mkdir(p, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPublishSanitizedReportDerivesLegacyFilenameAndCompletePublicBytes(t *testing.T) {
	invocation := sanitizedReportFixture(t)
	directory := publicDirectoryFixture(t)
	result, err := invocation.PublishSanitizedReport(context.Background(), directory)
	if err != nil || !result.Published {
		t.Fatal("public report was not acknowledged", err)
	}
	raw, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if result.SHA256 != "sha256:"+hex.EncodeToString(sum[:]) || result.SizeBytes != int64(len(raw)) {
		t.Fatal("published report byte binding differs")
	}
	info, err := os.Lstat(result.Path)
	if err != nil || info.Mode().Perm() != 0644 {
		t.Fatal("legacy public mode differs", err)
	}
	name := filepath.Base(result.Path)
	if !strings.HasPrefix(name, invocation.plan.Identity.Release+"-") || !strings.HasSuffix(name, "-"+invocation.plan.Target+".json") {
		t.Fatal("legacy filename identity differs")
	}
	if _, err := os.Stat(result.Path + ".partial"); !os.IsNotExist(err) {
		t.Fatal("successful publication left staging alias")
	}
	if _, err := os.Stat(invocation.driver.backup.inventory.Path); err != nil {
		t.Fatal("publication removed retained backup")
	}
}

func TestPublicReportPublicationNeverOverwritesExistingOrAliasedDestination(t *testing.T) {
	for _, mode := range []string{"existing", "symlink", "hardlink", "partial"} {
		t.Run(mode, func(t *testing.T) {
			directory := publicDirectoryFixture(t)
			name := "owned-20261010T000000Z-v1.2.3.json"
			path := filepath.Join(directory, name)
			retained := filepath.Join(t.TempDir(), "retained")
			original := []byte("original retained bytes\n")
			if err := os.WriteFile(retained, original, 0600); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "existing":
				if err := os.WriteFile(path, original, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(retained, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(retained, path); err != nil {
					t.Fatal(err)
				}
			case "partial":
				if err := os.Symlink(retained, path+".partial"); err != nil {
					t.Fatal(err)
				}
			}
			result, err := publishPublicReport(context.Background(), directory, name, []byte("sanitized complete bytes\n"))
			if err == nil || result.Published {
				t.Fatal("existing namespace overwritten")
			}
			raw, err := os.ReadFile(retained)
			if err != nil || string(raw) != string(original) {
				t.Fatal("unrelated retained target changed")
			}
			if mode == "existing" {
				raw, err := os.ReadFile(path)
				if err != nil || string(raw) != string(original) {
					t.Fatal("existing public original changed")
				}
			}
		})
	}
}

func TestPublicReportPublicationRejectsUnsafeScopeAndCanceledContextBeforeCreation(t *testing.T) {
	for _, mode := range []string{"relative", "private-mode", "symlink-parent", "canceled", "traversal", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			directory := publicDirectoryFixture(t)
			name := "owned.json"
			raw := []byte("sanitized bytes")
			ctx := context.Background()
			switch mode {
			case "relative":
				directory = "relative-public"
			case "private-mode":
				if err := os.Chmod(directory, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink-parent":
				alias := filepath.Join(t.TempDir(), "alias")
				if err := os.Symlink(directory, alias); err != nil {
					t.Fatal(err)
				}
				directory = alias
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "traversal":
				name = "../escape.json"
			case "oversized":
				raw = make([]byte, 16<<10+1)
			}
			result, err := publishPublicReport(ctx, directory, name, raw)
			if err == nil || result.Published || result.Path != "" {
				t.Fatal("unsafe publication namespace admitted")
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatal("publication cancellation identity lost")
			}
		})
	}
}

func TestPublishSanitizedReportCannotInventReportFromUnfinishedInvocation(t *testing.T) {
	invocation := sanitizedReportFixture(t)
	invocation.completed = false
	directory := publicDirectoryFixture(t)
	if result, err := invocation.PublishSanitizedReport(context.Background(), directory); err == nil || result.Published || result.Path != "" {
		t.Fatal("unfinished invocation published")
	}
	rows, err := os.ReadDir(directory)
	if err != nil || len(rows) != 0 {
		t.Fatal("failed admission left public file")
	}
}

// Err observes the real public path at each production cancellation boundary.
// This synchronously cancels only after the actual exclusive Link succeeds;
// it needs no polling goroutine, sleeps, production hook or guessed call count.
type publicationObservedContext struct {
	context.Context
	cancel   context.CancelFunc
	path     string
	observed bool
}

func (c *publicationObservedContext) Err() error {
	if _, err := os.Lstat(c.path); err == nil {
		c.observed = true
		c.cancel()
	}
	return c.Context.Err()
}

func TestPublicReportPublicationRetainsAcknowledgementAfterObservedCancellation(t *testing.T) {
	invocation := sanitizedReportFixture(t)
	directory := publicDirectoryFixture(t)
	runResult := invocation.lastResult
	raw, err := invocation.SanitizedReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	name := "owned-20261010T000000Z-v1.2.3.json"
	path := filepath.Join(directory, name)
	bounded, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	observed := &publicationObservedContext{Context: bounded, cancel: cancel, path: path}
	result, err := publishPublicReport(observed, directory, name, raw)
	if !observed.observed || !errors.Is(err, context.Canceled) || !result.Published {
		t.Fatal("actual publication cancellation lost its acknowledgement", err)
	}
	actual, readErr := os.ReadFile(path)
	if readErr != nil || string(actual) != string(raw) {
		t.Fatal("acknowledged complete public bytes not retained", readErr)
	}
	sum := sha256.Sum256(actual)
	if result.Path != path || result.SizeBytes != int64(len(actual)) || result.SHA256 != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatal("acknowledged public identity differs after cancellation")
	}
	info, statErr := os.Lstat(path)
	if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0644 {
		t.Fatal("acknowledged public mode differs after cancellation", statErr)
	}
	if _, err := os.Lstat(path + ".partial"); !os.IsNotExist(err) {
		t.Fatal("acknowledged cancellation unexpectedly retains staging alias")
	}
	if invocation.lastResult != runResult || !invocation.completed || invocation.closed {
		t.Fatal("publication cancellation rewrote the retained Run result")
	}
	if _, err := os.Stat(invocation.driver.backup.inventory.Path); err != nil {
		t.Fatal("publication cancellation removed recovery material")
	}
}

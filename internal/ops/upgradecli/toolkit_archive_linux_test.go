//go:build linux

package upgradecli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func toolkitArchiveFixture(t *testing.T) (map[string][]byte, []byte, ToolkitInventory) {
	t.Helper()
	files := map[string][]byte{"scripts/upgrade-release.sh": []byte(installedUpgradeWrapper), "bin/upgrade-release": []byte("synthetic executable; never run"), "share/astronomer-upgrade/toolchain/toolchain-resolution.json": []byte("synthetic resolution"), "share/astronomer-upgrade/toolchain/toolchain-resolution.sigstore.json": []byte("synthetic bundle"), "share/astronomer-upgrade/toolchain/helm-v4.3.0-linux-amd64.tar.gz": []byte("synthetic helm"), "share/astronomer-upgrade/toolchain/gh_2.102.0_linux_amd64.tar.gz": []byte("synthetic gh"), "share/astronomer-upgrade/toolchain/kubectl-linux-amd64": []byte("synthetic kubectl")}
	raw, inventory, err := encodeUpgradeToolkit(context.Background(), files, "v1.2.2", strings.Repeat("a", 40), "linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	return files, raw, inventory
}

func TestUpgradeToolkitArchiveDeterministicBoundedMemberRoundTrip(t *testing.T) {
	files, raw, inventory := toolkitArchiveFixture(t)
	again, same, err := encodeUpgradeToolkit(context.Background(), files, inventory.Target, inventory.SourceCommit, inventory.Platform)
	if err != nil || !bytes.Equal(raw, again) || same.ArchiveSHA256 != inventory.ArchiveSHA256 || same.ArchiveSizeBytes != int64(len(raw)) {
		t.Fatal("producer content depends on map order/time", err)
	}
	got, err := decodeUpgradeToolkit(context.Background(), raw, inventory)
	if err != nil || len(got) != 7 {
		t.Fatal("actual bounded gzip/tar decode failed", err)
	}
	for name, body := range files {
		if !bytes.Equal(body, got[name]) {
			t.Fatal("member bytes changed", name)
		}
	}
}

func TestUpgradeToolkitArchiveRejectsIncompleteFooterAndConcatenatedStreams(t *testing.T) {
	_, raw, inventory := toolkitArchiveFixture(t)
	for _, kind := range []string{"footer", "concatenated", "trailing-compressed"} {
		t.Run(kind, func(t *testing.T) {
			body := append([]byte(nil), raw...)
			switch kind {
			case "footer":
				body = body[:len(body)-4]
			case "concatenated":
				body = append(body, raw...)
			case "trailing-compressed":
				body = append(body, 0)
			}
			if files, err := decodeUpgradeToolkit(context.Background(), body, inventory); err == nil || files != nil {
				t.Fatal("incomplete/trailing archive published member map")
			}
		})
	}
}

func TestUpgradeToolkitArchiveRejectsDangerousAuthenticatedMembers(t *testing.T) {
	_, raw, original := toolkitArchiveFixture(t)
	for _, kind := range []string{"symlink", "hardlink", "mode", "owner", "traversal", "duplicate", "omitted", "body"} {
		t.Run(kind, func(t *testing.T) {
			zipped, err := gzip.NewReader(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			reader := tar.NewReader(zipped)
			var output bytes.Buffer
			writerZip := gzip.NewWriter(&output)
			writer := tar.NewWriter(writerZip)
			inventory := original
			inventory.Files = map[string]ToolkitFile{}
			for name, spec := range original.Files {
				inventory.Files[name] = spec
			}
			first := true
			for {
				header, err := reader.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(reader)
				if err != nil {
					t.Fatal(err)
				}
				if first {
					originalName := header.Name
					switch kind {
					case "symlink":
						header.Typeflag = tar.TypeSymlink
						header.Linkname = "../../outside"
						header.Size = 0
						body = nil
					case "hardlink":
						header.Typeflag = tar.TypeLink
						header.Linkname = "../../outside"
						header.Size = 0
						body = nil
					case "mode":
						header.Mode = 0777
					case "owner":
						header.Uid = 1
					case "traversal":
						header.Name = "../outside"
						inventory.Files[header.Name] = inventory.Files[originalName]
						delete(inventory.Files, originalName)
					case "omitted":
						first = false
						continue
					case "body":
						body[0] ^= 1
					}
				}
				if err := writer.WriteHeader(header); err != nil {
					t.Fatal(err)
				}
				if _, err := writer.Write(body); err != nil {
					t.Fatal(err)
				}
				if first && kind == "duplicate" {
					if err := writer.WriteHeader(header); err != nil {
						t.Fatal(err)
					}
					if _, err := writer.Write(body); err != nil {
						t.Fatal(err)
					}
				}
				first = false
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := writerZip.Close(); err != nil {
				t.Fatal(err)
			}
			if err := zipped.Close(); err != nil {
				t.Fatal(err)
			}
			if files, err := decodeUpgradeToolkit(context.Background(), output.Bytes(), inventory); err == nil || files != nil {
				t.Fatal("unsafe/truncated signed member map was returned")
			}
		})
	}
}

type toolkitCancelContext struct {
	context.Context
	cancel       context.CancelFunc
	after, calls int
}

func (c *toolkitCancelContext) Err() error {
	c.calls++
	if c.calls == c.after {
		c.cancel()
	}
	return c.Context.Err()
}
func TestUpgradeToolkitArchiveCancellationDuringMemberWorkReturnsNoPublication(t *testing.T) {
	files, raw, inventory := toolkitArchiveFixture(t)
	for _, operation := range []string{"encode", "decode"} {
		t.Run(operation, func(t *testing.T) {
			base, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			ctx := &toolkitCancelContext{Context: base, cancel: cancel, after: 3}
			if operation == "decode" {
				got, err := decodeUpgradeToolkit(ctx, raw, inventory)
				if !errors.Is(err, context.Canceled) || got != nil {
					t.Fatal("partly decoded members published after real owner cancellation", err)
				}
			} else {
				got, metadata, err := encodeUpgradeToolkit(ctx, files, inventory.Target, inventory.SourceCommit, inventory.Platform)
				if !errors.Is(err, context.Canceled) || got != nil || metadata.ArchiveSizeBytes != 0 {
					t.Fatal("partly encoded archive published after real owner cancellation", err)
				}
			}
			if ctx.calls < 3 {
				t.Fatal("fixture did not enter real member work")
			}
		})
	}
}

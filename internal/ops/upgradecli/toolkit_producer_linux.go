//go:build linux

package upgradecli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	canonical "github.com/alphabravocompany/astronomer-go/deploy/toolchain"
	"github.com/alphabravocompany/astronomer-go/internal/httpclient"
)

const installedUpgradeWrapper = `#!/usr/bin/env bash
set -euo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
exec "${script_dir}/../bin/upgrade-release" "$@"
`

const toolkitMaximum = int64(512 << 20)
const toolkitExecutableMaximum = int64(128 << 20)

type ToolkitFile struct {
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
	Mode      uint32 `json:"mode"`
}
type ToolkitInventory struct {
	SchemaVersion    int                    `json:"schemaVersion"`
	Target           string                 `json:"target"`
	SourceCommit     string                 `json:"sourceCommit"`
	Platform         string                 `json:"platform"`
	ArchiveName      string                 `json:"archiveName"`
	ArchiveSizeBytes int64                  `json:"archiveSizeBytes"`
	ArchiveSHA256    string                 `json:"archiveSHA256"`
	Files            map[string]ToolkitFile `json:"files"`
}

// BuildUpgradeToolkit is the release producer. Only the source-bound signed
// resolution and compiled canonical authority select downloads; no ambient PATH
// or operator repository can select tools. The independently compiled CLI input
// must carry this exact VCS revision. Publication separately signs the small
// inventory, whose digest binds the entire bounded archive and every member.
// Any partial outputs are retained; no existing archive or input is overwritten.
func BuildUpgradeToolkit(ctx context.Context, resolutionDirectory, executable, sourceCommit, target, output string) (ToolkitInventory, error) {
	var empty ToolkitInventory
	if ctx == nil || !bootstrapSourceCommit.MatchString(sourceCommit) || !legacyStableTarget.MatchString(target) || !filepath.IsAbs(executable) || !filepath.IsAbs(resolutionDirectory) {
		return empty, errors.New("exact compiled toolkit producer inputs required")
	}
	if err := privateBootstrapDirectory(output); err != nil {
		return empty, err
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		return empty, errors.New("fresh empty toolkit producer directory required")
	}
	rootDirectory := filepath.Join(output, "verification-root")
	if err := os.Mkdir(rootDirectory, 0700); err != nil {
		return empty, errors.New("create toolkit trust scope")
	}
	if err := writeBootstrapBytes(ctx, rootDirectory, "trusted_root.json", canonical.TrustedRootBytes(), 0600); err != nil {
		return empty, err
	}
	record, recordInput, err := readOfflineReleaseInput(ctx, filepath.Join(resolutionDirectory, "toolchain-resolution.json"), 1<<20)
	if err != nil {
		return empty, err
	}
	bundle, bundleInput, err := readOfflineReleaseInput(ctx, filepath.Join(resolutionDirectory, "toolchain-resolution.sigstore.json"), 1<<20)
	if err != nil {
		return empty, err
	}
	authority, err := verifyBootstrapResolution(ctx, record, bundle, rootDirectory, sourceCommit, target)
	if err != nil {
		return empty, err
	}
	cli, cliInput, err := readToolkitExecutable(ctx, executable, toolkitExecutableMaximum)
	if err != nil {
		return empty, err
	}
	platform, err := toolkitExecutablePlatform(cli, sourceCommit)
	if err != nil {
		return empty, err
	}
	files := map[string][]byte{"scripts/upgrade-release.sh": []byte(installedUpgradeWrapper), "bin/upgrade-release": cli, "share/astronomer-upgrade/toolchain/toolchain-resolution.json": record, "share/astronomer-upgrade/toolchain/toolchain-resolution.sigstore.json": bundle}
	client, err := httpclient.NewDestinationClientContext(ctx, httpclient.DestinationPolicy{}, httpclient.DestinationOptions{MaxResponseBytes: toolkitMaximum})
	if err != nil {
		return empty, err
	}
	defer client.CloseIdleConnections()
	total := int64(len(cli) + len(record) + len(bundle) + len(installedUpgradeWrapper))
	for _, component := range []string{"helm", "kubectl-shell", "github-cli"} {
		artifact, err := selectBootstrapArtifact(authority, component, platform)
		if err != nil {
			return empty, err
		}
		if artifact.SizeBytes > toolkitMaximum-total {
			return empty, errors.New("toolkit aggregate artifact bound exceeded")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
		if err != nil {
			return empty, errors.New("canonical toolkit artifact request invalid")
		}
		response, err := client.Do(request)
		if err != nil {
			return empty, errors.New("canonical toolkit artifact request failed")
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, artifact.SizeBytes+1))
		closeErr := response.Body.Close()
		if response.StatusCode != http.StatusOK || readErr != nil || closeErr != nil || int64(len(raw)) != artifact.SizeBytes || contentDigest(raw) != artifact.Digest {
			return empty, errors.New("canonical toolkit download length or digest differs")
		}
		if _, err := decodeToolArtifact(ctx, artifact, raw); err != nil {
			return empty, err
		}
		files["share/astronomer-upgrade/toolchain/"+artifact.Name] = raw
		total += int64(len(raw))
	}
	if err := recheckOfflineReleaseInputs(ctx, []offlineReleaseInput{recordInput, bundleInput}); err != nil {
		return empty, err
	}
	if err := recheckToolkitExecutable(ctx, cliInput); err != nil {
		return empty, err
	}
	if err := artifactCapacity(output, total*2); err != nil {
		return empty, err
	}
	archive, inventory, err := encodeUpgradeToolkit(ctx, files, target, sourceCommit, platform)
	if err != nil {
		return empty, err
	}
	if err := writeBootstrapBytes(ctx, output, inventory.ArchiveName, archive, 0600); err != nil {
		return empty, err
	}
	encoded, err := json.MarshalIndent(inventory, "", "  ")
	if err != nil {
		return empty, errors.New("encode closed toolkit inventory")
	}
	if err := writeBootstrapBytes(ctx, output, "upgrade-toolkit.json", append(encoded, '\n'), 0600); err != nil {
		return empty, err
	}
	if err := recheckOfflineReleaseInputs(ctx, []offlineReleaseInput{recordInput, bundleInput}); err != nil {
		return empty, err
	}
	if err := recheckToolkitExecutable(ctx, cliInput); err != nil {
		return empty, err
	}
	return inventory, ctx.Err()
}

func contentDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type toolkitContextWriter struct {
	ctx       context.Context
	writer    io.Writer
	remaining int64
}

func (w *toolkitContextWriter) Write(raw []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(raw)) > w.remaining {
		return 0, errors.New("toolkit compressed byte bound exceeded")
	}
	n, err := w.writer.Write(raw)
	w.remaining -= int64(n)
	return n, err
}

func encodeUpgradeToolkit(ctx context.Context, files map[string][]byte, target, source, platform string) ([]byte, ToolkitInventory, error) {
	var empty ToolkitInventory
	if ctx == nil || !legacyStableTarget.MatchString(target) || !bootstrapSourceCommit.MatchString(source) || (platform != "linux/amd64" && platform != "linux/arm64") || len(files) != 7 {
		return nil, empty, errors.New("closed toolkit file set required")
	}
	inventory := ToolkitInventory{SchemaVersion: 1, Target: target, SourceCommit: source, Platform: platform, ArchiveName: "astronomer-upgrade-" + target + "-" + strings.ReplaceAll(platform, "/", "-") + ".tar.gz", Files: map[string]ToolkitFile{}}
	var buffer bytes.Buffer
	limited := &toolkitContextWriter{ctx: ctx, writer: &buffer, remaining: toolkitMaximum}
	compressed := gzip.NewWriter(limited)
	writer := tar.NewWriter(compressed)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	total := int64(0)
	for _, name := range names {
		if name != "bin/upgrade-release" && name != "scripts/upgrade-release.sh" && !strings.HasPrefix(name, "share/astronomer-upgrade/toolchain/") {
			return nil, empty, errors.New("unexpected toolkit member scope")
		}
		if filepath.Clean(name) != name || strings.HasPrefix(name, "/") || strings.Contains(name, "..") || len(files[name]) == 0 {
			return nil, empty, errors.New("invalid toolkit member")
		}
		raw := files[name]
		if int64(len(raw)) > toolkitMaximum-total {
			return nil, empty, errors.New("toolkit expanded bound exceeded")
		}
		total += int64(len(raw))
		mode := int64(0600)
		if name == "bin/upgrade-release" || name == "scripts/upgrade-release.sh" {
			mode = 0700
		}
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(raw)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); err != nil {
			return nil, empty, err
		}
		for offset := 0; offset < len(raw); {
			if err := ctx.Err(); err != nil {
				return nil, empty, err
			}
			end := offset + (32 << 10)
			if end > len(raw) {
				end = len(raw)
			}
			if _, err := writer.Write(raw[offset:end]); err != nil {
				return nil, empty, err
			}
			offset = end
		}
		inventory.Files[name] = ToolkitFile{int64(len(raw)), contentDigest(raw), uint32(mode)}
	}
	if err := writer.Close(); err != nil {
		return nil, empty, err
	}
	if err := compressed.Close(); err != nil {
		return nil, empty, err
	}
	inventory.ArchiveSizeBytes = int64(buffer.Len())
	inventory.ArchiveSHA256 = contentDigest(buffer.Bytes())
	return buffer.Bytes(), inventory, ctx.Err()
}

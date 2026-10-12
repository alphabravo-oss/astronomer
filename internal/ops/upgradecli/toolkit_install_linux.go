//go:build linux

package upgradecli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	canonical "github.com/alphabravocompany/astronomer-go/deploy/toolchain"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/model"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
	"github.com/alphabravocompany/astronomer-go/internal/jsoncontract"
)

// InstallUpgradeToolkit is called by an independently trusted compiled installer,
// never by an archive member before verification. The compiled root and exact
// normal release publisher authenticate a small inventory which binds all
// compressed bytes and members. No PATH tool, archive-supplied root, checksum
// alone or operator environment supplies signing authority. A fresh private
// installation is retained after any failed publication; no overwrite/cleanup.
func InstallUpgradeToolkit(ctx context.Context, archivePath, inventoryPath, bundlePath, target, compiledSource, output string) (string, error) {
	if ctx == nil || !legacyStableTarget.MatchString(target) || !bootstrapSourceCommit.MatchString(compiledSource) {
		return "", errors.New("explicit trusted installer source and target required")
	}
	if err := privateBootstrapDirectory(output); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(output)
	if err != nil || len(entries) != 0 {
		return "", errors.New("fresh empty private installation required")
	}
	inventoryRaw, inventoryInput, err := readOfflineReleaseInput(ctx, inventoryPath, 1<<20)
	if err != nil {
		return "", err
	}
	signature, signatureInput, err := readOfflineReleaseInput(ctx, bundlePath, 1<<20)
	if err != nil {
		return "", err
	}
	trust := filepath.Join(output, "verification-root")
	if err := os.Mkdir(trust, 0700); err != nil {
		return "", errors.New("create compiled installer trust scope")
	}
	if err := writeBootstrapBytes(ctx, trust, "trusted_root.json", canonical.TrustedRootBytes(), 0600); err != nil {
		return "", err
	}
	verifier, err := resolver.NewExecVerifier(trust)
	if err != nil {
		return "", err
	}
	policy := model.TrustPolicy{Provider: model.SignatureCosignKeyless, Issuer: metadataIssuer, Identity: "https://github.com/alphabravo-oss/astronomer/.github/workflows/release.yaml@refs/tags/" + target}
	if _, err := verifier.VerifyOfflineMetadata(ctx, inventoryRaw, signature, policy); err != nil {
		return "", err
	}
	var inventory ToolkitInventory
	if jsoncontract.Decode(inventoryRaw, &inventory) != nil || inventory.SchemaVersion != 1 || inventory.Target != target || inventory.SourceCommit != compiledSource || inventory.Platform != "linux/"+runtime.GOARCH || runtime.GOOS != "linux" || inventory.ArchiveName != filepath.Base(archivePath) || inventory.ArchiveName != "astronomer-upgrade-"+target+"-linux-"+runtime.GOARCH+".tar.gz" || inventory.ArchiveSizeBytes <= 0 || inventory.ArchiveSizeBytes > toolkitMaximum || !contentSHA.MatchString(inventory.ArchiveSHA256) {
		return "", errors.New("authenticated installer inventory contract differs")
	}
	archive, archiveInput, err := readOfflineReleaseInput(ctx, archivePath, inventory.ArchiveSizeBytes)
	if err != nil {
		return "", err
	}
	if int64(len(archive)) != inventory.ArchiveSizeBytes || contentDigest(archive) != inventory.ArchiveSHA256 {
		return "", errors.New("signed toolkit compressed content differs")
	}
	files, err := decodeUpgradeToolkit(ctx, archive, inventory)
	if err != nil {
		return "", err
	}
	authority, err := verifyBootstrapResolution(ctx, files["share/astronomer-upgrade/toolchain/toolchain-resolution.json"], files["share/astronomer-upgrade/toolchain/toolchain-resolution.sigstore.json"], trust, compiledSource, target)
	if err != nil {
		return "", err
	}
	expected := map[string]bool{"scripts/upgrade-release.sh": true, "bin/upgrade-release": true, "share/astronomer-upgrade/toolchain/toolchain-resolution.json": true, "share/astronomer-upgrade/toolchain/toolchain-resolution.sigstore.json": true}
	total := int64(0)
	for _, component := range []string{"helm", "kubectl-shell", "github-cli"} {
		tuple, err := selectBootstrapArtifact(authority, component, inventory.Platform)
		if err != nil {
			return "", err
		}
		name := "share/astronomer-upgrade/toolchain/" + tuple.Name
		expected[name] = true
		raw, ok := files[name]
		if !ok || int64(len(raw)) != tuple.SizeBytes || contentDigest(raw) != tuple.Digest {
			return "", errors.New("installed tool input differs from canonical selected tuple")
		}
		if _, err := decodeToolArtifact(ctx, tuple, raw); err != nil {
			return "", err
		}
	}
	if string(files["scripts/upgrade-release.sh"]) != installedUpgradeWrapper {
		return "", errors.New("installed wrapper differs from canonical compiled entrypoint")
	}
	for name, raw := range files {
		if !expected[name] {
			return "", errors.New("unexpected signed toolkit member")
		}
		total += int64(len(raw))
	}
	if len(files) != len(expected) {
		return "", errors.New("incomplete signed toolkit file set")
	}
	if err := recheckOfflineReleaseInputs(ctx, []offlineReleaseInput{inventoryInput, signatureInput, archiveInput}); err != nil {
		return "", err
	}
	if err := artifactCapacity(output, total); err != nil {
		return "", err
	}
	for _, directory := range []string{"bin", "scripts", "share", "share/astronomer-upgrade", "share/astronomer-upgrade/toolchain"} {
		if err := os.Mkdir(filepath.Join(output, directory), 0700); err != nil {
			return "", errors.New("create exclusive installed toolkit scope")
		}
	}
	for name, raw := range files {
		mode := os.FileMode(0600)
		if name == "bin/upgrade-release" || name == "scripts/upgrade-release.sh" {
			mode = 0700
		}
		if err := writeBootstrapBytes(ctx, filepath.Dir(filepath.Join(output, name)), filepath.Base(name), raw, mode); err != nil {
			return "", err
		}
	}
	if err := recheckOfflineReleaseInputs(ctx, []offlineReleaseInput{inventoryInput, signatureInput, archiveInput}); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return filepath.Join(output, "bin", "upgrade-release"), nil
}

func decodeUpgradeToolkit(ctx context.Context, archive []byte, inventory ToolkitInventory) (map[string][]byte, error) {
	if ctx == nil || len(archive) == 0 || int64(len(archive)) > toolkitMaximum || len(inventory.Files) != 7 {
		return nil, errors.New("bounded exact toolkit inventory required")
	}
	compressed := bytes.NewReader(archive)
	stream, err := gzip.NewReader(compressed)
	if err != nil {
		return nil, errors.New("toolkit gzip is invalid")
	}
	defer func(close func() error) { _ = close() }(stream.Close)
	stream.Multistream(false)
	reader := tar.NewReader(stream)
	files := make(map[string][]byte, 7)
	total := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("toolkit tar is invalid")
		}
		spec, known := inventory.Files[header.Name]
		_, duplicate := files[header.Name]
		if !known || duplicate || header.Typeflag != tar.TypeReg || header.Linkname != "" || filepath.IsAbs(header.Name) || filepath.Clean(header.Name) != header.Name || strings.Contains(header.Name, "..") || header.Uid != 0 || header.Gid != 0 || len(header.PAXRecords) > 0 || len(header.Xattrs) > 0 || header.Size <= 0 || header.Size != spec.SizeBytes || header.Mode != int64(spec.Mode) || !contentSHA.MatchString(spec.SHA256) { //nolint:staticcheck // Reject legacy extended attributes as well as PAX metadata at the archive trust boundary.
			return nil, errors.New("toolkit member identity differs")
		}
		mode := uint32(0600)
		if header.Name == "bin/upgrade-release" || header.Name == "scripts/upgrade-release.sh" {
			mode = 0700
			if header.Size > toolkitExecutableMaximum {
				return nil, errors.New("toolkit executable exceeds bound")
			}
		}
		if spec.Mode != mode || header.Size > toolkitMaximum-total {
			return nil, errors.New("toolkit aggregate or mode bound differs")
		}
		total += header.Size
		var buffer bytes.Buffer
		chunk := make([]byte, 32<<10)
		remaining := header.Size
		for remaining > 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			size := int64(len(chunk))
			if remaining < size {
				size = remaining
			}
			n, err := io.ReadFull(reader, chunk[:int(size)])
			if err != nil {
				return nil, errors.New("toolkit member truncated")
			}
			buffer.Write(chunk[:n])
			remaining -= int64(n)
		}
		if contentDigest(buffer.Bytes()) != spec.SHA256 {
			return nil, errors.New("signed toolkit member digest differs")
		}
		files[header.Name] = buffer.Bytes()
	}
	// Read the gzip footer and reject concatenated streams or non-padding payload
	// after the tar terminator, including data hidden in the expanded byte stream.
	padding, err := io.ReadAll(io.LimitReader(stream, 4097))
	if err != nil || len(padding) > 4096 {
		return nil, errors.New("toolkit gzip footer or padding differs")
	}
	for _, value := range padding {
		if value != 0 {
			return nil, errors.New("toolkit trailing payload forbidden")
		}
	}
	if err := stream.Close(); err != nil || compressed.Len() != 0 || len(files) != len(inventory.Files) {
		return nil, errors.New("toolkit archive contains missing or trailing members")
	}
	return files, ctx.Err()
}

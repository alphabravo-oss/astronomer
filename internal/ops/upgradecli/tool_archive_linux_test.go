//go:build linux

package upgradecli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func toolArchiveFixture(t *testing.T, headers []*tar.Header, bodies [][]byte) (ExecutableArtifact, []byte) {
	t.Helper()
	var output bytes.Buffer
	zipped := gzip.NewWriter(&output)
	archive := tar.NewWriter(zipped)
	for i, header := range headers {
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(bodies[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	raw := output.Bytes()
	sum := sha256.Sum256(raw)
	return ExecutableArtifact{Kind: "tar.gz", ArchiveMember: "linux-amd64/helm", SizeBytes: int64(len(raw)), Digest: "sha256:" + hex.EncodeToString(sum[:])}, raw
}

func TestExecutableArchiveRequiresDigestAndCompleteFooter(t *testing.T) {
	artifact, raw := toolArchiveFixture(t, []*tar.Header{{Name: "linux-amd64/helm", Mode: 0755, Size: 3}}, [][]byte{[]byte("bin")})
	got, err := decodeToolArtifact(context.Background(), artifact, raw)
	if err != nil || string(got) != "bin" {
		t.Fatal("exact archive member did not decode")
	}
	for _, kind := range []string{"digest", "size", "footer", "concatenated"} {
		t.Run(kind, func(t *testing.T) {
			tuple := artifact
			body := append([]byte(nil), raw...)
			switch kind {
			case "digest":
				tuple.Digest = "sha256:" + string(bytes.Repeat([]byte{'0'}, 64))
			case "size":
				tuple.SizeBytes++
			case "footer":
				body = body[:len(body)-4]
			case "concatenated":
				body = append(body, raw...)
			}
			if kind == "footer" || kind == "concatenated" {
				sum := sha256.Sum256(body)
				tuple.Digest = "sha256:" + hex.EncodeToString(sum[:])
				tuple.SizeBytes = int64(len(body))
			}
			if returned, err := decodeToolArtifact(context.Background(), tuple, body); err == nil || returned != nil {
				t.Fatal("invalid whole archive returned executable")
			}
		})
	}
}

func TestExecutableArchiveRejectsSpecialDuplicateTraversalAndMissingMember(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "duplicate", "traversal", "missing"} {
		t.Run(kind, func(t *testing.T) {
			headers := []*tar.Header{{Name: "linux-amd64/helm", Mode: 0755, Size: 3}}
			bodies := [][]byte{[]byte("bin")}
			switch kind {
			case "symlink":
				headers[0].Typeflag = tar.TypeSymlink
				headers[0].Linkname = "outside"
				headers[0].Size = 0
				bodies[0] = nil
			case "hardlink":
				headers[0].Typeflag = tar.TypeLink
				headers[0].Linkname = "outside"
				headers[0].Size = 0
				bodies[0] = nil
			case "duplicate":
				headers = append(headers, &tar.Header{Name: "linux-amd64/helm", Mode: 0755, Size: 3})
				bodies = append(bodies, []byte("bad"))
			case "traversal":
				headers[0].Name = "../outside"
			case "missing":
				headers[0].Name = "linux-amd64/other"
			}
			artifact, raw := toolArchiveFixture(t, headers, bodies)
			if returned, err := decodeToolArtifact(context.Background(), artifact, raw); err == nil || returned != nil {
				t.Fatal("unsafe archive returned executable")
			}
		})
	}
}

func TestExecutableArchiveCanceledOwnerPublishesNothing(t *testing.T) {
	artifact, raw := toolArchiveFixture(t, []*tar.Header{{Name: "linux-amd64/helm", Mode: 0755, Size: 3}}, [][]byte{[]byte("bin")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := decodeToolArtifact(ctx, artifact, raw); err != context.Canceled || got != nil {
		t.Fatal("canceled archive returned executable")
	}
}

func TestExecutableArtifactPreservesDirectBinaryRole(t *testing.T) {
	raw := []byte("bound direct binary")
	sum := sha256.Sum256(raw)
	artifact := ExecutableArtifact{Kind: "binary", SizeBytes: int64(len(raw)), Digest: "sha256:" + hex.EncodeToString(sum[:])}
	got, err := decodeToolArtifact(context.Background(), artifact, raw)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatal("direct binary role lost")
	}
	artifact.ArchiveMember = "linux-amd64/helm"
	if got, err := decodeToolArtifact(context.Background(), artifact, raw); err == nil || got != nil {
		t.Fatal("direct binary admitted archive extraction role")
	}
}

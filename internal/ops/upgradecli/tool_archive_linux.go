//go:build linux

package upgradecli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path"
	"strings"
)

// ExecutableArtifact is the closed canonical authority tuple. The immutable
// archive digest, not an operator executable path or self-hash, is authority.
type ExecutableArtifact struct {
	Kind          string `json:"kind"`
	Version       string `json:"version"`
	Name          string `json:"name"`
	Platform      string `json:"platform"`
	URL           string `json:"url"`
	SizeBytes     int64  `json:"sizeBytes"`
	Digest        string `json:"digest"`
	Provenance    string `json:"provenance"`
	VerifiedAt    string `json:"verifiedAt"`
	ArchiveMember string `json:"archiveMember,omitempty"`
}

const toolMaximumBinary = int64(64 << 20)
const toolMaximumExpanded = int64(128 << 20)

// decodeToolArtifact validates all bytes before returning any executable bytes.
// Exact primary digest/size is required first, then every tar entry and gzip
// footer is consumed with context, expansion, item and trailing-data bounds.
// Direct binaries and archives cannot silently exchange extraction roles.
func decodeToolArtifact(ctx context.Context, artifact ExecutableArtifact, raw []byte) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("explicit executable artifact context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	if artifact.SizeBytes <= 0 || artifact.SizeBytes > 512<<20 || int64(len(raw)) != artifact.SizeBytes || artifact.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
		return nil, errors.New("canonical executable artifact bytes differ")
	}
	if artifact.Kind == "binary" {
		if artifact.ArchiveMember != "" || int64(len(raw)) > toolMaximumBinary {
			return nil, errors.New("direct executable role or size differs")
		}
		return append([]byte(nil), raw...), ctx.Err()
	}
	if artifact.Kind != "tar.gz" || !safeToolMember(artifact.ArchiveMember) {
		return nil, errors.New("canonical executable extraction member required")
	}
	source := bytes.NewReader(raw)
	zipped, err := gzip.NewReader(source)
	if err != nil {
		return nil, errors.New("executable gzip header invalid")
	}
	defer zipped.Close()
	zipped.Multistream(false)
	expanded := &toolContextReader{context: ctx, reader: io.LimitReader(zipped, toolMaximumExpanded+1)}
	archive := tar.NewReader(expanded)
	seen := make(map[string]struct{})
	var selected []byte
	for count := 0; ; count++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("executable tar header invalid")
		}
		if count >= 4096 || !safeToolMember(strings.TrimSuffix(header.Name, "/")) || header.Size < 0 || header.Size > toolMaximumBinary || header.PAXRecords != nil || header.Xattrs != nil {
			return nil, errors.New("executable archive entry exceeds closed contract")
		}
		if _, exists := seen[header.Name]; exists {
			return nil, errors.New("duplicate executable archive entry")
		}
		seen[header.Name] = struct{}{}
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 {
				return nil, errors.New("nonempty executable archive directory")
			}
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return nil, errors.New("executable archive special entry forbidden")
		}
		if header.Name == artifact.ArchiveMember {
			if selected != nil || header.Size == 0 {
				return nil, errors.New("executable member duplicate or empty")
			}
			selected, err = io.ReadAll(io.LimitReader(archive, toolMaximumBinary+1))
			if err != nil || int64(len(selected)) != header.Size {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, errors.New("executable member truncated")
			}
		} else {
			if _, err = io.Copy(io.Discard, archive); err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, errors.New("executable archive entry truncated")
			}
		}
	}
	// tar EOF alone does not establish gzip CRC/footer or exclude hidden bytes.
	// Only a bounded zero-padding trailer is accepted after the tar terminator.
	padding, err := io.ReadAll(io.LimitReader(expanded, (1<<20)+1))
	if err != nil || len(padding) > 1<<20 || expanded.bytes > toolMaximumExpanded || source.Len() != 0 || selected == nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("executable archive footer, expansion or member differs")
	}
	for _, b := range padding {
		if b != 0 {
			return nil, errors.New("executable archive has trailing payload")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return selected, nil
}

func safeToolMember(name string) bool {
	return name != "" && len(name) <= 256 && name == path.Clean(name) && !path.IsAbs(name) && name != "." && name != ".." && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\\\x00\r\n\t")
}

type toolContextReader struct {
	context context.Context
	reader  io.Reader
	bytes   int64
}

func (r *toolContextReader) Read(p []byte) (int, error) {
	if err := r.context.Err(); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p)
	r.bytes += int64(n)
	if canceled := r.context.Err(); canceled != nil {
		return n, canceled
	}
	return n, err
}

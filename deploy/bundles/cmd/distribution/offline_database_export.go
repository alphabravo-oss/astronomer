package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

// Caller-owned paths identify prepared original bytes, never trust authority.
// The canonical metadata constructor authenticates the exact claims before
// permitting staging. The completed kit is then consumed by the same reader.
func writeOfflineDatabaseTransfer(ctx context.Context, output string, metadata []byte, files map[string]string, options distribution.OfflineOptions) error {
	return writeDatabaseTransferWithWriter(ctx, output, metadata, files, options, func(writer io.Writer) io.Writer { return writer })
}

// The private constructor hook is used only to exercise cancellation after an
// actual owned-file write. No flag, document or runtime composition selects it.
func writeDatabaseTransferWithWriter(ctx context.Context, output string, metadata []byte, files map[string]string, options distribution.OfflineOptions, wrap func(io.Writer) io.Writer) (failure error) {
	if ctx == nil || output == "" || wrap == nil {
		return errors.New("offline database export context and new output required")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	defer func() { failure = transferContextError(ctx, failure) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	_, _, graph, err := readOfflineDatabaseMetadata(ctx, metadata, options)
	if err != nil {
		return err
	}
	inputs, maximum, err := planOfflineDatabaseExport(metadata, files, graph.DatabaseLayers())
	if err != nil {
		return err
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		return errors.New("offline database export output must be new")
	}
	if err := checkOfflineDatabaseCapacity(output, maximum); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(output), ".astronomer-database-kit-")
	if err != nil {
		return errors.New("owned offline database export unavailable")
	}
	identity, err := file.Stat()
	if err != nil || !identity.Mode().IsRegular() {
		_ = file.Close()
		return errors.New("owned offline database export identity unavailable")
	}
	defer removeOwnedDatabaseExport(file.Name(), identity)
	if err := streamOfflineDatabaseExport(ctx, wrap(file), maximum, metadata, inputs, graph); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return errors.New("owned offline database export sync failed")
	}
	owned, err := file.Stat()
	if err != nil || !owned.Mode().IsRegular() {
		_ = file.Close()
		return errors.New("owned offline database export identity unavailable")
	}
	if err := file.Close(); err != nil {
		return errors.New("owned offline database export close failed")
	}
	// No successful compression/copy command alone accepts an artifact. This
	// independently reauthenticates all original subjects and every layer byte.
	if err := readOfflineDatabaseTransfer(ctx, file.Name(), "", options); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := os.Lstat(file.Name())
	if err != nil || !sameTransferInput(owned, current) {
		return errors.New("owned offline database export changed during readback")
	}
	// A same-directory hard link commits only the completed owned regular
	// file and never replaces an existing output, even across a path race.
	if err := os.Link(file.Name(), output); err != nil {
		return errors.New("offline database export destination changed or unavailable")
	}
	committed, err := os.Lstat(output)
	if err != nil || !sameTransferInput(owned, committed) {
		return errors.New("offline database export committed identity changed")
	}
	// The operator-owned filesystem is the local commit boundary. A privileged
	// host can mutate any path after this check; each later consumer must use
	// the canonical transfer reader again rather than trusting path existence.
	return nil
}

type databaseExportInput struct {
	path    string
	subject string
	before  os.FileInfo
}

func planOfflineDatabaseExport(metadata []byte, files map[string]string, claims map[string]distribution.DatabaseDescriptor) ([]databaseExportInput, int64, error) {
	if len(claims) == 0 || len(claims) > 2 || len(files) != len(claims) || len(metadata) == 0 || int64(len(metadata)) > 2*distribution.MaximumExpandedBytes {
		return nil, 0, errors.New("offline database export differs from exact authenticated file set")
	}
	inputs := make([]databaseExportInput, 0, len(claims))
	inputSize := int64(len(metadata))
	for subject, claim := range claims {
		path, exists := files[subject]
		info, err := os.Lstat(path)
		if !exists || err != nil || !info.Mode().IsRegular() || info.Size() != claim.Size || claim.Size <= 0 || claim.Size > distribution.MaximumDatabaseLayerBytes {
			return nil, 0, errors.New("offline database export requires exact regular original layers")
		}
		if claim.Size > distribution.MaximumDatabaseKitBytes-inputSize {
			return nil, 0, errors.New("offline database export input aggregate invalid")
		}
		inputSize += claim.Size
		inputs = append(inputs, databaseExportInput{path: path, subject: subject, before: info})
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].subject < inputs[j].subject })
	// Staging includes the original inputs and bounded output simultaneously.
	// Reserve covers tar padding and gzip overhead, not extra payload authority.
	reserve := 2 * distribution.MaximumExpandedBytes
	// Eight MiB is a finite encoding allowance, not an inferred unbounded
	// compressor promise. Any actual output beyond it fails and is discarded.
	maximum := inputSize + (8 << 20)
	if inputSize <= 0 || maximum > distribution.MaximumDatabaseKitBytes-inputSize-reserve {
		return nil, 0, errors.New("offline database export input and staging exceed aggregate bound")
	}
	return inputs, maximum, nil
}

func streamOfflineDatabaseExport(ctx context.Context, file io.Writer, maximum int64, metadata []byte, inputs []databaseExportInput, graph *distribution.VerifiedOfflineDatabaseGraph) error {
	bounded := &boundedDatabaseExportWriter{destination: file, maximum: maximum}
	compressed := gzip.NewWriter(bounded)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{Name: databaseMetadataMember, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR, Mode: 0600, Size: int64(len(metadata))}); err != nil {
		return errors.New("offline database export metadata header failed")
	}
	if _, err := archive.Write(metadata); err != nil {
		return errors.New("offline database export metadata write failed")
	}
	for _, input := range inputs {
		header := tar.Header{Name: "blobs/sha256/" + strings.TrimPrefix(input.subject, "sha256:"), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR, Mode: 0600, Size: input.before.Size()}
		if err := archive.WriteHeader(&header); err != nil {
			return errors.New("offline database export layer header failed")
		}
		if err := streamOfflineDatabaseExportInput(ctx, input, graph, archive); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return errors.New("offline database export archive close failed")
	}
	if err := compressed.Close(); err != nil {
		return errors.New("offline database export compression close failed")
	}
	return nil
}

func streamOfflineDatabaseExportInput(ctx context.Context, input databaseExportInput, graph *distribution.VerifiedOfflineDatabaseGraph, destination io.Writer) error {
	file, err := os.Open(input.path)
	if err != nil {
		return errors.New("offline database export original layer unavailable")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameTransferInput(input.before, opened) {
		return errors.New("offline database export original changed before opening")
	}
	if err := graph.CopyDatabaseLayer(ctx, input.subject, file, destination); err != nil {
		return err
	}
	final, err := os.Lstat(input.path)
	if err != nil || !sameTransferInput(input.before, final) {
		return errors.New("offline database export original changed during capture")
	}
	return nil
}

type boundedDatabaseExportWriter struct {
	destination io.Writer
	maximum     int64
	written     int64
}

func (writer *boundedDatabaseExportWriter) Write(content []byte) (int, error) {
	if writer.maximum <= 0 || int64(len(content)) > writer.maximum-writer.written {
		return 0, errors.New("offline database export exceeds finite staging limit")
	}
	count, err := writer.destination.Write(content)
	writer.written += int64(count)
	return count, err
}

// Failed staging cleanup never removes a replacement path owned by another
// operation. Same-UID filesystem tampering is not an artifact trust authority.
func removeOwnedDatabaseExport(path string, identity os.FileInfo) {
	current, err := os.Lstat(path)
	if err == nil && current.Mode().IsRegular() && os.SameFile(identity, current) {
		_ = os.Remove(path)
	}
}

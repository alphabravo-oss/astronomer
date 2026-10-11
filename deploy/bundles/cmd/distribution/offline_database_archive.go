package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

const databaseMetadataMember = "offline-metadata.tar.gz"

type databaseQuarantineWriter func(string) (io.WriteCloser, error)

// The operator reader always uses the fixed owned-file writer. Only tests wrap
// that writer to cancel after actual staged bytes; transfer data cannot select
// a writer, trust authority or network policy.
func readOfflineDatabaseTransfer(ctx context.Context, archive, destination string, options distribution.OfflineOptions) error {
	return readDatabaseTransferWithQuarantine(ctx, archive, destination, options, openDatabaseQuarantine)
}

// Authenticate the bounded metadata graph before creating any output directory
// or permitting large-layer writes. Payloads remain opaque and freshness unknown.
func readDatabaseTransferWithQuarantine(ctx context.Context, archive, destination string, options distribution.OfflineOptions, create databaseQuarantineWriter) (failure error) {
	if ctx == nil || create == nil {
		return errors.New("offline transfer context and owned writer required")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	defer func() { failure = transferContextError(ctx, failure) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	source, metadata, err := openDatabaseTransfer(ctx, archive)
	if err != nil {
		return err
	}
	defer source.close()
	index, blobs, graph, err := readOfflineDatabaseMetadata(ctx, metadata, options)
	if err != nil {
		return err
	}
	claims := graph.DatabaseLayers()
	planned, err := databaseTransferStagingSize(source.before.Size(), blobs, claims)
	if err != nil {
		return err
	}
	owned, err := newDatabaseTransferDestination(destination, planned)
	if err != nil {
		return err
	}
	defer owned.cleanup()
	if err := owned.captureMetadata(blobs); err != nil {
		return err
	}
	if err := streamDatabaseTransferLayers(ctx, source.archive, graph, claims, owned, create); err != nil {
		return err
	}
	if err := source.finish(); err != nil {
		return err
	}
	if err := owned.readback(ctx, blobs, claims); err != nil {
		return err
	}
	if owned != nil {
		if err := writeOfflineImportedMetadata(owned.path, index, blobs); err != nil {
			return errors.New("owned database layout metadata write failed")
		}
	}
	if err := source.checkIdentity(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if owned != nil {
		owned.success = true
	}
	return nil
}

type databaseTransferSource struct {
	path       string
	before     os.FileInfo
	file       *os.File
	compressed *bufio.Reader
	zipped     *gzip.Reader
	expanded   *transferCountReader
	archive    *tar.Reader
}

func openDatabaseTransfer(ctx context.Context, path string) (*databaseTransferSource, []byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > distribution.MaximumDatabaseKitBytes {
		return nil, nil, errors.New("bounded regular database transfer required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, errors.New("offline database transfer unavailable")
	}
	source := &databaseTransferSource{path: path, before: before, file: file}
	opened, err := file.Stat()
	if err != nil || !sameTransferInput(before, opened) {
		source.close()
		return nil, nil, errors.New("offline database transfer changed before opening")
	}
	source.compressed = bufio.NewReader(io.LimitReader(transferContextReader{ctx, file}, distribution.MaximumDatabaseKitBytes+1))
	source.zipped, err = gzip.NewReader(source.compressed)
	if err != nil {
		source.close()
		return nil, nil, errors.New("offline database compression invalid")
	}
	source.zipped.Multistream(false)
	source.expanded = &transferCountReader{source: io.LimitReader(source.zipped, distribution.MaximumDatabaseKitBytes+1)}
	source.archive = tar.NewReader(source.expanded)
	metadata, err := source.readMetadata()
	if err != nil {
		source.close()
		return nil, nil, err
	}
	return source, metadata, nil
}

func (source *databaseTransferSource) readMetadata() ([]byte, error) {
	header, err := source.archive.Next()
	if err != nil || !regularDatabaseTransferMember(header) || header.Name != databaseMetadataMember || header.Size <= 0 || header.Size > 2*distribution.MaximumExpandedBytes {
		return nil, errors.New("offline database metadata must be the first bounded regular member")
	}
	metadata, err := io.ReadAll(io.LimitReader(source.archive, header.Size+1))
	if err != nil || int64(len(metadata)) != header.Size {
		return nil, errors.New("offline database metadata truncated")
	}
	return metadata, nil
}

func regularDatabaseTransferMember(header *tar.Header) bool {
	return header != nil && header.Typeflag == tar.TypeReg && header.Format == tar.FormatUSTAR && header.Linkname == "" && len(header.PAXRecords) == 0
}

func (source *databaseTransferSource) finish() error {
	// Consume the footer and reject extra expanded bytes or a second stream.
	remainder, err := io.Copy(io.Discard, source.expanded)
	if err != nil || remainder != 0 || source.expanded.count > distribution.MaximumDatabaseKitBytes {
		return errors.New("offline database archive has trailing expanded content")
	}
	if _, err := source.compressed.ReadByte(); err != io.EOF {
		return errors.New("offline database archive has trailing compressed content")
	}
	return source.checkIdentity()
}

func (source *databaseTransferSource) checkIdentity() error {
	final, err := os.Lstat(source.path)
	if err != nil || !sameTransferInput(source.before, final) {
		return errors.New("offline database input changed during verification")
	}
	return nil
}

func sameTransferInput(before, after os.FileInfo) bool {
	return after.Mode().IsRegular() && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime().Equal(after.ModTime())
}

func (source *databaseTransferSource) close() {
	if source.zipped != nil {
		_ = source.zipped.Close()
	}
	_ = source.file.Close()
}

func databaseTransferStagingSize(input int64, blobs map[string][]byte, claims map[string]distribution.DatabaseDescriptor) (int64, error) {
	planned := int64(0)
	for _, content := range blobs {
		if int64(len(content)) > distribution.MaximumDatabaseKitBytes-planned {
			return 0, errors.New("offline metadata aggregate invalid")
		}
		planned += int64(len(content))
	}
	for _, claim := range claims {
		if claim.Size <= 0 || claim.Size > distribution.MaximumDatabaseKitBytes-planned {
			return 0, errors.New("offline database aggregate invalid")
		}
		planned += claim.Size
	}
	// Existing input, output and reserve are accounted independently. Free space
	// already excludes the input; aggregate arithmetic also includes that input.
	reserve := 2 * distribution.MaximumExpandedBytes
	if input <= 0 || planned > distribution.MaximumDatabaseKitBytes-reserve || input > distribution.MaximumDatabaseKitBytes-planned-reserve {
		return 0, errors.New("offline database input and staging exceed aggregate bound")
	}
	return planned, nil
}

func streamDatabaseTransferLayers(ctx context.Context, archive *tar.Reader, graph *distribution.VerifiedOfflineDatabaseGraph, claims map[string]distribution.DatabaseDescriptor, owned *databaseTransferDestination, create databaseQuarantineWriter) error {
	seen := map[string]bool{}
	for count := 0; ; count++ {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil || count >= len(claims) || !regularDatabaseTransferMember(header) || !strings.HasPrefix(header.Name, "blobs/sha256/") || len(header.Name) != len("blobs/sha256/")+64 {
			return errors.New("unexpected offline database member")
		}
		subject := "sha256:" + strings.TrimPrefix(header.Name, "blobs/sha256/")
		claim, permitted := claims[subject]
		if !permitted || seen[subject] || header.Size != claim.Size {
			return errors.New("offline database member outside authenticated exact set")
		}
		seen[subject] = true
		if err := captureDatabaseTransferLayer(ctx, graph, subject, archive, owned, create); err != nil {
			return err
		}
	}
	if len(seen) != len(claims) {
		return errors.New("offline database transfer missing a required layer")
	}
	return nil
}

func captureDatabaseTransferLayer(ctx context.Context, graph *distribution.VerifiedOfflineDatabaseGraph, subject string, source io.Reader, owned *databaseTransferDestination, create databaseQuarantineWriter) error {
	if owned == nil {
		return graph.CopyDatabaseLayer(ctx, subject, source, nil)
	}
	output, err := create(filepath.Join(owned.path, "blobs", "sha256", subject[7:]))
	if err != nil || output == nil {
		return errors.New("owned database quarantine unavailable")
	}
	copyErr := graph.CopyDatabaseLayer(ctx, subject, source, output)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return errors.New("offline database payload close failed")
	}
	return nil
}

func openDatabaseQuarantine(path string) (io.WriteCloser, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
}

type databaseTransferDestination struct {
	path    string
	before  os.FileInfo
	success bool
}

func newDatabaseTransferDestination(path string, planned int64) (*databaseTransferDestination, error) {
	if path == "" {
		return nil, nil
	}
	if err := checkOfflineDatabaseCapacity(path, planned); err != nil {
		return nil, err
	}
	if err := os.Mkdir(path, 0700); err != nil {
		return nil, errors.New("offline database destination must be new")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return nil, errors.New("owned database destination unavailable")
	}
	return &databaseTransferDestination{path: path, before: info}, nil
}

func (owned *databaseTransferDestination) cleanup() {
	if owned == nil || owned.success {
		return
	}
	current, err := os.Lstat(owned.path)
	if err == nil && current.IsDir() && os.SameFile(owned.before, current) {
		_ = os.RemoveAll(owned.path)
	}
}

func (owned *databaseTransferDestination) captureMetadata(blobs map[string][]byte) error {
	if owned == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(owned.path, "blobs", "sha256"), 0700); err != nil {
		return errors.New("owned database blob directory unavailable")
	}
	for subject, content := range blobs {
		if err := writeOwnedTransferBlob(filepath.Join(owned.path, "blobs", "sha256", subject[7:]), content); err != nil {
			return errors.New("owned database metadata blob write failed")
		}
	}
	return nil
}

func (owned *databaseTransferDestination) readback(ctx context.Context, blobs map[string][]byte, claims map[string]distribution.DatabaseDescriptor) error {
	if owned == nil {
		return nil
	}
	for subject, content := range blobs {
		if err := verifyOwnedTransferBlob(ctx, filepath.Join(owned.path, "blobs", "sha256", subject[7:]), subject, int64(len(content))); err != nil {
			return err
		}
	}
	for subject, claim := range claims {
		if err := verifyOwnedTransferBlob(ctx, filepath.Join(owned.path, "blobs", "sha256", subject[7:]), subject, claim.Size); err != nil {
			return err
		}
	}
	return nil
}

func checkOfflineDatabaseCapacity(destination string, planned int64) error {
	if planned < 0 || planned > distribution.MaximumDatabaseKitBytes {
		return errors.New("offline database staging arithmetic invalid")
	}
	var filesystem syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(destination), &filesystem); err != nil || filesystem.Bsize <= 0 || filesystem.Bavail > ^uint64(0)/uint64(filesystem.Bsize) {
		return errors.New("offline database capacity unavailable")
	}
	if uint64(filesystem.Bsize)*filesystem.Bavail < uint64(planned)+uint64(2*distribution.MaximumExpandedBytes) {
		return errors.New("offline database staging has insufficient reserve")
	}
	return nil
}

// Reopen only the captured regular file and verify complete bytes with fixed
// memory. A link, replacement, partial output or changed size fails readback.
func verifyOwnedTransferBlob(ctx context.Context, path, subject string, size int64) error {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() != size {
		return errors.New("owned transfer readback is not an exact regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return errors.New("owned transfer readback unavailable")
	}
	defer func(close func() error) { _ = close() }(file.Close)
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() != size {
		return errors.New("owned transfer readback changed before opening")
	}
	hash := sha256.New()
	count, err := io.CopyBuffer(hash, io.LimitReader(transferContextReader{ctx, file}, size+1), make([]byte, 64<<10))
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	if err != nil || count != size || fmt.Sprintf("sha256:%x", hash.Sum(nil)) != subject {
		return errors.New("owned transfer readback differs from authenticated bytes")
	}
	after, err := os.Lstat(path)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() != size {
		return errors.New("owned transfer readback changed during verification")
	}
	return nil
}

func writeOwnedTransferBlob(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("new owned transfer blob required")
	}
	count, writeErr := file.Write(content)
	closeErr := file.Close()
	if writeErr != nil || count != len(content) || closeErr != nil {
		return errors.New("owned transfer blob write failed")
	}
	return nil
}

func transferContextError(ctx context.Context, failure error) error {
	if failure == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if errors.Is(failure, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(failure, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return failure
}

type transferContextReader struct {
	context context.Context
	source  io.Reader
}

func (reader transferContextReader) Read(buffer []byte) (int, error) {
	if err := reader.context.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(buffer)
}

type transferCountReader struct {
	source io.Reader
	count  int64
}

func (reader *transferCountReader) Read(buffer []byte) (int, error) {
	count, err := reader.source.Read(buffer)
	reader.count += int64(count)
	return count, err
}

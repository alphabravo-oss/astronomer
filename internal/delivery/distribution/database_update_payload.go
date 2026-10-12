package distribution

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

const MaximumDatabaseExpandedBytes int64 = 8 << 30

// VerifiedDatabaseUpdatePayload proves the whole compressed layer hash and its
// exact upstream metadata. It is not actual scan-used or durable-state proof.
type VerifiedDatabaseUpdatePayload struct {
	feedDigest string
	snapshot   DatabaseUpdateSnapshot
	content    DatabaseCacheContent
}

type upstreamDatabaseMetadata struct {
	Version      int
	NextUpdate   time.Time
	UpdatedAt    time.Time
	DownloadedAt time.Time
}

// InspectDatabaseUpdatePayload pins an operator-owned regular file and hashes
// the complete compressed layer before interpreting its archive or metadata.
// A second complete hash and inode/size/mtime checks protect the decode pass.
// It never writes or unpacks members. Later consumers must verify again;
// privileged local filesystem mutation after this call remains a host boundary.
func InspectDatabaseUpdatePayload(ctx context.Context, update *AuthenticatedDatabaseUpdate, role, inputPath string, timing DatabaseUpdateTiming, now time.Time) (*VerifiedDatabaseUpdatePayload, error) {
	return inspectDatabaseUpdateFile(ctx, update, role, inputPath, timing, now, nil)
}

// afterHash is an internal fixture phase hook; production always supplies nil.
func inspectDatabaseUpdateFile(ctx context.Context, update *AuthenticatedDatabaseUpdate, role, inputPath string, timing DatabaseUpdateTiming, now time.Time, afterHash func()) (*VerifiedDatabaseUpdatePayload, error) {
	if ctx == nil || !filepath.IsAbs(inputPath) || now.IsZero() || timing.validate() != nil {
		return nil, errors.New("database payload inspection inputs invalid")
	}
	feed, document, ok := update.Snapshot()
	if !ok || !now.Before(document.ExpiresAt) {
		return nil, errors.New("database payload authenticated feed unavailable or expired")
	}
	var snapshot DatabaseUpdateSnapshot
	for _, candidate := range document.Databases {
		if candidate.Role == role {
			snapshot = candidate
		}
	}
	if snapshot.validate(document.Binding.Scanner, timing, now, document.PublishedAt) != nil {
		return nil, errors.New("database payload snapshot unavailable or stale")
	}
	inspected, err := inspectPinnedDatabaseContent(ctx, snapshot.Role, inputPath, snapshot.Layer, snapshot.Metadata.Size, afterHash)
	if err != nil {
		return nil, err
	}
	if err := bindDatabasePayloadMetadata(inspected.metadata, snapshot.Metadata); err != nil {
		return nil, databaseInspectionError(ctx, err.Error())
	}
	return &VerifiedDatabaseUpdatePayload{feedDigest: feed, snapshot: snapshot, content: inspected.database}, nil
}

// The consumer and operator supplier adapter share this complete compressed-byte
// authentication and bounded archive parser. metadataSize zero means the producer
// must derive its unknown metadata size, still under the same 64-KiB ceiling.
func inspectPinnedDatabasePayload(ctx context.Context, role, inputPath string, layer DatabaseDescriptor, metadataSize int64, afterHash func()) ([]byte, error) {
	inspected, err := inspectPinnedDatabaseContent(ctx, role, inputPath, layer, metadataSize, afterHash)
	return inspected.metadata, err
}

func inspectPinnedDatabaseContent(ctx context.Context, role, inputPath string, layer DatabaseDescriptor, metadataSize int64, afterHash func()) (databaseInspectedContent, error) {
	return inspectPinnedDatabaseContentTo(ctx, role, inputPath, layer, metadataSize, afterHash, nil)
}

// The optional private sink is chosen by the cache owner, never archive paths.
// It is reached only after the complete compressed layer has authenticated.
func inspectPinnedDatabaseContentTo(ctx context.Context, role, inputPath string, layer DatabaseDescriptor, metadataSize int64, afterHash func(), sink func(string, int64) (io.Writer, error)) (databaseInspectedContent, error) {
	if ctx == nil || !filepath.IsAbs(inputPath) || layer.validate(role) != nil || metadataSize < 0 || metadataSize > 64<<10 {
		return databaseInspectedContent{}, errors.New("database payload inspection inputs invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	file, before, err := openDatabaseInspectionFile(inputPath, layer.Size)
	if err != nil {
		return databaseInspectedContent{}, err
	}
	defer func(close func() error) { _ = close() }(file.Close)
	if err := hashDatabaseInspectionFile(ctx, file, layer); err != nil {
		return databaseInspectedContent{}, err
	}
	if afterHash != nil {
		afterHash()
	}
	if !sameDatabaseInspectionFile(file, inputPath, before) {
		return databaseInspectedContent{}, errors.New("database payload file changed after authentication")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return databaseInspectedContent{}, databaseInspectionError(ctx, "database payload rewind failed")
	}
	hash := sha256.New()
	compressed := &io.LimitedReader{R: databaseInspectionReader{ctx: ctx, input: file}, N: layer.Size + 1}
	buffered := bufio.NewReaderSize(io.TeeReader(compressed, hash), 64<<10)
	zipper, err := gzip.NewReader(buffered)
	if err != nil {
		return databaseInspectedContent{}, databaseInspectionError(ctx, "database payload gzip invalid")
	}
	defer func(close func() error) { _ = close() }(zipper.Close)
	zipper.Multistream(false)
	expanded := &io.LimitedReader{R: zipper, N: MaximumDatabaseExpandedBytes + 1}
	inspected, err := inspectDatabaseMemberContentTo(ctx, expanded, role, metadataSize, sink)
	if err != nil {
		return databaseInspectedContent{}, err
	}
	if err := finishDatabasePayload(ctx, expanded, buffered); err != nil {
		return databaseInspectedContent{}, err
	}
	if expanded.N <= 0 || layer.Size+1-compressed.N != layer.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != layer.Digest || !sameDatabaseInspectionFile(file, inputPath, before) {
		return databaseInspectedContent{}, databaseInspectionError(ctx, "database payload layer size/hash or expansion binding failed")
	}
	if err := ctx.Err(); err != nil {
		return databaseInspectedContent{}, err
	}
	return inspected, nil
}

func openDatabaseInspectionFile(path string, size int64) (*os.File, os.FileInfo, error) {
	return openDatabaseInspectionFileWithHook(path, size, nil)
}

// beforeOpen is an internal deterministic path-substitution fixture hook.
func openDatabaseInspectionFileWithHook(path string, size int64, beforeOpen func()) (*os.File, os.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || size <= 0 || size > MaximumDatabaseLayerBytes || before.Size() != size {
		return nil, nil, errors.New("database payload owned regular file unavailable")
	}
	if beforeOpen != nil {
		beforeOpen()
	}
	file, err := openDatabaseInspectionRegular(path)
	if err != nil {
		return nil, nil, errors.New("database payload file open failed")
	}
	if !sameDatabaseInspectionFile(file, path, before) {
		_ = file.Close()
		return nil, nil, errors.New("database payload file identity changed before read")
	}
	return file, before, nil
}

func sameDatabaseInspectionFile(file *os.File, path string, before os.FileInfo) bool {
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() != before.Size() || !opened.ModTime().Equal(before.ModTime()) {
		return false
	}
	current, err := os.Lstat(path)
	return err == nil && current.Mode().IsRegular() && os.SameFile(before, current) && current.Size() == before.Size() && current.ModTime().Equal(before.ModTime())
}

func hashDatabaseInspectionFile(ctx context.Context, file *os.File, expected DatabaseDescriptor) error {
	hash := sha256.New()
	input := &io.LimitedReader{R: databaseInspectionReader{ctx: ctx, input: file}, N: expected.Size + 1}
	count, err := io.CopyBuffer(hash, input, make([]byte, 64<<10))
	if err != nil || count != expected.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != expected.Digest {
		return databaseInspectionError(ctx, "database payload complete compressed hash/size failed")
	}
	return ctx.Err()
}

func inspectDatabaseMemberContentTo(ctx context.Context, expanded io.Reader, role string, metadataSize int64, sink func(string, int64) (io.Writer, error)) (databaseInspectedContent, error) {
	archive := tar.NewReader(expanded)
	databaseName := "trivy.db"
	if role == "trivy-java-database" {
		databaseName = "trivy-java.db"
	}
	seen := map[string]bool{}
	var metadata []byte
	var database DatabaseCacheContent
	for count := 0; ; count++ {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil || count >= 2 || header == nil || header.Typeflag != tar.TypeReg || header.Linkname != "" || len(header.PAXRecords) != 0 || len(header.Xattrs) != 0 || header.Format != tar.FormatUSTAR && header.Format != tar.FormatGNU || seen[header.Name] || header.Size <= 0 || header.Size > MaximumDatabaseExpandedBytes || header.Name != databaseName && header.Name != "metadata.json" { //nolint:staticcheck // Reject legacy extended attributes as well as PAX metadata at the archive trust boundary.
			return databaseInspectedContent{}, databaseInspectionError(ctx, "database payload archive member invalid")
		}
		if header.Name == "metadata.json" && (metadataSize > 0 && header.Size != metadataSize || header.Size > 64<<10) {
			return databaseInspectedContent{}, errors.New("database payload metadata size binding failed")
		}
		seen[header.Name] = true
		var output = io.Discard
		if sink != nil {
			output, err = sink(header.Name, header.Size)
			if err != nil || output == nil {
				return databaseInspectedContent{}, databaseInspectionError(ctx, "database cache member destination unavailable")
			}
		}
		if header.Name == "metadata.json" {
			metadata, err = io.ReadAll(io.LimitReader(archive, header.Size+1))
			if err != nil || int64(len(metadata)) != header.Size {
				return databaseInspectedContent{}, databaseInspectionError(ctx, "database payload metadata truncated")
			}
			if written, err := output.Write(metadata); err != nil || written != len(metadata) {
				return databaseInspectedContent{}, databaseInspectionError(ctx, "database cache metadata write failed")
			}
		} else {
			hash := sha256.New()
			copied, err := io.CopyBuffer(io.MultiWriter(hash, output), archive, make([]byte, 64<<10))
			if err != nil || copied != header.Size {
				return databaseInspectedContent{}, databaseInspectionError(ctx, "database payload database member truncated")
			}
			database = DatabaseCacheContent{Role: role, File: databaseName, Digest: "sha256:" + hex.EncodeToString(hash.Sum(nil)), Size: copied}
		}
	}
	if !seen[databaseName] || !seen["metadata.json"] {
		return databaseInspectedContent{}, errors.New("database payload required member missing")
	}
	return databaseInspectedContent{metadata: metadata, database: database}, nil
}

func finishDatabasePayload(ctx context.Context, expanded io.Reader, compressed *bufio.Reader) error {
	buffer := make([]byte, 64<<10)
	for {
		count, err := expanded.Read(buffer)
		for _, value := range buffer[:count] {
			if value != 0 {
				return errors.New("database payload trailing expanded content")
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return databaseInspectionError(ctx, "database payload gzip checksum/read failed")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if _, err := compressed.ReadByte(); err != io.EOF {
		return databaseInspectionError(ctx, "database payload trailing compressed content")
	}
	return nil
}

func bindDatabasePayloadMetadata(payload []byte, expected DatabaseUpdateMetadata) error {
	if int64(len(payload)) != expected.Size || contentDigest(payload) != expected.Digest {
		return errors.New("database payload upstream metadata digest/size differs")
	}
	metadata, err := decodeDatabasePayloadMetadata(payload)
	if err != nil {
		return err
	}
	if metadata.Version != expected.Schema || !metadata.UpdatedAt.Equal(expected.UpdatedAt) || !metadata.NextUpdate.Equal(expected.NextUpdate) {
		return errors.New("database payload actual metadata differs from authenticated claim")
	}
	return nil
}

func decodeDatabasePayloadMetadata(payload []byte) (upstreamDatabaseMetadata, error) {
	var metadata upstreamDatabaseMetadata
	if len(payload) == 0 || len(payload) > 64<<10 {
		return metadata, errors.New("database payload metadata size invalid")
	}
	// Upstream uses capitalized keys. Exact keys avoid Go's case-insensitive
	// struct matching creating alternate interpretations of signed metadata.
	var fields map[string]any
	if decodeClosed(payload, &fields) != nil || fields == nil {
		return metadata, errors.New("database payload metadata closed JSON invalid")
	}
	for name := range fields {
		if name != "Version" && name != "UpdatedAt" && name != "NextUpdate" && name != "DownloadedAt" {
			return metadata, errors.New("database payload metadata unknown field")
		}
	}
	for _, name := range []string{"Version", "UpdatedAt", "NextUpdate"} {
		if _, ok := fields[name]; !ok {
			return metadata, errors.New("database payload metadata required field missing")
		}
	}
	if decodeClosed(payload, &metadata) != nil || !databaseUpdateUTC(metadata.UpdatedAt) || !databaseUpdateUTC(metadata.NextUpdate) {
		return metadata, errors.New("database payload actual metadata differs from authenticated claim")
	}
	// DownloadedAt is local operational information, never a freshness clock.
	return metadata, nil
}

func (verified *VerifiedDatabaseUpdatePayload) Snapshot() (string, DatabaseUpdateSnapshot, bool) {
	if verified == nil || !digestPattern.MatchString(verified.feedDigest) || verified.snapshot.Layer.validate(verified.snapshot.Role) != nil {
		return "", DatabaseUpdateSnapshot{}, false
	}
	return verified.feedDigest, verified.snapshot, true
}

type databaseInspectionReader struct {
	ctx   context.Context
	input io.Reader
}

func (reader databaseInspectionReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.input.Read(buffer)
}

func databaseInspectionError(ctx context.Context, message string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.New(message)
}

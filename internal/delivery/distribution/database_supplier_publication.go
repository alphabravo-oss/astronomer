package distribution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"time"
)

// SupplierPublicationReader is a pinned original layer, authenticated completely
// before its first byte is exposed for copy. A second full hash and stable file
// identity are mandatory before Close succeeds. It is not a publication grant:
// destinations must quarantine incomplete copies and sign only after success.
// The owning synchronous caller must join reads before Close; concurrent use is
// intentionally unsupported, as with an ordinary pinned file reader.
type SupplierPublicationReader struct {
	ctx      context.Context
	cancel   context.CancelFunc
	file     *os.File
	before   os.FileInfo
	path     string
	layer    DatabaseDescriptor
	hash     hash.Hash
	read     int64
	complete bool
	failure  error
	closed   bool
}

func (inspector *SupplierDatabaseInspector) OpenPublicationPayload(ctx context.Context, captured *CapturedSupplierDatabase, verified *VerifiedSupplierDatabasePayload, inputPath string, now time.Time) (*SupplierPublicationReader, error) {
	if ctx == nil || inspector == nil || captured == nil || verified == nil || captured.owner != inspector || verified.owner != inspector || !filepath.IsAbs(inputPath) || !databaseUpdateUTC(now) {
		return nil, errors.New("supplier publication current payload authority differs")
	}
	original, measured := captured.snapshot, verified.snapshot
	if original.Role != measured.Role || original.SupplierSubject != measured.SupplierSubject || original.Manifest != measured.Manifest || original.Config != measured.Config || original.Layer != measured.Layer || validateDatabaseUpdateMetadata(measured.Role, measured.Metadata, inspector.options.Scanner, inspector.options.Timing, now, now) != nil {
		return nil, errors.New("supplier publication descriptor or actual metadata differs")
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Minute)
	file, before, err := openDatabaseInspectionFile(inputPath, measured.Layer.Size)
	if err != nil {
		cancel()
		return nil, err
	}
	if err := hashDatabaseInspectionFile(bounded, file, measured.Layer); err != nil {
		_ = file.Close()
		cancel()
		return nil, err
	}
	if !sameDatabaseInspectionFile(file, inputPath, before) {
		_ = file.Close()
		cancel()
		return nil, errors.New("supplier publication payload changed after whole authentication")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		failure := supplierDatabaseError(bounded, "supplier publication payload rewind failed")
		cancel()
		return nil, failure
	}
	return &SupplierPublicationReader{ctx: bounded, cancel: cancel, file: file, before: before, path: inputPath, layer: measured.Layer, hash: sha256.New()}, nil
}

func (reader *SupplierPublicationReader) Read(output []byte) (int, error) {
	if reader == nil || reader.closed || reader.file == nil {
		return 0, errors.New("supplier publication payload reader unavailable")
	}
	if reader.failure != nil {
		return 0, reader.failure
	}
	if err := reader.ctx.Err(); err != nil {
		reader.failure = err
		return 0, err
	}
	if reader.complete {
		return 0, io.EOF
	}
	remaining := reader.layer.Size - reader.read
	if remaining == 0 {
		if "sha256:"+hex.EncodeToString(reader.hash.Sum(nil)) != reader.layer.Digest || !sameDatabaseInspectionFile(reader.file, reader.path, reader.before) {
			reader.failure = errors.New("supplier publication copied layer hash or identity changed")
			return 0, reader.failure
		}
		reader.complete = true
		return 0, io.EOF
	}
	if int64(len(output)) > remaining {
		output = output[:remaining]
	}
	n, err := reader.file.Read(output)
	reader.read += int64(n)
	_, _ = reader.hash.Write(output[:n])
	if err != nil && (err != io.EOF || reader.read != reader.layer.Size) {
		reader.failure = supplierDatabaseError(reader.ctx, "supplier publication payload read failed or truncated")
		return n, reader.failure
	}
	// Authentication of the final copy is performed on the next read or Close,
	// so a consumer using an exact LimitedReader must still call Close and check.
	if err == io.EOF {
		return n, nil
	}
	return n, err
}

func (reader *SupplierPublicationReader) Close() error {
	if reader == nil || reader.closed || reader.file == nil || reader.cancel == nil || reader.ctx == nil {
		return errors.New("supplier publication payload reader unavailable or already closed")
	}
	reader.closed = true
	defer reader.cancel()
	defer reader.file.Close()
	if reader.failure != nil {
		return reader.failure
	}
	if err := reader.ctx.Err(); err != nil {
		return err
	}
	if reader.read != reader.layer.Size || "sha256:"+hex.EncodeToString(reader.hash.Sum(nil)) != reader.layer.Digest || !sameDatabaseInspectionFile(reader.file, reader.path, reader.before) {
		return errors.New("supplier publication incomplete copy or changed original payload")
	}
	return nil
}

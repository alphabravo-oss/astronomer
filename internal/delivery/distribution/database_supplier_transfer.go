package distribution

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	digest "github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

const SupplierDatabaseTransferReserveBytes int64 = 512 << 20

// Transfer downloads the constructor-bound exact supplier layer into private
// owned staging. Original content hash, shared two-pass inspection and actual
// upstream metadata must all pass before a new output is committed. It does not
// endorse the child, publish a feed, accept desired state or prove scan usage.
func (inspector *SupplierDatabaseInspector) Transfer(ctx context.Context, captured *CapturedSupplierDatabase, outputPath string, now time.Time) (*VerifiedSupplierDatabasePayload, error) {
	return inspector.transfer(ctx, captured, outputPath, now, nil)
}

// afterWrite is an internal fixture hook; production always supplies nil.
func (inspector *SupplierDatabaseInspector) transfer(ctx context.Context, captured *CapturedSupplierDatabase, outputPath string, now time.Time, afterWrite func(*os.File)) (*VerifiedSupplierDatabasePayload, error) {
	if inspector == nil || ctx == nil || captured == nil || captured.owner != inspector || !filepath.IsAbs(outputPath) || filepath.Clean(outputPath) != outputPath || now.IsZero() || captured.snapshot.Layer.validate(captured.snapshot.Role) != nil {
		return nil, errors.New("supplier database transfer authority invalid")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if inspector.options.Network.ValidateURL(ctx, "https://"+inspector.options.Repository) != nil {
		return nil, supplierDatabaseError(ctx, "supplier database transfer destination denied")
	}
	state, err := newSupplierTransferState(outputPath, captured.snapshot.Layer.Size)
	if err != nil {
		return nil, err
	}
	defer state.close()
	layer := captured.snapshot.Layer
	expected := ocispec.Descriptor{MediaType: layer.MediaType, Digest: digest.Digest(layer.Digest), Size: layer.Size}
	writer := &supplierTransferWriter{file: state.file, afterWrite: afterWrite}
	if err := resolver.StreamSupplierDatabaseLayer(ctx, inspector.options.Client, inspector.options.Repository, expected, writer); err != nil {
		return nil, supplierDatabaseError(ctx, "supplier database complete transfer failed")
	}
	if err := state.finishStaging(layer.Size); err != nil {
		return nil, supplierDatabaseError(ctx, err.Error())
	}
	verified, err := inspector.Inspect(ctx, captured, state.temporary, now)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := state.commit(); err != nil {
		return nil, err
	}
	return verified, nil
}

type supplierTransferState struct {
	directory     *os.File
	directoryInfo os.FileInfo
	file          *os.File
	fileInfo      os.FileInfo
	temporary     string
	output        string
	linked        bool
	accepted      bool
}

func newSupplierTransferState(output string, layerSize int64) (*supplierTransferState, error) {
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("supplier database output already exists or is unavailable")
	}
	directory, info, err := openSupplierTransferDirectory(filepath.Dir(output), layerSize)
	if err != nil {
		return nil, err
	}
	state := &supplierTransferState{directory: directory, directoryInfo: info, output: output}
	file, err := os.CreateTemp(filepath.Dir(output), ".supplier-database-")
	if err != nil {
		state.close()
		return nil, errors.New("supplier database private staging unavailable")
	}
	state.file, state.temporary = file, file.Name()
	state.fileInfo, err = file.Stat()
	if err != nil || !state.fileInfo.Mode().IsRegular() || state.fileInfo.Mode().Perm() != 0600 || !state.sameDirectory() || !state.sameTemporary() {
		state.close()
		return nil, errors.New("supplier database staging ownership changed")
	}
	return state, nil
}

func (state *supplierTransferState) finishStaging(size int64) error {
	if err := state.file.Sync(); err != nil {
		return errors.New("supplier database staging sync failed")
	}
	current, err := state.file.Stat()
	if err != nil || current.Size() != size || !state.sameDirectory() || !state.sameTemporary() {
		return errors.New("supplier database staging changed before inspection")
	}
	state.fileInfo = current
	return nil
}

func (state *supplierTransferState) sameDirectory() bool {
	if state == nil || state.directory == nil || state.directoryInfo == nil {
		return false
	}
	current, err := os.Lstat(filepath.Dir(state.output))
	opened, openedError := state.directory.Stat()
	return err == nil && openedError == nil && current.IsDir() && current.Mode().Perm()&0077 == 0 && opened.Mode().Perm()&0077 == 0 && os.SameFile(state.directoryInfo, current) && os.SameFile(state.directoryInfo, opened)
}

func (state *supplierTransferState) sameTemporary() bool {
	if state == nil || state.file == nil || state.fileInfo == nil {
		return false
	}
	current, err := os.Lstat(state.temporary)
	return err == nil && current.Mode().IsRegular() && os.SameFile(state.fileInfo, current)
}

func (state *supplierTransferState) commit() error {
	if !state.sameDirectory() || !sameDatabaseInspectionFile(state.file, state.temporary, state.fileInfo) {
		return errors.New("supplier database verified staging changed before commit")
	}
	if err := os.Link(state.temporary, state.output); err != nil {
		return errors.New("supplier database new output commit refused")
	}
	state.linked = true
	if !state.sameDirectory() || !sameDatabaseInspectionFile(state.file, state.output, state.fileInfo) {
		return errors.New("supplier database committed output identity differs")
	}
	if !state.sameTemporary() || os.Remove(state.temporary) != nil {
		return errors.New("supplier database owned staging cleanup failed")
	}
	if err := state.directory.Sync(); err != nil {
		return errors.New("supplier database committed directory sync failed")
	}
	if !state.sameDirectory() || !sameDatabaseInspectionFile(state.file, state.output, state.fileInfo) {
		return errors.New("supplier database output changed after directory sync")
	}
	state.accepted = true
	return nil
}

func (state *supplierTransferState) close() {
	if state == nil {
		return
	}
	if !state.accepted && state.linked && state.sameDirectory() && state.fileInfo != nil {
		current, err := os.Lstat(state.output)
		if err == nil && current.Mode().IsRegular() && os.SameFile(state.fileInfo, current) {
			_ = os.Remove(state.output)
		}
	}
	if state.sameDirectory() && state.sameTemporary() {
		_ = os.Remove(state.temporary)
	}
	if state.file != nil {
		_ = state.file.Close()
	}
	if state.directory != nil {
		_ = state.directory.Close()
	}
}

type supplierTransferWriter struct {
	file       *os.File
	afterWrite func(*os.File)
}

func (writer *supplierTransferWriter) Write(buffer []byte) (int, error) {
	count, err := writer.file.Write(buffer)
	if count > 0 && writer.afterWrite != nil {
		hook := writer.afterWrite
		writer.afterWrite = nil
		hook(writer.file)
	}
	return count, err
}

var _ io.Writer = (*supplierTransferWriter)(nil)

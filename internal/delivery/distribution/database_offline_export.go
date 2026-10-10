package distribution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/resolver"
)

// DatabaseOfflineExportResult reports authenticated byte transfer only. It does
// not claim import acceptance, signing, publication, scan use or fresh posture.
type DatabaseOfflineExportResult struct {
	Generation        int64
	FeedDigest        string
	MetadataBlobCount int
}

// ExportOfflineUpdateKit uses the existing canonical signed feed/child loader,
// complete payload transfer and inspection before creating any output member.
// An exclusive flat directory retains partial failure evidence. Index is written
// last, and the finished directory is reread through the same no-network import
// verifier before success. No caller-selected transport or trust exists here.
func (authority *DatabaseAuthority) ExportOfflineUpdateKit(ctx context.Context, proof *VerifiedDatabaseAuthorityBase, repository, subject, cacheDirectory, outputDirectory string, now time.Time) (DatabaseOfflineExportResult, error) {
	var result DatabaseOfflineExportResult
	if ctx == nil || !databaseUpdateUTC(now) || !filepath.IsAbs(outputDirectory) || filepath.Clean(outputDirectory) != outputDirectory || !filepath.IsAbs(cacheDirectory) || filepath.Clean(cacheDirectory) != cacheDirectory || outputDirectory == cacheDirectory || strings.HasPrefix(outputDirectory, cacheDirectory+string(os.PathSeparator)) || strings.HasPrefix(cacheDirectory, outputDirectory+string(os.PathSeparator)) {
		return result, errors.New("offline database export owned output invalid")
	}
	bounded, cancel := context.WithTimeout(ctx, 60*time.Minute)
	defer cancel()
	started := time.Now()
	update, err := authority.LoadUpdate(bounded, proof, repository, subject, now)
	if err != nil {
		return result, err
	}
	captured, err := authority.CaptureRefreshPayloads(bounded, proof, repository, update, cacheDirectory, now.Add(time.Since(started)))
	if err != nil {
		return result, err
	}
	defer captured.Close()
	kit, blobs, err := authority.captureOfflineKitMetadata(bounded, proof, repository, update)
	if err != nil {
		return result, err
	}
	original, err := authority.CaptureOfflineUpdate(bounded, proof, OfflineDatabaseUpdateInput{Repository: repository, Digest: subject, Subjects: kit.Subjects, Blobs: blobs, MainPayloadPath: captured.path(0), JavaPayloadPath: captured.path(1)}, now.Add(time.Since(started)))
	if err != nil {
		return result, err
	}
	defer original.Close()
	index, err := json.Marshal(kit)
	if err != nil || len(index) > 64<<10 {
		return result, errors.New("offline database export bounded index invalid")
	}
	layers := kit.Main.Size + kit.Java.Size
	var metadata int64
	for _, value := range blobs {
		metadata += int64(len(value))
	}
	// Original captures, output copies and reserve all count toward the unchanged
	// aggregate. Maximum-size pairs can legitimately exceed this finite budget.
	if layers*2+metadata*2+int64(len(index))+SupplierDatabaseTransferReserveBytes > MaximumOfflineDatabaseUpdateKitBytes {
		return result, errors.New("offline database export staging exceeds aggregate cap")
	}
	parent, before, err := openSupplierTransferDirectory(filepath.Dir(outputDirectory), min(layers, MaximumDatabaseLayerBytes))
	if err != nil {
		return result, err
	}
	defer func() { _ = parent.Close() }()
	if err := supplierRefreshCapacity(parent, layers+metadata+int64(len(index))); err != nil {
		return result, err
	}
	if _, err := os.Lstat(outputDirectory); !errors.Is(err, os.ErrNotExist) {
		return result, errors.New("offline database export requires absent output")
	}
	if err := os.Mkdir(outputDirectory, 0700); err != nil {
		return result, errors.New("offline database export exclusive directory unavailable")
	}
	current, err := os.Lstat(filepath.Dir(outputDirectory))
	if err != nil || !os.SameFile(before, current) {
		return result, errors.New("offline database export parent changed")
	}
	root, rootInfo, err := openSupplierTransferDirectory(outputDirectory, min(layers, MaximumDatabaseLayerBytes))
	if err != nil {
		return result, err
	}
	defer func() { _ = root.Close() }()
	sameRoot := func() bool {
		opened, e1 := root.Stat()
		live, e2 := os.Lstat(outputDirectory)
		return e1 == nil && e2 == nil && live.IsDir() && live.Mode().Perm() == 0700 && os.SameFile(rootInfo, live) && os.SameFile(rootInfo, opened)
	}
	for _, member := range kit.Metadata {
		if !sameRoot() {
			return result, errors.New("offline database export output ownership changed")
		}
		if err := writeOfflineDatabaseKitBytes(bounded, filepath.Join(outputDirectory, strings.TrimPrefix(member.Digest, "sha256:")), blobs[member.Digest]); err != nil {
			return result, err
		}
	}
	captured.mutex.Lock()
	if captured.closed || !captured.sameDirectory() {
		captured.mutex.Unlock()
		return result, errors.New("offline database export pinned input closed or changed")
	}
	for index, layer := range []DatabaseDescriptor{kit.Main, kit.Java} {
		if !sameRoot() || !sameDatabaseInspectionFile(captured.fileFD[index], captured.path(index), captured.files[index]) {
			captured.mutex.Unlock()
			return result, errors.New("offline database export pinned payload changed")
		}
		name := "main.tar.gz"
		if index == 1 {
			name = "java.tar.gz"
		}
		if err := copyOfflineDatabaseKitPayload(bounded, captured.path(index), filepath.Join(outputDirectory, name), layer); err != nil {
			captured.mutex.Unlock()
			return result, err
		}
	}
	captured.mutex.Unlock()
	if !sameRoot() {
		return result, errors.New("offline database export output changed before index")
	}
	if err := captured.ValidateCurrent(bounded, authority, proof, now.Add(time.Since(started))); err != nil {
		return result, err
	}
	if err := writeOfflineDatabaseKitBytes(bounded, filepath.Join(outputDirectory, "index.json"), index); err != nil {
		return result, err
	}
	verified, err := authority.CaptureOfflineUpdateKit(bounded, proof, outputDirectory, now.Add(time.Since(started)))
	if err != nil {
		return result, err
	}
	defer verified.Close()
	feed, document, _, _, valid := verified.Snapshot()
	if !valid || feed != subject || !sameRoot() {
		return result, errors.New("offline database export canonical final readback differs")
	}
	return DatabaseOfflineExportResult{Generation: document.Generation, FeedDigest: feed, MetadataBlobCount: len(blobs)}, nil
}

func (authority *DatabaseAuthority) captureOfflineKitMetadata(ctx context.Context, proof *VerifiedDatabaseAuthorityBase, repository string, update *AuthenticatedDatabaseUpdate) (OfflineDatabaseUpdateKit, map[string][]byte, error) {
	var kit OfflineDatabaseUpdateKit
	feed, document, valid := update.Snapshot()
	if !valid || len(document.Databases) != 2 {
		return kit, nil, errors.New("offline database export signed role closure unavailable")
	}
	if err := authority.Revalidate(ctx, proof); err != nil {
		return kit, nil, err
	}
	kit = OfflineDatabaseUpdateKit{SchemaVersion: 1, Repository: repository, Digest: feed}
	originals := []string{repository + "@" + feed}
	media := []string{DatabaseUpdateMediaType}
	maximum := []int64{MaximumDatabaseUpdateBytes}
	for _, snapshot := range document.Databases {
		originals = append(originals, snapshot.PublishedSubject)
		media = append(media, snapshot.Layer.MediaType)
		maximum = append(maximum, snapshot.Layer.Size)
		if snapshot.Role == "trivy-database" {
			kit.Main = snapshot.Layer
		} else if snapshot.Role == "trivy-java-database" {
			kit.Java = snapshot.Layer
		} else {
			return kit, nil, errors.New("offline database export unexpected role")
		}
	}
	blobs := map[string][]byte{}
	for index, original := range originals {
		destination, err := authority.DatabaseUpdateDestination(ctx, proof, original)
		if err != nil {
			return kit, nil, err
		}
		target, subject, ok := strings.Cut(destination, "@")
		source, originalSubject, sourceOK := strings.Cut(original, "@")
		if !ok || !sourceOK || subject != originalSubject {
			return kit, nil, errors.New("offline database export subject mapping differs")
		}
		authority.mutex.RLock()
		if authority.closed {
			authority.mutex.RUnlock()
			return kit, nil, errors.New("offline database export physical authority closed")
		}
		options := authority.loader.options
		metadata, err := resolver.CaptureVerifiedOfflineArtifactMetadata(ctx, options.Client, target, subject, media[index], options.Policy, authority.trust.FrozenDirectory(), maximum[index])
		authority.mutex.RUnlock()
		if err != nil {
			return kit, nil, err
		}
		kit.Subjects = append(kit.Subjects, StoredSubject{Repository: source, Digest: subject, SignatureDigest: metadata.SignatureDigest})
		for digest, value := range metadata.Blobs {
			if previous, ok := blobs[digest]; ok && !bytes.Equal(previous, value) {
				return kit, nil, errors.New("offline database export duplicate content differs")
			}
			blobs[digest] = value
		}
	}
	var aggregate int64
	keys := make([]string, 0, len(blobs))
	for digest, value := range blobs {
		aggregate += int64(len(value))
		keys = append(keys, digest)
	}
	if len(blobs) > 24 || aggregate > MaximumOfflineDatabaseUpdateMetadataBytes {
		return kit, nil, errors.New("offline database export metadata aggregate exceeds bound")
	}
	sort.Strings(keys)
	for _, digest := range keys {
		kit.Metadata = append(kit.Metadata, OfflineDatabaseUpdateBlob{Digest: digest, Size: int64(len(blobs[digest]))})
	}
	if err := authority.Revalidate(ctx, proof); err != nil {
		return kit, nil, err
	}
	return kit, blobs, nil
}

func writeOfflineDatabaseKitBytes(ctx context.Context, output string, value []byte) error {
	if ctx == nil || len(value) == 0 || len(value) > 1<<20 {
		return errors.New("offline database export metadata bound invalid")
	}
	state, err := newSupplierTransferState(output, int64(len(value)))
	if err != nil {
		return err
	}
	defer state.close()
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := io.Copy(&supplierTransferWriter{file: state.file}, bytes.NewReader(value)); err != nil {
		return databaseInspectionError(ctx, "offline database export metadata write failed")
	}
	if err := state.finishStaging(int64(len(value))); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return state.commit()
}

func copyOfflineDatabaseKitPayload(ctx context.Context, input, output string, layer DatabaseDescriptor) error {
	reader, err := openDatabasePublicationPayload(ctx, input, layer)
	if err != nil {
		return err
	}
	state, err := newSupplierTransferState(output, layer.Size)
	if err != nil {
		_ = reader.Close()
		return err
	}
	defer state.close()
	// Local io.Copy returns only after all reads have joined. Second full hash and
	// pinned identity are checked by Close before committing any output name.
	copied, copyErr := io.Copy(&supplierTransferWriter{file: state.file}, reader)
	closeErr := reader.Close()
	if copyErr != nil || closeErr != nil || copied != layer.Size {
		return databaseInspectionError(ctx, "offline database export complete payload copy failed")
	}
	if err := state.finishStaging(layer.Size); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return state.commit()
}

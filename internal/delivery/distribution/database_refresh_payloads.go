package distribution

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DatabaseRefreshPayloads owns exactly one authenticated full-payload capture
// for a finite refresh sweep. Paths and timestamps are retrieval DATA, never a
// scanner-use witness. Close must follow all synchronous acceptance calls.
type DatabaseRefreshPayloads struct {
	mutex         sync.Mutex
	owner         *DatabaseAuthority
	proof         *VerifiedDatabaseAuthorityBase
	update        *AuthenticatedDatabaseUpdate
	directory     string
	directoryInfo os.FileInfo
	directoryFD   *os.File
	fileFD        [2]*os.File
	files         [2]os.FileInfo
	main, java    *VerifiedDatabaseUpdatePayload
	closed        bool
}

func (authority *DatabaseAuthority) CaptureRefreshPayloads(ctx context.Context, proof *VerifiedDatabaseAuthorityBase, repository string, update *AuthenticatedDatabaseUpdate, cacheDirectory string, now time.Time) (*DatabaseRefreshPayloads, error) {
	if ctx == nil || !filepath.IsAbs(cacheDirectory) || filepath.Clean(cacheDirectory) != cacheDirectory {
		return nil, errors.New("database refresh private cache invalid")
	}
	if err := authority.Revalidate(ctx, proof); err != nil {
		return nil, err
	}
	started := time.Now()
	feed, document, valid := update.Snapshot()
	if !valid || len(document.Databases) != 2 {
		return nil, errors.New("database refresh signed role closure unavailable")
	}
	current, err := authority.LoadUpdate(ctx, proof, repository, feed, now)
	if err != nil {
		return nil, err
	}
	_, currentDocument, currentValid := current.Snapshot()
	if !currentValid || !reflect.DeepEqual(document, currentDocument) {
		return nil, errors.New("database refresh current signed decision differs")
	}
	update = current
	var size int64
	for _, snapshot := range document.Databases {
		if snapshot.Layer.validate(snapshot.Role) != nil {
			return nil, errors.New("database refresh signed layer invalid")
		}
		size += snapshot.Layer.Size
	}
	// Both roles are <=2GiB; aggregate <=4GiB is checked before any staging.
	root, rootInfo, err := openSupplierTransferDirectory(cacheDirectory, min(size, MaximumDatabaseLayerBytes))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	if err := supplierRefreshCapacity(root, size); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(cacheDirectory, ".database-refresh-")
	if err != nil {
		return nil, errors.New("database refresh private staging unavailable")
	}
	captured := &DatabaseRefreshPayloads{owner: authority, proof: proof, update: update, directory: directory}
	accepted := false
	defer func() {
		if !accepted {
			captured.Close()
		}
	}()
	currentRoot, err := os.Lstat(cacheDirectory)
	captured.directoryInfo, _ = os.Lstat(directory)
	if err != nil || !os.SameFile(rootInfo, currentRoot) || captured.directoryInfo == nil || !captured.directoryInfo.IsDir() || captured.directoryInfo.Mode().Perm() != 0700 {
		return nil, errors.New("database refresh cache ownership changed")
	}
	captured.directoryFD, _, err = openSupplierTransferDirectory(directory, min(size, MaximumDatabaseLayerBytes))
	if err != nil {
		return nil, err
	}
	for index, role := range []string{"trivy-database", "trivy-java-database"} {
		payload, err := authority.TransferUpdatePayload(ctx, proof, repository, update, role, captured.path(index), now.Add(time.Since(started)))
		if err != nil {
			return nil, err
		}
		_, snapshot, valid := payload.Snapshot()
		if !valid {
			return nil, errors.New("database refresh transferred proof unavailable")
		}
		captured.fileFD[index], captured.files[index], err = openDatabaseInspectionFile(captured.path(index), snapshot.Layer.Size)
		if err != nil || captured.files[index].Mode().Perm() != 0600 {
			return nil, errors.New("database refresh captured file ownership changed")
		}
		// Pin the accepted inode until every commit/cleanup finishes. Rehash the
		// newly opened FD before retaining it: a path replacement after transfer
		// cannot inherit the old proof or exploit later inode reuse.
		if err := hashDatabaseInspectionFile(ctx, captured.fileFD[index], snapshot.Layer); err != nil {
			return nil, err
		}
		if !sameDatabaseInspectionFile(captured.fileFD[index], captured.path(index), captured.files[index]) {
			return nil, errors.New("database refresh pinned capture changed after hash")
		}
		if index == 0 {
			captured.main = payload
		} else {
			captured.java = payload
		}
	}
	if err := captured.ValidateCurrent(ctx, authority, proof, now.Add(time.Since(started))); err != nil {
		return nil, err
	}
	accepted = true
	return captured, nil
}

func (captured *DatabaseRefreshPayloads) ValidateCurrent(ctx context.Context, authority *DatabaseAuthority, proof *VerifiedDatabaseAuthorityBase, now time.Time) error {
	if ctx == nil || captured == nil {
		return errors.New("database refresh capture unavailable")
	}
	captured.mutex.Lock()
	defer captured.mutex.Unlock()
	if captured.closed || captured.owner != authority || captured.proof != proof || !captured.sameDirectory() {
		return errors.New("database refresh capture owner differs")
	}
	for index := range captured.files {
		if captured.fileFD[index] == nil || captured.files[index] == nil || captured.files[index].Mode().Perm() != 0600 || !sameDatabaseInspectionFile(captured.fileFD[index], captured.path(index), captured.files[index]) {
			return errors.New("database refresh inspected payload changed")
		}
	}
	if err := authority.Revalidate(ctx, proof); err != nil {
		return err
	}
	return captured.update.ValidateCurrentMetadata(now)
}

// Snapshot copies authenticated immutable DATA. The original constructor-owned
// capture must still ValidateCurrent immediately before each commit.
func (captured *DatabaseRefreshPayloads) Snapshot() (*AuthenticatedDatabaseUpdate, DatabaseUpdateSnapshot, DatabaseUpdateSnapshot, bool) {
	if captured == nil {
		return nil, DatabaseUpdateSnapshot{}, DatabaseUpdateSnapshot{}, false
	}
	captured.mutex.Lock()
	defer captured.mutex.Unlock()
	if captured.closed {
		return nil, DatabaseUpdateSnapshot{}, DatabaseUpdateSnapshot{}, false
	}
	mainFeed, main, mainValid := captured.main.Snapshot()
	javaFeed, java, javaValid := captured.java.Snapshot()
	feed, _, valid := captured.update.Snapshot()
	return captured.update, main, java, valid && mainValid && javaValid && mainFeed == feed && javaFeed == feed
}

func (captured *DatabaseRefreshPayloads) path(index int) string {
	if index == 0 {
		return filepath.Join(captured.directory, "main.tar.gz")
	}
	return filepath.Join(captured.directory, "java.tar.gz")
}
func (captured *DatabaseRefreshPayloads) sameDirectory() bool {
	if captured.directoryFD == nil || captured.directoryInfo == nil {
		return false
	}
	current, err := os.Lstat(captured.directory)
	opened, openedError := captured.directoryFD.Stat()
	return err == nil && openedError == nil && current.IsDir() && current.Mode().Perm() == 0700 && opened.IsDir() && opened.Mode().Perm() == 0700 && os.SameFile(current, captured.directoryInfo) && os.SameFile(opened, captured.directoryInfo)
}

// Cleanup removes only the exact files and directory created by this owner.
// Replacements are preserved; no recursive RemoveAll or arbitrary path cleanup.
func (captured *DatabaseRefreshPayloads) Close() {
	if captured == nil {
		return
	}
	captured.mutex.Lock()
	defer captured.mutex.Unlock()
	if captured.closed {
		return
	}
	captured.closed = true
	defer func() {
		for _, file := range captured.fileFD {
			if file != nil {
				_ = file.Close()
			}
		}
		if captured.directoryFD != nil {
			_ = captured.directoryFD.Close()
		}
	}()
	if !captured.sameDirectory() {
		return
	}
	for index, original := range captured.files {
		current, err := os.Lstat(captured.path(index))
		if err == nil && original != nil && current.Mode().IsRegular() && os.SameFile(original, current) {
			_ = os.Remove(captured.path(index))
		}
	}
	_ = os.Remove(captured.directory)
}

// MaterializeCacheForJob consumes only this captured owner's pinned complete
// original payloads. A claim UUID names an exclusive per-job directory; it does
// not grant DB acceptance, workload scope or measured scan-use authority.
func (captured *DatabaseRefreshPayloads) MaterializeCacheForJob(ctx context.Context, authority *DatabaseAuthority, proof *VerifiedDatabaseAuthorityBase, cacheRoot string, claim uuid.UUID, now time.Time) (*MaterializedDatabaseCache, error) {
	if err := captured.ValidateCurrent(ctx, authority, proof, now); err != nil {
		return nil, err
	}
	started := time.Now()
	captured.mutex.Lock()
	defer captured.mutex.Unlock()
	if captured.closed || captured.owner != authority || captured.proof != proof || !captured.sameDirectory() {
		return nil, errors.New("database job captured payload owner changed")
	}
	for index := range captured.files {
		if captured.fileFD[index] == nil || captured.files[index] == nil || !sameDatabaseInspectionFile(captured.fileFD[index], captured.path(index), captured.files[index]) {
			return nil, errors.New("database job pinned payload changed")
		}
	}
	cache, err := MaterializeDatabaseCacheForJob(ctx, captured.update, captured.path(0), captured.path(1), cacheRoot, claim, now)
	if err != nil {
		return nil, err
	}
	if err := authority.Revalidate(ctx, proof); err != nil {
		cache.Close()
		return nil, err
	}
	if err := captured.update.ValidateCurrentMetadata(now.Add(time.Since(started))); err != nil {
		cache.Close()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		cache.Close()
		return nil, err
	}
	return cache, nil
}

package distribution

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DatabaseCacheSnapshot is measured file DATA. It does not prove a scanner used
// these files: a running job must bind its own before/after measurements, actual
// scanner and workload image, report, pod/job UIDs and current assignment.
type DatabaseCacheSnapshot struct {
	FeedDigest string
	Main, Java DatabaseCacheContent
}

// MaterializedDatabaseCache owns a private, newly-created per-job directory.
// Partial originals are retained on failure; Close joins this owner's handles
// without deleting path replacements or certifying a scanner or network result.
type MaterializedDatabaseCache struct {
	mutex       sync.Mutex
	directory   string
	directoryFD *os.File
	directories [2]*os.File
	files       [4]*os.File
	snapshot    DatabaseCacheSnapshot
	claims      [2]DatabaseUpdateSnapshot
	closed      bool
}

// MaterializeDatabaseCache refuses staging until BOTH authenticated payloads
// have their complete compressed hash and closed member metadata verified. The
// write pass independently rehashes the same pinned complete layers BEFORE
// extracting their two literal owned members. There is no general unpacker.
func MaterializeDatabaseCache(ctx context.Context, update *AuthenticatedDatabaseUpdate, mainPath, javaPath, cacheRoot string, now time.Time) (*MaterializedDatabaseCache, error) {
	return materializeDatabaseCache(ctx, update, mainPath, javaPath, cacheRoot, "", now)
}

// MaterializeDatabaseCacheForJob selects only the constructor's canonical UUID
// child name. This logical claim identifier is DATA, not a Kubernetes Job UID or
// durable acceptance grant. The same complete authenticated payload checks run
// before staging; an existing child is never adopted or replaced.
func MaterializeDatabaseCacheForJob(ctx context.Context, update *AuthenticatedDatabaseUpdate, mainPath, javaPath, cacheRoot string, claimID uuid.UUID, now time.Time) (*MaterializedDatabaseCache, error) {
	if claimID.Version() != 4 || claimID.Variant() != uuid.RFC4122 {
		return nil, errors.New("database cache job claim identifier invalid")
	}
	return materializeDatabaseCache(ctx, update, mainPath, javaPath, cacheRoot, "database-cache-"+claimID.String(), now)
}

func materializeDatabaseCache(ctx context.Context, update *AuthenticatedDatabaseUpdate, mainPath, javaPath, cacheRoot, jobName string, now time.Time) (*MaterializedDatabaseCache, error) {
	if ctx == nil || !databaseUpdateUTC(now) || !filepath.IsAbs(cacheRoot) || filepath.Clean(cacheRoot) != cacheRoot {
		return nil, errors.New("database cache materialization inputs invalid")
	}
	bounded, cancel := context.WithTimeout(ctx, 35*time.Minute)
	defer cancel()
	started := time.Now()
	feed, document, valid := update.Snapshot()
	if !valid || len(document.Databases) != 2 {
		return nil, errors.New("database cache authenticated roles unavailable")
	}
	var proofs [2]*VerifiedDatabaseUpdatePayload
	for index, role := range []string{"trivy-database", "trivy-java-database"} {
		path := mainPath
		if index == 1 {
			path = javaPath
		}
		proof, err := InspectDatabaseUpdatePayload(bounded, update, role, path, CadenceDatabaseUpdateTiming(), now.Add(time.Since(started)))
		if err != nil {
			return nil, err
		}
		proofs[index] = proof
	}
	var content [2]DatabaseCacheContent
	var required int64
	for index, proof := range proofs {
		var ok bool
		content[index], ok = proof.CacheContent()
		if !ok || content[index].Size > MaximumDatabaseExpandedBytes-required {
			return nil, errors.New("database cache aggregate expansion exceeds bound")
		}
		required += content[index].Size
		_, snapshot, _ := proof.Snapshot()
		if snapshot.Metadata.Size > MaximumDatabaseExpandedBytes-required {
			return nil, errors.New("database cache metadata aggregate exceeds bound")
		}
		required += snapshot.Metadata.Size
	}
	// Existing compressed inputs already occupy disk. The available-space guard
	// adds the complete expanded output plus the canonical finite reserve.
	captured, err := createDatabaseCacheDestinationForJob(cacheRoot, required, jobName)
	if err != nil {
		return nil, err
	}
	accepted := false
	defer func() {
		if !accepted {
			captured.Close()
		}
	}()
	captured.snapshot = DatabaseCacheSnapshot{FeedDigest: feed, Main: content[0], Java: content[1]}
	for index, proof := range proofs {
		path := mainPath
		if index == 1 {
			path = javaPath
		}
		_, snapshot, ok := proof.Snapshot()
		if !ok {
			return nil, errors.New("database cache payload proof unavailable")
		}
		captured.claims[index] = snapshot
		sink := func(name string, size int64) (io.Writer, error) {
			return captured.createMember(index, name, size, snapshot, content[index])
		}
		inspected, err := inspectPinnedDatabaseContentTo(bounded, snapshot.Role, path, snapshot.Layer, snapshot.Metadata.Size, nil, sink)
		if err != nil {
			return nil, err
		}
		if inspected.database != content[index] || bindDatabasePayloadMetadata(inspected.metadata, snapshot.Metadata) != nil {
			return nil, errors.New("database cache original members changed while writing")
		}
	}
	if err := update.ValidateCurrentMetadata(now.Add(time.Since(started))); err != nil {
		return nil, err
	}
	if err := captured.sealAndMeasure(bounded); err != nil {
		return nil, err
	}
	accepted = true
	return captured, nil
}

func (cache *MaterializedDatabaseCache) Snapshot() (DatabaseCacheSnapshot, bool) {
	if cache == nil {
		return DatabaseCacheSnapshot{}, false
	}
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	return cache.snapshot, !cache.closed && cache.directoryFD != nil && cache.files[0] != nil && cache.files[1] != nil && cache.files[2] != nil && cache.files[3] != nil
}
func (cache *MaterializedDatabaseCache) Directory() (string, bool) {
	if _, ok := cache.Snapshot(); !ok {
		return "", false
	}
	return cache.directory, true
}
func (cache *MaterializedDatabaseCache) Close() {
	if cache == nil {
		return
	}
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	if cache.closed {
		return
	}
	cache.closed = true
	for _, file := range cache.files {
		if file != nil {
			_ = file.Close()
		}
	}
	for _, file := range cache.directories {
		if file != nil {
			_ = file.Close()
		}
	}
	if cache.directoryFD != nil {
		_ = cache.directoryFD.Close()
	}
}

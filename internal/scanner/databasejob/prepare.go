package databasejob

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

// PrepareCache consumes the exact immutable feed under canonical physical
// authority, authenticates both unchanged child payloads, creates sealed cache
// files and waits for initial helper reads BEFORE the init process may exit.
// The worker still must bind current relational base/intent and actual runtime
// objects; this function grants neither acceptance nor scan-used provenance.
func PrepareCache(ctx context.Context, authority *distribution.DatabaseAuthority, proof *distribution.VerifiedDatabaseAuthorityBase, repository, feedDigest, stagingRoot, cacheRoot, coordinationRoot string, claim uuid.UUID, now time.Time) (Preparation, error) {
	if ctx == nil || authority == nil || proof == nil || !recordDigest.MatchString(feedDigest) || claim.Version() != 4 || claim.Variant() != uuid.RFC4122 || !utcClock(now) || !filepath.IsAbs(stagingRoot) || filepath.Clean(stagingRoot) != stagingRoot || !filepath.IsAbs(cacheRoot) || filepath.Clean(cacheRoot) != cacheRoot || !filepath.IsAbs(coordinationRoot) || filepath.Clean(coordinationRoot) != coordinationRoot || stagingRoot == cacheRoot || stagingRoot == coordinationRoot || cacheRoot == coordinationRoot {
		return Preparation{}, errors.New("database job prepare inputs invalid")
	}
	bounded, cancel := context.WithTimeout(ctx, 40*time.Minute)
	defer cancel()
	started := time.Now()
	update, err := authority.LoadUpdate(bounded, proof, repository, feedDigest, now)
	if err != nil {
		return Preparation{}, err
	}
	captured, err := authority.CaptureRefreshPayloads(bounded, proof, repository, update, stagingRoot, now.Add(time.Since(started)))
	if err != nil {
		return Preparation{}, err
	}
	defer captured.Close()
	cache, err := captured.MaterializeCacheForJob(bounded, authority, proof, cacheRoot, claim, now.Add(time.Since(started)))
	if err != nil {
		return Preparation{}, err
	}
	defer cache.Close()
	value, err := NewPreparation(bounded, update, cache, claim)
	if err != nil {
		return Preparation{}, err
	}
	if err := PublishPreparation(bounded, cacheRoot, value); err != nil {
		return Preparation{}, err
	}
	if err := WaitInitialMeasurement(bounded, coordinationRoot, value); err != nil {
		return Preparation{}, err
	}
	if err := captured.ValidateCurrent(bounded, authority, proof, now.Add(time.Since(started))); err != nil {
		return Preparation{}, err
	}
	if err := bounded.Err(); err != nil {
		return Preparation{}, err
	}
	return value, nil
}

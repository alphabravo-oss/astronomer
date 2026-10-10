package databasejob

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

// PrepareScanInput adds local workload closure to canonical DB preparation.
// These arguments are comparison DATA from the owner's current locked target;
// they cannot choose network policy, credentials, scanner image or acceptance.
func PrepareScanInput(ctx context.Context, authority *distribution.DatabaseAuthority, proof *distribution.VerifiedDatabaseAuthorityBase, feedRepository, feedDigest, workloadRepository, workloadDigest, architecture, stagingRoot, cacheRoot, workloadRoot, coordinationRoot string, claim uuid.UUID, now time.Time) (Preparation, error) {
	return prepareScanInput(ctx, authority, proof, feedRepository, feedDigest, workloadRepository, workloadDigest, architecture, stagingRoot, cacheRoot, workloadRoot, coordinationRoot, claim, now, true)
}

// StageScanInput prepares immutable local input in a separate network-enabled
// preparation Job. It returns only after whole payload authentication, sealing
// and current physical authority checks. It never waits for a scan-side ACK:
// the scan Pod is a DIFFERENT Pod under an existing deny-egress policy, and its
// independent readiness init must verify these bytes before the scanner starts.
// The return remains comparison DATA, not accepted or scan-used provenance.
func StageScanInput(ctx context.Context, authority *distribution.DatabaseAuthority, proof *distribution.VerifiedDatabaseAuthorityBase, feedRepository, feedDigest, workloadRepository, workloadDigest, architecture, stagingRoot, cacheRoot, workloadRoot, coordinationRoot string, claim uuid.UUID, now time.Time) (Preparation, error) {
	return prepareScanInput(ctx, authority, proof, feedRepository, feedDigest, workloadRepository, workloadDigest, architecture, stagingRoot, cacheRoot, workloadRoot, coordinationRoot, claim, now, false)
}

func prepareScanInput(ctx context.Context, authority *distribution.DatabaseAuthority, proof *distribution.VerifiedDatabaseAuthorityBase, feedRepository, feedDigest, workloadRepository, workloadDigest, architecture, stagingRoot, cacheRoot, workloadRoot, coordinationRoot string, claim uuid.UUID, now time.Time, waitForScan bool) (Preparation, error) {
	if ctx == nil || authority == nil || proof == nil || !recordDigest.MatchString(feedDigest) || !recordDigest.MatchString(workloadDigest) || claim.Version() != 4 || claim.Variant() != uuid.RFC4122 || !utcClock(now) {
		return Preparation{}, errors.New("owned scan preparation inputs invalid")
	}
	seen := map[string]bool{}
	for _, path := range []string{stagingRoot, cacheRoot, workloadRoot, coordinationRoot} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || seen[path] {
			return Preparation{}, errors.New("owned scan preparation paths invalid")
		}
		seen[path] = true
	}
	bounded, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	started := time.Now()
	update, err := authority.LoadUpdate(bounded, proof, feedRepository, feedDigest, now)
	if err != nil {
		return Preparation{}, err
	}
	captured, err := authority.CaptureRefreshPayloads(bounded, proof, feedRepository, update, stagingRoot, now.Add(time.Since(started)))
	if err != nil {
		return Preparation{}, err
	}
	defer captured.Close()
	cache, err := captured.MaterializeCacheForJob(bounded, authority, proof, cacheRoot, claim, now.Add(time.Since(started)))
	if err != nil {
		return Preparation{}, err
	}
	defer cache.Close()
	workload, err := CaptureWorkloadInput(bounded, authority, proof, workloadRepository, workloadDigest, architecture, workloadRoot, claim)
	if err != nil {
		return Preparation{}, err
	}
	defer workload.Close()
	value, err := NewWorkloadPreparation(bounded, update, cache, workload, claim)
	if err != nil {
		return Preparation{}, err
	}
	// No record is published until current captured authority is revalidated.
	if err := captured.ValidateCurrent(bounded, authority, proof, now.Add(time.Since(started))); err != nil {
		return Preparation{}, err
	}
	if err := PublishPreparation(bounded, cacheRoot, value); err != nil {
		return Preparation{}, err
	}
	if waitForScan {
		if err := WaitInitialMeasurement(bounded, coordinationRoot, value); err != nil {
			return Preparation{}, err
		}
	}
	// Both routes recheck complete content and actual mounted feed authority
	// before success. The connected init additionally waited for observer ACK;
	// the separate staging Job makes no claim that a scan observer has started.
	if _, err := workload.Measure(bounded); err != nil {
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

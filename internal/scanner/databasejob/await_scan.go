package databasejob

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

// AwaitPreparedScanInput is the no-network scan Pod's readiness init. The
// caller cannot supply expected record content: it opens the exact sealed
// preparation and both owned filesystem inputs, then requires the native
// observer's matching initial measurement. This proves ordering/content DATA
// only; Kubernetes/current relational authority remain the worker's obligation.
func AwaitPreparedScanInput(ctx context.Context, cacheRoot, workloadRoot, coordinationRoot string) error {
	if ctx == nil {
		return errors.New("owned scan readiness context unavailable")
	}
	seen := map[string]bool{}
	for _, path := range []string{cacheRoot, workloadRoot, coordinationRoot} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || seen[path] {
			return errors.New("owned scan readiness paths invalid")
		}
		seen[path] = true
	}
	bounded, cancel := context.WithTimeout(ctx, 17*time.Minute)
	defer cancel()
	preparation, ready, err := readPreparation(bounded, cacheRoot)
	if err != nil {
		return err
	}
	if !ready || preparation.SchemaVersion != 2 || preparation.Workload == nil {
		return errors.New("owned scan readiness full preparation unavailable")
	}
	cache, err := distribution.OpenSealedDatabaseCache(bounded, cacheRoot, preparation.ClaimID, preparation.Cache, preparation.MainMetadata, preparation.JavaMetadata)
	if err != nil {
		return err
	}
	defer cache.Close()
	workload, err := ReopenWorkloadInput(bounded, workloadRoot, preparation.ClaimID, *preparation.Workload)
	if err != nil {
		return err
	}
	defer workload.Close()
	if err := WaitInitialMeasurement(bounded, coordinationRoot, preparation); err != nil {
		return err
	}
	// Detect changes made during the ACK wait. Both reads remain synchronous
	// and share the same finite deadline; cancellation never releases the init.
	measured, err := cache.Measure(bounded)
	if err != nil {
		return err
	}
	if measured != preparation.Cache {
		return errors.New("owned scan readiness database changed")
	}
	image, err := workload.Measure(bounded)
	if err != nil {
		return err
	}
	if !sameWorkloadSnapshot(&image, preparation.Workload) {
		return errors.New("owned scan readiness workload changed")
	}
	return bounded.Err()
}

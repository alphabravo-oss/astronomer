package databasejob

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
)

// ObserveCache waits for the prepare init process, measures pinned read-only
// files, then waits for the native-sidecar final signal. An overall deadline or
// parent cancellation is a failure; neither is relabeled a successful final
// observation. SIGTERM's final remeasurement has its own finite 15 minute cap.
// Every read is synchronous; handles close after measurements return.
func ObserveCache(ctx context.Context, cacheRoot, coordinationRoot string, output io.Writer, finalSignal <-chan struct{}) error {
	return observeInput(ctx, cacheRoot, "", coordinationRoot, output, finalSignal)
}

// ObserveScanInput additionally measures the sealed local workload layout before
// initial readiness and after scanner exit. The Job route requires this schema
// 2 path; legacy DB-only helper output cannot qualify a workload witness.
func ObserveScanInput(ctx context.Context, cacheRoot, workloadRoot, coordinationRoot string, output io.Writer, finalSignal <-chan struct{}) error {
	if !filepath.IsAbs(workloadRoot) || filepath.Clean(workloadRoot) != workloadRoot || workloadRoot == cacheRoot || workloadRoot == coordinationRoot {
		return errors.New("owned workload observer root invalid")
	}
	return observeInput(ctx, cacheRoot, workloadRoot, coordinationRoot, output, finalSignal)
}
func observeInput(ctx context.Context, cacheRoot, workloadRoot, coordinationRoot string, output io.Writer, finalSignal <-chan struct{}) error {
	if ctx == nil || output == nil || finalSignal == nil || !filepath.IsAbs(cacheRoot) || filepath.Clean(cacheRoot) != cacheRoot || !filepath.IsAbs(coordinationRoot) || filepath.Clean(coordinationRoot) != coordinationRoot || coordinationRoot == cacheRoot {
		return errors.New("database cache observer inputs invalid")
	}
	bounded, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	prepared, err := waitPreparation(bounded, cacheRoot, finalSignal)
	if err != nil {
		return err
	}
	owner, err := distribution.OpenSealedDatabaseCache(bounded, cacheRoot, prepared.ClaimID, prepared.Cache, prepared.MainMetadata, prepared.JavaMetadata)
	if err != nil {
		return err
	}
	defer owner.Close()
	var workload *WorkloadInput
	if workloadRoot != "" {
		if prepared.SchemaVersion != 2 || prepared.Workload == nil {
			return errors.New("owned Job requires full workload preparation")
		}
		workload, err = ReopenWorkloadInput(bounded, workloadRoot, prepared.ClaimID, *prepared.Workload)
		if err != nil {
			return err
		}
		defer workload.Close()
	} else if prepared.Workload != nil {
		return errors.New("workload measurement cannot use DB-only observer")
	}
	// Reopening itself remeasures every file. The initial event is emitted after
	// those reads, without inventing a timestamp from preparation/completion.
	snapshot, valid := owner.Snapshot()
	initialTime := time.Now().UTC()
	if !valid || emitMeasurement(output, prepared, "initial", snapshot, initialTime) != nil {
		return errors.New("database cache initial observation unavailable")
	}
	if err := publishInitialMeasurement(bounded, coordinationRoot, prepared, snapshot, initialTime); err != nil {
		return err
	}
	select {
	case <-bounded.Done():
		return bounded.Err()
	case <-finalSignal:
	}
	// Keep the overall budget even during SIGTERM. Native sidecar shutdown must
	// reserve enough terminationGracePeriodSeconds; missing output stays UNKNOWN.
	final, stop := context.WithTimeout(bounded, 15*time.Minute)
	defer stop()
	snapshot, err = owner.Measure(final)
	if err != nil {
		return err
	}
	if workload != nil {
		measured, err := workload.Measure(final)
		if err != nil {
			return err
		}
		if !sameWorkloadSnapshot(&measured, prepared.Workload) {
			return errors.New("final workload input differs from preparation")
		}
	}
	if err := final.Err(); err != nil {
		return err
	}
	return emitMeasurement(output, prepared, "final", snapshot, time.Now().UTC())
}

func waitPreparation(ctx context.Context, cacheRoot string, finalSignal <-chan struct{}) (Preparation, error) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return Preparation{}, ctx.Err()
		case <-finalSignal:
			return Preparation{}, errors.New("database cache observer stopped before preparation")
		default:
		}
		value, ready, err := readPreparation(ctx, cacheRoot)
		if err != nil {
			return Preparation{}, err
		}
		if ready {
			return value, nil
		}
		select {
		case <-ctx.Done():
			return Preparation{}, ctx.Err()
		case <-finalSignal:
			return Preparation{}, errors.New("database cache observer stopped before preparation")
		case <-ticker.C:
		}
	}
}

// WaitInitialMeasurement is used by the owned prepare init process before it
// exits. This initial acknowledgement enforces ordering only, not scanner-use
// authority. The scanner mounts neither the writable coordination volume nor
// the prepare credentials.
func WaitInitialMeasurement(ctx context.Context, coordinationRoot string, expected Preparation) error {
	if ctx == nil || expected.validate() != nil {
		return errors.New("database scan readiness inputs invalid")
	}
	bounded, cancel := context.WithTimeout(ctx, 17*time.Minute)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		payload, ready, err := readOwnedRecord(bounded, coordinationRoot, "initial.json")
		if err != nil {
			return err
		}
		if ready {
			row, err := DecodeMeasurement(payload, expected)
			if err != nil || row.Phase != "initial" {
				return errors.New("database scan readiness differs from preparation")
			}
			return nil
		}
		select {
		case <-bounded.Done():
			return bounded.Err()
		case <-ticker.C:
		}
	}
}

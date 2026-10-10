package server

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/delivery/distribution"
	"github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
)

// DatabaseJobPreparation is immutable comparison DATA passed by the owned Job
// process. It grants no DB acceptance, workload selection or scan witness.
type DatabaseJobPreparation struct {
	FeedDigest                                             string
	ClaimID                                                uuid.UUID
	StagingRoot, CacheRoot, WorkloadRoot, CoordinationRoot string
	WorkloadRepository, WorkloadDigest, Architecture       string
}

// PrepareDatabaseCacheJob is a process-only preparation path. It reuses the
// SAME typed mounted trust, authenticated mirror and pinned registry authority
// as server startup. It starts no server, agent, migration or database session.
// The worker must independently lock current relational intent both before Job
// creation and after actual completed runtime observations before any witness.
func PrepareDatabaseCacheJob(ctx context.Context, cfg *config.Config, command DatabaseJobPreparation) error {
	return prepareDatabaseScanInput(ctx, cfg, command, false)
}

// StageDatabaseScanInput is the distinct process-only preparation Job route.
// Both routes use the SAME canonical typed mounted trust and registry client;
// staging cannot waive acceptance, physical checks, private pull or image closure.
func StageDatabaseScanInput(ctx context.Context, cfg *config.Config, command DatabaseJobPreparation) error {
	return prepareDatabaseScanInput(ctx, cfg, command, true)
}

// StageDatabaseScanInputRecord returns comparison DATA from the SAME actual
// canonical staging route. It starts no DB/session/HTTP server and grants no
// witness. Only the private owned worker records it after actual prep Job/Pod,
// selected runtime image and current relational/physical checks.
func StageDatabaseScanInputRecord(ctx context.Context, cfg *config.Config, command DatabaseJobPreparation) (databasejob.Preparation, error) {
	return prepareDatabaseScanInputRecord(ctx, cfg, command, true)
}
func prepareDatabaseScanInput(ctx context.Context, cfg *config.Config, command DatabaseJobPreparation, stageOnly bool) error {
	_, err := prepareDatabaseScanInputRecord(ctx, cfg, command, stageOnly)
	return err
}
func prepareDatabaseScanInputRecord(ctx context.Context, cfg *config.Config, command DatabaseJobPreparation, stageOnly bool) (databasejob.Preparation, error) {
	if ctx == nil || cfg == nil || !cfg.DeliveryDatabaseUpdatesEnabled || !databaseConfiguredDigest.MatchString(command.FeedDigest) || command.ClaimID.Version() != 4 || command.ClaimID.Variant() != uuid.RFC4122 {
		return databasejob.Preparation{}, errors.New("database Job preparation optional feature or identity invalid")
	}
	if err := ctx.Err(); err != nil {
		return databasejob.Preparation{}, err
	}
	for _, path := range []string{command.StagingRoot, command.CacheRoot, command.WorkloadRoot, command.CoordinationRoot} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return databasejob.Preparation{}, errors.New("database Job preparation owned path invalid")
		}
	}
	if command.StagingRoot == command.CacheRoot || command.StagingRoot == command.CoordinationRoot || command.CacheRoot == command.CoordinationRoot || command.WorkloadRoot == command.StagingRoot || command.WorkloadRoot == command.CacheRoot || command.WorkloadRoot == command.CoordinationRoot {
		return databasejob.Preparation{}, errors.New("database Job preparation owned paths overlap")
	}
	if !databaseConfiguredDigest.MatchString(command.WorkloadDigest) || (command.Architecture != "amd64" && command.Architecture != "arm64") || command.WorkloadRepository == "" {
		return databasejob.Preparation{}, errors.New("owned Job workload identity invalid")
	}
	bounded, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	options, err := databaseStartupOptions(bounded, cfg)
	if err != nil {
		return databasejob.Preparation{}, err
	}
	mirror, err := distributionMirrorPlanWithContext(bounded, cfg)
	if err != nil {
		return databasejob.Preparation{}, err
	}
	if mirror == nil || mirror.Identity() != options.Distribution.UpdateBinding.MirrorEndorsementDigest {
		return databasejob.Preparation{}, errors.New("database Job preparation authenticated mirror changed")
	}
	options.Distribution.MirrorPlan = mirror
	authority, err := distribution.NewPinnedDatabaseAuthority(bounded, options.Distribution, *options.Registry)
	if err != nil {
		return databasejob.Preparation{}, err
	}
	defer authority.Close()
	proof, err := authority.Base(bounded)
	if err != nil {
		return databasejob.Preparation{}, err
	}
	// Missing measured helper capability cannot make a legacy profile qualified.
	// This is authenticated image DATA only, not proof of the current process's
	// imageID; the owned worker still must compare the actual platform image IDs.
	if _, err = authority.DatabaseScanImages(bounded, proof); err != nil {
		return databasejob.Preparation{}, err
	}
	prepare := databasejob.PrepareScanInput
	if stageOnly {
		prepare = databasejob.StageScanInput
	}
	value, err := prepare(bounded, authority, proof, cfg.DeliveryDatabaseUpdateRepository, command.FeedDigest, command.WorkloadRepository, command.WorkloadDigest, command.Architecture, command.StagingRoot, command.CacheRoot, command.WorkloadRoot, command.CoordinationRoot, command.ClaimID, time.Now().UTC())
	return value, err
}

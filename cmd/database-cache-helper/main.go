package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/scanner/databasejob"
	"github.com/alphabravocompany/astronomer-go/internal/server"
)

func main() { os.Exit(run()) }
func run() int {
	flags := flag.NewFlagSet("database-cache-helper", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	initializeInput := flags.Bool("initialize-input", false, "initialize the exclusive claim child beneath the owned new PVC")
	initializeCoordination := flags.Bool("initialize-coordination", false, "seal the owned new coordination emptyDir")
	initializeMaterial := flags.Bool("initialize-material", false, "prepare the owned new authority emptyDir")
	bootstrap := flags.Bool("bootstrap-material", false, "copy the closed projected preparation Secret to private regular files")
	mappedRelease := flags.Bool("mapped-release", false, "include the constructor-selected release mapping in bootstrap only")
	privatePull := flags.Bool("private-pull", false, "include the independently scoped private pull member in bootstrap only")
	prepare := flags.Bool("prepare", false, "run the owned preparation init process")
	stage := flags.Bool("stage", false, "prepare sealed input in the separate network-enabled Job")
	await := flags.Bool("await-prepared", false, "verify sealed local inputs and observer ACK without trust or network")
	feed := flags.String("feed-digest", "", "immutable authenticated database feed")
	claim := flags.String("claim-id", "", "owned logical Job claim")
	staging := flags.String("staging-root", "", "owned compressed payload staging root")
	root := flags.String("cache-root", "", "owned shared cache root")
	workloadRoot := flags.String("workload-root", "", "owned sealed local OCI image root")
	workloadRepository := flags.String("workload-repository", "", "exact owner-selected workload repository")
	workloadDigest := flags.String("workload-digest", "", "exact platform workload manifest")
	architecture := flags.String("architecture", "", "owner-selected Linux node architecture")
	coordination := flags.String("coordination-root", "", "owned initial acknowledgement root")
	if flags.Parse(os.Args[1:]) != nil || flags.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "database cache helper arguments invalid")
		return 2
	}
	if *initializeInput || *initializeCoordination || *initializeMaterial {
		return initializeJobProcess(*initializeInput, *initializeCoordination, *initializeMaterial, *claim, *bootstrap || *privatePull || *mappedRelease || *prepare || *stage || *await || *feed != "" || *staging != "" || *root != "" || *workloadRoot != "" || *workloadRepository != "" || *workloadDigest != "" || *architecture != "" || *coordination != "")
	}
	if *bootstrap {
		if *prepare || *stage || *await || *feed != "" || *claim != "" || *staging != "" || *root != "" || *workloadRoot != "" || *workloadRepository != "" || *workloadDigest != "" || *architecture != "" || *coordination != "" {
			fmt.Fprintln(os.Stderr, "owned material bootstrap arguments invalid")
			return 2
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()
		copyMaterial := databasejob.CopyProjectedPreparationMaterial
		if *mappedRelease {
			copyMaterial = databasejob.CopyProjectedMappedPreparationMaterial
		}
		if copyMaterial(ctx, *privatePull) != nil {
			fmt.Fprintln(os.Stderr, "owned material bootstrap failed")
			return 1
		}
		return 0
	}
	if *privatePull || *mappedRelease || (*prepare && *stage) || (*await && (*prepare || *stage)) || *root == "" || *coordination == "" {
		fmt.Fprintln(os.Stderr, "database cache helper arguments invalid")
		return 2
	}
	if *prepare || *stage {
		identifier, err := uuid.Parse(*claim)
		if err != nil || *feed == "" || *staging == "" || *workloadRoot == "" || *workloadRepository == "" || *workloadDigest == "" || *architecture == "" {
			fmt.Fprintln(os.Stderr, "database cache preparation arguments invalid")
			return 2
		}
		return prepareJob(*stage, server.DatabaseJobPreparation{FeedDigest: *feed, ClaimID: identifier, StagingRoot: *staging, CacheRoot: *root, CoordinationRoot: *coordination, WorkloadRoot: *workloadRoot, WorkloadRepository: *workloadRepository, WorkloadDigest: *workloadDigest, Architecture: *architecture})
	}
	if *feed != "" || *claim != "" || *staging != "" || *workloadRepository != "" || *workloadDigest != "" || *architecture != "" {
		fmt.Fprintln(os.Stderr, "database cache observer arguments invalid")
		return 2
	}
	if *await {
		if *workloadRoot == "" {
			fmt.Fprintln(os.Stderr, "owned scan readiness arguments invalid")
			return 2
		}
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()
		if databasejob.AwaitPreparedScanInput(ctx, *root, *workloadRoot, *coordination) != nil {
			fmt.Fprintln(os.Stderr, "owned scan readiness failed")
			return 1
		}
		return 0
	}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	final := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	joined := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case value := <-signals:
			if value == syscall.SIGTERM {
				close(final)
			} else {
				cancel()
				return
			}
		case <-done:
			return
		}
		select {
		case <-signals:
			cancel()
		case <-done:
		}
	}()
	output, err := databasejob.NewMeasurementPipe(ctx, os.Stdout)
	if err == nil {
		defer output.Close()
		if *workloadRoot != "" {
			err = databasejob.ObserveScanInput(ctx, *root, *workloadRoot, *coordination, output, final)
		} else {
			err = databasejob.ObserveCache(ctx, *root, *coordination, output, final)
		}
	}
	close(done)
	<-joined
	if err != nil {
		fmt.Fprintln(os.Stderr, "database cache helper measurement failed")
		return 1
	}
	return 0
}

func prepareJob(stageOnly bool, command server.DatabaseJobPreparation) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	cfg, err := config.Load()
	if err == nil {
		if stageOnly {
			var prepared databasejob.Preparation
			prepared, err = server.StageDatabaseScanInputRecord(ctx, cfg, command)
			if err == nil {
				output, openErr := databasejob.NewMeasurementPipe(ctx, os.Stdout)
				if openErr != nil {
					err = openErr
				} else {
					err = databasejob.WritePreparationRecord(ctx, output, prepared)
					output.Close()
				}
			}
		} else {
			err = server.PrepareDatabaseCacheJob(ctx, cfg, command)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "database cache preparation failed")
		return 1
	}
	return 0
}

// Initializers have fixed constructor-selected mount paths and UID. A parsed
// claim is comparison DATA only; no arbitrary path, trust or configuration is
// loaded, and no job/current-row/witness authority is obtained here.
func initializeJobProcess(input, coordination, material bool, claim string, incompatible bool) int {
	count := 0
	for _, selected := range []bool{input, coordination, material} {
		if selected {
			count++
		}
	}
	if count != 1 || incompatible || (!input && claim != "") {
		fmt.Fprintln(os.Stderr, "owned Job initialization arguments invalid")
		return 2
	}
	var identifier uuid.UUID
	if input {
		value, err := uuid.Parse(claim)
		if err != nil || value.Version() != 4 || value.Variant() != uuid.RFC4122 {
			fmt.Fprintln(os.Stderr, "owned Job initialization claim invalid")
			return 2
		}
		identifier = value
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	var err error
	switch {
	case input:
		err = databasejob.InitializeJobInput(ctx, identifier)
	case coordination:
		err = databasejob.InitializeJobCoordination(ctx)
	case material:
		err = databasejob.InitializeJobMaterialDestination(ctx)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "owned Job initialization failed")
		return 1
	}
	return 0
}

// delivery-qualification emits machine-readable scale and soak evidence for
// Plan 007 Wave 10. It accepts no credentials, URLs, or arbitrary commands.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/delivery/qualification"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "delivery-qualification:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: delivery-qualification scale|soak [flags]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch os.Args[1] {
	case "scale":
		return runScale(ctx, os.Args[2:])
	case "soak":
		return runSoak(ctx, os.Args[2:])
	default:
		return fmt.Errorf("unsupported mode %q (expected scale or soak)", os.Args[1])
	}
}

type commonFlags struct {
	output       string
	capacityPath string
	clusters     int
	agents       int
	servers      int
	workers      int
	deployments  int
	statusEvents int
	iterations   int
	warmup       int
	allowSmaller bool
}

func bindCommon(set *flag.FlagSet) *commonFlags {
	defaults := qualification.DefaultScaleConfig()
	values := &commonFlags{}
	set.StringVar(&values.output, "output", "", "required JSON evidence path in an existing directory")
	set.StringVar(&values.capacityPath, "capacity-path", ".", "filesystem sampled for capacity evidence")
	set.IntVar(&values.clusters, "clusters", defaults.Clusters, "registered cluster count")
	set.IntVar(&values.agents, "connected-agents", defaults.ConnectedAgents, "concurrent reconnect count")
	set.IntVar(&values.servers, "server-replicas", defaults.ServerReplicas, "simulated server replica count")
	set.IntVar(&values.workers, "worker-replicas", defaults.WorkerReplicas, "simulated worker replica count")
	set.IntVar(&values.deployments, "deployments", defaults.Deployments, "current deployment count")
	set.IntVar(&values.statusEvents, "status-events-per-cluster", defaults.StatusEventsPerCluster, "burst transitions per cluster")
	set.IntVar(&values.iterations, "iterations", defaults.Iterations, "cold and warm placement samples per cycle")
	set.IntVar(&values.warmup, "warmup", defaults.Warmup, "unmeasured placement warmups per cycle")
	set.BoolVar(&values.allowSmaller, "allow-smaller-test-dataset", false, "allow below-capacity values for harness tests; never release evidence")
	return values
}

func (values commonFlags) scaleConfig() (qualification.ScaleConfig, error) {
	if values.output == "" {
		return qualification.ScaleConfig{}, errors.New("--output is required")
	}
	if !values.allowSmaller && (values.clusters < qualification.CapacityClusters ||
		values.agents < qualification.CapacityConnectedAgents || values.deployments < qualification.CapacityDeployments ||
		values.servers < qualification.CapacityReplicas || values.workers < qualification.CapacityReplicas ||
		values.statusEvents < qualification.CapacityStatusEvents) {
		return qualification.ScaleConfig{}, errors.New("capacity evidence requires 10000 clusters, 5000 agents, 100000 deployments, three replicas, and 10 status events; use --allow-smaller-test-dataset only for harness tests")
	}
	config := qualification.DefaultScaleConfig()
	config.Clusters, config.ConnectedAgents = values.clusters, values.agents
	config.ServerReplicas, config.WorkerReplicas = values.servers, values.workers
	config.Deployments, config.StatusEventsPerCluster = values.deployments, values.statusEvents
	config.Iterations, config.Warmup, config.CapacityPath = values.iterations, values.warmup, values.capacityPath
	config.Commit, config.Dirty = repositoryIdentity()
	config.Command = sanitizedCommand()
	return config, config.Validate()
}

func runScale(ctx context.Context, arguments []string) error {
	set := flag.NewFlagSet("scale", flag.ContinueOnError)
	values := bindCommon(set)
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	config, err := values.scaleConfig()
	if err != nil {
		return err
	}
	report := qualification.RunScale(ctx, config)
	if values.allowSmaller {
		report.Limitations = append(report.Limitations, "The explicit test-only dataset is below at least one release capacity target.")
	}
	if err := qualification.WriteJSONAtomic(values.output, report); err != nil {
		return err
	}
	if report.Status != "passed" {
		return fmt.Errorf("scale qualification failed; inspect %s", values.output)
	}
	fmt.Printf("delivery-qualification: scale passed; evidence=%s release_eligible=%t\n", values.output, report.ReleaseEligible)
	return nil
}

func runSoak(ctx context.Context, arguments []string) error {
	set := flag.NewFlagSet("soak", flag.ContinueOnError)
	values := bindCommon(set)
	duration := set.Duration("duration", qualification.ReleaseSoakDuration, "requested soak duration")
	interval := set.Duration("interval", time.Minute, "delay between complete capacity cycles")
	allowShort := set.Bool("allow-short-test-duration", false, "allow less than 24h for harness tests; never release evidence")
	if err := set.Parse(arguments); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if *duration < qualification.ReleaseSoakDuration && !*allowShort {
		return errors.New("release soak duration must be at least 24h; use --allow-short-test-duration only for harness tests")
	}
	scale, err := values.scaleConfig()
	if err != nil {
		return err
	}
	config := qualification.SoakConfig{Duration: *duration, Interval: *interval, Scale: scale, Command: sanitizedCommand()}
	config.Checkpoint = func(report qualification.SoakReport) error {
		return qualification.WriteJSONAtomic(values.output, report)
	}
	report := qualification.RunSoak(ctx, config)
	if *allowShort {
		report.Limitations = append(report.Limitations, "The explicit test-only soak duration is shorter than 24 hours.")
		if err := qualification.WriteJSONAtomic(values.output, report); err != nil {
			return err
		}
	}
	if report.Status != "passed" {
		return fmt.Errorf("soak qualification failed or was interrupted; inspect %s", values.output)
	}
	fmt.Printf("delivery-qualification: soak passed; evidence=%s observed=%s release_eligible=%t\n", values.output, report.Observed, report.ReleaseEligible)
	return nil
}

func repositoryIdentity() (string, bool) {
	commit := strings.TrimSpace(os.Getenv("ASTRONOMER_QUALIFICATION_COMMIT"))
	if commit == "" {
		command := exec.Command("git", "rev-parse", "HEAD")
		if raw, err := command.Output(); err == nil {
			commit = strings.TrimSpace(string(raw))
		}
	}
	if commit == "" {
		commit = "unknown"
	}
	command := exec.Command("git", "status", "--porcelain", "--untracked-files=no")
	raw, err := command.Output()
	return commit, err != nil || len(strings.TrimSpace(string(raw))) != 0
}

func sanitizedCommand() []string {
	// The CLI intentionally accepts no credential-bearing flag. Preserve exact
	// arguments so a reviewer can reproduce the measured workload.
	return append([]string(nil), os.Args...)
}

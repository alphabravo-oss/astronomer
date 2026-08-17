package qualification

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const ReleaseSoakDuration = 24 * time.Hour

type SoakConfig struct {
	Duration   time.Duration
	Interval   time.Duration
	Scale      ScaleConfig
	Command    []string
	Checkpoint func(SoakReport) error
}

func (config SoakConfig) Validate() error {
	if config.Duration <= 0 || config.Duration > 7*24*time.Hour {
		return errors.New("soak duration must be in (0, 168h]")
	}
	if config.Interval <= 0 || config.Interval > config.Duration {
		return errors.New("soak interval must be in (0, duration]")
	}
	return config.Scale.Validate()
}

// RunSoak repeatedly executes the complete deterministic capacity workload and
// checkpoints evidence after every cycle. A SIGTERM-canceled caller retains an
// explicit interrupted report rather than a stale file that looks complete.
// Short durations are supported for harness verification but are clearly
// marked as not satisfying the 24-hour release criterion.
func RunSoak(ctx context.Context, config SoakConfig) SoakReport {
	started := time.Now().UTC()
	report := SoakReport{
		SchemaVersion: ReportSchemaVersion, Kind: "soak", EvidenceScope: "deterministic_control_plane_soak",
		Status: "running", StartedAt: started, Requested: config.Duration.String(), Interval: config.Interval.String(),
		Command: append([]string(nil), config.Command...), Environment: CurrentEnvironment(config.Scale.Commit, config.Scale.Dirty),
		Dataset: config.Scale.Dataset(), Invariants: make(map[string]bool),
		Limitations: []string{
			"The soak repeatedly measures in-process production algorithms; combine it with PostgreSQL, queue, network, and multi-replica runtime telemetry.",
			"A short test duration validates harness behavior only and never satisfies the 24-hour release criterion.",
		},
	}
	if err := config.Validate(); err != nil {
		report.Errors = append(report.Errors, err.Error())
		return finishSoakReport(report, config, started)
	}
	report.Resources = sampleResources(config.Scale.CapacityPath)
	if err := checkpoint(config, report); err != nil {
		report.Errors = append(report.Errors, "initial checkpoint: "+err.Error())
		return finishSoakReport(report, config, started)
	}

	deadline := started.Add(config.Duration)
	sequence := 0
	for {
		if err := ctx.Err(); err != nil {
			report.Status = "interrupted"
			report.Errors = append(report.Errors, err.Error())
			break
		}
		cycleStarted := time.Now().UTC()
		cycleConfig := config.Scale
		cycleConfig.Command = nil
		cycle := RunScale(ctx, cycleConfig)
		sequence++
		report.Samples = append(report.Samples, SoakSample{Sequence: sequence, StartedAt: cycleStarted,
			CompletedAt: cycle.CompletedAt, Status: cycle.Status, Metrics: cycle.Metrics, Resources: cycle.Resources})
		report.Resources = mergeResourcePeaks(report.Resources, cycle.Resources)
		if cycle.Status != "passed" {
			report.Errors = append(report.Errors, fmt.Sprintf("cycle %d failed: %v", sequence, cycle.Errors))
			break
		}
		now := time.Now().UTC()
		report.Observed = now.Sub(started).String()
		if err := checkpoint(config, report); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("cycle %d checkpoint: %v", sequence, err))
			break
		}
		if !now.Before(deadline) {
			break
		}
		wait := min(config.Interval, time.Until(deadline))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			report.Status = "interrupted"
			report.Errors = append(report.Errors, ctx.Err().Error())
		case <-timer.C:
		}
		if report.Status == "interrupted" {
			break
		}
	}
	return finishSoakReport(report, config, started)
}

func finishSoakReport(report SoakReport, config SoakConfig, started time.Time) SoakReport {
	report.CompletedAt = time.Now().UTC()
	report.Observed = report.CompletedAt.Sub(started).String()
	report.Resources = mergeResourcePeaks(report.Resources, sampleResources(config.Scale.CapacityPath))
	report.Invariants["all_cycles_passed"] = len(report.Errors) == 0 && len(report.Samples) > 0
	report.Invariants["requested_duration_reached"] = report.CompletedAt.Sub(started) >= config.Duration
	report.Invariants["release_duration_24h"] = report.CompletedAt.Sub(started) >= ReleaseSoakDuration && config.Duration >= ReleaseSoakDuration
	report.Invariants["full_capacity_dataset"] = config.Scale.Clusters >= CapacityClusters &&
		config.Scale.ConnectedAgents >= CapacityConnectedAgents && config.Scale.Deployments >= CapacityDeployments &&
		config.Scale.ServerReplicas >= CapacityReplicas && config.Scale.WorkerReplicas >= CapacityReplicas
	if report.Status != "interrupted" {
		if len(report.Errors) == 0 && report.Invariants["requested_duration_reached"] {
			report.Status = "passed"
		} else {
			report.Status = "failed"
		}
	}
	// Runtime DB/queue/network evidence and a reviewed owned-cluster drill must
	// be combined with this report before the release can be eligible.
	report.ReleaseEligible = false
	if err := checkpoint(config, report); err != nil {
		report.Status = "failed"
		report.Errors = append(report.Errors, "final checkpoint: "+err.Error())
	}
	return report
}

func checkpoint(config SoakConfig, report SoakReport) error {
	if config.Checkpoint == nil {
		return nil
	}
	return config.Checkpoint(report)
}

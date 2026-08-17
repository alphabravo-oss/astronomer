// Package metrics owns the bounded-label Prometheus contract for the delivery
// control plane and downstream agent. Callers provide domain enums only; every
// helper normalizes unexpected values to "unknown" so IDs, URLs, revisions,
// user input, and error text can never become labels.
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	sourceResolutionDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "astronomer_delivery_source_resolution_duration_seconds",
		Help:    "Duration of immutable delivery source resolution.",
		Buckets: prometheus.DefBuckets,
	}, []string{"source_type", "result", "verification"})
	sourceResolutions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "astronomer_delivery_source_resolutions_total",
		Help: "Delivery source resolution attempts by bounded outcome.",
	}, []string{"source_type", "result", "verification"})
	plannerDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "astronomer_delivery_planner_duration_seconds",
		Help:    "Duration of deterministic target placement evaluation.",
		Buckets: prometheus.DefBuckets,
	}, []string{"result"})
	plannerCandidates = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "astronomer_delivery_planner_candidates",
		Help:    "Candidate cluster count per placement evaluation.",
		Buckets: []float64{1, 10, 50, 100, 500, 1_000, 5_000, 10_000},
	})
	plannerDecisions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "astronomer_delivery_planner_decisions_total",
		Help: "Placement decisions by fixed exclusion reason.",
	}, []string{"reason"})
	rolloutTransitions = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "astronomer_delivery_rollout_transitions_total",
		Help: "Delivery rollout transitions by strategy, state, and outcome.",
	}, []string{"strategy", "state", "outcome"})
	rolloutsCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "astronomer_delivery_rollouts",
		Help: "Current delivery rollouts by strategy and state.",
	}, []string{"strategy", "state"})
	cohortLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "astronomer_delivery_cohort_latency_seconds",
		Help:    "Cohort release-to-ack and acknowledgment-to-ready latency.",
		Buckets: []float64{1, 2, 5, 10, 30, 60, 120, 300, 600, 1_800, 3_600},
	}, []string{"stage", "outcome"})
	deploymentObservations = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "astronomer_delivery_deployment_observations_total",
		Help: "Normalized downstream deployment observations.",
	}, []string{"phase", "drift"})
	deploymentsCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "astronomer_delivery_deployments",
		Help: "Current cluster deployments by normalized phase and drift state.",
	}, []string{"phase", "drift"})
	staleDeploymentStatus = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "astronomer_delivery_stale_deployments",
		Help: "Cluster deployments whose downstream status is stale.",
	})
	sourcesCurrent = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "astronomer_delivery_sources",
		Help: "Current delivery sources by bounded status and verification state.",
	}, []string{"status", "verification"})
	snapshotRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "astronomer_delivery_assignment_snapshots_total",
		Help: "Assignment snapshot responses by bounded result.",
	}, []string{"result"})
	snapshotBytes = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "astronomer_delivery_assignment_snapshot_bytes",
		Help:    "Serialized assignment snapshot size before transport.",
		Buckets: prometheus.ExponentialBuckets(1_024, 4, 8),
	})
	snapshotObjects = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "astronomer_delivery_assignment_snapshot_objects",
		Help:    "Assignment and deletion count in a snapshot.",
		Buckets: []float64{0, 1, 10, 50, 100, 250, 500, 1_000, 2_000},
	})
	protocolEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "astronomer_delivery_protocol_events_total",
		Help: "Delivery protocol outcomes including stale and replay rejection.",
	}, []string{"direction", "result"})
	fluxReadiness = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "astronomer_delivery_flux_readiness",
		Help: "Count of clusters by bounded Flux compatibility and readiness state.",
	}, []string{"compatibility", "ready"})
	statusEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "astronomer_delivery_status_events_total",
		Help: "Status ingestion events by bounded disposition.",
	}, []string{"result"})
	credentialEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "astronomer_delivery_credential_events_total",
		Help: "Credential lifecycle events without credential or tenant labels.",
	}, []string{"action", "result"})
	workerEvents = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "astronomer_delivery_worker_events_total",
		Help: "Delivery worker claims, fencing conflicts, retries, and outcomes.",
	}, []string{"worker", "result"})
)

func init() {
	prometheus.MustRegister(
		sourceResolutionDuration, sourceResolutions, plannerDuration,
		plannerCandidates, plannerDecisions, rolloutTransitions, rolloutsCurrent, cohortLatency,
		deploymentObservations, deploymentsCurrent, staleDeploymentStatus, sourcesCurrent,
		snapshotRequests, snapshotBytes, snapshotObjects,
		protocolEvents, fluxReadiness, statusEvents, credentialEvents, workerEvents,
	)
}

type RolloutCount struct {
	Strategy string
	State    string
	Count    int64
}

type DeploymentCount struct {
	Phase string
	Drift bool
	Count int64
}

type SourceCount struct {
	Status       string
	Verification string
	Count        int64
}

type FluxCount struct {
	Compatibility string
	Ready         bool
	Count         int64
}

type DatabaseSnapshot struct {
	Rollouts         []RolloutCount
	Deployments      []DeploymentCount
	Sources          []SourceCount
	Flux             []FluxCount
	StaleDeployments int64
}

// ReplaceDatabaseSnapshot atomically refreshes low-cardinality state gauges
// from one database polling cycle. Reset prevents departed states from leaving
// stale series values behind.
func ReplaceDatabaseSnapshot(snapshot DatabaseSnapshot) {
	rolloutsCurrent.Reset()
	for _, row := range snapshot.Rollouts {
		rolloutsCurrent.WithLabelValues(
			normalize(row.Strategy, "all_at_once", "rolling", "canary", "partitioned"),
			normalize(row.State, "draft", "resolving", "awaiting_approval", "rejected", "queued", "progressing", "paused", "aborted", "succeeded", "failed", "rolling_back", "rolled_back", "rollback_failed"),
		).Set(float64(max(0, row.Count)))
	}
	deploymentsCurrent.Reset()
	for _, row := range snapshot.Deployments {
		drift := "false"
		if row.Drift {
			drift = "true"
		}
		deploymentsCurrent.WithLabelValues(
			normalize(row.Phase, "pending", "blocked", "applying", "ready", "degraded", "failed", "suspended", "deleting", "removed", "unknown"), drift,
		).Set(float64(max(0, row.Count)))
	}
	sourcesCurrent.Reset()
	for _, row := range snapshot.Sources {
		sourcesCurrent.WithLabelValues(
			normalize(row.Status, "pending", "ready", "degraded", "revoked"),
			normalize(row.Verification, "pending", "verified", "unsigned", "failed"),
		).Set(float64(max(0, row.Count)))
	}
	fluxReadiness.Reset()
	for _, row := range snapshot.Flux {
		SetFluxReadiness(row.Compatibility, row.Ready, int(row.Count))
	}
	staleDeploymentStatus.Set(float64(max(0, snapshot.StaleDeployments)))
}

func ObserveSourceResolution(sourceType, result, verification string, elapsed time.Duration) {
	labels := []string{
		normalize(sourceType, "git", "oci_artifact", "helm_http", "helm_oci"),
		normalize(result, "success", "invalid", "network_denied", "authentication_failed", "not_found", "digest_mismatch", "verification_failed", "limit_exceeded", "upstream_temporary", "canceled"),
		normalize(verification, "verified", "unsigned", "failed", "not_attempted"),
	}
	sourceResolutions.WithLabelValues(labels...).Inc()
	sourceResolutionDuration.WithLabelValues(labels...).Observe(nonNegative(elapsed.Seconds()))
}

func ObservePlacement(result string, elapsed time.Duration, candidates int, reasons []string) {
	plannerDuration.WithLabelValues(normalize(result, "success", "invalid", "conflict", "stale", "confirmation_required")).Observe(nonNegative(elapsed.Seconds()))
	plannerCandidates.Observe(float64(max(0, candidates)))
	for _, reason := range reasons {
		plannerDecisions.WithLabelValues(normalize(reason,
			"selected", "excluded_by_selector", "excluded_explicitly", "unauthorized",
			"disconnected", "incompatible", "missing_capability", "decommissioning")).Inc()
	}
}

func ObserveRollout(strategy, state, outcome string) {
	rolloutTransitions.WithLabelValues(
		normalize(strategy, "all_at_once", "rolling", "canary", "partitioned"),
		normalize(state, "draft", "resolving", "awaiting_approval", "rejected", "queued", "progressing", "paused", "aborted", "succeeded", "failed", "rolling_back", "rolled_back", "rollback_failed"),
		normalize(outcome, "transition", "success", "failure", "blocked", "retry", "rollback"),
	).Inc()
}

func ObserveCohort(stage, outcome string, elapsed time.Duration) {
	cohortLatency.WithLabelValues(
		normalize(stage, "release_to_ack", "ack_to_ready"),
		normalize(outcome, "success", "failure", "timeout"),
	).Observe(nonNegative(elapsed.Seconds()))
}

func ObserveDeployment(phase string, drifted bool) {
	drift := "false"
	if drifted {
		drift = "true"
	}
	deploymentObservations.WithLabelValues(normalize(phase, "pending", "blocked", "applying", "ready", "degraded", "failed", "suspended", "deleting", "removed", "unknown"), drift).Inc()
}

func ObserveSnapshot(result string, bytes, objects int) {
	snapshotRequests.WithLabelValues(normalize(result, "success", "not_modified", "invalid", "identity_mismatch", "changed", "failure")).Inc()
	if bytes >= 0 {
		snapshotBytes.Observe(float64(bytes))
	}
	if objects >= 0 {
		snapshotObjects.Observe(float64(objects))
	}
}

func ObserveProtocol(direction, result string) {
	protocolEvents.WithLabelValues(
		normalize(direction, "server_to_agent", "agent_to_server"),
		normalize(result, "accepted", "not_modified", "stale_rejected", "replay_rejected", "invalid", "too_large", "full_resync", "failure"),
	).Inc()
}

func SetFluxReadiness(compatibility string, ready bool, count int) {
	readyLabel := "false"
	if ready {
		readyLabel = "true"
	}
	fluxReadiness.WithLabelValues(normalize(compatibility, "compatible", "incompatible", "upgrade_required", "degraded", "unknown"), readyLabel).Set(float64(max(0, count)))
}

func ObserveStatus(result string) {
	statusEvents.WithLabelValues(normalize(result, "accepted", "coalesced", "dropped", "queue_full", "full_resync", "stale_rejected", "replay_rejected", "invalid", "failure")).Inc()
}

func ObserveCredential(action, result string) {
	credentialEvents.WithLabelValues(
		normalize(action, "created", "rotated", "revoked", "expired", "decrypt"),
		normalize(result, "success", "failure", "pending"),
	).Inc()
}

func ObserveWorker(worker, result string) {
	workerEvents.WithLabelValues(
		normalize(worker, "source_resolver", "rollout", "system_rollout", "status", "outbox"),
		normalize(result, "claimed", "success", "failure", "retry", "fence_conflict", "lease_expired"),
	).Inc()
}

func normalize(value string, allowed ...string) string {
	for _, candidate := range allowed {
		if value == candidate {
			return value
		}
	}
	return "unknown"
}

func nonNegative(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

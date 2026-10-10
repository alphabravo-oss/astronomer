package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type RegistrationMetricSnapshotRow struct{ ClusterID, Phase string }

var registrationMetricObservations = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "astronomer", Name: "registration_metric_observations_total",
	Help: "Registration phase observations by complete, owner_budget, invalid, stale, canceled or failed result.",
}, MetricLabels("result"))
var registrationMetricLastComplete = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "astronomer", Name: "registration_metric_last_complete_timestamp_seconds",
	Help: "Collection-start timestamp of the last complete published registration phase observation; failed or refused observations preserve it.",
}, MetricLabels())

func init() { prometheus.MustRegister(registrationMetricObservations, registrationMetricLastComplete) }

func RecordRegistrationMetricFailure(result string) {
	switch result {
	case "canceled", "owner_budget":
	default:
		result = "failed"
	}
	registrationMetricObservations.WithLabelValues(MetricValues(result)...).Inc()
}

// PublishRegistrationMetricSnapshot resynchronizes committed phases, including
// a ready phase whose captured flush was retired before its transaction commit.
// Caller must supply only a successfully committed complete RR observation.
// Preflight preserves all gauges/owners/timestamp if the entire result cannot
// fit. Admissions after the first-read watermark remain authoritative.
func PublishRegistrationMetricSnapshot(current []RegistrationMetricSnapshotRow, watermark uint64, started time.Time) bool {
	instance := InstanceID()
	if len(current) > MetricOwnerMax {
		registrationMetricObservations.WithLabelValues(instance, "owner_budget").Inc()
		return false
	}
	live := make(map[registrationMetricKey]string, len(current))
	for _, row := range current {
		key := registrationMetricKey{instance, row.ClusterID}
		if row.ClusterID == "" {
			registrationMetricObservations.WithLabelValues(instance, "invalid").Inc()
			return false
		}
		if _, duplicate := live[key]; duplicate {
			registrationMetricObservations.WithLabelValues(instance, "invalid").Inc()
			return false
		}
		live[key] = registrationPhaseLabel(row.Phase)
	}
	registrationMetricOwners.Lock()
	defer registrationMetricOwners.Unlock()
	if watermark <= registrationMetricOwners.observed {
		registrationMetricObservations.WithLabelValues(instance, "stale").Inc()
		return false
	}
	finalOwners := len(registrationMetricOwners.rows)
	for key := range live {
		if registrationMetricOwners.rows[key] == nil {
			finalOwners++
		}
	}
	for key, owner := range registrationMetricOwners.rows {
		if key.instance == instance && owner.admitted <= watermark {
			if _, exists := live[key]; !exists {
				finalOwners--
			}
		}
	}
	if finalOwners > MetricOwnerMax {
		registrationMetricObservations.WithLabelValues(instance, "owner_budget").Inc()
		return false
	}
	registrationMetricOwners.observed = watermark
	// Retire before adding so the registry never temporarily exceeds its cap.
	for key, owner := range registrationMetricOwners.rows {
		if key.instance != instance || owner.admitted > watermark {
			continue
		}
		if _, exists := live[key]; exists {
			continue
		}
		if owner.phase != "" {
			clusterRegistrationPhase.DeleteLabelValues(key.instance, key.cluster, owner.phase)
		}
		delete(registrationMetricOwners.rows, key)
	}
	for key, phase := range live {
		owner := registrationMetricOwners.rows[key]
		if owner != nil && owner.admitted > watermark {
			continue
		}
		if owner == nil {
			owner = &registrationMetricOwner{}
			registrationMetricOwners.rows[key] = owner
		}
		if owner.phase != "" && owner.phase != phase {
			clusterRegistrationPhase.DeleteLabelValues(key.instance, key.cluster, owner.phase)
		}
		owner.phase = phase
		// Never let a capture from before this observation overwrite its committed
		// state after publication. Later admissions always get a larger serial.
		owner.published = max(owner.published, watermark)
		clusterRegistrationPhase.WithLabelValues(key.instance, key.cluster, phase).Set(1)
	}
	registrationMetricObservations.WithLabelValues(instance, "complete").Inc()
	registrationMetricLastComplete.WithLabelValues(instance).Set(float64(started.UnixNano()) / 1e9)
	return true
}

package observability

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// MetricOwnerMax is an engineering admission bound, not a qualified fleet size.
// Metrics overflow must never reject the product operation.
const MetricOwnerMax = 10_000

type registrationMetricKey struct{ instance, cluster string }
type registrationMetricOwner struct {
	admitted, published uint64
	phase               string
}

var registrationMetricOwners = struct {
	sync.Mutex
	serial uint64
	rows   map[registrationMetricKey]*registrationMetricOwner
}{rows: make(map[registrationMetricKey]*registrationMetricOwner)}

var registrationMetricAdmissions = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "astronomer", Name: "registration_metric_admissions_total",
	Help: "Registration metric captures by captured, budget_dropped or late_dropped result; product transitions remain unaffected.",
}, MetricLabels("result"))

func init() { prometheus.MustRegister(registrationMetricAdmissions) }

// CapturePhaseTransition is called inside the successful registration write,
// before commit. No phase child is published until the returned closure runs.
// Failed transactions can discard it. Every admitted capture renews freshness;
// owner identity and sequence reject deleted or out-of-order late flushes.
func (h *RegistrationMetricsHook) CapturePhaseTransition(clusterID, _, to string) func() {
	if h == nil || clusterID == "" || to == "" {
		return func() {}
	}
	key := registrationMetricKey{InstanceID(), clusterID}
	registrationMetricOwners.Lock()
	owner := registrationMetricOwners.rows[key]
	if owner == nil && len(registrationMetricOwners.rows) >= MetricOwnerMax {
		registrationMetricAdmissions.WithLabelValues(key.instance, "budget_dropped").Inc()
		registrationMetricOwners.Unlock()
		return func() {}
	}
	registrationMetricOwners.serial++
	sequence := registrationMetricOwners.serial
	if owner == nil {
		owner = &registrationMetricOwner{}
		registrationMetricOwners.rows[key] = owner
	}
	owner.admitted = sequence
	registrationMetricAdmissions.WithLabelValues(key.instance, "captured").Inc()
	registrationMetricOwners.Unlock()
	phase := registrationPhaseLabel(to)
	return func() {
		registrationMetricOwners.Lock()
		defer registrationMetricOwners.Unlock()
		if registrationMetricOwners.rows[key] != owner || sequence <= owner.published {
			registrationMetricAdmissions.WithLabelValues(key.instance, "late_dropped").Inc()
			return
		}
		if owner.phase != "" && owner.phase != phase {
			clusterRegistrationPhase.DeleteLabelValues(key.instance, key.cluster, owner.phase)
		}
		owner.phase, owner.published = phase, sequence
		clusterRegistrationPhase.WithLabelValues(key.instance, key.cluster, phase).Set(1)
	}
}

// RegistrationMetricsWatermark must be captured before the first consistent
// database read. The snapshot consumer never retires captures admitted later.
func RegistrationMetricsWatermark() uint64 {
	registrationMetricOwners.Lock()
	defer registrationMetricOwners.Unlock()
	return registrationMetricOwners.serial
}

// RetireRegistrationMetrics accepts only a complete bounded snapshot of current
// cluster IDs. It adds no owners and never resets a live owner's metric.
// Database failure, cancellation or exhausted collection budget must not call it.
func RetireRegistrationMetrics(current []string, watermark uint64) bool {
	if len(current) > MetricOwnerMax {
		return false
	}
	live := make(map[string]bool, len(current))
	for _, id := range current {
		live[id] = true
	}
	instance := InstanceID()
	registrationMetricOwners.Lock()
	defer registrationMetricOwners.Unlock()
	for key, owner := range registrationMetricOwners.rows {
		if key.instance != instance || live[key.cluster] || owner.admitted > watermark {
			continue
		}
		if owner.phase != "" {
			clusterRegistrationPhase.DeleteLabelValues(key.instance, key.cluster, owner.phase)
		}
		delete(registrationMetricOwners.rows, key)
	}
	return true
}

package observability

import (
	"sync"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

type vaultMetricKey struct {
	instance string
	id       uuid.UUID
}
type vaultMetricOwner struct{ admitted uint64 }

var vaultMetricOwners = struct {
	sync.Mutex
	serial, epoch uint64
	rows          map[vaultMetricKey]*vaultMetricOwner
}{rows: make(map[vaultMetricKey]*vaultMetricOwner)}

var vaultMetricAdmissions = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "astronomer", Name: "vault_metric_admissions_total",
	Help: "Vault metric captures and observations by captured, budget_dropped or late_dropped result; product operations remain unaffected.",
}, MetricLabels("result"))

func init() { prometheus.MustRegister(vaultMetricAdmissions) }

// VaultMetricLookup is captured BEFORE reading the database. A deletion or
// complete snapshot publication invalidates outstanding unbound lookups without
// retaining unbounded deleted-ID tombstones. Its serial also protects admissions
// newer than the database snapshot's first read.
type VaultMetricLookup struct {
	instance      string
	serial, epoch uint64
}

func BeginVaultMetricLookup() VaultMetricLookup {
	vaultMetricOwners.Lock()
	defer vaultMetricOwners.Unlock()
	vaultMetricOwners.serial++
	return VaultMetricLookup{InstanceID(), vaultMetricOwners.serial, vaultMetricOwners.epoch}
}

// VaultMetricLease captures a verified database owner before the network call.
// A deleted child is never recreated by a late network/probe callback.
type VaultMetricLease struct {
	key   vaultMetricKey
	owner *vaultMetricOwner
}

func (lookup VaultMetricLookup) Bind(id uuid.UUID, name string) VaultMetricLease {
	key := vaultMetricKey{lookup.instance, id}
	vaultMetricOwners.Lock()
	defer vaultMetricOwners.Unlock()
	if id == uuid.Nil || name == "" || lookup.instance == "" || lookup.serial == 0 || lookup.epoch != vaultMetricOwners.epoch {
		vaultMetricAdmissions.WithLabelValues(key.instance, "late_dropped").Inc()
		return VaultMetricLease{}
	}
	owner := vaultMetricOwners.rows[key]
	if owner == nil && len(vaultMetricOwners.rows) >= MetricOwnerMax {
		vaultMetricAdmissions.WithLabelValues(key.instance, "budget_dropped").Inc()
		return VaultMetricLease{}
	}
	if owner == nil {
		owner = &vaultMetricOwner{}
		vaultMetricOwners.rows[key] = owner
	}
	owner.admitted = max(owner.admitted, lookup.serial)
	vaultMetricAdmissions.WithLabelValues(key.instance, "captured").Inc()
	return VaultMetricLease{key, owner}
}

func (lease VaultMetricLease) live() bool {
	if lease.owner == nil {
		return false
	}
	if vaultMetricOwners.rows[lease.key] != lease.owner {
		vaultMetricAdmissions.WithLabelValues(lease.key.instance, "late_dropped").Inc()
		return false
	}
	return true
}

func (lease VaultMetricLease) RecordResolve(outcome string, seconds float64) {
	vaultMetricOwners.Lock()
	defer vaultMetricOwners.Unlock()
	if !lease.live() {
		return
	}
	outcome = vaultOutcomeLabel(outcome)
	vaultResolvesTotal.WithLabelValues(lease.key.instance, lease.key.id.String(), outcome).Inc()
	if outcome == VaultOutcomeSucceeded && seconds > 0 {
		vaultResolveDuration.WithLabelValues(lease.key.instance, lease.key.id.String()).Observe(seconds)
	}
}

func (lease VaultMetricLease) RecordHealth(ok bool) {
	vaultMetricOwners.Lock()
	defer vaultMetricOwners.Unlock()
	if !lease.live() {
		return
	}
	value := 0.0
	if ok {
		value = 1
	}
	vaultConnectionHealth.WithLabelValues(lease.key.instance, lease.key.id.String()).Set(value)
}

func deleteVaultMetricChildren(key vaultMetricKey) {
	for _, outcome := range []string{VaultOutcomeSucceeded, VaultOutcomeFailed, "other"} {
		vaultResolvesTotal.DeleteLabelValues(key.instance, key.id.String(), outcome)
	}
	vaultResolveDuration.DeleteLabelValues(key.instance, key.id.String())
	vaultConnectionHealth.DeleteLabelValues(key.instance, key.id.String())
	delete(vaultMetricOwners.rows, key)
}

// RetireVaultMetricOwner is called only after the deletion transaction commits.
// Incrementing the lookup epoch fences even a lookup whose ID wasn't bound yet.
// Cache invalidation for an update must not call this: live counters keep rates.
func RetireVaultMetricOwner(id uuid.UUID) {
	vaultMetricOwners.Lock()
	defer vaultMetricOwners.Unlock()
	vaultMetricOwners.epoch++
	key := vaultMetricKey{InstanceID(), id}
	if owner := vaultMetricOwners.rows[key]; owner != nil {
		deleteVaultMetricChildren(key)
	}
}

type VaultMetricSnapshotRow struct {
	ID   uuid.UUID
	Name string
}

func VaultMetricsWatermark() uint64 {
	vaultMetricOwners.Lock()
	defer vaultMetricOwners.Unlock()
	return vaultMetricOwners.serial
}

// RetireVaultMetrics accepts a complete bounded repeatable-read observation.
// Error/partial/canceled/over-budget observations must never invoke it.
func RetireVaultMetrics(current []VaultMetricSnapshotRow, watermark uint64) bool {
	if len(current) > MetricOwnerMax {
		return false
	}
	live := make(map[uuid.UUID]string, len(current))
	for _, row := range current {
		if row.ID == uuid.Nil || row.Name == "" {
			return false
		}
		if _, duplicate := live[row.ID]; duplicate {
			return false
		}
		live[row.ID] = row.Name
	}
	vaultMetricOwners.Lock()
	defer vaultMetricOwners.Unlock()
	// All successful publications fence unbound lookups that began earlier,
	// including IDs not yet present in the owner map. No history map is kept.
	vaultMetricOwners.epoch++
	instance := InstanceID()
	for key, owner := range vaultMetricOwners.rows {
		if key.instance != instance || owner.admitted > watermark {
			continue
		}
		if _, exists := live[key.id]; !exists {
			deleteVaultMetricChildren(key)
		}
	}
	return true
}

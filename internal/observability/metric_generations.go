package observability

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
)

// These are telemetry partitions, not authorization domains or actor claims.
type MetricOwnerFamily string

const (
	MetricSSOProvider   MetricOwnerFamily = "sso_provider"
	MetricSIEMForwarder MetricOwnerFamily = "siem_forwarder"
	MetricClusterGroup  MetricOwnerFamily = "cluster_group"
	MetricCluster       MetricOwnerFamily = "cluster"
)

func (f MetricOwnerFamily) valid() bool {
	switch f {
	case MetricSSOProvider, MetricSIEMForwarder, MetricClusterGroup, MetricCluster:
		return true
	}
	return false
}

type MetricGenerationReader interface {
	GetMetricOwnerGeneration(context.Context, sqlc.GetMetricOwnerGenerationParams) (uuid.UUID, error)
}

type MetricGenerationRow struct{ ID, Generation uuid.UUID }
type generationKey struct {
	instance string
	family   MetricOwnerFamily
	id       uuid.UUID
}
type generationOwner struct {
	generation uuid.UUID
	admitted   uint64
}

var metricGenerations = struct {
	sync.Mutex
	serial    uint64
	epochs    map[MetricOwnerFamily]uint64
	published map[MetricOwnerFamily]uint64
	counts    map[MetricOwnerFamily]int
	rows      map[generationKey]*generationOwner
}{rows: make(map[generationKey]*generationOwner), epochs: make(map[MetricOwnerFamily]uint64), published: make(map[MetricOwnerFamily]uint64), counts: make(map[MetricOwnerFamily]int)}

var metricGenerationResults = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "astronomer", Name: "metric_owner_generation_results_total",
	Help: "Telemetry-only ownership operations by finite family/result; dropped observations do not reject product operations.",
}, MetricLabels("family", "result"))
var metricGenerationLastComplete = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Namespace: "astronomer", Name: "metric_owner_generation_last_complete_timestamp_seconds",
	Help: "Collection-start timestamp of a complete committed owner observation; incomplete observations preserve the preceding timestamp.",
}, MetricLabels("family"))

func init() { prometheus.MustRegister(metricGenerationResults, metricGenerationLastComplete) }
func generationResult(instance string, family MetricOwnerFamily, result string) {
	if !family.valid() {
		return
	}
	switch result {
	case "captured", "recorded", "late_dropped", "budget_dropped", "complete", "invalid", "canceled":
	default:
		result = "failed"
	}
	metricGenerationResults.WithLabelValues(instance, string(family), result).Inc()
}

// The caller supplies only collectors for this finite family. Retirement never
// resets unrelated active counters. Existing counters survive ordinary updates.
var generationRetire = map[MetricOwnerFamily]func(string, string){}

func RegisterMetricGenerationRetirement(family MetricOwnerFamily, retire func(instance, ownerID string)) {
	if !family.valid() || retire == nil {
		return
	}
	metricGenerations.Lock()
	defer metricGenerations.Unlock()
	if generationRetire[family] == nil {
		generationRetire[family] = retire
	}
}

type MetricGenerationLookup struct {
	instance      string
	family        MetricOwnerFamily
	serial, epoch uint64
}

func BeginMetricGenerationLookup(family MetricOwnerFamily) MetricGenerationLookup {
	metricGenerations.Lock()
	defer metricGenerations.Unlock()
	if !family.valid() {
		return MetricGenerationLookup{}
	}
	metricGenerations.serial++
	return MetricGenerationLookup{InstanceID(), family, metricGenerations.serial, metricGenerations.epochs[family]}
}

type MetricGenerationLease struct {
	key    generationKey
	owner  *generationOwner
	reader MetricGenerationReader
}

// IsCaptured reports whether capture obtained a handle. It does not establish
// current ownership: every Record still re-reads and verifies its generation.
func (lease MetricGenerationLease) IsCaptured() bool {
	return lease.owner != nil && lease.reader != nil
}

// Bind follows a real canonical database read, not a session/name-only claim.
// Reading ownership before an external call does not authorize that call.
func (lookup MetricGenerationLookup) Bind(reader MetricGenerationReader, row MetricGenerationRow) MetricGenerationLease {
	key := generationKey{lookup.instance, lookup.family, row.ID}
	metricGenerations.Lock()
	defer metricGenerations.Unlock()
	if reader == nil || lookup.serial == 0 || !lookup.family.valid() || lookup.instance == "" || row.ID == uuid.Nil || row.Generation == uuid.Nil {
		generationResult(lookup.instance, lookup.family, "invalid")
		return MetricGenerationLease{}
	}
	if lookup.epoch != metricGenerations.epochs[lookup.family] {
		generationResult(key.instance, key.family, "late_dropped")
		return MetricGenerationLease{}
	}
	owner := metricGenerations.rows[key]
	if owner != nil && owner.admitted > lookup.serial {
		generationResult(key.instance, key.family, "late_dropped")
		return MetricGenerationLease{}
	}
	if owner == nil {
		if metricGenerations.counts[key.family] >= MetricOwnerMax {
			generationResult(key.instance, key.family, "budget_dropped")
			return MetricGenerationLease{}
		}
	}
	if owner == nil || owner.generation != row.Generation {
		if owner != nil {
			retireGeneration(key)
		}
		owner = &generationOwner{generation: row.Generation}
		metricGenerations.rows[key] = owner
		metricGenerations.counts[key.family]++
	}
	owner.admitted = lookup.serial
	generationResult(key.instance, key.family, "captured")
	return MetricGenerationLease{key, owner, reader}
}

func CaptureMetricGeneration(ctx context.Context, source any, family MetricOwnerFamily, id uuid.UUID) MetricGenerationLease {
	lookup := BeginMetricGenerationLookup(family)
	reader, ok := source.(MetricGenerationReader)
	if ctx == nil || ctx.Err() != nil || !ok || id == uuid.Nil || !family.valid() {
		return MetricGenerationLease{}
	}
	bounded, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	generation, err := reader.GetMetricOwnerGeneration(bounded, sqlc.GetMetricOwnerGenerationParams{Family: string(family), OwnerID: id})
	if err != nil || bounded.Err() != nil {
		generationResult(lookup.instance, family, "failed")
		return MetricGenerationLease{}
	}
	return lookup.Bind(reader, MetricGenerationRow{id, generation})
}

// Record compares the captured generation with a fresh bounded canonical read
// before touching a collector, then holds the owner lock through finite metric
// updates. A retired/recreated owner's callback cannot re-admit its old handle.
// No error payload, provider name, endpoint or credential becomes a label.
func (lease MetricGenerationLease) Record(ctx context.Context, update func(instance, ownerID string)) bool {
	if ctx == nil || lease.owner == nil || lease.reader == nil || update == nil || ctx.Err() != nil {
		return false
	}
	bounded, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	generation, err := lease.reader.GetMetricOwnerGeneration(bounded, sqlc.GetMetricOwnerGenerationParams{Family: string(lease.key.family), OwnerID: lease.key.id})
	metricGenerations.Lock()
	defer metricGenerations.Unlock()
	if err != nil || bounded.Err() != nil || generation != lease.owner.generation || metricGenerations.rows[lease.key] != lease.owner {
		generationResult(lease.key.instance, lease.key.family, "late_dropped")
		return false
	}
	update(lease.key.instance, lease.key.id.String())
	generationResult(lease.key.instance, lease.key.family, "recorded")
	return true
}

func retireGeneration(key generationKey) {
	if retire := generationRetire[key.family]; retire != nil {
		retire(key.instance, key.id.String())
	}
	delete(metricGenerations.rows, key)
	metricGenerations.counts[key.family]--
}

// Publish receives only a fully traversed/committed RR observation. Invalid or
// oversized observations change no owners, counters or success timestamp.
// Admissions newer than the collection watermark remain protected. Publishing
// does not allocate metric children for quiet owners that have no observations.
func (lookup MetricGenerationLookup) Publish(current []MetricGenerationRow, started time.Time) bool {
	if !lookup.family.valid() || lookup.serial == 0 || len(current) > MetricOwnerMax || started.IsZero() {
		return false
	}
	observed := make(map[uuid.UUID]uuid.UUID, len(current))
	for _, row := range current {
		if row.ID == uuid.Nil || row.Generation == uuid.Nil {
			return false
		}
		if _, duplicate := observed[row.ID]; duplicate {
			return false
		}
		observed[row.ID] = row.Generation
	}
	metricGenerations.Lock()
	defer metricGenerations.Unlock()
	if lookup.serial <= metricGenerations.published[lookup.family] {
		return false
	}
	metricGenerations.published[lookup.family] = lookup.serial
	metricGenerations.epochs[lookup.family]++
	for key, owner := range metricGenerations.rows {
		if key.instance != lookup.instance || key.family != lookup.family || owner.admitted > lookup.serial {
			continue
		}
		if generation, exists := observed[key.id]; !exists || generation != owner.generation {
			retireGeneration(key)
		}
	}
	generationResult(lookup.instance, lookup.family, "complete")
	metricGenerationLastComplete.WithLabelValues(lookup.instance, string(lookup.family)).Set(float64(started.UnixNano()) / 1e9)
	return true
}

// PublishValues is reserved for complete committed gauge snapshots. It validates
// and preflights every owner before changing any existing gauge publication.
// update is a finite collector operation; it must not issue I/O or reenter this
// owner registry. No callback from external work should use this snapshot path.
func (lookup MetricGenerationLookup) PublishValues(current []MetricGenerationRow, started time.Time, update func(instance, id string)) bool {
	return lookup.PublishValuesAndComplete(current, started, update, nil)
}

// complete runs under the same publication lock, including an empty snapshot;
// aggregate gauges cannot be overwritten afterward by an older publication.
func (lookup MetricGenerationLookup) PublishValuesAndComplete(current []MetricGenerationRow, started time.Time, update func(instance, id string), complete func()) bool {
	if !lookup.family.valid() || lookup.serial == 0 || len(current) > MetricOwnerMax || started.IsZero() || update == nil {
		return false
	}
	observed := make(map[uuid.UUID]uuid.UUID, len(current))
	for _, row := range current {
		if row.ID == uuid.Nil || row.Generation == uuid.Nil {
			return false
		}
		if _, duplicate := observed[row.ID]; duplicate {
			return false
		}
		observed[row.ID] = row.Generation
	}
	metricGenerations.Lock()
	defer metricGenerations.Unlock()
	if lookup.serial <= metricGenerations.published[lookup.family] {
		return false
	}
	finalCount := metricGenerations.counts[lookup.family]
	for key, owner := range metricGenerations.rows {
		if key.instance != lookup.instance || key.family != lookup.family || owner.admitted > lookup.serial {
			continue
		}
		if _, exists := observed[key.id]; !exists {
			finalCount--
		}
	}
	for id := range observed {
		if metricGenerations.rows[generationKey{lookup.instance, lookup.family, id}] == nil {
			finalCount++
		}
	}
	if finalCount > MetricOwnerMax {
		generationResult(lookup.instance, lookup.family, "budget_dropped")
		return false
	}
	metricGenerations.published[lookup.family] = lookup.serial
	metricGenerations.epochs[lookup.family]++
	for key, owner := range metricGenerations.rows {
		if key.instance != lookup.instance || key.family != lookup.family || owner.admitted > lookup.serial {
			continue
		}
		if generation, exists := observed[key.id]; !exists || generation != owner.generation {
			retireGeneration(key)
		}
	}
	for _, row := range current {
		key := generationKey{lookup.instance, lookup.family, row.ID}
		owner := metricGenerations.rows[key]
		if owner != nil && owner.admitted > lookup.serial {
			continue
		}
		if owner == nil {
			owner = &generationOwner{generation: row.Generation}
			metricGenerations.rows[key] = owner
			metricGenerations.counts[key.family]++
		}
		owner.admitted = lookup.serial
		update(key.instance, key.id.String())
	}
	if complete != nil {
		complete()
	}
	generationResult(lookup.instance, lookup.family, "complete")
	metricGenerationLastComplete.WithLabelValues(lookup.instance, string(lookup.family)).Set(float64(started.UnixNano()) / 1e9)
	return true
}

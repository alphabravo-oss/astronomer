package tunnel

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/alphabravocompany/astronomer-go/internal/db/sqlc"
	"github.com/alphabravocompany/astronomer-go/internal/observability"
)

// A connection captures children once, after admission. A retired child can
// still receive a late callback, but incrementing it cannot recreate the
// deleted vector entry. No deleted-owner tombstones are retained.
type agentMetricHandles struct {
	reconnect prometheus.Counter
	inbound   prometheus.Counter
	outbound  prometheus.Counter
}

func (m agentMetricHandles) reconnected() {
	if m.reconnect != nil {
		m.reconnect.Inc()
	}
}

func (m agentMetricHandles) message(direction string) {
	switch direction {
	case "inbound":
		if m.inbound != nil {
			m.inbound.Inc()
		}
	case "outbound":
		if m.outbound != nil {
			m.outbound.Inc()
		}
	}
}

type connectionMetricKey struct{ instance, cluster string }
type connectionMetricOwner struct {
	handles agentMetricHandles
	created uint64
	name    string
	gauges  bool
}

var connectionMetricOwners = struct {
	sync.Mutex
	serial uint64
	rows   map[connectionMetricKey]*connectionMetricOwner
}{rows: make(map[connectionMetricKey]*connectionMetricOwner)}

func captureAgentMetrics(clusterID string) agentMetricHandles {
	registerConnectionMetrics()
	connectionMetricOwners.Lock()
	defer connectionMetricOwners.Unlock()
	key := connectionMetricKey{observability.InstanceID(), clusterID}
	owner := connectionMetricOwners.rows[key]
	if owner == nil && len(connectionMetricOwners.rows) >= ConnectionMetricsMaxOwners {
		connectionMetricAdmissions.WithLabelValues(key.instance, "budget_dropped").Inc()
		return agentMetricHandles{}
	}
	// Every admitted capture refreshes freshness, including reconnects for an
	// existing owner. Keep its children so current-owner rates never reset.
	connectionMetricOwners.serial++
	if owner == nil {
		owner = &connectionMetricOwner{}
		connectionMetricOwners.rows[key] = owner
	}
	owner.created = connectionMetricOwners.serial
	if owner.handles.reconnect == nil {
		owner.handles = agentMetricHandles{
			reconnect: agentReconnectsTotal.WithLabelValues(key.instance, key.cluster),
			inbound:   agentMessagesTotal.WithLabelValues(key.instance, key.cluster, "inbound"),
			outbound:  agentMessagesTotal.WithLabelValues(key.instance, key.cluster, "outbound"),
		}
	}
	connectionMetricAdmissions.WithLabelValues(key.instance, "recorded").Inc()
	return owner.handles
}

// Capture before querying the first page. An admitted connection created while
// collection was in flight must not be retired by that older observation.
func connectionMetricsWatermark() uint64 {
	connectionMetricOwners.Lock()
	defer connectionMetricOwners.Unlock()
	return connectionMetricOwners.serial
}

func publishConnectionMetrics(rows []sqlc.ListClusterConnectionStatusRow, now time.Time, watermark uint64) bool {
	connectionMetricOwners.Lock()
	defer connectionMetricOwners.Unlock()
	instance := observability.InstanceID()
	if len(rows) > ConnectionMetricsMaxOwners {
		connectionMetricObservations.WithLabelValues(instance, "page_budget").Inc()
		return false
	}
	current := make(map[connectionMetricKey]bool, len(rows))
	for _, row := range rows {
		key := connectionMetricKey{instance, row.ClusterID.String()}
		current[key] = true
	}
	finalOwners := len(connectionMetricOwners.rows)
	for key := range current {
		if connectionMetricOwners.rows[key] == nil {
			finalOwners++
		}
	}
	for key, owner := range connectionMetricOwners.rows {
		if key.instance == instance && !current[key] && owner.created <= watermark {
			finalOwners--
		}
	}
	if finalOwners > ConnectionMetricsMaxOwners {
		connectionMetricObservations.WithLabelValues(instance, "owner_budget").Inc()
		return false
	}
	// No vector or registry changes occur until the entire publication fits.
	// Drop removed entries first so the registry never transiently exceeds its
	// hard cap while an equally sized observation replaces them.
	for key, owner := range connectionMetricOwners.rows {
		if key.instance != instance || current[key] || owner.created > watermark {
			continue
		}
		if owner.gauges {
			deleteConnectionGauges(key, owner.name)
		}
		agentReconnectsTotal.DeleteLabelValues(key.instance, key.cluster)
		for _, direction := range []string{"inbound", "outbound"} {
			agentMessagesTotal.DeleteLabelValues(key.instance, key.cluster, direction)
		}
		delete(connectionMetricOwners.rows, key)
	}
	for _, row := range rows {
		key := connectionMetricKey{instance, row.ClusterID.String()}
		owner := connectionMetricOwners.rows[key]
		if owner == nil {
			connectionMetricOwners.serial++
			owner = &connectionMetricOwner{created: connectionMetricOwners.serial}
			connectionMetricOwners.rows[key] = owner
		}
		if owner.gauges && owner.name != row.ClusterName {
			deleteConnectionGauges(key, owner.name)
		}
		owner.name, owner.gauges = row.ClusterName, true
		connected := 0.0
		if row.Status == "connected" {
			connected = 1
		}
		agentConnectionsGauge.WithLabelValues(key.instance, key.cluster, row.ClusterName).Set(connected)
		age := max(0, now.Sub(row.LastActivity).Seconds())
		agentLastSeenSecondsGauge.WithLabelValues(key.instance, key.cluster, row.ClusterName).Set(age)
	}
	connectionMetricObservations.WithLabelValues(instance, "complete").Inc()
	connectionMetricLastComplete.WithLabelValues(instance).Set(float64(now.Unix()))
	return true
}

func deleteConnectionGauges(key connectionMetricKey, name string) {
	agentConnectionsGauge.DeleteLabelValues(key.instance, key.cluster, name)
	agentLastSeenSecondsGauge.DeleteLabelValues(key.instance, key.cluster, name)
}

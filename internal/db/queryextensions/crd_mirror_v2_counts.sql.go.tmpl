// Migration 069 + T6.069 — CRD-mirror v2 row count rollup.
//
// One UNION ALL over the five mirror tables, grouped by cluster_id and
// the literal kind label. Powers the periodic populator that updates
// the astronomer_crd_mirror_rows{kind,cluster} gauge so operators can
// see at a glance how many rows each kind contributes per cluster
// without reading the raw tables.
//
// Hand-authored to match the crd_mirror_v2_ext.sql.go pattern; the
// sqlc CLI is not part of the local Make path.

package sqlc

import (
	"context"

	"github.com/google/uuid"
)

const countMirroredRowsByKind = `
SELECT 'ingress_class'::text  AS kind, cluster_id, count(*) FROM mirrored_ingress_classes  GROUP BY cluster_id
UNION ALL
SELECT 'gateway_class'::text  AS kind, cluster_id, count(*) FROM mirrored_gateway_classes  GROUP BY cluster_id
UNION ALL
SELECT 'network_policy'::text AS kind, cluster_id, count(*) FROM mirrored_network_policies GROUP BY cluster_id
UNION ALL
SELECT 'resource_quota'::text AS kind, cluster_id, count(*) FROM mirrored_resource_quotas  GROUP BY cluster_id
UNION ALL
SELECT 'limit_range'::text    AS kind, cluster_id, count(*) FROM mirrored_limit_ranges     GROUP BY cluster_id
`

// MirrorRowCount is one (kind, cluster, count) tuple returned by
// CountMirroredRowsByKind. The cluster name lookup happens above this
// layer.
type MirrorRowCount struct {
	Kind      string
	ClusterID uuid.UUID
	Count     int64
}

// CountMirroredRowsByKind returns the per-(kind, cluster) row count
// across the five v2 mirror tables. Output rows are unsorted; callers
// that need stable order should sort post-fetch.
func (q *Queries) CountMirroredRowsByKind(ctx context.Context) ([]MirrorRowCount, error) {
	rows, err := q.db.Query(ctx, countMirroredRowsByKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MirrorRowCount{}
	for rows.Next() {
		var r MirrorRowCount
		if err := rows.Scan(&r.Kind, &r.ClusterID, &r.Count); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
